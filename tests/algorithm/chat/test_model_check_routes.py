import asyncio

import httpx
import pytest

from lazymind.chat.api import model_check_routes
from lazymind.chat.provider_connection import check_provider_connection


@pytest.mark.parametrize(('status', 'payload', 'reason'), [
    (200, {'data': []}, ''),
    (401, {'error': {'message': 'private-key'}}, 'authentication_failed'),
    (403, {}, 'permission_denied'),
    (404, {}, 'model_list_unavailable'),
    (402, {}, 'quota_exceeded'),
    (429, {}, 'rate_limited'),
    (429, {'error': {'code': 'insufficient_quota'}}, 'quota_exceeded'),
    (200, {'error': {'message': 'private-key'}}, 'upstream_error'),
    (200, {'code': 50505, 'message': 'private-key', 'data': []}, 'upstream_error'),
    (200, {'code': 'insufficient_quota', 'data': []}, 'quota_exceeded'),
    (200, {'code': 0, 'data': []}, ''),
])
def test_check_is_read_only_and_returns_safe_reasons(status, payload, reason):
    calls = []

    def respond(request):
        calls.append(request)
        return httpx.Response(status, json=payload)

    async def invoke():
        async with httpx.AsyncClient(transport=httpx.MockTransport(respond)) as client:
            return await check_provider_connection(source='OpenAI', url='https://provider.example/api/v3/',
                                                   api_key='private-key', model='ignored-model', client=client)

    result = asyncio.run(invoke())
    assert result['success'] is (not reason)
    assert result.get('reason', '') == reason
    assert len(calls) == 1
    assert calls[0].method == 'GET'
    assert calls[0].url.path == '/api/v3/models'
    assert calls[0].headers['authorization'] == 'Bearer private-key'
    assert 'private-key' not in str(result)
    assert 'provider.example' not in str(result)


def test_check_preserves_core_models_url_and_keyless_authentication():
    calls = []

    def respond(request):
        calls.append(request)
        return httpx.Response(200, json={'data': []})

    async def invoke():
        async with httpx.AsyncClient(transport=httpx.MockTransport(respond)) as client:
            return await check_provider_connection(source='OpenAI', url='http://local.example/',
                                                   api_key='', skip_auth=True,
                                                   models_url='http://local.example/proxy/v4/models', client=client)
    assert asyncio.run(invoke())['success']
    assert calls[0].url.path == '/proxy/v4/models'
    assert 'authorization' not in calls[0].headers


def test_check_does_not_inherit_a_process_key(monkeypatch):
    monkeypatch.setenv('OPENAI_API_KEY', 'process-secret')
    calls = []

    def respond(request):
        calls.append(request)
        return httpx.Response(401, json={})

    async def invoke():
        async with httpx.AsyncClient(transport=httpx.MockTransport(respond)) as client:
            return await check_provider_connection(source='OpenAI', url='https://example.test/v1/', client=client)
    assert asyncio.run(invoke())['reason'] == 'authentication_failed'
    assert 'authorization' not in calls[0].headers


def test_check_timeout_does_not_expose_transport_details():
    def respond(request):
        raise httpx.ReadTimeout('private-key provider.example')

    async def invoke():
        async with httpx.AsyncClient(transport=httpx.MockTransport(respond)) as client:
            return await check_provider_connection(source='OpenAI', url='https://example.test/v1/', client=client)
    result = asyncio.run(invoke())
    assert result['reason'] == 'timeout'
    assert 'private-key' not in str(result)


def test_route_forwards_only_configuration_and_never_constructs_a_default_model(monkeypatch):
    called = []

    async def check(**kwargs):
        called.append(kwargs)
        return {'success': False, 'reason': 'model_list_unavailable', 'message': 'provider connection check failed'}
    monkeypatch.setattr(model_check_routes, 'check_provider_connection', check)
    result = asyncio.run(model_check_routes.check_model_connection(
        source='Doubao', url='https://example.test/api/v3/', api_key='fixture', model='named-model',
    ))
    assert result['reason'] == 'model_list_unavailable'
    assert called[0]['model'] == 'named-model'


def test_check_applies_gateway_headers_and_query_without_inference_body():
    calls = []

    def respond(request):
        calls.append(request)
        return httpx.Response(200, json={'data': []})

    async def invoke():
        async with httpx.AsyncClient(transport=httpx.MockTransport(respond)) as client:
            return await check_provider_connection(
                source='Claude', url='https://example.test/proxy', api_key='saved-key', client=client,
                request_advanced={'headers': {'X-Gateway-Token': 'private'}, 'query': {'tenant': 'two'},
                                  'extra_body': {'metadata': {'p': 1}}},
            )
    assert asyncio.run(invoke())['success']
    assert calls[0].headers['x-api-key'] == 'saved-key'
    assert calls[0].headers['x-gateway-token'] == 'private'
    assert calls[0].url.params['tenant'] == 'two'
    assert calls[0].content == b''
