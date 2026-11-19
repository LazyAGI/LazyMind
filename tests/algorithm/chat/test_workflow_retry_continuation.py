"""User retries continue through the real agent loop until a workflow boundary."""
import json
import uuid
from unittest.mock import MagicMock

import lazyllm
import pytest
from lazyllm.tools import ReactAgent

from lazymind.chat.workflow import workflow_manager as manager
from lazymind.workflow_sdk import WorkflowClientError


def run_agent(tool, steps, tmp_path):
    class Model:
        def __init__(self):
            self._module_id = uuid.uuid4().hex
            self.calls = 0

        def share(self, **kwargs):
            return self

        def used_by(self, module_id):
            return self

        def __call__(self, *args, **kwargs):
            assert self.calls < len(steps), 'Agent continued beyond the workflow boundary'
            step = steps[self.calls]
            self.calls += 1
            return {'content': '', 'tool_calls': [{
                'id': f'advance-{self.calls}', 'type': 'function',
                'function': {'name': 'advance_step', 'arguments': json.dumps({'step_ids': [step]})},
            }]}

    with lazyllm.locals._scope():
        model = Model()
        agent = ReactAgent(model, [tool], workspace=str(tmp_path), force_summarize=True,
                           enable_builtin_tools=False, sandbox=None)
        result = agent('重试并继续执行')
    assert model.calls == len(steps)
    return result


@pytest.fixture
def retry_tool(monkeypatch):
    cfg = {'user_authorized_workflow_retry': True}
    monkeypatch.setattr(manager, '_agentic_config', lambda: cfg)
    toolkit = MagicMock()
    toolkit.get_ready_steps.return_value = {
        'state_version': 7, 'retryable_steps': ['generate_backgrounds'], 'ready_steps': ['build_outline'],
    }
    toolkit.get_workflow_state.return_value = {}

    def create(workflow_id='ppt-workflow', checkpoint=None):
        return next(tool for tool in manager._safe_session_tools(
            toolkit, 'session', '重试', checkpoint=checkpoint, workflow_context={'workflow_id': workflow_id},
        ) if tool.__name__ == 'advance_step')

    return toolkit, cfg, create


@pytest.mark.parametrize('workflow_id', ['ppt-workflow', 'writer-workflow', 'paper-writing'])
@pytest.mark.parametrize('conflict', [False, True])
def test_successful_retry_continues_except_for_paper(retry_tool, tmp_path, workflow_id, conflict):
    toolkit, cfg, create = retry_tool
    responses = [WorkflowClientError('STATE_VERSION_CONFLICT', 'stale')] if conflict else []
    toolkit.advance_step.side_effect = responses + [
        {'status': 'active', 'outcome': 'step_succeeded', 'ready_steps': ['build_outline']},
        {'status': 'completed', 'outcome': 'workflow_completed'},
    ]
    steps = ['generate_backgrounds'] + ([] if workflow_id == 'paper-writing' else ['build_outline'])
    run_agent(create(workflow_id), steps, tmp_path)
    calls = toolkit.advance_step.call_args_list
    assert [call.args[2][0].step_id for call in calls] == (['generate_backgrounds'] if conflict else []) + steps
    assert [call.kwargs['retry_origin'] for call in calls] == (
        (['user'] if conflict else []) + ['user'] + ['automatic'] * (len(steps) - 1)
    )
    assert not cfg.get('user_authorized_workflow_retry')


@pytest.mark.parametrize('receipt', [
    {'status': 'failed', 'outcome': 'step_failed', 'retryable_steps': ['generate_backgrounds']},
    {'status': 'active', 'outcome': 'step_succeeded', 'workflow_state': {'display': {'status': 'paused'}}},
    {'status': 'active', 'outcome': 'step_succeeded', 'workflow_state': {'display': {
        'status': 'waiting', 'approval_step_id': 'generate_backgrounds',
    }}},
    {'status': 'active', 'workflow_state': {'attempt_history': {'generate_backgrounds': [{
        'attempt': 2, 'status': 'cancelled', 'input_required': {'question': '请选择需要生成的底图风格'},
    }]}}},
])
def test_retry_respects_failure_pause_approval_and_question(retry_tool, tmp_path, receipt):
    toolkit, cfg, create = retry_tool
    toolkit.advance_step.return_value = receipt
    run_agent(create(), ['generate_backgrounds'], tmp_path)
    toolkit.advance_step.assert_called_once()
    assert not cfg.get('user_authorized_workflow_retry')


def test_post_retry_checkpoint_consumes_authorization(retry_tool, tmp_path):
    toolkit, cfg, create = retry_tool
    checkpoint = MagicMock()
    checkpoint.guard.side_effect = [None, {
        'status': 'waiting', 'outcome': 'workflow_input_required',
        '_agent_control': {'stop': True, 'reason': 'workflow_input_required', 'final_text': '请确认后继续。'},
    }]
    toolkit.advance_step.return_value = {'status': 'active', 'outcome': 'step_succeeded'}
    run_agent(create(checkpoint=checkpoint), ['generate_backgrounds'], tmp_path)
    checkpoint.guard.assert_called_with('session', completed_step='generate_backgrounds')
    assert not cfg.get('user_authorized_workflow_retry')


def test_paused_frontier_blocks_retry_and_can_resume(retry_tool, tmp_path):
    toolkit, cfg, create = retry_tool
    frontier = toolkit.get_ready_steps.return_value
    toolkit.get_ready_steps.return_value = {**frontier, 'display': {'status': 'paused'}}
    tool = create()
    run_agent(tool, ['generate_backgrounds'], tmp_path)
    toolkit.advance_step.assert_not_called()
    assert cfg['user_authorized_workflow_retry'] is True
    # The runtime clears pause on the user's resume; the same tool can proceed.
    toolkit.get_ready_steps.return_value = frontier
    toolkit.advance_step.side_effect = [
        {'status': 'active', 'outcome': 'step_succeeded', 'ready_steps': ['build_outline']},
        {'status': 'completed', 'outcome': 'workflow_completed'},
    ]
    run_agent(tool, ['generate_backgrounds', 'build_outline'], tmp_path)
    assert [call.kwargs['retry_origin'] for call in toolkit.advance_step.call_args_list] == ['user', 'automatic']
