from lazymind.chat.engine.subagent.db import TaskQueryDB


def test_task_queries_use_core_api(monkeypatch):
    responses = {
        '/internal/subagent/tasks/task-1': {
            'task_id': 'task-1', 'status': 'running', 'progress': 25,
        },
        '/internal/subagent/conversations/conv-1/tasks': {
            'tasks': [{'task_id': 'task-1', 'title': 'Research', 'status': 'running'}],
        },
        '/internal/subagent/artifacts': {
            'artifacts': [{'task_id': 'task-1', 'slot': 'report', 'content_type': 'text',
                           'value': {'text': 'draft'}, 'seq': 1}],
        },
    }
    calls = []

    def fake_get(path, params=None):
        calls.append((path, params))
        return responses[path]

    monkeypatch.setattr(TaskQueryDB, '_get', staticmethod(fake_get))
    query = TaskQueryDB()

    status = query.get_task_status('task-1')
    assert status['id'] == 'task-1'
    assert status['progress_pct'] == 25
    assert query.list_tasks_by_conversation('conv-1')[0]['id'] == 'task-1'
    assert query.load_artifacts_for_tasks(['task-1'])[0]['task_id'] == 'task-1'
    assert calls == [
        ('/internal/subagent/tasks/task-1', None),
        ('/internal/subagent/conversations/conv-1/tasks', None),
        ('/internal/subagent/artifacts', {'task_id': ['task-1']}),
    ]
