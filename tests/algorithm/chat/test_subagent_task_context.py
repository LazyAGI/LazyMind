from __future__ import annotations

from contextlib import contextmanager

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

    class FakeResult:
        def mappings(self):
            return self

        def all(self):
            return [{
                'task_id': 'task-1',
                'artifact_key': 'refs',
                'content_type': 'text',
                'value': {'text': 'reference summary'},
                'seq': 1,
            }]

    class FakeConn:
        def execute(self, stmt, params):
            assert params == {'ids': ['task-1']}
            return FakeResult()

    @contextmanager
    def fake_conn():
        yield FakeConn()

    monkeypatch.setattr(db, '_conn', fake_conn)

    context = db.build_chat_agent_task_context('conv-1')

    assert 'Task 1. Collect references [done]: finished' in context
    assert 'Available artifacts' in context
    assert '"refs" [text]' in context
    assert 'reference summary' in context


def test_task_query_db_load_artifacts_for_tasks_returns_empty_for_empty_input():
    assert TaskQueryDB().load_artifacts_for_tasks([]) == []
