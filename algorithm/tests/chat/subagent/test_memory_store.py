from lazymind.chat.engine.subagent.db import MemorySubAgentStore, TaskQueryDB


def test_memory_store_restores_steps_and_allocates_sequences():
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
