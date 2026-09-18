import asyncio
import json

import httpx
import pytest

from lazymind.chat.service.mcp_oauth import (
    MCPOAuthAdapter, MCPAuthorizationRequired, MCPAuthUnavailable,
)


def config(user='alice'):
    return dict(user_id=user, server_id='server-1', server_url='https://mcp.example',
                grant_id='grant-' + user, grant_version=1)


def install_service(monkeypatch, handler):
    client = httpx.AsyncClient
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_URL', 'http://auth-service:8000')
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'internal-test')
    monkeypatch.setattr(httpx, 'AsyncClient', lambda **kwargs: client(
        **kwargs, transport=httpx.MockTransport(handler)))


def test_identity_is_copied_and_rejected_version_is_per_operation(monkeypatch):
    calls = []

    def handler(request):
        assert request.url.path == '/api/authservice/v1/mcp-oauth/token'
        assert request.headers['X-LazyMind-Internal-Token'] == 'internal-test'
        data = json.loads(request.content)
        calls.append(data)
        version = 8 if data['user_id'] == 'alice' else 15
        return httpx.Response(200, json={'code': 200, 'data': {
            'status': 'authorized', 'access_token': data['user_id'] + '-token',
            'token_version': version, 'expires_at': '2099-01-01T00:00:00Z'}})
    install_service(monkeypatch, handler)
    source = config()
    alice = MCPOAuthAdapter(source, 'https://mcp.example')
    bob = MCPOAuthAdapter(config('bob'), 'https://mcp.example')
    source['user_id'] = 'mallory'

    async def operation(adapter, user):
        assert await adapter.headers() == {'Authorization': 'Bearer ' + user + '-token'}
        await asyncio.sleep(0)
        assert await adapter.recover()

    async def run():
        await asyncio.gather(operation(alice, 'alice'), operation(bob, 'bob'))
    asyncio.run(run())
    assert {(c['user_id'], c.get('rejected_token_version')) for c in calls} == {
        ('alice', None), ('bob', None), ('alice', 8), ('bob', 15)}


@pytest.mark.parametrize('response,expected', [
    (httpx.Response(401, json={'code': 1000901, 'status': 'needs_authorization', 'message': 'SECRET'}),
     MCPAuthorizationRequired),
    (httpx.Response(502, text='SECRET'), MCPAuthUnavailable),
    (httpx.Response(200, json={'data': {'access_token': 'SECRET'}}), MCPAuthUnavailable),
])
def test_failures_are_sanitized(monkeypatch, response, expected):
    install_service(monkeypatch, lambda request: response)
    with pytest.raises(expected) as caught:
        asyncio.run(MCPOAuthAdapter(config(), 'https://mcp.example').headers())
    assert 'SECRET' not in str(caught.value)


def test_missing_identity_and_mismatched_url_fail_closed():
    for reference in ({}, {**config(), 'user_id': ''}, {**config(), 'server_url': 'https://other.example'}):
        with pytest.raises(MCPAuthorizationRequired):
            MCPOAuthAdapter(reference, 'https://mcp.example')


def test_parallel_calls_on_same_tool_reject_their_own_version(monkeypatch):
    requests = []

    def handler(request):
        data = json.loads(request.content)
        requests.append(data)
        return httpx.Response(200, json={'code': 200, 'data': {
            'status': 'authorized', 'access_token': 'token', 'token_version': len(requests)}})
    install_service(monkeypatch, handler)
    adapter = MCPOAuthAdapter(config(), 'https://mcp.example')

    async def operation():
        await adapter.headers()
        await asyncio.sleep(0)
        await adapter.recover()

    async def run():
        await asyncio.gather(operation(), operation())
    asyncio.run(run())
    assert [r.get('rejected_token_version') for r in requests] == [None, None, 1, 2]


def test_auth_service_url_accepts_existing_api_prefix(monkeypatch):
    paths = []

    def handler(request):
        paths.append(request.url.path)
        return httpx.Response(200, json={'code': 200, 'data': {
            'status': 'authorized', 'access_token': 'token', 'token_version': 1}})
    install_service(monkeypatch, handler)
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_URL', 'http://auth-service:8000/api/authservice/')
    asyncio.run(MCPOAuthAdapter(config(), 'https://mcp.example').headers())
    assert paths == ['/api/authservice/v1/mcp-oauth/token']
