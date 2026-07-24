import asyncio

from lazymind.chat.api import model_check_routes


def test_check_uses_validate_api_key(monkeypatch):
    called = {'validate': False}

    class _FakeModule:
        def _validate_api_key(self):
            called['validate'] = True
            return True

    monkeypatch.setattr(
        model_check_routes.lazyllm,
        'OnlineModule',
        lambda **kwargs: _FakeModule(),
    )

    result = asyncio.run(model_check_routes.check_model_connection(
        source='Doubao',
        url='https://ark.cn-beijing.volces.com/api/v3/',
        api_key='test-key',
    ))

    assert result['success'] is True
    assert called['validate'] is True


def test_check_rejects_failed_validation(monkeypatch):
    class _FakeModule:
        def _validate_api_key(self):
            return False

    monkeypatch.setattr(
        model_check_routes.lazyllm,
        'OnlineModule',
        lambda **kwargs: _FakeModule(),
    )

    result = asyncio.run(model_check_routes.check_model_connection(
        source='OpenAI',
        url='https://api.openai.com/v1/',
        api_key='bad-key',
    ))

    assert result['success'] is False
    assert 'API key validation failed' in result['message']
