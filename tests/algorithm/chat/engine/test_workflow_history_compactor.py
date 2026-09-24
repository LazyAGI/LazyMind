from __future__ import annotations

from lazymind.chat.engine.agent_runtime.workflow_compactor import (
    make_workflow_history_compactor,
)


def _tool_turn(call_id: str, result: str, *, assistant_content: str = '') -> list[dict]:
    return [
        {
            'role': 'assistant',
            'content': assistant_content,
            'tool_calls': [{
                'id': call_id,
                'function': {'name': 'read_file', 'arguments': '{}'},
            }],
        },
        {
            'role': 'tool',
            'tool_call_id': call_id,
            'name': 'read_file',
            'content': result,
        },
    ]


def _compact(history: list[dict], *, workspace: str, keep_recent: int = 0):
    return make_workflow_history_compactor(
        max_input_tokens='32K',
        workspace=workspace,
        keep_recent=keep_recent,
    )(
        history,
        prefix={'system_prompt': 'workflow system'},
        current_input='complete the current workflow step',
        current_round_messages=[],
    )


def test_workflow_history_is_unchanged_when_complete_request_fits(tmp_path):
    history = _tool_turn('call-1', 'small result')

    prior, current = _compact(history, workspace=str(tmp_path))

    assert prior == history
    assert current == []
    assert not (tmp_path / 'tool_spills').exists()


def test_workflow_overflow_replaces_old_tool_result_with_workspace_reference(tmp_path):
    history = _tool_turn('call-1', 'x' * 120_000)

    prior, _current = _compact(history, workspace=str(tmp_path))

    assert prior[0] == history[0]
    assert prior[1]['tool_call_id'] == 'call-1'
    assert prior[1]['content'].startswith('[Large tool result offloaded to workspace]')
    assert list((tmp_path / 'tool_spills').glob('read_file_*.txt'))


def test_workflow_overflow_drops_old_complete_turn_before_recent_turn(tmp_path):
    history = (
        _tool_turn('old', 'old result', assistant_content='z' * 120_000)
        + _tool_turn('recent', 'fresh result')
    )

    prior, _current = _compact(history, workspace=str(tmp_path), keep_recent=1)

    assert [message.get('tool_call_id') for message in prior if message['role'] == 'tool'] == ['recent']
    assert [call['id'] for message in prior if message['role'] == 'assistant'
            for call in message.get('tool_calls', [])] == ['recent']


def test_workflow_overflow_spills_protected_recent_result_without_dropping_pair(tmp_path):
    history = _tool_turn('recent', 'x' * 120_000)

    prior, _current = _compact(history, workspace=str(tmp_path), keep_recent=1)

    assert prior[0]['tool_calls'][0]['id'] == 'recent'
    assert prior[1]['tool_call_id'] == 'recent'
    assert 'workspace://tool_spills/' in prior[1]['content']


def test_workflow_overflow_can_reference_current_tool_result_without_removing_pair(tmp_path):
    current_turn = _tool_turn('active', 'x' * 120_000)
    compactor = make_workflow_history_compactor(
        max_input_tokens='32K', workspace=str(tmp_path), keep_recent=2,
    )

    prior, compacted_current = compactor(
        [current_turn[0]],
        prefix={'system_prompt': 'workflow system'},
        current_input='complete the current workflow step',
        current_round_messages=[current_turn[1]],
    )

    assert prior == [current_turn[0]]
    assert compacted_current[0]['tool_call_id'] == 'active'
    assert compacted_current[0]['content'].startswith('[Large tool result offloaded to workspace]')


def test_interleaved_message_does_not_make_tool_turn_removable(tmp_path):
    history = [
        _tool_turn('call-1', 'x' * 120_000)[0],
        {'role': 'user', 'content': 'do not drop this message'},
        _tool_turn('call-1', 'x' * 120_000)[1],
    ]

    prior, _current = _compact(history, workspace=str(tmp_path))

    assert prior == history


def test_partial_current_result_split_preserves_pair_and_can_spill(tmp_path):
    history = [{
        'role': 'assistant', 'content': '', 'tool_calls': [
            {'id': 'a', 'function': {'name': 'read_file', 'arguments': '{}'}},
            {'id': 'b', 'function': {'name': 'read_file', 'arguments': '{}'}},
        ],
    }, {'role': 'tool', 'name': 'read_file', 'tool_call_id': 'a', 'content': 'x' * 120_000}]
    current = [{'role': 'tool', 'name': 'read_file', 'tool_call_id': 'b', 'content': 'fresh'}]
    compact = make_workflow_history_compactor(max_input_tokens='32K', workspace=str(tmp_path), keep_recent=0)
    prior, result = compact(history, current_round_messages=current)
    assert prior[0] == history[0]
    assert prior[1]['content'].startswith('[Large tool result offloaded to workspace]')
    assert result == current


def test_duplicate_call_ids_do_not_make_round_removable(tmp_path):
    history = _tool_turn('duplicate', 'x' * 120_000)
    history[0]['tool_calls'].append(dict(history[0]['tool_calls'][0]))
    prior, _ = _compact(history, workspace=str(tmp_path))
    assert prior == history


def test_workflow_spill_locator_uses_workspace_uri(tmp_path):
    prior, _ = _compact(_tool_turn('call-1', 'x' * 120_000), workspace=str(tmp_path))
    spill = next((tmp_path / 'tool_spills').glob('read_file_*.txt'))
    assert f'File path: workspace://tool_spills/{spill.name}' in prior[1]['content']
