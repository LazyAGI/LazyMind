from unittest.mock import MagicMock

import lazyllm
import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.subagent import tools
from lazymind.chat.engine.subagent.context import SubAgentContext
from lazymind.chat.engine.subagent.db import MemorySubAgentStore


@pytest.fixture
def batch_context(tmp_path, monkeypatch):
    events = []
    ctx = SubAgentContext(
        task_id='task', conversation_id='conversation', agent_type='workflow_step',
        objective='save outputs', params={}, workspace_path=str(tmp_path),
        input_slots=[], output_slots=['report', 'summary', 'images'],
        db=MemorySubAgentStore({'id': 'task'}, artifacts=[{'slot': 'images', 'seq': 4}]),
        emit=events.append,
    )
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    monkeypatch.setitem(lazyllm.globals, 'agentic_config', {'workflow_session_id': 'session'})
    return ctx, events


def test_batch_saves_mixed_outputs_and_repeated_keys(batch_context):
    ctx, events = batch_context
    result = tools.save_artifacts([
        {'key': 'report', 'value': 'Report'},
        {'key': 'summary', 'value': {'ok': True}, 'content_type': 'json'},
        {'key': 'images', 'value': 'https://example.test/one.png', 'content_type': 'image'},
        {'key': 'images', 'value': 'https://example.test/two.png', 'content_type': 'image'},
    ])
    assert result['saved_count'] == 4
    assert [event['slot'] for event in events] == ['report', 'summary', 'images', 'images']
    assert [event['seq'] for event in events] == [1, 1, 5, 6]
    assert ctx.read_draft('report') == ('Report', 'text')
    assert ctx.read_draft('summary') == ('{"ok": true}', 'json')
    assert ctx.list_pending_drafts() == []


@pytest.mark.parametrize('invalid', [
    {'key': 'summary', 'content': 'wrong field'},
    {'key': 'undeclared', 'value': 'not allowed'},
    {'key': 'summary', 'value': 'bad type', 'content_type': 'unsupported'},
    {'key': 'images', 'value': 'placeholder', 'content_type': 'image'},
    {'key': 'summary', 'value': 'missing.pdf', 'content_type': 'file'},
    {'key': 'summary', 'value': 'wrong declared type', 'content_type': 'text'},
    {'key': 'images', 'value': 'publisher output', 'content_type': 'text'},
])
def test_invalid_later_entry_does_not_publish_or_consume_sequence(batch_context, invalid):
    ctx, events = batch_context
    if invalid.get('value') == 'wrong declared type':
        ctx.params['output_slot_types'] = {'summary': 'json'}
    if invalid.get('value') == 'publisher output':
        ctx.params['workflow_runtime'] = {'publisher_owned_slots': ['images']}
    with pytest.raises(ToolExecutionError):
        tools.save_artifacts([{'key': 'report', 'value': 'ready'}, invalid])
    assert events == []
    assert ctx.local_artifacts() == []
    assert ctx.read_draft('report') is None
    assert ctx.next_artifact_seq('report') == 1


def test_batch_reuses_order_snapshot_but_refreshes_on_next_call(batch_context, monkeypatch):
    ctx, events = batch_context
    client = MagicMock()
    client.get_slot_order.return_value.result = {'order_list': [7, 3, 11]}
    monkeypatch.setattr(tools, '_workflow_client', lambda: client)
    result = tools.save_artifacts([
        {'key': 'images', 'value': 'first', 'sort_order': 1},
        {'key': 'images', 'value': 'third', 'sort_order': 3},
        {'key': 'images', 'value': 'append'},
        {'key': 'images', 'value': 'out of range', 'sort_order': 4},
    ])
    client.get_slot_order.assert_called_once_with('session', 'images')
    assert [event['value'].get('list_index') for event in events] == [7, 11, None, None]
    assert 'WARNING' in result['results'][-1]['message']
    assert ctx.read_draft('images', 7) == ('first', 'text')
    assert ctx.read_draft('images', 11) == ('third', 'text')

    client.get_slot_order.return_value.result = {'order_list': [11, 7, 3]}
    tools.save_artifacts([{'key': 'images', 'value': 'new first', 'sort_order': 1}])
    assert client.get_slot_order.call_count == 2
    assert events[-1]['value']['list_index'] == 11


@pytest.mark.parametrize('failed', [False, True])
def test_empty_or_failed_order_lookup_is_reused(batch_context, monkeypatch, failed):
    _, events = batch_context
    client = MagicMock()
    client.get_slot_order.return_value.result = {'order_list': []}
    if failed:
        client.get_slot_order.side_effect = RuntimeError('unavailable')
    monkeypatch.setattr(tools, '_workflow_client', lambda: client)
    tools.save_artifacts([
        {'key': 'images', 'value': 'first', 'sort_order': 1},
        {'key': 'images', 'value': 'second', 'sort_order': 2},
    ])
    client.get_slot_order.assert_called_once()
    assert all('list_index' not in event['value'] for event in events)


@pytest.mark.parametrize('count', [0, 51])
def test_invalid_batch_size_is_rejected_before_file_resolution(batch_context, count):
    _, events = batch_context
    with pytest.raises(ToolExecutionError):
        tools.resolve_artifact_files({'artifacts': [{'key': 'report', 'value': 'text'}] * count})
    assert events == []
