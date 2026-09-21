from __future__ import annotations

import ast
import importlib.util
import json
import re
from pathlib import Path
from types import ModuleType, SimpleNamespace

import pytest
import yaml


_ROOT = Path(__file__).resolve().parents[3]
_SCRIPTS_PATH = _ROOT / "workflows" / "writer-workflow" / "scripts"
_TOOLS_PATH = _SCRIPTS_PATH / "tools.py"
_EXECUTION_PATH = _ROOT / "algorithm" / "lazymind" / "document_tools" / "execution.py"
_WORKFLOW_PATH = _SCRIPTS_PATH.parent / "workflow.yaml"


def _load_tools() -> ModuleType:
    spec = importlib.util.spec_from_file_location(
        "writer_workflow_tools_runtime_test", _TOOLS_PATH
    )
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_yaml_tools_keep_only_private_orchestration_and_shared_execution_adapters():
    tools_tree = ast.parse(_TOOLS_PATH.read_text(encoding="utf-8"))
    execution_tree = ast.parse(_EXECUTION_PATH.read_text(encoding="utf-8"))
    adapters = [
        node
        for node in tools_tree.body
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
        and node.name.startswith("writer_")
    ]
    tool_functions = {
        node.name: node
        for node in tools_tree.body
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
    }

    assert not any(
        isinstance(node, (ast.Import, ast.ImportFrom))
        and any(
            (alias.name if isinstance(node, ast.Import) else node.module or "").startswith(
                "lazyllm"
            )
            for alias in node.names
        )
        for node in tools_tree.body
    )
    workflow_imports = {
        node.module or ""
        for node in tools_tree.body
        if isinstance(node, ast.ImportFrom)
        and (node.module or "").startswith(("lazyllm", "lazymind"))
    }
    assert workflow_imports <= {
        "lazymind.chat.engine.subagent.context",
        "lazymind.document_tools",
        "lazymind.document_tools.artifacts",
        "lazymind.document_tools.resources",
        "lazymind.document_tools.revision",
        "lazymind.document_tools.writing",
    }
    assert not any(
        isinstance(node, ast.ImportFrom)
        and (node.module or "").startswith(
            ("lazymind.chat", "workflows", "writer_workflow")
        )
        for node in execution_tree.body
    )
    locally_owned = {
        "writer_classify_structure",
        "writer_resolve_command",
        "writer_prepare_workspace",
        "writer_outline_workspace",
        "writer_draft_workspace",
        "writer_flat_draft_workspace",
    }
    for adapter in adapters:
        if adapter.name in locally_owned:
            continue
        statements = adapter.body[1:] if ast.get_docstring(adapter) else adapter.body
        assert len(statements) == 1 and isinstance(statements[0], ast.Return)
        calls = [node for node in ast.walk(adapter) if isinstance(node, ast.Call)]
        assert len(calls) == 3  # invoke(...) plus globals() and locals()
        invoke_call = next(call for call in calls if isinstance(call.func, ast.Attribute))
        assert isinstance(invoke_call.func.value, ast.Name)
        assert invoke_call.func.value.id == "_DOCUMENT_EXECUTION"
        assert invoke_call.func.attr == "invoke"

    workflow_text = _WORKFLOW_PATH.read_text(encoding="utf-8")
    workflow = yaml.safe_load(workflow_text)
    declared_tools = set(
        re.findall(
            r"\bwriter_[a-z_]+\b",
            workflow_text.split("tool_scripts:", 1)[1].split("artifact_actions:", 1)[0],
        )
    )
    assert declared_tools <= {adapter.name for adapter in adapters}
    assert workflow["artifact_actions"] == {
        "rewrite_selection": {
            "slots": ["outline_document", "flat_draft_document", "draft_document"],
            "preview_tool": "builtin:document.rewrite_selection.v1",
            "execute_tool": "builtin:document.rewrite_selection.v1",
        },
        "sync_document": {
            "slots": ["draft_document"],
            "execute_tool": "builtin:document.sync_document.v1",
        },
        "convert_document": {
            "slots": ["source_document", "outline_document", "flat_draft_document", "draft_document"],
            "preview_tool": "builtin:document.convert_document.v1",
            "execute_tool": "builtin:document.convert_document.v1",
        },
        "write_document": {
            "slots": ["flat_draft_document", "draft_document"],
            "execute_tool": "builtin:document.write_document.v1",
        },
        "render_document": {
            "slots": ["source_document", "outline_document", "flat_draft_document", "draft_document"],
            "preview_tool": "builtin:document.render_document.v1",
            "execute_tool": "builtin:document.render_document.v1",
        },
        "save_document": {
            "slots": ["outline_document", "flat_draft_document", "draft_document"],
            "execute_tool": "builtin:document.save_document.v1",
        },
    }

    assert not (_SCRIPTS_PATH / "runtime.py").exists()
    assert not (_SCRIPTS_PATH / "orchestration.py").exists()
    assert not (_SCRIPTS_PATH / "state.py").exists()


def test_authoritative_step_inputs_reject_unbound_agent_paths(tmp_path):
    runtime = _load_tools()
    context = SimpleNamespace(
        params={"step_id": "write_document", "remote_inputs": {}},
        workspace_path=str(tmp_path),
    )

    assert runtime._state_authoritative_input_path(context, "optional", "/guessed") == ""
    with pytest.raises(ValueError, match="required is missing"):
        runtime._state_authoritative_input_path(
            context,
            "required",
            "/guessed",
            require_workflow_binding=True,
        )


def test_workspace_checkpoint_round_trip_and_stale_fingerprint(tmp_path):
    runtime = _load_tools()
    context = SimpleNamespace(params={}, workspace_path=str(tmp_path))
    fingerprint = runtime._state_workspace_fingerprint(operation="generate", source="a")
    state, path = runtime._state_load_workspace_state(context, "draft", fingerprint)
    assert path is not None
    assert state == {
        "schema_version": 1,
        "fingerprint": fingerprint,
        "result": {},
        "completed": False,
    }

    state["result"] = {"draft_document": "/tmp/draft.lmd"}
    runtime._state_persist_workspace_state(state, path, completed=True)
    restored, restored_path = runtime._state_load_workspace_state(
        context, "draft", fingerprint
    )
    assert restored_path == path
    assert restored["completed"] is True
    assert restored["result"] == {"draft_document": "/tmp/draft.lmd"}
    assert not list(path.parent.glob(f".{path.name}.*.tmp"))

    path.write_text(
        json.dumps({"fingerprint": "stale", "result": {"wrong": True}}),
        encoding="utf-8",
    )
    fresh, _ = runtime._state_load_workspace_state(context, "draft", fingerprint)
    assert fresh["fingerprint"] == fingerprint
    assert fresh["result"] == {}
    assert fresh["completed"] is False


def test_corrupt_checkpoint_fails_closed_to_fresh_state(tmp_path):
    runtime = _load_tools()
    context = SimpleNamespace(params={}, workspace_path=str(tmp_path))
    fingerprint = runtime._state_request_fingerprint(" write   this ")
    _, path = runtime._state_load_workspace_state(context, "outline", fingerprint)
    assert path is not None
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("{broken", encoding="utf-8")

    restored, _ = runtime._state_load_workspace_state(context, "outline", fingerprint)
    assert restored["fingerprint"] == fingerprint
    assert restored["result"] == {}
    assert restored["completed"] is False
