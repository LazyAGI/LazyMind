import json
from unittest.mock import MagicMock

from lazymind.chat.service.component.history import (
    is_workflow_rewind_action,
    normalize_history_for_agent,
)
from lazymind.chat.service.component.tool_rendering import (
    _tool_call_frame_text,
    _tool_result_frame_text,
)
from lazymind.chat.workflow.workflow_manager import (
    _compact_transition_result,
    _safe_session_tools,
)


def _large_transition_result():
    return {
        'ok': True,
        'value': {
            'accepted': True,
            'status': 'active',
            'state_version': 4,
            'command_id': 'command-1',
            'projection': {
                'ready': ['generate_image'],
                'retryable': ['optimize_prompt'],
                'rewindable': ['analyze_subject'],
                'nodes': {
                    'generate_image': {'prompt': 'x' * 20_000},
                },
            },
            'workflow_state': {
                'status': 'active',
                'graph': {'nodes': {'generate_image': {'prompt': 'y' * 20_000}}},
                'attempt_history': {'analyze_subject': ['z' * 10_000]},
            },
        },
    }


def test_workflow_rewind_ui_command_enables_pre_model_compaction():
    context = {'session_id': 'session-1'}

    assert is_workflow_rewind_action('请重新执行步骤 collect_materials', context)
    assert is_workflow_rewind_action('Please re-run step generate_ppt', context)


def test_normal_chat_and_unbound_commands_do_not_enable_rewind_compaction():
    assert not is_workflow_rewind_action('帮我重新生成一下', {'session_id': 'session-1'})
    assert not is_workflow_rewind_action('请重新执行步骤 collect_materials', None)


def test_normal_history_preserves_workflow_transition_projection():
    tool_call = {
        'id': 'call-1',
        'type': 'function',
        'function': {
            'name': 'advance_step',
            'arguments': json.dumps({'step_ids': ['analyze_subject']}),
        },
    }
    call_frame, preview = _tool_call_frame_text(tool_call)
    result_frame = _tool_result_frame_text({
        'id': 'call-1',
        'name': 'advance_step',
        'result': _large_transition_result(),
    }, preview_value=preview)

    normalized = normalize_history_for_agent([{
        'role': 'assistant',
        'content': call_frame + result_frame,
    }])

    receipt = json.loads(normalized[1]['content'])['value']
    assert receipt['projection']['ready'] == ['generate_image']
    assert receipt['projection']['retryable'] == ['optimize_prompt']
    assert receipt['projection']['rewindable'] == ['analyze_subject']
    assert 'projection' in receipt
    assert 'workflow_state' in receipt
    assert len(normalized[1]['content']) > 40_000


def test_rewind_history_compacts_workflow_transition_projection_before_model_call():
    tool_call = {
        'id': 'call-1',
        'type': 'function',
        'function': {'name': 'advance_step', 'arguments': '{}'},
    }
    call_frame, preview = _tool_call_frame_text(tool_call)
    result_frame = _tool_result_frame_text({
        'id': 'call-1',
        'name': 'advance_step',
        'result': _large_transition_result(),
    }, preview_value=preview)
    history = [{
        'role': 'assistant',
        'content': call_frame + result_frame,
    }]

    normalized = normalize_history_for_agent(
        history,
        compact_workflow_receipts=True,
    )

    receipt = json.loads(normalized[1]['content'])['value']
    assert receipt['ready_steps'] == ['generate_image']
    assert receipt['retryable_steps'] == ['optimize_prompt']
    assert receipt['rewindable_steps'] == ['analyze_subject']
    assert 'projection' not in receipt
    assert 'workflow_state' not in receipt
    assert len(normalized[1]['content']) < 1_000


def test_live_transition_compacts_only_rewindable_target():
    toolkit = MagicMock()
    toolkit.get_ready_steps.return_value = {
        'session_id': 'session-1',
        'state_version': 4,
        'ready_steps': ['generate_image'],
        'retryable_steps': [],
        'rewindable_steps': ['analyze_subject'],
        'continue_steps': [],
    }
    toolkit.advance_step.return_value = _large_transition_result()['value']
    advance_step = next(
        tool for tool in _safe_session_tools(toolkit, 'session-1')
        if tool.__name__ == 'advance_step'
    )

    forward = advance_step(['generate_image'])
    assert 'projection' in forward
    assert 'workflow_state' in forward

    rewind = advance_step(['analyze_subject'])
    assert rewind['ready_steps'] == ['generate_image']
    assert rewind['retryable_steps'] == ['optimize_prompt']
    assert rewind['rewindable_steps'] == ['analyze_subject']
    assert 'projection' not in rewind
    assert 'workflow_state' not in rewind


def test_live_transition_receipt_drops_graph_bodies_and_preserves_control():
    compact = _compact_transition_result({
        'accepted': True,
        'projection': {'completed': True, 'ready': [], 'nodes': {'done': 'x' * 10_000}},
        'workflow_state': {'status': 'completed', 'graph': {'nodes': 'y' * 10_000}},
        '_agent_control': {'stop': True, 'reason': 'workflow_completed'},
    })

    assert compact['status'] == 'completed'
    assert compact['outcome'] == 'workflow_completed'
    assert compact['_agent_control']['stop'] is True
    assert 'projection' not in compact
    assert 'workflow_state' not in compact
