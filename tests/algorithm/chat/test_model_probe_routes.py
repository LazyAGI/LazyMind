import asyncio
import json

from fastapi import FastAPI
import httpx
import pytest

from lazymind.chat.api import model_probe_routes
from lazymind.chat.model_probe import runner


def call_route(monkeypatch, body, *, response=None, provider_status=200):
    requests = []
    original_client = httpx.AsyncClient

    def handle(request):
        requests.append(request)
        if isinstance(response, Exception):
            raise response
        return httpx.Response(provider_status, json=response)

    class ProviderClient(original_client):
        def __init__(self, *args, **kwargs):
            kwargs.setdefault('transport', httpx.MockTransport(handle))
            super().__init__(*args, **kwargs)

    monkeypatch.setattr(runner.httpx, 'AsyncClient', ProviderClient)
    app = FastAPI()
    app.include_router(model_probe_routes.router)

    async def invoke():
        async with original_client(transport=httpx.ASGITransport(app=app), base_url='http://algorithm') as client:
            return await client.post('/api/model/probe', json=body)

    result = asyncio.run(invoke())
    return result, requests


def request_body(**values):
    return {
        'source': 'OpenAI', 'model': 'test-model', 'model_type': 'llm',
        'url': 'https://provider.example/custom/v1/', 'api_key': 'secret-do-not-return', **values,
    }


def test_route_runs_the_actual_probe_with_validated_internal_input(monkeypatch):
    response, requests = call_route(monkeypatch, request_body(), response={
        'choices': [{'message': {'content': 'secret-do-not-return'}, 'finish_reason': 'stop'}],
    })
    assert response.status_code == 200
    body = response.json()
    assert set(body) == {'status', 'code', 'model_type', 'elapsed_ms', 'http_status'}
    assert body['status'] == 'passed'
    assert body['code'] == 'ok'
    assert body['model_type'] == 'llm'
    assert body['http_status'] == 200
    assert body['elapsed_ms'] >= 0
    assert len(requests) == 1
    assert requests[0].url.path == '/custom/v1/chat/completions'
    assert requests[0].headers['authorization'] == 'Bearer secret-do-not-return'
    assert 'secret-do-not-return' not in response.text
    assert 'provider.example' not in response.text


def test_route_sends_the_png_when_vision_is_enabled_on_a_chat_model(monkeypatch):
    response, requests = call_route(monkeypatch, request_body(vision=True), response={
        'choices': [{'message': {'content': 'OK'}}],
    })
    assert response.json()['status'] == 'passed'
    content = json.loads(requests[0].content)['messages'][0]['content']
    assert content[1]['image_url']['url'].startswith('data:image/png;base64,')
    assert 'data:image' not in response.text


@pytest.mark.parametrize(('values', 'code'), [
    ({'model_type': ''}, 'unknown_type'),
    ({'model_type': 'unknown-future-task'}, 'unsupported_type'),
    ({'model_type': 'rerank', 'source': 'OpenRouter'}, 'unsupported_provider'),
])
def test_route_reports_unsupported_without_network_or_state_changes(monkeypatch, values, code):
    response, requests = call_route(monkeypatch, request_body(**values))
    assert response.status_code == 200
    assert response.json()['status'] == 'unsupported'
    assert response.json()['code'] == code
    assert requests == []


def test_route_does_not_send_authentication_for_self_hosted_models(monkeypatch):
    response, requests = call_route(monkeypatch, request_body(source='VLLM', skip_auth=True), response={
        'choices': [{'message': {'content': 'OK'}}],
    })
    assert response.json()['status'] == 'passed'
    assert 'authorization' not in requests[0].headers


def test_route_reports_async_generation_as_accepted_without_polling(monkeypatch):
    response, requests = call_route(monkeypatch, request_body(model_type='text2video'), response={
        'id': 'secret-do-not-return', 'status': 'queued',
    })
    assert response.json()['status'] == 'accepted'
    assert response.json()['code'] == 'accepted'
    assert len(requests) == 1
    assert requests[0].url.path == '/custom/v1/videos'
    assert 'secret-do-not-return' not in response.text


def test_route_translates_transport_exceptions_without_echoing_credentials(monkeypatch):
    response, requests = call_route(monkeypatch, request_body(), response=httpx.ReadTimeout('secret-do-not-return'))
    assert response.status_code == 200
    assert response.json()['status'] == 'failed'
    assert response.json()['code'] == 'timeout'
    assert 'secret-do-not-return' not in response.text
    assert len(requests) == 1


def test_route_rejects_missing_model_without_contacting_a_provider(monkeypatch):
    response, requests = call_route(monkeypatch, request_body(model=''))
    assert response.status_code == 422
    assert requests == []


def test_credential_bearing_input_repr_is_redacted():
    from lazymind.chat.model_probe import ProbeInput

    body = model_probe_routes.ModelProbeRequest(**request_body())
    assert 'secret-do-not-return' not in repr(body)
    item = ProbeInput('model', 'llm', 'OpenAI', 'https://example.com/', api_key='secret-do-not-return')
    assert 'secret-do-not-return' not in repr(item)
