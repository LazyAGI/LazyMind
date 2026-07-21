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
