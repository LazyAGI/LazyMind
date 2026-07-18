import asyncio

from lazymind.chat.api import model_check_routes


class _FakeResponse:
    def __init__(self, status_code: int, text: str):
        self.status_code = status_code
        self.text = text


class _FakeAsyncClient:
    def __init__(self, response: _FakeResponse, *, timeout: float):
        self.response = response
        self.timeout = timeout
        self.requests = []

    async def __aenter__(self):
        return self

    async def __aexit__(self, exc_type, exc, tb):
        return False

    async def get(self, url, *, headers):
        self.requests.append((url, headers))
        return self.response


def test_official_doubao_url_uses_models_endpoint(monkeypatch):
    client = _FakeAsyncClient(_FakeResponse(200, '{"data":[]}'), timeout=30.0)
    monkeypatch.setattr(
        model_check_routes.httpx,
        'AsyncClient',
        lambda timeout: client,
    )

    result = asyncio.run(model_check_routes.check_model_connection(
        source='Doubao',
        url='https://ark.cn-beijing.volces.com/api/v3/',
        api_key='test-key',
    ))

    assert result['success'] is True
    assert client.requests == [(
        'https://ark.cn-beijing.volces.com/api/v3/models',
        {'Authorization': 'Bearer test-key'},
    )]


def test_official_doubao_check_rejects_auth_failure(monkeypatch):
    client = _FakeAsyncClient(
        _FakeResponse(401, '{"message":"invalid key"}'),
        timeout=30.0,
    )
    monkeypatch.setattr(
        model_check_routes.httpx,
        'AsyncClient',
        lambda timeout: client,
    )

    result = asyncio.run(model_check_routes.check_model_connection(
        source='Doubao',
        url='https://ark.cn-beijing.volces.com/api/v3/',
        api_key='test-key',
    ))

    assert result['success'] is False
    assert 'HTTP 401' in result['message']
    assert client.requests[0][0] == 'https://ark.cn-beijing.volces.com/api/v3/models'


def test_custom_url_does_not_use_models_endpoint(monkeypatch):
    called = {'online': False}

    class _FakeModule:
        def __call__(self, prompt):
            called['online'] = True
            return 'ok'

    monkeypatch.setattr(
        model_check_routes.lazyllm,
        'OnlineModule',
        lambda **kwargs: _FakeModule(),
    )

    result = asyncio.run(model_check_routes.check_model_connection(
        source='Doubao',
        url='https://ark.example/api/v3/',
        api_key='test-key',
    ))

    assert result['success'] is True
    assert called['online'] is True
