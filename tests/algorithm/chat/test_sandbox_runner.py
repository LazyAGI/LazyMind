from __future__ import annotations

import base64
import hashlib
import io
import shutil
import ssl
import tarfile
import tempfile
import threading
import time
from pathlib import Path

import pytest

from lazymind.sandbox_runner.client import SandboxRunnerClient
from lazymind.sandbox_runner.egress_proxy import EgressPolicyStore, _domain_allowed, _tls_server_name
from lazymind.sandbox_runner.server import FIXED_PATH, SandboxExecutionEngine, ThreadingUnixServer


def _skill(tmp_path, script_name='write.sh', script='printf "bash-ok\\n"\nprintf "shared" > state.txt\n'):
    skill = tmp_path / 'skill'
    (skill / 'scripts').mkdir(parents=True, exist_ok=True)
    (skill / 'SKILL.md').write_text('---\nname: demo\ndescription: demo\n---\n', encoding='utf-8')
    (skill / 'scripts' / script_name).write_text(script, encoding='utf-8')
    return skill


def _request(client, skill, **updates):
    bundle = client._bundle(str(skill))
    request = {
        'version': 1,
        'action': 'execute',
        'request_id': 'test-request',
        'session_id': 'test-session',
        'mode': 'script',
        'runtime': 'bash',
        'entrypoint': 'scripts/write.sh',
        'argv': [],
        'args': [],
        'cwd': '.',
        'bundle': bundle,
        'tree_hash': bundle['sha256'],
        'input_files': [],
        'declared_outputs': [],
        'env': {},
        'policy': {},
        'network': {'mode': 'deny'},
        'limits': {
            'timeout_seconds': 5,
            'memory_mb': 512,
            'cpu_seconds': 5,
            'pids': 16,
            'output_bytes': 65536,
        },
    }
    request.update(updates)
    if 'runtime' not in updates:
        request['runtime'] = 'cli' if request['mode'] == 'command' else {
            '.py': 'python', '.sh': 'bash', '.bash': 'bash',
            '.js': 'node', '.cjs': 'node', '.mjs': 'node',
        }.get(Path(str(request.get('entrypoint') or '')).suffix.lower(), 'unsupported')
    if 'network' not in updates:
        request['network'] = {
            'mode': 'domain_allowlist' if (request.get('policy') or {}).get('allowed_domains') else 'deny',
        }
    return request


def test_default_limits_use_180_second_wall_timeout_and_60_second_cpu_limit(tmp_path):
    client = SandboxRunnerClient(str(tmp_path / 'unused.sock'))

    assert client.timeout == 180
    assert client.cpu_seconds == 60
    assert SandboxExecutionEngine._limits({}) == (180, 1024, 60, 64, 1024 * 1024)


@pytest.fixture
def runner(tmp_path):
    client = SandboxRunnerClient(str(tmp_path / 'unused.sock'), timeout=5)
    engine = SandboxExecutionEngine(
        str(tmp_path / 'sessions'),
        sandbox_uid_base=None,
        session_ttl_seconds=60,
    )
    return client, engine


def test_bash_cli_and_python_share_one_session_workspace(tmp_path, runner):
    client, engine = runner
    skill = _skill(
        tmp_path,
        script='python3 -c \'from pathlib import Path; Path("state.txt").write_text("mixed-ok")\'\n'
               'printf "bash-ok\\n"\n',
    )

    first = engine.execute(_request(client, skill))
    second = engine.execute(_request(
        client,
        skill,
        mode='command',
        argv=['cat', 'state.txt'],
        entrypoint=None,
    ))

    assert first['status'] == 'ok', first
    assert first['stdout'] == 'bash-ok\n'
    assert second['status'] == 'ok', second
    assert second['stdout'] == 'mixed-ok'


def test_declared_outputs_are_exported_and_undeclared_files_stay_private(tmp_path, runner):
    client, engine = runner
    skill = _skill(
        tmp_path,
        script='printf "public" > report.txt\nprintf "private" > secret.txt\n',
    )

    result = engine.execute(_request(
        client, skill, declared_outputs=['report.txt'],
    ))

    assert result['status'] == 'ok', result
    assert [item['path'] for item in result['output_files']] == ['report.txt']
    assert base64.b64decode(result['output_files'][0]['data']) == b'public'
    assert all(item['path'] != 'secret.txt' for item in result['output_files'])


