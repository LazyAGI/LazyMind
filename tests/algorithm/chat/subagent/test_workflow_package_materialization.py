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
    except RuntimeError:
        pass
    else:
        raise AssertionError('path traversal must be rejected')


def test_runner_loads_only_requested_declared_scripts(monkeypatch, tmp_path):
    from types import SimpleNamespace
    from lazymind.chat.engine.subagent.runner import load_workflow_tools
    from lazymind.workflow_sdk import WorkflowClient

    monkeypatch.setattr('tempfile.gettempdir', lambda: str(tmp_path))
    package = {'revision_id': 'revision-1', 'tree_hash': 'abc123', 'files': {
        'workflow.yaml': b'''tool_scripts:
  - path: scripts/a_unrequested.py
    functions: [another_tool]
  - path: scripts/tools.py
    functions: [selected_tool]
''',
        'scripts/a_unrequested.py': b'raise AssertionError("unrequested script executed")',
        'scripts/helpers.py': b'raise AssertionError("undeclared script executed")',
        'scripts/tests/test_tool.py': b'raise AssertionError("test script executed")',
        'scripts/tools.py': b'''from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
def selected_tool():
    return (ROOT / 'runtime/data.txt').read_text()
def hidden_tool():
    return 'not declared'
''',
        'runtime/data.txt': b'runtime asset',
    }}
    monkeypatch.setattr(WorkflowClient, 'get_workflow', lambda *args: SimpleNamespace(result=package))
    loaded = load_workflow_tools({
        'workflow_id': 'renamed-workflow', 'revision_id': 'revision-1', 'tree_hash': 'abc123',
    }, ['selected_tool', 'hidden_tool'])
    assert set(loaded) == {'selected_tool'}
    assert loaded['selected_tool']() == 'runtime asset'
    assert loaded['selected_tool'].__doc__


def test_runner_loads_ppt_scripts_without_replacing_live_modules(monkeypatch, tmp_path):
    import sys
    from pathlib import Path
    from types import SimpleNamespace
    import lazyllm
    import lazymind.model_config
    from lazymind.chat.engine.subagent.runner import load_workflow_tools
    from lazymind.workflow_sdk import WorkflowClient

    root = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow'
    files = {
        path.relative_to(root).as_posix(): path.read_bytes()
        for path in (root / 'scripts').rglob('*.py')
    }
    files['workflow.yaml'] = (root / 'workflow.yaml').read_bytes()
    package = {'revision_id': 'revision-ppt', 'files': files}
    original_modules = {name: module for name, module in sys.modules.items()
                        if name == 'lazyllm' or name.startswith('lazymind')}
    monkeypatch.setattr('tempfile.gettempdir', lambda: str(tmp_path))
    monkeypatch.setattr(WorkflowClient, 'get_workflow', lambda *args: SimpleNamespace(result=package))
    try:
        loaded = load_workflow_tools({
            'workflow_id': 'renamed-ppt', 'revision_id': 'revision-ppt',
        }, ['ppt_prepare_style_choices'])
        assert callable(loaded['ppt_prepare_style_choices'])
        assert all(sys.modules[name] is module for name, module in original_modules.items())
        assert hasattr(lazyllm, 'globals')
        from lazyllm import LOG
        assert LOG is not None
    finally:
        # Keep a regression failure from corrupting the rest of the test process.
        sys.modules.update(original_modules)
