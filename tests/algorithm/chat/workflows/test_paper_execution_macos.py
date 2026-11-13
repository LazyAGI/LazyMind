"""Core macOS sandbox failure, resource-limit and cleanup regressions."""
import sys

import pytest

from lazymind.document_tools.paper.execution import container, macos


NATIVE_MACOS = pytest.mark.skipif(sys.platform != 'darwin', reason='Requires macOS Seatbelt')


@NATIVE_MACOS
@pytest.mark.parametrize('failure', ['timeout', 'storage', 'memory', 'profile'])
def test_macos_failure_terminates_and_cleans(monkeypatch, tmp_path, failure):
    start = macos.start
    children = []

    def capture(worker, scratch, timeout, streams, env):
        process = start(worker, scratch, timeout, streams, env)
        children.append((process, scratch.parent))
        if failure == 'storage':
            process.wait(timeout=5)  # Enforce storage even after a fast worker exits.
        return process

    monkeypatch.setattr(macos, 'start', capture)
    code = 'import time; time.sleep(30)'
    error = '超时'
    if failure == 'storage':
        monkeypatch.setattr(macos, 'STORAGE_LIMIT', 1024)
        code = 'from pathlib import Path\nPath("one").write_bytes(b"x" * 768)\n' \
               'Path("two").write_bytes(b"x" * 768)\nprint("{}")\n'
        error = '临时存储合计'
    elif failure == 'memory':
        monkeypatch.setattr(macos, 'MEMORY_LIMIT', 32 * 1024 ** 2)
        code = 'import time\ndata = bytearray(64 * 1024 ** 2)\ntime.sleep(30)\n'
        error = '内存超过'
    elif failure == 'profile':
        monkeypatch.setattr(macos, '_profile', lambda _: '(version 1)\n(invalid-operation)')
        marker = tmp_path / 'must-not-execute'
        code = f'from pathlib import Path\nPath({str(marker)!r}).write_text("executed")\nprint("{{}}")\n'
        error = '沙箱启动失败'
    worker = tmp_path / 'worker.py'
    worker.write_text(code, encoding='utf-8')
    with pytest.raises(RuntimeError, match=error):
        container.execute({}, worker, 1 if failure == 'timeout' else 10, label='macOS test')
    assert children and all(process.poll() is not None and not root.exists() for process, root in children)
    if failure == 'profile':
        assert not marker.exists()
