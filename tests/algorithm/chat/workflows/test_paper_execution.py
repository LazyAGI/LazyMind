"""Core Windows execution, isolation and Desktop compatibility regressions."""
import base64
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import sysconfig
from types import SimpleNamespace
from unittest.mock import Mock

import pytest

from lazymind.document_tools.paper.analysis import generated_executor
from lazymind.document_tools.paper.execution import container, windows
from lazymind.document_tools.paper.plotting.executor import execute_plot


NATIVE_WINDOWS = pytest.mark.skipif(sys.platform != 'win32', reason='Requires native Windows AppContainer APIs')


@pytest.mark.parametrize('available', [True, False])
def test_windows_environment_validation(monkeypatch, available):
    validate = Mock(side_effect=None if available else OSError('AppContainer unavailable'))
    monkeypatch.setattr(container, 'sys', SimpleNamespace(platform='win32'))
    monkeypatch.setattr(windows, 'validate_environment', validate)
    if available:
        container.validate_environment()
    else:
        with pytest.raises(container.ContainerEnvironmentError, match='AppContainer unavailable'):
            container.validate_environment()
    validate.assert_called_once()


@pytest.mark.parametrize('code,error', [
    ('import json,sys; print(json.dumps(json.load(sys.stdin), ensure_ascii=False))', None),
    ('import time; time.sleep(30)', '超时'),
    ('from pathlib import Path; Path("large").write_bytes(b"x" * 2048)', '临时文件超过'),
    ('from pathlib import Path\nfor name in ("one", "two"):\n    Path(name).write_bytes(b"x" * 1024)', '临时存储合计'),
])
def test_execution_result_and_cleanup(monkeypatch, tmp_path, code, error):
    """Exercise the shared protocol with a real trusted worker on every platform."""
    worker = tmp_path / '协议 worker.py'
    worker.write_text(code, encoding='utf-8')
    children = []

    class Child(subprocess.Popen):
        def check_storage(self):
            windows.check_storage([Path(self.cwd)])

        def close(self):
            if self.poll() is None:
                self.kill()
            self.wait(timeout=5)

    def start(program, scratch, _timeout, streams, env):
        if os.name == 'nt':
            env = dict(env, SystemRoot=os.environ['SystemRoot'])
        child = Child([sys.executable, '-I', '-X', 'utf8', str(program)], cwd=scratch,
                      stdin=streams[0], stdout=streams[1], stderr=streams[2], env=env)
        child.cwd = scratch
        children.append((child, scratch))
        if error and error != '超时':
            child.wait(timeout=5)  # Verify final inspection even when the worker has already exited.
        return child

    monkeypatch.setattr(container, 'sys', SimpleNamespace(platform='win32', executable=sys.executable))
    monkeypatch.setattr(windows, 'start', start)
    monkeypatch.setattr(windows, 'FILE_LIMIT', 1024)
    monkeypatch.setattr(windows, 'STORAGE_LIMIT', 1536)
    if error:
        with pytest.raises(RuntimeError, match=error):
            container.execute({}, worker, 1 if error == '超时' else 5, label='test')
    else:
        payload = {'message': '均值 μ = 2'}
        assert container.execute(payload, worker, 5, label='test') == payload
    child, scratch = children[0]
    assert child.poll() is not None
    assert not scratch.exists()


def test_windows_launch_failure_does_not_fall_back_to_host(monkeypatch, tmp_path):
    monkeypatch.setattr(container, 'sys', SimpleNamespace(platform='win32', executable=sys.executable))
    monkeypatch.setattr(windows, 'start', Mock(side_effect=OSError('job assignment failed')))
    host = Mock(side_effect=AssertionError('Must not launch an unrestricted worker'))
    monkeypatch.setattr(container.subprocess, 'Popen', host)
    with pytest.raises(container.ContainerEnvironmentError, match='job assignment failed'):
        container.execute({}, tmp_path / 'worker.py', 1, label='test')
    host.assert_not_called()


@NATIVE_WINDOWS
def test_real_windows_statistics_and_three_plot_formats():
    # Go through both public executors, including their environment validation.
    result = generated_executor.execute(dict(
        tables={'data': {'rows': [[1.0], [3.0], [5.0]]}}, plan={}, seed=42,
        code='import numpy as np\n'
             'def analyze(tables, plan, rng):\n'
             '    values = np.array(tables["data"]["rows"])\n'
             '    return {"mean": float(values.mean()), "label": "均值"}\n'))
    assert result['results'] == {'mean': 3.0, 'label': '均值'}
    assert 'numpy' in result['versions']
    assert result['seed'] == 42
    figure = execute_plot(dict(table={}, statistics=result['results'], spec={}, data_sha256='windows-test',
                               code='import matplotlib.pyplot as plt\n'
                                    'def draw(data, statistics, spec):\n'
                                    '    fig, ax = plt.subplots()\n'
                                    '    ax.bar(["Mean"], [statistics["mean"]])\n'
                                    '    return fig\n'))
    assert figure['svg'].startswith('<svg')
    assert base64.b64decode(figure['exports']['png']).startswith(b'\x89PNG\r\n\x1a\n')
    assert base64.b64decode(figure['exports']['pdf']).startswith(b'%PDF-')


