import base64

from lazymind.chat.engine.subagent.runner import _materialize_workflow_package


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
