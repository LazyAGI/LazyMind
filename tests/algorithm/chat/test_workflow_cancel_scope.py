from fastapi import FastAPI
from fastapi.testclient import TestClient

from lazymind.chat.api import workflow_routes
from lazymind.chat.engine.agent_runtime import cancellation
from lazymind.chat.service import chat_service


def test_chat_pause_does_not_cancel_another_history(monkeypatch):
    monkeypatch.setattr(chat_service, '_active_sessions', {'conv': 'background'})
    monkeypatch.setattr(chat_service, '_active_turn_sessions', {
        ('conv', 'foreground-history'): 'foreground',
        ('conv', 'background-history'): 'background',
    })
    calls = []
    monkeypatch.setattr(cancellation, 'request_cancel', calls.append)
    app = FastAPI()
    app.include_router(workflow_routes.router)
    client = TestClient(app)
    response = client.post('/api/workflow/task-cancel', json={
        'conversation_id': 'conv', 'history_id': 'foreground-history',
    })
    assert response.json() == {'ok': True}
    assert calls == ['foreground']
    response = client.post('/api/workflow/task-cancel', json={
        'conversation_id': 'conv', 'history_id': 'finished-history',
    })
    assert response.json() == {'ok': False}
    assert calls == ['foreground']
    chat_service._unregister_active_session('conv', 'foreground')
    assert chat_service._active_turn_sessions == {('conv', 'background-history'): 'background'}
    assert chat_service._active_sessions == {'conv': 'background'}


def test_workflow_pause_confirms_absent_executor_but_not_live_cleanup(monkeypatch):
    from lazymind.chat.workflow import remote_executor

    monkeypatch.setattr(workflow_routes, 'config', {'enable_router': False})
    monkeypatch.setattr(cancellation, 'request_cancel', lambda _sid: None)
    app = FastAPI()
    app.include_router(workflow_routes.router)
    client = TestClient(app)
    request = {'task_id': 'task', 'attempt_id': 'attempt', 'lease_token': 'lease'}
    with remote_executor._active_attempts_lock:
        remote_executor._active_attempts.add(('attempt', 'lease'))
    try:
        response = client.post('/api/workflow/task-cancel', json=request)
        assert response.json() == {'ok': True, 'executor_stopped': False}
    finally:
        with remote_executor._active_attempts_lock:
            remote_executor._active_attempts.discard(('attempt', 'lease'))
    response = client.post('/api/workflow/task-cancel', json=request)
    assert response.json() == {'ok': True, 'executor_stopped': True}


def test_router_workflow_pause_does_not_depend_on_model_workers(monkeypatch):
    from types import SimpleNamespace
    from unittest.mock import AsyncMock
    from lazymind.chat.workflow import remote_executor
    from lazymind.router.core import registry

    monkeypatch.setattr(workflow_routes, 'config', {
        'enable_router': True, 'router_child_proxied_only': False,
    })
    calls = []
    monkeypatch.setattr(cancellation, 'request_cancel', calls.append)
    worker_lookup = AsyncMock(side_effect=RuntimeError('model worker unavailable'))
    monkeypatch.setattr(registry, 'get_global_registry', lambda: SimpleNamespace(
        list_active_instances=worker_lookup))
    app = FastAPI()
    app.include_router(workflow_routes.router)
    client = TestClient(app)
    request = {'task_id': 'task', 'attempt_id': 'attempt', 'lease_token': 'lease'}
    for running in (True, False):
        monkeypatch.setattr(remote_executor, 'workflow_attempt_is_running',
                            lambda attempt, lease, active=running: active)
        assert client.post('/api/workflow/task-cancel', json=request).json() == {
            'ok': True, 'executor_stopped': not running,
        }
    assert calls == ['task', 'task']
    worker_lookup.assert_not_called()
