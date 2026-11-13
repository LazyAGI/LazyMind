from unittest.mock import MagicMock

import pytest
from lazyllm.tools.agent.functionCall import _tool_result_stop_text

from lazymind.chat.workflow import workflow_manager as manager
from lazymind.workflow_sdk import WorkflowClient, WorkflowClientError
from lazymind.workflow_toolkit import HostWorkflowToolkit


def session_tools(monkeypatch, display_status, workflow_id='ppt-workflow'):
    client = MagicMock()
    client.get_state.return_value = {
        'session_id': 'session', 'state_version': 1,
        'display': {'status': display_status},
        'projection': {'ready': [], 'retryable': ['generate']},
    }
    # Exercise the real SDK shape: get_ready_steps wraps the state in projection.
    client.get_ready_steps.side_effect = lambda session: WorkflowClient.get_ready_steps(client, session)
    monkeypatch.setattr(manager, '_client', lambda: client)
    toolkit = HostWorkflowToolkit(lambda: client)
    tools = {tool.__name__: tool for tool in manager._safe_session_tools(
        toolkit, 'session', workflow_context={'workflow_id': workflow_id},
    )}
    tools['advance_step_and_hand_off'] = manager._handoff_tool('session')
    return client, tools


@pytest.mark.parametrize('name', [
    'get_workflow_state', 'get_ready_steps', 'advance_step', 'advance_step_and_hand_off',
])
def test_already_paused_session_stops_chat_without_another_execution(monkeypatch, name):
    client, tools = session_tools(monkeypatch, 'paused')
    if name == 'advance_step':
        result = tools[name](['generate'])
    elif name == 'advance_step_and_hand_off':
        result = tools[name]('generate')
    else:
        result = tools[name]()
    assert result['outcome'] == 'workflow_paused'
    assert result['next_action']['tool'] is None
    text = _tool_result_stop_text({'ok': True, 'value': result})
    assert text and '已暂停' in text and '继续' in text
    client.advance.assert_not_called()


@pytest.mark.parametrize('handoff', [False, True])
@pytest.mark.parametrize('code', ['WORKFLOW_PAUSED', 'WORKFLOW_PAUSING', 'AUTOMATIC_RETRY_EXHAUSTED'])
def test_only_confirmed_pause_errors_end_chat(monkeypatch, handoff, code):
    client, tools = session_tools(monkeypatch, 'running')
    client.advance.side_effect = WorkflowClientError(code, 'execution rejected')
    tool = tools['advance_step_and_hand_off' if handoff else 'advance_step']
    argument = 'generate' if handoff else ['generate']
    if code == 'WORKFLOW_PAUSED':
        result = tool(argument)
        assert result['_agent_control']['reason'] == 'workflow_paused'
        assert _tool_result_stop_text(result)
    else:
        with pytest.raises(WorkflowClientError) as exc:
            tool(argument)
        assert exc.value.code == code
    client.advance.assert_called_once()


@pytest.mark.parametrize('name', ['get_workflow_state', 'get_ready_steps'])
@pytest.mark.parametrize('status', ['running', 'pausing', 'waiting', 'failed', 'completed'])
def test_queries_do_not_invent_pause_from_empty_frontier_or_other_states(monkeypatch, name, status):
    _, tools = session_tools(monkeypatch, status)
    result = tools[name]()
    assert _tool_result_stop_text(result) is None
    assert '_agent_control' not in result


@pytest.mark.parametrize('workflow_id', ['ppt-workflow', 'writer-workflow'])
@pytest.mark.parametrize('status', ['failed', 'interrupted'])
@pytest.mark.parametrize('remaining', [2, 1, 0])
def test_failure_retry_budget_remains_available_to_chat(monkeypatch, workflow_id, status, remaining):
    client, tools = session_tools(monkeypatch, 'failed', workflow_id)
    client.advance.return_value.result = {
        'attempt_statuses': {'task': status},
        'attempt_results': [{'task_id': 'task', 'failure_reason': 'provider unavailable'}],
        'workflow_state': {'display': {'status': 'failed'}},
        'projection': {'retryable': ['generate'] if remaining else []},
        'max_automatic_attempts': 3, 'automatic_retry_remaining': remaining,
    }
    result = tools['advance_step'](['generate'])
    assert result['outcome'] == 'step_failed'
    assert result['automatic_retry_remaining'] == remaining
    assert result['max_automatic_attempts'] == 3
    assert result['retryable_steps'] == (['generate'] if remaining else [])
    assert result['next_action']['decision_owner'] == 'ChatAgent'
    assert _tool_result_stop_text(result) is None


def test_paper_writing_keeps_its_existing_no_automatic_retry_policy(monkeypatch):
    client, tools = session_tools(monkeypatch, 'failed', 'paper-writing')
    client.advance.return_value.result = {
        'attempt_statuses': {'task': 'failed'},
        'workflow_state': {'display': {'status': 'failed'}},
        'projection': {'retryable': ['generate']},
    }
    result = tools['advance_step'](['generate'])
    assert result['_agent_control']['reason'] == 'workflow_step_failed'


