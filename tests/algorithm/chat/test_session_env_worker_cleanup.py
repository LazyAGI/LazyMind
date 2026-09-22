import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from lazymind.chat.api import agent_control_routes
from lazymind.router.core import session_env


@pytest.mark.parametrize('failed', [False, True])
def test_router_clear_broadcasts_to_every_worker(monkeypatch, failed):
    registry = SimpleNamespace(list_active_instances=AsyncMock(return_value=[
        SimpleNamespace(url='http://healthy:8046'),
        SimpleNamespace(url='http://unhealthy:8047'),
        SimpleNamespace(url='http://healthy:8046'),
    ]))
    monkeypatch.setattr(session_env, 'get_global_registry', lambda: registry)
    calls = []

    def handle(request):
        calls.append(request.url.host)
        if failed and request.url.host == 'unhealthy':
            return httpx.Response(503)
        assert request.url.path == '/api/chat/session-env:clear'
        return httpx.Response(200, json={'ok': True, 'cleared': ['test-conversation']})

    client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
    monkeypatch.setattr(session_env.httpx, 'AsyncClient', lambda **_: client)
    if failed:
        with pytest.raises(RuntimeError, match='cleanup failed'):
            asyncio.run(session_env.clear_worker_session_env(['test-conversation']))
    else:
        assert asyncio.run(session_env.clear_worker_session_env(['test-conversation'])) == ['test-conversation']
    assert sorted(calls) == ['healthy', 'unhealthy']


@pytest.mark.parametrize('failed', [False, True])
def test_registered_clear_route_awaits_worker_cleanup(monkeypatch, failed):
    monkeypatch.setattr(agent_control_routes, 'config', {'enable_router': True})
    clear = AsyncMock(side_effect=RuntimeError('unreachable')) if failed else AsyncMock(return_value=['worker-conversation'])
    monkeypatch.setattr(session_env, 'clear_worker_session_env', clear)
    app = FastAPI()
    app.include_router(agent_control_routes.router)
    response = TestClient(app).post('/api/chat/session-env:clear', json={'conversation_ids': ['worker-conversation']})
    assert response.status_code == (503 if failed else 200)
    clear.assert_awaited_once_with(['worker-conversation'])
    if not failed:
        assert response.json() == {'ok': True, 'cleared': ['worker-conversation']}
