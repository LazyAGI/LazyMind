from __future__ import annotations

from lazymind.chat.engine.subagent.db import TaskQueryDB


def test_task_query_db_context_includes_terminal_ordinary_task_artifacts(monkeypatch):
    db = TaskQueryDB()

    monkeypatch.setattr(
        db,
        'list_tasks_by_conversation',
        lambda conv_id: [{
            'id': 'task-1',
            'task_id': 'task-1',
            'title': 'Collect references',
            'agent_type': 'research',
            'status': 'succeeded',
            'summary': 'finished',
            'seq_in_conversation': 1,
        }],
    )

    monkeypatch.setattr(
        db, 'format_task_artifacts',
        lambda task_ids: ['"refs" [text]: reference summary'] if task_ids == ['task-1'] else [],
    )

    context = db.build_chat_agent_task_context('conv-1')

    assert 'Task 1. Collect references [done]: finished' in context
    assert '"refs" [text]' in context
    assert 'reference summary' in context


def test_task_query_db_load_artifacts_for_tasks_returns_empty_for_empty_input():
    assert TaskQueryDB().load_artifacts_for_tasks([]) == []


def test_memory_store_restores_steps_and_allocates_sequences():
    from lazymind.chat.engine.subagent.db import MemorySubAgentStore

    store = MemorySubAgentStore(
        {'id': 'task-1', 'objective': 'continue'},
        [{'seq': 1, 'role': 'text', 'content': {'content': 'old'}}],
        [{'slot': 'report', 'content_type': 'text', 'value': {'text': 'v1'}, 'seq': 2}],
    )
    assert store.load_task('task-1')['objective'] == 'continue'
    assert store.load_task('other') is None
    assert store.max_step_seq('task-1') == 1
    store.append_step('task-1', 2, 'tool', {'tool_results': []})
    assert [step['seq'] for step in store.load_steps('task-1')] == [1, 2]
    assert store.next_artifact_seq('task-1', 'report') == 3
    assert store.next_artifact_seq('task-1', 'new-slot') == 1
    assert store.load_artifacts('task-1', ['report']) == [
        {'slot': 'report', 'content_type': 'text', 'value': {'text': 'v1'}, 'seq': 2},
    ]
