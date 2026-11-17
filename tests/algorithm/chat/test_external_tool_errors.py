"""Provider failures traverse the real registry, ToolManager and Chat HTTP boundary."""

import json

import httpx
import pytest
import requests
from fastapi import FastAPI
from fastapi.testclient import TestClient

from lazymind.chat.api import chat_routes, knowledge_search_routes
from lazymind.chat.engine.tools import multimodal
from lazymind.chat.service import external_tools


@pytest.fixture
def gateway(monkeypatch):
    async def ready():
        pass

    monkeypatch.setattr(chat_routes, '_chat_service', ready)
    monkeypatch.setattr(knowledge_search_routes, 'expected_internal_token', lambda: 'internal-test')
    config = next(item for item in external_tools.DEFAULT_TOOLS if item.name == 'image_generator')
    monkeypatch.setattr(external_tools, 'DEFAULT_TOOLS', [config])
    app = FastAPI()
    app.include_router(chat_routes.router)
    with TestClient(app) as client:
        yield client


def invoke(client):
    return client.post('/api/chat/tools/execute', headers={'X-LazyMind-Internal-Token': 'internal-test'}, json={
        'tool_name': 'image_generator',
        'llm_config': {'text2image': {
            'source': 'siliconflow', 'model': 'Kwai-Kolors/Kolors', 'api_key': 'private-model-secret',
        }},
        'tool_config': {'tavily': 'private-tool-secret'},
        'arguments': {'prompt': 'A calm presentation background', 'image_size': '1280x720'},
    })


@pytest.mark.parametrize('status,code,retryable', [
    (400, 'INVALID_ARGUMENT', False),
    (401, 'PROVIDER_REJECTED', False),
    (451, 'PROVIDER_REJECTED', False),
    (429, 'RATE_LIMITED', True),
    (503, 'UNAVAILABLE', True),
])
@pytest.mark.parametrize('sdk', ['requests', 'httpx'])
def test_provider_error_keeps_status_reason_and_trace_without_secrets(
        gateway, monkeypatch, status, code, retryable, sdk):
    calls = []

    def provider(role, prompt, **kwargs):
        calls.append(role)
        message = ('Provider rejected this request: private-model-secret private-tool-secret '
                   'https://internal.invalid/path?token=hidden /private/customer/report.pdf')
        body = {'error': {'code': 'provider_policy', 'message': message}}
        headers = {'x-siliconcloud-trace-id': 'request-123', 'Authorization': 'Bearer hidden-header'}
        if sdk == 'httpx':
            response = httpx.Response(status, json=body, headers=headers,
                                      request=httpx.Request('POST', 'https://provider.invalid/images'))
        else:
            response = requests.Response()
            response.status_code = status
            response._content = json.dumps(body).encode()
            response.headers.update(headers)
        response.raise_for_status()

    monkeypatch.setattr(multimodal, 'run_image_model', provider)
    response = invoke(gateway)
    assert response.status_code == 502
    detail = response.json()['detail']
    assert detail['code'] == code and detail['retryable'] is retryable
    assert f'HTTP {status}' in detail['message'] and 'Provider rejected this request' in detail['message']
    assert detail['upstream'] == {'http_status': status, 'code': 'provider_policy', 'request_id': 'request-123'}
    for private in ('private-model-secret', 'private-tool-secret', 'internal.invalid', '/private/', 'hidden-header'):
        assert private not in response.text
    assert calls == ['image_generator']


@pytest.mark.parametrize('body', ['html', 'oversized', 'unread_stream'])
def test_provider_rejection_without_json_does_not_invent_a_reason(gateway, monkeypatch, body):
    def provider(*args, **kwargs):
        if body == 'unread_stream':
            response = httpx.Response(451, stream=httpx.ByteStream(b'private internal response'),
                                      request=httpx.Request('POST', 'https://provider.invalid/images'))
            response.raise_for_status()
        response = requests.Response()
        response.status_code = 451
        response._content = (b'<html>private internal response</html>' if body == 'html'
                             else json.dumps({'message': 'private ' * 5000}).encode())
        response.raise_for_status()

    monkeypatch.setattr(multimodal, 'run_image_model', provider)
    detail = invoke(gateway).json()['detail']
    assert detail['code'] == 'PROVIDER_REJECTED'
    assert detail['upstream'] == {'http_status': 451}
    assert 'no public error details' in detail['message']
    assert 'private' not in detail['message']


@pytest.mark.parametrize('failure,code,retryable', [
    (requests.ReadTimeout('private timeout context'), 'DEADLINE_EXCEEDED', False),
    (requests.ConnectionError('private connection context'), 'UNAVAILABLE', True),
    (RuntimeError('private exception context'), 'TOOL_EXECUTION_FAILED', False),
])
def test_transport_and_unexpected_failures_keep_recovery_semantics(gateway, monkeypatch, failure, code, retryable):
    def provider(*args, **kwargs):
        raise failure

    monkeypatch.setattr(multimodal, 'run_image_model', provider)
    detail = invoke(gateway).json()['detail']
    assert detail['code'] == code and detail['retryable'] is retryable
    assert 'private' not in json.dumps(detail) and 'authorization' not in detail['message']


def test_failed_result_keeps_actionable_tool_message(gateway, monkeypatch):
    monkeypatch.setattr(multimodal, 'run_image_model', lambda *a, **kw: {
        'ok': False, 'value': 'image_size must be 1280x720; api_key=private-model-secret',
    })
    detail = invoke(gateway).json()['detail']
    assert detail['code'] == 'TOOL_EXECUTION_FAILED'
    assert 'image_size must be 1280x720' in detail['message']
    assert 'private-model-secret' not in detail['message']