def test_declared_output_symlink_is_not_exported(tmp_path, runner):
    client, engine = runner
    skill = _skill(tmp_path, script='ln -s /etc/passwd leak.txt\n')

    result = engine.execute(_request(
        client, skill, declared_outputs=['leak.txt'],
    ))

    assert result['error_type'] == 'FILESYSTEM_DENIED'
    assert result['blocked_resource'] == 'leak.txt'


def test_runner_reports_policy_and_dependency_failures(tmp_path, runner):
    client, engine = runner
    skill = _skill(tmp_path)

    denied = engine.execute(_request(client, skill, mode='command', argv=['sudo', 'true']))
    missing = engine.execute(_request(
        client,
        skill,
        mode='command',
        argv=['cat', 'SKILL.md'],
        policy={'required_bins': ['definitely-missing-cli']},
    ))
    missing_env = engine.execute(_request(
        client,
        skill,
        policy={'required_env': ['SERVICE_TOKEN']},
    ))
    network = engine.execute(_request(
        client,
        skill,
        policy={'allowed_domains': ['api.example.com']},
    ))
    invalid_network_mode = engine.execute(_request(
        client,
        skill,
        network={'mode': 'allow'},
    ))

    assert denied['error_type'] == 'COMMAND_DENIED'
    assert missing['error_type'] == 'COMMAND_DENIED'
    assert missing_env['error_type'] == 'MISSING_ENV'
    assert missing_env['missing_env'] == ['SERVICE_TOKEN']
    assert network['error_type'] == 'NETWORK_DENIED'
    assert invalid_network_mode['error_type'] == 'NETWORK_DENIED'


def test_allowed_but_uninstalled_cli_reports_command_not_found(tmp_path):
    command = 'definitely-missing-cli'
    client = SandboxRunnerClient(str(tmp_path / 'unused.sock'), timeout=5)
    engine = SandboxExecutionEngine(
        str(tmp_path / 'sessions'),
        allowed_commands={command},
        sandbox_uid_base=None,
        session_ttl_seconds=60,
    )
    skill = _skill(tmp_path)

    result = engine.execute(_request(
        client,
        skill,
        mode='command',
        argv=[command, '--version'],
        entrypoint=None,
        policy={'required_bins': [command]},
    ))

    assert result['error_type'] == 'COMMAND_NOT_FOUND'
    assert result['dependency'] == command


@pytest.mark.parametrize('command', ['sudo', 'apt', 'brew', 'docker', 'mount', 'systemctl', 'ssh'])
def test_privileged_and_tunneling_commands_are_denied(tmp_path, runner, command):
    client, engine = runner
    skill = _skill(tmp_path)

    result = engine.execute(_request(
        client, skill, mode='command', argv=[command, '--version'],
    ))

    assert result['error_type'] == 'COMMAND_DENIED'
    assert result['blocked_resource'] == command


def test_runner_only_exposes_declared_environment_variables(tmp_path, runner):
    client, engine = runner
    skill = _skill(
        tmp_path,
        script='printf "%s:%s" "$SERVICE_TOKEN" "${UNDECLARED-unset}"\n',
    )
    result = engine.execute(_request(
        client,
        skill,
        env={'SERVICE_TOKEN': 'allowed', 'UNDECLARED': 'secret'},
        policy={'required_env': ['SERVICE_TOKEN']},
    ))
    assert result['status'] == 'ok', result
    assert result['stdout'] == 'allowed:unset'


def test_runner_rejects_inline_shell_and_cwd_symlink_escape(tmp_path, runner):
    client, engine = runner
    skill = _skill(tmp_path)
    inline = engine.execute(_request(
        client,
        skill,
        mode='command',
        argv=['bash', '-c', 'touch escaped'],
    ))
    assert inline['error_type'] == 'COMMAND_DENIED'
    absolute = engine.execute(_request(
        client,
        skill,
        mode='command',
        argv=['cat', '/etc/passwd'],
    ))
    assert absolute['error_type'] == 'FILESYSTEM_DENIED'
    assert absolute['blocked_resource'] == '/etc/passwd'

    initial = engine.execute(_request(client, skill))
    assert initial['status'] == 'ok'
    bundle_sha = client._bundle(str(skill))['sha256']
    session_key = hashlib.sha256(b'test-session').hexdigest()
    workspace = tmp_path / 'sessions' / session_key / bundle_sha / 'workspace'
    (workspace / 'outside').symlink_to(tmp_path, target_is_directory=True)
    escaped = engine.execute(_request(client, skill, cwd='outside'))
    assert escaped['error_type'] == 'FILESYSTEM_DENIED'