@pytest.mark.parametrize('query', ['重新执行分析步骤', 'Rerun the analysis step.'])
@pytest.mark.parametrize('handoff', [True, False])
def test_explicit_paused_rerun_binds_user_request_and_snapshot(monkeypatch, query, handoff):
    client, _ = session_tools(monkeypatch, 'paused')
    state = client.get_state.return_value
    client.advance.return_value.result = {'accepted': True, 'outcome': 'step_succeeded',
                                          'workflow_state': {'display': {'status': 'running'}}}
    tool = manager._paused_rerun_tool('session', state, query, 'ppt-workflow', None)
    result = tool('generate', query, handoff)
    request = client.advance.call_args.args[0]
    assert request.resume_for_rerun and request.retry_origin == 'user'
    assert request.expected_state_version == 1 and request.handoff == handoff
    assert request.steps[0].step_id == 'generate'
    assert 'workflow_paused' != result.get('outcome')
    with pytest.raises(WorkflowClientError, match='Select a prior step'):
        tool('generate', query, handoff)
    assert client.advance.call_count == 1


@pytest.mark.parametrize('step,quote', [('unknown', '重跑分析'), ('generate', ''), ('generate', '历史中的重跑请求')])
def test_paused_rerun_rejects_unbound_target_or_evidence(monkeypatch, step, quote):
    client, _ = session_tools(monkeypatch, 'paused')
    tool = manager._paused_rerun_tool('session', client.get_state.return_value, '重跑分析', 'ppt-workflow', None)
    with pytest.raises(WorkflowClientError):
        tool(step, quote)
    client.advance.assert_not_called()


@pytest.mark.parametrize('code', ['STATE_VERSION_CONFLICT', 'WORKFLOW_APPROVAL_REQUIRED'])
def test_paused_rerun_does_not_refresh_permission_or_bypass_approval(monkeypatch, code):
    client, _ = session_tools(monkeypatch, 'paused')
    client.advance.side_effect = WorkflowClientError(code, 'rejected')
    tool = manager._paused_rerun_tool('session', client.get_state.return_value, '重跑分析', 'ppt-workflow', None)
    if code == 'STATE_VERSION_CONFLICT':
        with pytest.raises(WorkflowClientError):
            tool('generate', '重跑分析')
    else:
        assert tool('generate', '重跑分析')['outcome'] == 'workflow_approval_required'
    client.advance.assert_called_once()


@pytest.mark.parametrize('workflow_id', ['ppt-workflow', 'paper-writing'])
@pytest.mark.parametrize('status,display', [
    ('succeeded', 'running'), ('failed', 'failed'), ('interrupted', 'paused'), ('succeeded', 'waiting'),
])
def test_paused_rerun_preserves_execution_boundaries(monkeypatch, workflow_id, status, display):
    client, _ = session_tools(monkeypatch, 'paused')
    client.advance.return_value.result = {
        'accepted': True, 'attempt_statuses': {'task': status},
        'workflow_state': {'display': {
            'status': display, 'approval_step_id': 'generate' if display == 'waiting' else '',
        }},
        'projection': {'ready': ['next'], 'retryable': ['generate'] if status == 'failed' else []},
    }
    tool = manager._paused_rerun_tool('session', client.get_state.return_value, '重跑分析', workflow_id, None)
    result = tool('generate', '重跑分析')
    if status == 'succeeded' and display == 'running' and workflow_id != 'paper-writing':
        assert result['outcome'] == 'step_succeeded'
        assert result['ready_steps'] == ['next']
        assert result['next_action']['tool'] == 'advance_step'
        assert '_agent_control' not in result
    else:
        assert result['_agent_control']['stop'] is True


@pytest.mark.parametrize('quote,amendment', [
    ('重新执行分析步骤', ''),
    ('Rerun the analysis step', ''),
    ('重新执行分析步骤', '改为六页，使用蓝色主题'),
    ('Rerun the analysis step', 'Use six slides and a blue theme'),
])
@pytest.mark.parametrize('handoff', [False, True])
@pytest.mark.parametrize('paused', [False, True])
def test_rerun_control_does_not_reach_downstream_but_content_amendments_do(
    monkeypatch, quote, amendment, handoff, paused,
):
    import lazyllm

    query = quote + ('; ' + amendment if amendment else '')
    monkeypatch.setattr(manager, '_agentic_config', lambda: lazyllm.globals['agentic_config'])
    lazyllm.globals['agentic_config'] = {'query': query}
    client, tools = session_tools(monkeypatch, 'paused' if paused else 'running')
    client.advance.return_value.result = {'accepted': True, 'outcome': 'step_succeeded',
                                          'workflow_state': {'display': {'status': 'running'}}}
    if paused:
        rerun = manager._paused_rerun_tool('session', client.get_state.return_value, query, 'ppt-workflow', None)
        rerun('generate', quote)
    else:
        tools['advance_step'](['generate'], request_quote=quote)
    first = client.advance.call_args.args[0].steps[0]
    assert first.user_input == amendment
    assert quote not in first.runtime_instruction

    client.get_state.return_value.update({
        'display': {'status': 'running'}, 'projection': {'ready': ['slides']},
    })
    if handoff:
        tools['advance_step_and_hand_off']('slides')
    else:
        tools['advance_step'](['slides'])
    downstream = client.advance.call_args.args[0].steps[0]
    assert downstream.user_input == amendment
    assert quote not in downstream.runtime_instruction
    # A new question or another session must not inherit this turn's classification.
    assert manager._step_request_input('改为三页', 'session') == '改为三页'
    assert manager._step_request_input(query, 'other-session') == query
