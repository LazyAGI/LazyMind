import pytest

from lazyllm.tools.tools.search import BingSearch, GoogleSearch


def test_bing_search_raises_on_error_when_enabled(monkeypatch):
    provider = BingSearch(subscription_key='key')

    def fake_get(*_args, **_kwargs):
        raise RuntimeError('bing failed')

    monkeypatch.setattr('lazyllm.tools.tools.search.bing_search.httpx.get', fake_get)

    with pytest.raises(RuntimeError, match='bing failed'):
        provider('query', raise_on_error=True)


def test_bing_search_keeps_empty_result_without_raise(monkeypatch):
    provider = BingSearch(subscription_key='key')

    def fake_get(*_args, **_kwargs):
        raise RuntimeError('bing failed')

    monkeypatch.setattr('lazyllm.tools.tools.search.bing_search.httpx.get', fake_get)

    assert provider('query') == []


def test_google_search_raises_on_error_when_enabled(monkeypatch):
    provider = GoogleSearch(custom_search_api_key='key', search_engine_id='cx')

    def fake_forward(*_args, **_kwargs):
        raise RuntimeError('google failed')

    monkeypatch.setattr(provider._http, 'forward', fake_forward)

    with pytest.raises(RuntimeError, match='google failed'):
        provider('query', raise_on_error=True)
