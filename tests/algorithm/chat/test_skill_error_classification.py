import pytest

from lazyllm.tools.agent import ToolManager
from lazyllm.tools.agent.toolError import ToolExecutionError
from lazymind.chat.engine.agent_runtime.skill_errors import classify_skill_failure
from lazymind.chat.engine.agent_runtime import telemetry
from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
from lazymind.chat.engine.agent_runtime.tool_call_guard import _summarize_tool_result
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext


@pytest.mark.parametrize('message,expected', [
    ('Required runtime not found: node', {'error_type': 'missing_dependency', 'dependency': 'node'}),
    ('Unsupported Skill script extension.', {'error_type': 'unsupported_runtime'}),
    ("Skill script execution failed with exit code 1: Traceback:\nModuleNotFoundError: No module named 'pandas'",
     {'error_type': 'missing_dependency', 'dependency': 'pandas', 'exit_code': 1}),
    ("Skill script execution failed with exit code 1: Error: Cannot find module 'sharp'",
     {'error_type': 'missing_dependency', 'dependency': 'sharp', 'exit_code': 1}),
    ("Error [ERR_MODULE_NOT_FOUND]: Cannot find package '@org/tool' imported from /tmp/test.mjs",
     {'error_type': 'missing_dependency', 'dependency': '@org/tool'}),
    ("Skill script execution failed with exit code 1: Error: Cannot find module './missing.js'",
     {'error_type': 'script_failed', 'exit_code': 1}),
    (r"Skill script execution failed with exit code 1: Error: Cannot find module 'C:\skills\missing.js'",
     {'error_type': 'script_failed', 'exit_code': 1}),
    (r"Skill script execution failed with exit code 1: Error: Cannot find module '\\server\share\missing.js'",
     {'error_type': 'script_failed', 'exit_code': 1}),
    ('Skill script execution failed with exit code 2: SyntaxError: bad syntax',
     {'error_type': 'script_failed', 'exit_code': 2}),
    ('429 Too Many Requests', {'error_type': 'unknown'}),
    ('permission denied', {'error_type': 'unknown'}),
    ('please install pandas', {'error_type': 'unknown'}),
])
def test_failure_hints_preserve_original(message, expected):
    original = {'ok': False, 'value': message}
    assert classify_skill_failure(original) == {**original, **expected}
    assert original == {'ok': False, 'value': message}


@pytest.mark.parametrize('result', [
    {'ok': True, 'value': 'Required runtime not found: node'},
    {'ok': False, 'value': 'failed', 'needs_approval': {'reason': 'confirm'}},
    {'ok': False, 'value': 'failed', 'error_type': 'timeout'},
    {'ok': False, 'value': {'error': 'nested'}},
    'not a tool failure',
])
def test_do_not_override_existing_contract(result):
    assert classify_skill_failure(result) is result


def test_missing_env_takes_priority():
    result = {'ok': False, 'value': 'Required runtime not found: node', 'missing_env': ['SERVICE_KEY']}
    assert classify_skill_failure(result) == {**result, 'error_type': 'missing_env'}


def test_long_script_error_keeps_diagnostics_in_backend_log_summary():
    message = ('Skill script execution failed with exit code 1: '
               + 'traceback line\n' * 100
               + "ModuleNotFoundError: No module named 'plotly'")
    result = classify_skill_failure({'ok': False, 'value': message})
    assert _summarize_tool_result(result) == {
        'ok': False, 'error_type': 'missing_dependency', 'dependency': 'plotly', 'exit_code': '1',
    }


def test_long_script_error_keeps_diagnostics_as_independent_telemetry_fields(monkeypatch):
    message = ('Skill script execution failed with exit code 1: '
               + 'traceback line\n' * 100
               + "ModuleNotFoundError: No module named 'plotly'")
    result = classify_skill_failure({'ok': False, 'value': message})
    emitted = []
    monkeypatch.setattr(telemetry, 'telemetry_enabled', lambda: True)
    monkeypatch.setattr(telemetry, 'append_event', lambda kind, **payload: emitted.append((kind, payload)))
    telemetry.emit_tool_result(
        {'id': 'script-1', 'function': {'name': 'run_skill_script'}}, result,
    )
    kind, event = emitted[0]
    assert kind == 'tool_result'
    assert event['error_type'] == 'missing_dependency'
    assert event['dependency'] == 'plotly'
    assert event['exit_code'] == 1
    assert len(event['result_preview']) < len(result['value'])


@pytest.mark.parametrize('trusted', [True, False])
def test_real_tool_exception_serialization_reaches_results_records_and_telemetry(monkeypatch, trusted):
    def run_skill_script(name: str) -> str:
        """Run a script.

        Args:
            name (str): Skill name.
        """
        raise ToolExecutionError('Skill script execution failed with exit code 1: '
                                 "ModuleNotFoundError: No module named 'pandas'")

    manager = ToolManager([run_skill_script])
    emitted = []
    monkeypatch.setattr('lazymind.chat.engine.agent_runtime.tool_call_guard.emit_tool_result',
                        lambda call, result: emitted.append(result))
    middleware = ToolExecutionMiddleware(
        manager, workspace_permission=WorkspaceContext(local_runtime=False),
        trusted_opaque_tools=[manager.tools_info['run_skill_script']] if trusted else [],
    )
    batch = middleware.execute_with_records({
        'id': 'script-1', 'function': {'name': 'run_skill_script', 'arguments': {'name': 'demo'}},
    })
    result = batch.results[0]
    assert result['ok'] is False
    assert result == batch.records[0].result == emitted[0]
    if trusted:
        assert result['error_type'] == 'missing_dependency'
        assert result['dependency'] == 'pandas'
        assert result['exit_code'] == 1
    else:
        assert 'error_type' not in result
