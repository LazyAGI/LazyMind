import asyncio
import json
from types import SimpleNamespace
from unittest.mock import AsyncMock

import httpx
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from lazymind.chat.api import agent_control_routes
from lazymind.router.core import session_env


def test_input_value_is_sent_only_to_owning_worker(monkeypatch):
    registry = SimpleNamespace(list_active_instances=AsyncMock(return_value=[
        SimpleNamespace(url='http://a'), SimpleNamespace(url='http://b'),
    ]))
    monkeypatch.setattr(session_env, 'get_global_registry', lambda: registry)
    writes = []

    def handle(request):
        if request.method == 'GET':
            assert 'synthetic-value' not in str(request.url)
            assert not request.content
            return httpx.Response(200, json={'ok': request.url.host == 'b'})
        writes.append(request.url.host)
        assert json.loads(request.content)['value'] == 'synthetic-value'
        return httpx.Response(200, json={'ok': True, 'status': 'configured'})
    client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
    monkeypatch.setattr(session_env.httpx, 'AsyncClient', lambda **_: client)
    assert asyncio.run(session_env.submit_worker_session_env('ask', 'conv', 'synthetic-value')) == 'configured'
    assert writes == ['b']


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


@pytest.mark.parametrize('cancel', [False, True])
def test_input_discovers_owner_without_waiting_for_unreachable_workers(monkeypatch, cancel):
    registry = SimpleNamespace(list_active_instances=AsyncMock(return_value=[
        *(SimpleNamespace(url=f'http://a{i}') for i in range(4)), SimpleNamespace(url='http://owner'),
    ]))
    monkeypatch.setattr(session_env, 'get_global_registry', lambda: registry)
    cancelled = []
    writes = []

    async def handle(request):
        if request.method == 'GET':
            assert 'synthetic-value' not in str(request.url)
            assert not request.content
            if request.url.host != 'owner':
                try:
                    await asyncio.Future()
                finally:
                    cancelled.append(request.url.host)
            return httpx.Response(200, json={'ok': True})
        # Discovery tasks must already be cleaned up when we send the secret.
        assert len(cancelled) == 4
        writes.append(request.url.host)
        assert json.loads(request.content) == {
            'ask_id': 'ask', 'conversation_id': 'conv', 'cancel': cancel,
            **({} if cancel else {'value': 'synthetic-value'}),
        }
        return httpx.Response(200, json={'ok': True, 'status': 'canceled' if cancel else 'configured'})

    client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
    monkeypatch.setattr(session_env.httpx, 'AsyncClient', lambda **_: client)

    async def run():
        return await asyncio.wait_for(
            session_env.submit_worker_session_env('ask', 'conv', 'synthetic-value', cancel=cancel), 1,
        )

    assert asyncio.run(run()) == ('canceled' if cancel else 'configured')
    assert writes == ['owner']


@pytest.mark.parametrize('external_cancel', [False, True])
def test_input_limits_probe_concurrency_and_cleans_up_on_timeout_or_cancel(monkeypatch, external_cancel):
    registry = SimpleNamespace(list_active_instances=AsyncMock(return_value=[
        SimpleNamespace(url=f'http://worker{i}') for i in range(20)
    ]))
    monkeypatch.setattr(session_env, 'get_global_registry', lambda: registry)
    monkeypatch.setattr(session_env, '_PROBE_CONCURRENCY', 3)
    monkeypatch.setattr(session_env, '_INPUT_TIMEOUT', 0.1)
    active = peak = 0

    async def run():
        saturated = asyncio.Event()

        async def handle(request):
            nonlocal active, peak
            assert request.method == 'GET'
            active += 1
            peak = max(peak, active)
            if active == 3:
                saturated.set()
            try:
                await asyncio.Future()
            finally:
                active -= 1

        client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
        monkeypatch.setattr(session_env.httpx, 'AsyncClient', lambda **_: client)
        task = asyncio.create_task(session_env.submit_worker_session_env('ask', 'conv', 'synthetic-value'))
        if external_cancel:
            await asyncio.wait_for(saturated.wait(), 1)
            task.cancel()
        with pytest.raises(asyncio.CancelledError if external_cancel else TimeoutError):
            await task
        assert client.is_closed
        assert active == 0
        assert peak == 3

    asyncio.run(run())


@pytest.mark.parametrize('stage', ['registry', 'write'])
def test_input_deadline_also_covers_registry_and_write(monkeypatch, stage):
    async def list_workers():
        if stage == 'registry':
            await asyncio.Future()
        return [SimpleNamespace(url='http://owner')]

    monkeypatch.setattr(session_env, 'get_global_registry', lambda: SimpleNamespace(list_active_instances=list_workers))
    monkeypatch.setattr(session_env, '_INPUT_TIMEOUT', 0.05)
    writes = []

    async def handle(request):
        if request.method == 'GET':
            return httpx.Response(200, json={'ok': True})
        writes.append(request.url.host)
        await asyncio.Future()

    client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
    monkeypatch.setattr(session_env.httpx, 'AsyncClient', lambda **_: client)
    with pytest.raises(TimeoutError):
        asyncio.run(session_env.submit_worker_session_env('ask', 'conv', 'synthetic-value'))
    assert writes == ([] if stage == 'registry' else ['owner'])


def test_input_ignores_invalid_probes_and_does_not_retry_uncertain_write(monkeypatch):
    registry = SimpleNamespace(list_active_instances=AsyncMock(return_value=[
        SimpleNamespace(url=f'http://{host}') for host in ['bad-json', 'bad-status', 'owner', 'other']
    ]))
    monkeypatch.setattr(session_env, 'get_global_registry', lambda: registry)
    writes = []

    async def handle(request):
        if request.method == 'GET':
            if request.url.host == 'bad-json':
                return httpx.Response(200, content=b'not-json')
            if request.url.host == 'bad-status':
                return httpx.Response(503)
            return httpx.Response(200, json={'ok': True})
        writes.append(request.url.host)
        raise httpx.ReadTimeout('uncertain write')

    client = httpx.AsyncClient(transport=httpx.MockTransport(handle))
    monkeypatch.setattr(session_env.httpx, 'AsyncClient', lambda **_: client)
    with pytest.raises(httpx.ReadTimeout):
        asyncio.run(session_env.submit_worker_session_env('ask', 'conv', 'synthetic-value'))
    assert len(writes) == 1


@pytest.mark.parametrize('failed', [False, True])
def test_registered_clear_route_awaits_worker_cleanup(monkeypatch, failed):
    monkeypatch.setattr(agent_control_routes, 'config', {'enable_router': True})
    clear = (AsyncMock(side_effect=RuntimeError('unreachable')) if failed
             else AsyncMock(return_value=['worker-conversation']))
    monkeypatch.setattr(session_env, 'clear_worker_session_env', clear)
    app = FastAPI()
    app.include_router(agent_control_routes.router)
    response = TestClient(app).post('/api/chat/session-env:clear', json={'conversation_ids': ['worker-conversation']})
    assert response.status_code == (503 if failed else 200)
    clear.assert_awaited_once_with(['worker-conversation'])
    if not failed:
        assert response.json() == {'ok': True, 'cleared': ['worker-conversation']}