def test_runner_rejects_archive_traversal(tmp_path, runner):
    client, engine = runner
    payload = io.BytesIO()
    with tarfile.open(fileobj=payload, mode='w:gz') as archive:
        body = b'bad'
        member = tarfile.TarInfo('../escape')
        member.size = len(body)
        archive.addfile(member, io.BytesIO(body))
    data = payload.getvalue()
    skill = _skill(tmp_path)
    request = _request(client, skill)
    request['bundle'] = {
        'format': 'tar.gz+base64',
        'sha256': hashlib.sha256(data).hexdigest(),
        'data': base64.b64encode(data).decode('ascii'),
    }

    result = engine.execute(request)

    assert result['error_type'] == 'FILESYSTEM_DENIED'
    assert not (tmp_path / 'escape').exists()


def test_runner_enforces_timeout_and_output_limit(tmp_path, runner):
    client, engine = runner
    timeout_skill = _skill(tmp_path, script='sleep 5\n')
    timed_out = engine.execute(_request(
        client,
        timeout_skill,
        limits={
            'timeout_seconds': 1, 'memory_mb': 512, 'cpu_seconds': 5,
            'pids': 16, 'output_bytes': 65536,
        },
    ))
    assert timed_out['error_type'] == 'TIMEOUT'
    assert timed_out['duration_ms'] >= 900

    output_skill = _skill(tmp_path, script='python3 -c \'print("x" * 5000)\'\n')
    too_much = engine.execute(_request(
        client,
        output_skill,
        session_id='output-session',
        limits={
            'timeout_seconds': 5, 'memory_mb': 512, 'cpu_seconds': 5,
            'pids': 16, 'output_bytes': 1024,
        },
    ))
    assert too_much['error_type'] == 'OUTPUT_LIMIT'


@pytest.mark.parametrize('stderr', [
    'Traceback (most recent call last):\nMemoryError\n',
    'bash: fork: retry: Resource temporarily unavailable\n',
    'sh: cannot fork\n',
])
def test_runner_classifies_memory_and_pid_failures_as_resource_limits(stderr):
    assert SandboxExecutionEngine._process_error_type(1, stderr.lower()) == 'RESOURCE_LIMIT'


def test_runner_preserves_network_and_ordinary_nonzero_classification():
    assert SandboxExecutionEngine._process_error_type(
        1, 'curl: could not resolve host: example.invalid',
    ) == 'NETWORK_DENIED'
    assert SandboxExecutionEngine._process_error_type(2, 'application validation failed') == 'EXIT_NON_ZERO'


def test_runner_kills_background_children_after_command_returns(tmp_path, runner):
    client, engine = runner
    skill = _skill(
        tmp_path,
        script='(sleep 1; printf "escaped" > survived.txt) &\nprintf "parent-done\\n"\n',
    )

    result = engine.execute(_request(client, skill))
    assert result['status'] == 'ok', result
    bundle_sha = client._bundle(str(skill))['sha256']
    session_key = hashlib.sha256(b'test-session').hexdigest()
    workspace = tmp_path / 'sessions' / session_key / bundle_sha / 'workspace'
    time.sleep(1.2)
    assert not (workspace / 'survived.txt').exists()


def test_unix_socket_transport_and_unavailable_error(tmp_path):
    socket_root = Path(tempfile.mkdtemp(prefix='lmsock-', dir='/private/tmp'))
    socket_path = socket_root / 'runner.sock'
    engine = SandboxExecutionEngine(str(tmp_path / 'sessions'), sandbox_uid_base=None)
    try:
        server = ThreadingUnixServer(str(socket_path), engine)
    except PermissionError:
        shutil.rmtree(socket_root)
        pytest.skip('The test sandbox forbids binding Unix domain sockets')
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        client = SandboxRunnerClient(str(socket_path), timeout=5)
        assert client.ping() == {'status': 'ok', 'version': 1}
        skill = _skill(tmp_path)
        result = client.execute_script(str(skill), 'scripts/write.sh', session_id='socket-session')
        assert result['status'] == 'ok', result
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)

    missing = SandboxRunnerClient(str(tmp_path / 'missing.sock'), timeout=1).ping()
    assert missing['error_type'] == 'SANDBOX_UNAVAILABLE'
    shutil.rmtree(socket_root)


