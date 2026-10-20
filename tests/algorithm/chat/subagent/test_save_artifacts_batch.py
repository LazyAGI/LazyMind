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


def test_confirmed_empty_order_lookup_is_reused(batch_context, monkeypatch):
    _, events = batch_context
    client = MagicMock()
    client.get_slot_order.return_value.result = {'order_list': []}
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


@pytest.mark.parametrize('kind', ['file', 'image', 'file_list'])
def test_same_basename_inputs_keep_independent_content(batch_context, tmp_path, kind):
    from pathlib import Path

    ctx, events = batch_context
    sources = []
    for directory, content in [('a', 'FIRST'), ('b', 'SECOND')]:
        source = tmp_path / directory / 'report.png'
        source.parent.mkdir()
        source.write_text(content)
        sources.append(str(source))
    existing = tmp_path / 'report.png'
    existing.write_text('PREVIOUS')
    artifacts = ([{'key': 'images', 'value': sources, 'content_type': kind}]
                 if kind == 'file_list' else [
                     {'key': 'images', 'value': source, 'content_type': kind} for source in sources])
    tools.save_artifacts(artifacts)
    paths = (events[0]['value']['paths'] if kind == 'file_list'
             else [event['value']['path'] for event in events])
    assert len(set(paths)) == 2
    assert [Path(path).read_text() for path in paths] == ['FIRST', 'SECOND']
    assert all(Path(path).name == 'report.png' for path in paths)
    assert existing.read_text() == 'PREVIOUS'
    # A later save from the same source must not mutate the previous snapshot.
    Path(sources[0]).write_text('REVISED')
    tools.save_artifacts([{'key': 'images', 'value': sources[0], 'content_type': 'file'}])
    assert [Path(path).read_text() for path in paths] == ['FIRST', 'SECOND']
    assert Path(events[-1]['value']['path']).read_text() == 'REVISED'


def test_failed_batch_preserves_previous_files_and_can_retry(batch_context, tmp_path):
    from pathlib import Path

    _, events = batch_context
    source = tmp_path / 'incoming' / 'report.txt'
    source.parent.mkdir()
    source.write_text('PUBLISHED')
    item = {'key': 'report', 'value': str(source), 'content_type': 'file'}
    tools.save_artifacts([item])
    saved = Path(events[-1]['value']['path'])
    source.write_text('NEW')
    events.clear()
    with pytest.raises(ToolExecutionError):
        tools.save_artifacts([item, {'key': 'undeclared', 'value': 'invalid'}])
    assert events == []
    assert saved.read_text() == 'PUBLISHED'
    tools.save_artifacts([item])
    assert saved.read_text() == 'PUBLISHED'
    assert Path(events[-1]['value']['path']).read_text() == 'NEW'


@pytest.mark.parametrize('failure', [RuntimeError('unavailable'), {}, {'order_list': '7,3'}])
def test_failed_order_lookup_aborts_batch_and_retry_refreshes(batch_context, monkeypatch, failure):
    ctx, events = batch_context
    client = MagicMock()
    if isinstance(failure, Exception):
        client.get_slot_order.side_effect = failure
    else:
        client.get_slot_order.return_value.result = failure
    monkeypatch.setattr(tools, '_workflow_client', lambda: client)
    items = [
        {'key': 'report', 'value': 'ready'},
        {'key': 'images', 'value': 'replace first', 'sort_order': 1},
        {'key': 'images', 'value': 'replace second', 'sort_order': 2},
    ]
    with pytest.raises(ToolExecutionError, match='list order is unavailable'):
        tools.save_artifacts(items)
    assert events == []
    assert ctx.local_artifacts() == []
    assert ctx.read_draft('report') is None
    assert ctx._artifact_counts == {}
    client.get_slot_order.assert_called_once()
    client.get_slot_order.side_effect = None
    client.get_slot_order.return_value.result = {'order_list': [7, 3]}
    tools.save_artifacts(items)
    assert client.get_slot_order.call_count == 2
    assert [event['value'].get('list_index') for event in events] == [None, 7, 3]
    assert [event['seq'] for event in events] == [1, 5, 6]
