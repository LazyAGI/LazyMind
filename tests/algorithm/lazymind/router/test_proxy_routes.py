import asyncio
from types import SimpleNamespace

import httpx
from starlette.requests import Request

from lazymind.router.api import proxy_routes


def test_chat_steer_probes_workers_in_parallel_and_returns_first_owner(monkeypatch):
    asyncio.run(_exercise_parallel_probe(monkeypatch))


async def _exercise_parallel_probe(monkeypatch):
    instances = [
        SimpleNamespace(url='http://slow', host='slow', status='healthy'),
        SimpleNamespace(url='http://owner', host='owner', status='healthy'),
    ]

    class ABRouter:
        async def select_algorithm(self, _caller_algorithm_id):
            return 'algorithm'

    class Registry:
        def get_all_instances(self, _algorithm_id):
            return instances

    slow_started = asyncio.Event()
    slow_cancelled = asyncio.Event()

    class Client:
        def __init__(self, **_kwargs):
            pass

        async def __aenter__(self):
            return self

        async def __aexit__(self, *_args):
            return False

        async def request(self, _method, url, **_kwargs):
            if url.startswith('http://slow'):
                slow_started.set()
                try:
                    await asyncio.sleep(60)
                except asyncio.CancelledError:
                    slow_cancelled.set()
                    raise
            await slow_started.wait()
            return httpx.Response(
                200,
                json={'accepted': True, 'steer_id': 'guide'},
                headers={'content-type': 'application/json'},
            )

    monkeypatch.setattr(proxy_routes, 'get_ab_router', lambda: ABRouter())
    monkeypatch.setattr(proxy_routes, 'get_global_registry', lambda: Registry())
    monkeypatch.setattr(proxy_routes, 'expected_internal_token', lambda: 'token')
    monkeypatch.setattr(proxy_routes.httpx, 'AsyncClient', Client)

    async def receive():
        return {'type': 'http.request', 'body': b'{}', 'more_body': False}

    request = Request({
        'type': 'http',
        'method': 'POST',
        'path': '/api/chat/steer',
        'query_string': b'',
        'headers': [],
    }, receive)
    response = await asyncio.wait_for(
        proxy_routes._find_and_forward_chat_steer(request, None),
        timeout=1,
    )

    assert response.status_code == 200
    assert response.headers['x-instance-host'] == 'owner'
    assert slow_cancelled.is_set()
