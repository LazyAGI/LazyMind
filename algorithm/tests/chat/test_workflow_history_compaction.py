import json

from lazymind.chat.service.component.history import normalize_history_for_agent
from lazymind.chat.service.component.tool_rendering import (
    _tool_call_frame_text,
    _tool_result_frame_text,
)
from lazymind.chat.workflow.workflow_manager import _compact_transition_result


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


def test_retry_history_keeps_only_workflow_transition_receipt():
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
    assert receipt['ready_steps'] == ['generate_image']
    assert receipt['retryable_steps'] == ['optimize_prompt']
    assert receipt['rewindable_steps'] == ['analyze_subject']
    assert 'projection' not in receipt
    assert 'workflow_state' not in receipt
    assert len(normalized[1]['content']) < 1_000


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
