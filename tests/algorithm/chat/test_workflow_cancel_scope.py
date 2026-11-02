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