def _native_worker(tmp_path, code, payload=None, timeout=20):
    worker = tmp_path / 'trusted-worker.py'
    worker.write_text(code, encoding='utf-8')
    return container.execute(payload or {}, worker, timeout, label='Windows test')


@NATIVE_WINDOWS
def test_real_windows_isolation(tmp_path, monkeypatch):
    private = tmp_path / 'service-secret.txt'
    private.write_text('service credential', encoding='utf-8')
    monkeypatch.setenv('PAPER_TEST_CREDENTIAL', 'must not inherit')
    with socket.socket() as listener:
        listener.bind(('127.0.0.1', 0))
        listener.listen()
        result = _native_worker(tmp_path, '''import json, os, pathlib, socket, subprocess, sys
data = json.load(sys.stdin)
checks = {}
scratch = pathlib.Path.cwd() / 'local.txt'
scratch.write_text('private scratch', encoding='utf-8')
checks['scratch'] = scratch.read_text(encoding='utf-8')
checks['credential'] = os.environ.get('PAPER_TEST_CREDENTIAL')
checks['pythonpath'] = os.environ.get('PYTHONPATH')
for name, mode in [('read', 'r'), ('write', 'w')]:
    try:
        with open(data['private'], mode) as stream:
            if mode == 'r':
                stream.read()
        checks[name] = 'allowed'
    except PermissionError:
        checks[name] = 'denied'
try:
    with socket.create_connection(('127.0.0.1', data['port']), timeout=1):
        checks['network'] = 'allowed'
except OSError:
    checks['network'] = 'denied'
try:
    subprocess.run([sys.executable, '-c', 'pass'], check=True, timeout=5)
    checks['process'] = 'allowed'
except (OSError, subprocess.SubprocessError):
    checks['process'] = 'denied'
print(json.dumps(checks))
''', dict(private=str(private), port=listener.getsockname()[1]))
    assert result == dict(scratch='private scratch', credential=None, pythonpath=None,
                          read='denied', write='denied', network='denied', process='denied')
    assert private.read_text(encoding='utf-8') == 'service credential'


@NATIVE_WINDOWS
@pytest.mark.parametrize('failure', ['timeout', 'scratch', 'profile'])
def test_real_windows_failure_terminates_and_cleans(tmp_path, monkeypatch, failure):
    launch = windows._launch
    directories = []

    def capture(process, command, scratch, streams, environment, timeout):
        directories.extend((scratch.parent, process.storage[1]))
        environment['PAPER_TEST_TARGET'] = str(process.storage[1 if failure == 'profile' else 0] / 'large.bin')
        launch(process, command, scratch, streams, environment, timeout)

    monkeypatch.setattr(windows, '_launch', capture)
    # Trusted runner bypasses AST diagnostics: parent supervision must still apply.
    code = '''from numpy import memmap as mapped
import os, time
array = mapped(os.environ['PAPER_TEST_TARGET'], dtype='uint8', mode='w+', shape=(20 * 1024 ** 2,))
array[:] = 1
array.flush()
time.sleep(30)
'''
    if failure == 'timeout':
        code = 'import time; time.sleep(30)'
    with pytest.raises(RuntimeError, match='超时' if failure == 'timeout' else '临时文件超过'):
        _native_worker(tmp_path, code, timeout=1 if failure == 'timeout' else 20)
    assert directories and all(not root.exists() for root in directories)


@NATIVE_WINDOWS
def test_real_windows_desktop_interpreter_and_supervisor_job(tmp_path):
    environment = tmp_path / '桌面 runtime' / 'algorithm'
    base = Path(getattr(sys, '_base_executable', None) or sys.executable)
    subprocess.run([str(base), '-m', 'venv', '--without-pip', str(environment)], check=True, timeout=60)
    # Desktop normalization replaces the venv redirector with the real interpreter
    # and adjacent DLLs. Exercise that layout with its own private site-packages.
    for source in base.parent.iterdir():
        if source.is_file() and (source.name == 'python.exe' or source.suffix.lower() == '.dll'):
            shutil.copy2(source, environment / 'Scripts' / source.name)
    site = environment / 'Lib' / 'site-packages'
    (site / 'test-dependencies.pth').write_text(sysconfig.get_path('purelib') + '\n', encoding='utf-8')
    worker = tmp_path / 'desktop-worker.py'
    worker.write_text('import json, numpy\nprint(json.dumps({"mean": float(numpy.mean([1, 3, 5]))}))\n',
                      encoding='utf-8')
    controller = '''import json, sys
from pathlib import Path
from lazymind.document_tools.paper.execution import container, windows
windows._prepare_runtime()
api = windows._api
job = api.CreateJobObjectW(None, None)
api.AssignProcessToJobObject(job, api.GetCurrentProcess())
try:
    container.validate_environment()
    result = container.execute({}, Path(sys.argv[1]), 30, label='desktop')
    print(json.dumps({'prefix': sys.prefix, 'result': result}))
finally:
    api.CloseHandle(job)
'''
    completed = subprocess.run([str(environment / 'Scripts' / 'python.exe'), '-c', controller, str(worker)],
                               capture_output=True, text=True, encoding='utf-8', timeout=180)
    assert completed.returncode == 0, completed.stderr
    result = json.loads(completed.stdout.splitlines()[-1])
    assert Path(result['prefix']).resolve() == environment.resolve()
    assert result['result'] == {'mean': 3.0}
