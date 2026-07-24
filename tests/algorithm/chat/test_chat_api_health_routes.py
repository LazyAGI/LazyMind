import asyncio
import importlib.util
import sys
from pathlib import Path

import httpx


class _FakeAsyncClient:
    def __init__(self, *, timeout):
        self.timeout = timeout
        self.requested_urls = []

    async def __aenter__(self):
        return self

    async def __aexit__(self, exc_type, exc, tb):
        return False

    async def get(self, url):
        self.requested_urls.append(url)
        return {'ok': True}


def _load_health_routes_module():
    module_name = 'test_health_routes_isolated'
    module_path = Path(__file__).resolve().parents[3] / 'algorithm/lazymind/chat/api/health_routes.py'
    spec = importlib.util.spec_from_file_location(module_name, module_path)
    module = importlib.util.module_from_spec(spec)
    sys.modules.pop(module_name, None)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def test_health_route_reports_reachable_document_server(monkeypatch):
    module = _load_health_routes_module()
    client = _FakeAsyncClient(timeout=3.0)

    monkeypatch.setenv('LAZYMIND_DOCUMENT_SERVER_URL', 'http://doc-service:8080/path/')
    monkeypatch.setattr(module.httpx, 'AsyncClient', lambda timeout: client)

    result = asyncio.run(module.health())

    assert result == {
        'document_server_url': 'http://doc-service:8080/path/',
        'document_server_reachable': True,
    }
    assert client.requested_urls == ['http://doc-service:8080/path/']


def test_health_route_captures_connection_error(monkeypatch):
    module = _load_health_routes_module()

    class _FailingAsyncClient(_FakeAsyncClient):
        async def get(self, url):
            raise httpx.ConnectError('network down')

    monkeypatch.setenv('LAZYMIND_DOCUMENT_SERVER_URL', 'http://doc-service:8080')
    monkeypatch.setattr(module.httpx, 'AsyncClient', lambda timeout: _FailingAsyncClient(timeout=timeout))

    result = asyncio.run(module.health())

    assert result['document_server_url'] == 'http://doc-service:8080'
    assert result['document_server_reachable'] is False
    assert 'network down' in result['document_server_error']