def test_client_rejects_skill_symlinks(tmp_path):
    skill = _skill(tmp_path)
    (skill / 'linked').symlink_to(skill / 'SKILL.md')
    client = SandboxRunnerClient(str(tmp_path / 'unused.sock'))

    result = client.execute_script(str(skill), 'scripts/write.sh')

    assert result['error_type'] == 'FILESYSTEM_DENIED'


def test_runner_capabilities_preflight_cancel_and_release(tmp_path, runner):
    client, engine = runner
    skill = _skill(tmp_path, script='sleep 30\n')
    capabilities = engine.capabilities()
    assert capabilities['supports']['preflight'] is True
    assert capabilities['supports']['declared_outputs'] is True
    assert capabilities['runtimes']['bash'] is True

    request = _request(client, skill, request_id='cancel-me', session_id='release-me')
    assert engine.preflight(request)['status'] == 'ok'
    holder = {}
    thread = threading.Thread(target=lambda: holder.setdefault('result', engine.execute(request)))
    thread.start()
    deadline = time.time() + 5
    while time.time() < deadline:
        with engine._state_lock:
            if 'cancel-me' in engine._active_processes:
                break
        time.sleep(0.01)
    assert engine.cancel('cancel-me')['status'] == 'ok'
    thread.join(timeout=5)
    assert not thread.is_alive()
    assert holder['result']['error_type'] == 'CANCELLED'
    assert engine.release_session('release-me')['status'] == 'ok'
    session_key = hashlib.sha256(b'release-me').hexdigest()
    assert not (tmp_path / 'sessions' / session_key).exists()


def test_concurrent_sessions_do_not_share_workspace(tmp_path, runner):
    client, engine = runner
    skill = _skill(tmp_path, script='printf "%s" "$1" > owner.txt\n')
    results = {}

    def run(session):
        results[session] = engine.execute(_request(
            client, skill, request_id=f'request-{session}', session_id=session, argv=[session],
        ))

    threads = [threading.Thread(target=run, args=(session,)) for session in ('alpha', 'beta')]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(timeout=5)
    assert all(result['status'] == 'ok' for result in results.values())
    for session in ('alpha', 'beta'):
        read = engine.execute(_request(
            client, skill, session_id=session, mode='command', argv=['cat', 'owner.txt'],
        ))
        assert read['stdout'] == session


def test_egress_policy_requires_registered_domain_and_rejects_ips():
    store = EgressPolicyStore()
    store.register('deny-token', [], 60)
    assert store.registered('deny-token') is True
    assert store.domains('deny-token') == ()
    store.register('token', ['api.example.com', '*.allowed.example'], 60)
    assert store.domains('token') == ('api.example.com', '*.allowed.example')
    assert _domain_allowed('api.example.com', store.domains('token'))
    assert _domain_allowed('sub.allowed.example', store.domains('token'))
    assert not _domain_allowed('allowed.example', store.domains('token'))
    assert not _domain_allowed('127.0.0.1', store.domains('token'))
    assert not _domain_allowed('evil.example', store.domains('token'))
    store.record_violation('token', 'evil.example', 'Destination is not allowed')
    assert store.violation('token') == {
        'blocked_resource': 'evil.example',
        'reason': 'Destination is not allowed',
    }
    store.unregister('token')
    assert store.domains('token') == ()
    assert store.violation('token') is None


def test_egress_proxy_reads_and_pins_tls_server_name():
    incoming = ssl.MemoryBIO()
    outgoing = ssl.MemoryBIO()
    tls = ssl.create_default_context().wrap_bio(
        incoming, outgoing, server_side=False, server_hostname='api.example.com',
    )
    with pytest.raises(ssl.SSLWantReadError):
        tls.do_handshake()
    client_hello = outgoing.read()
    record_size = 5 + int.from_bytes(client_hello[3:5], 'big')

    assert _tls_server_name(client_hello[:record_size]) == 'api.example.com'


@pytest.mark.skipif(not shutil.which('node', path=FIXED_PATH), reason='Node.js is not installed on runner PATH')
def test_javascript_runtime_can_coexist_with_bash(tmp_path, runner):
    client, engine = runner
    skill = _skill(tmp_path, script_name='check.js', script='console.log("node-ok")\n')
    result = engine.execute(_request(
        client,
        skill,
        entrypoint='scripts/check.js',
    ))
    assert result['status'] == 'ok', result
    assert result['stdout'] == 'node-ok\n'
