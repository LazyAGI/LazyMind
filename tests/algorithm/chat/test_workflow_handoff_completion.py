from unittest.mock import MagicMock

import pytest
from lazyllm.tools.agent.functionCall import _tool_result_stop_text

from lazymind.chat.workflow import workflow_manager as manager


@pytest.mark.parametrize('rewind', [False, True])
@pytest.mark.parametrize('status,reason,text', [
    ('paused', 'workflow_paused', '已暂停'),
    ('completed', 'workflow_completed', '已完成'),
    ('waiting', 'workflow_approval_required', '审核'),
    ('failed', 'workflow_handoff', '已结束'),
])
def test_handoff_uses_confirmed_execution_boundary(monkeypatch, rewind, status, reason, text):
    client = MagicMock()
    client.get_ready_steps.return_value = {
        'state_version': 7, 'rewindable_steps' if rewind else 'ready_steps': ['generate'],
    }
    state = {'status': 'completed' if status == 'completed' else 'waiting',
             'display': {'status': status, 'approval_step_id': 'generate' if status == 'waiting' else ''}}
    client.advance.return_value.result = {
        'accepted': True, 'task_id': 'task-1', 'execution_mode': 'synchronous',
        'attempt_statuses': {'task-1': {'paused': 'interrupted', 'failed': 'failed'}.get(status, 'succeeded')},
        'workflow_state': state,
    }
    monkeypatch.setattr(manager, '_client', lambda: client)
    result = manager._handoff_tool('session')('generate')
    assert result['_agent_control']['reason'] == reason
    assert text in _tool_result_stop_text({'ok': True, 'value': result})
    assert result['attempt_statuses'] == client.advance.return_value.result['attempt_statuses']
    assert client.advance.call_args.args[0].handoff is True

    if status == 'paused':
        # User resume makes the same step actionable again. No local latch may
        # preserve the earlier stop signal or prevent a fresh execution.
        client.advance.return_value.result = {
            'accepted': True, 'task_id': 'task-2', 'execution_mode': 'synchronous',
            'attempt_statuses': {'task-2': 'succeeded'},
            'workflow_state': {'status': 'completed', 'display': {'status': 'completed'}},
        }
        resumed = manager._handoff_tool('session')('generate')
        assert resumed['_agent_control']['reason'] == 'workflow_completed'
        assert resumed['task_id'] == 'task-2'
        assert client.advance.call_count == 2
