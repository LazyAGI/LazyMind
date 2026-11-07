"""A signature must describe the exact embedding paths used by knowledge indexing."""
import hashlib
import json
import struct
from unittest.mock import patch

from fastapi import FastAPI
import httpx
import requests

from lazymind.chat.api import model_probe_routes
from lazymind.chat.model_probe.contracts import ProbeInput
from lazymind.chat.model_probe.signature import sign_embedding_model


def _digest(values):
    encoded = struct.pack('<I', len(values)) + b''.join(struct.pack('<f', value) for value in values)
    return hashlib.sha256(encoded).hexdigest()


def test_signature_uses_runtime_query_and_passage_prefixes_without_returning_vectors():
    inputs = []

    def post(_url, *, json, **_kwargs):
        inputs.append(json['input'][0])
        response = requests.Response()
        response.status_code = 200
        vector = [1.0, 0.0] if json['input'][0].startswith('q: ') else [0.0, 1.0]
        response._content = __import__('json').dumps({'data': [{'embedding': vector}]}).encode()
        return response

    request = ProbeInput(
        model='fixture-vector', model_type='embed', source='openai',
        url='https://provider.example/v1/', api_key='secret-do-not-return',
        embedding={'query_prefix': 'q: ', 'passage_prefix': 'p: '},
    )
    with patch('requests.post', side_effect=post), patch('requests.Session.post', side_effect=post):
        result = sign_embedding_model(request)

    assert inputs == ['p: lazymind', 'q: lazymind']
    assert result == {
        'status': 'ready', 'code': 'ok', 'model_type': 'embed', 'dimensions': 2,
        'passage_signature': _digest([0.0, 1.0]),
        'query_signature': _digest([1.0, 0.0]),
    }
    assert 'secret-do-not-return' not in json.dumps(result)
    assert '0.0' not in json.dumps(result)


def test_signature_rejects_malformed_vectors_and_non_embedding_models():
    with patch('requests.post') as post:
        result = sign_embedding_model(ProbeInput('chat', 'llm', 'openai', 'https://provider.example/v1/'))
    assert result['status'] == 'unsupported'
    assert post.call_count == 0

    def post_bad(_url, *, json, **_kwargs):
        response = requests.Response()
        response.status_code = 200
        response._content = b'{"data":[{"embedding":[1.0,"bad"]}]}'
        return response

    with patch('requests.post', side_effect=post_bad), patch('requests.Session.post', side_effect=post_bad):
        result = sign_embedding_model(ProbeInput('vector', 'embed', 'openai', 'https://provider.example/v1/'))
    assert result['status'] == 'failed'
    assert 'signature' not in result


def test_static_embedding_role_cannot_sign_a_different_candidate_model():
    request = ProbeInput('vector', 'embed', 'openai', 'https://provider.example/v1/')
    with patch('lazymind.chat.model_probe.signature.get_dynamic_role_slot_map', return_value={}), \
            patch('requests.post') as post:
        result = sign_embedding_model(request)
    assert result == {'status': 'unsupported', 'code': 'unsupported_provider', 'model_type': 'embed'}
    post.assert_not_called()


def test_signature_route_returns_only_safe_metadata():
    app = FastAPI()
    app.include_router(model_probe_routes.router)

    def post(_url, *, json, **_kwargs):
        response = requests.Response()
        response.status_code = 200
        response._content = b'{"data":[{"embedding":[1.0,0.0]}]}'
        return response

    async def call():
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://algorithm') as client:
            return await client.post('/api/model/embedding-signature', json={
                'source': 'openai', 'model': 'fixture-vector', 'model_type': 'embed',
                'url': 'https://provider.example/v1/', 'api_key': 'secret-do-not-return',
            })

    import asyncio
    with patch('requests.post', side_effect=post), patch('requests.Session.post', side_effect=post):
        response = asyncio.run(call())
    assert response.status_code == 200
    assert set(response.json()) == {
        'status', 'code', 'model_type', 'dimensions', 'passage_signature', 'query_signature',
    }
    assert 'secret-do-not-return' not in response.text
