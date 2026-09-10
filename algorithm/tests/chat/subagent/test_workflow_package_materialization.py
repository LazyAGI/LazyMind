import base64
import os
from concurrent.futures import ThreadPoolExecutor
from threading import Barrier

from lazymind.workflow_toolkit import _materialize_workflow_package


def test_concurrent_materialization_keeps_independent_temporary_files(monkeypatch, tmp_path):
    monkeypatch.setattr('tempfile.gettempdir', lambda: str(tmp_path))
    ready = Barrier(2)
    replace = os.replace

    def concurrent_replace(source, destination):
        ready.wait(timeout=5)
        replace(source, destination)

    monkeypatch.setattr(os, 'replace', concurrent_replace)
    files = {'scripts/tools.py': b'def run(): return "pinned"\n'}
    with ThreadPoolExecutor(max_workers=2) as pool:
        first, second = [pool.submit(_materialize_workflow_package, 'workflow', 'revision', 'abc', files)
                         for _ in range(2)]
        root = first.result()
        assert second.result() == root
    assert (root / 'scripts/tools.py').read_bytes() == files['scripts/tools.py']
    assert not list(root.rglob('*.tmp'))


def test_materialize_workflow_package_preserves_sibling_runtime(monkeypatch, tmp_path):
    monkeypatch.setattr('tempfile.gettempdir', lambda: str(tmp_path))
    files = {
        'scripts/tools.py': base64.b64encode(
            b'from pathlib import Path\nRUNTIME = Path(__file__).resolve().parents[1] / "runtime"\n'
        ).decode(),
        'runtime/scripts/run_stage.py': base64.b64encode(b'print("ok")\n').decode(),
        'runtime/lib/__init__.py': None,
    }

    root = _materialize_workflow_package('ppt-workflow', 'revision-1', 'abc123', files)

    assert (root / 'scripts/tools.py').is_file()
    assert (root / 'runtime/scripts/run_stage.py').read_text() == 'print("ok")\n'
    assert (root / 'runtime/lib/__init__.py').read_bytes() == b''


def test_materialize_workflow_package_rejects_path_traversal(monkeypatch, tmp_path):
    monkeypatch.setattr('tempfile.gettempdir', lambda: str(tmp_path))

    try:
        _materialize_workflow_package(
            'ppt-workflow', 'revision-1', 'abc123', {'../escape.py': b'bad'},
        )
    except RuntimeError as exc:
        assert 'unsafe Workflow package path' in str(exc)
    else:
        raise AssertionError('path traversal must be rejected')
