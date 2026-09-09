"""A0 contracts that do not import the optional full algorithm dependency graph."""
from __future__ import annotations

import ast
import base64
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest


ROOT = Path(__file__).resolve().parents[3]
REGISTRY = ROOT / 'algorithm/lazymind/chat/service/component/tool_registry.py'
GUARD = ROOT / 'algorithm/lazymind/chat/engine/agent_runtime/tool_call_guard.py'
RUNNER = ROOT / 'algorithm/lazymind/chat/engine/subagent/runner.py'


def _source(path: Path) -> str:
    return path.read_text(encoding='utf-8')


def test_tool_config_declares_authorization_metadata():
    tree = ast.parse(_source(REGISTRY))
    tool_config = next(
        node for node in tree.body
        if isinstance(node, ast.ClassDef) and node.name == 'ToolConfig'
    )
    fields = {
        node.target.id
        for node in tool_config.body
        if isinstance(node, ast.AnnAssign) and isinstance(node.target, ast.Name)
    }
    assert 'authorization' in fields


def test_middleware_exposes_an_authorization_gate_before_manager_dispatch():
    source = _source(GUARD)
    tree = ast.parse(source)
    middleware = next(node for node in tree.body if isinstance(node, ast.ClassDef) and node.name == 'ToolExecutionMiddleware')
    init_node = next(node for node in middleware.body if isinstance(node, ast.FunctionDef) and node.name == '__init__')
    init = ast.get_source_segment(source, init_node)
    assert init is not None and 'authorization_gate' in init
    execute_pos = source.index('self._manager.execute_with_records(')
    gate_pos = source.index('authorization_gate', source.index('def execute_with_records'))
    assert gate_pos < execute_pos


def test_workflow_workspace_gate_rejects_bound_script_before_compilation():
    source = _source(RUNNER)
    function_start = source.index('def load_workflow_tools(')
    function_source = source[function_start:]
    gate_pos = function_source.find('_validate_workflow_workspace_package(')
    compile_pos = function_source.find('exec(compile(')
    assert gate_pos >= 0 and compile_pos >= 0 and gate_pos < compile_pos
    assert 'workflow_package_authorized' not in function_source

    helper_start = source.index('def _validate_workflow_workspace_package(')
    helper_end = source.index('\ndef load_workflow_tools(', helper_start)
    helper_source = source[helper_start:helper_end]
    namespace = {'Dict': dict, 'List': list}
    exec(compile(helper_source, str(RUNNER), 'exec'), namespace)

    with pytest.raises(RuntimeError, match='not admitted'):
        namespace['_validate_workflow_workspace_package'](
            {'workspace_context': {'workspace_id': 'workspace-1'}},
            ['declared_tool'],
            {'scripts/tools.py': 'encoded'},
        )
