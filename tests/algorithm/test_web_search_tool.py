from lazyllm.tools.tools.search import ArxivSearch, BingSearch, BochaSearch, GoogleSearch, WikipediaSearch
from lazyllm.tools.tools.search.base import SearchBase

from lazymind.chat.engine.tools import web_search as web_search_mod


def _patch_provider(provider, monkeypatch):
    calls = []

    def fake_search(**kwargs):
        calls.append(kwargs)
        return [{
            'title': 'Result',
            'url': 'https://example.com',
            'snippet': 'snippet',
            'source': provider.source_name,
        }]

    monkeypatch.setattr(provider, 'search', fake_search)
    monkeypatch.setattr(provider, 'get_content', lambda _item: 'page content')
    return calls


def test_lazyllm_search_public_apis_are_provider_specific():
    base_apis = ['search', 'get_content', 'get_contents']
    assert WikipediaSearch.__public_apis__ == base_apis
    assert ArxivSearch.__public_apis__ == base_apis
    assert GoogleSearch.__public_apis__ == base_apis
    assert BingSearch.__public_apis__ == base_apis
    assert BochaSearch.__public_apis__ == base_apis


def test_google_search_public_api_dispatches(monkeypatch):
    provider = GoogleSearch(custom_search_api_key='key', search_engine_id='cx')
    calls = _patch_provider(provider, monkeypatch)

    result = provider.search('test query', topk=3, include_content=True, date_restrict='d7')

    assert calls == [{'query': 'test query', 'date_restrict': 'd7'}]
    assert result[0]['content'] == 'page content'


def test_bing_search_public_api_dispatches(monkeypatch):
    provider = BingSearch(subscription_key='key')
    calls = _patch_provider(provider, monkeypatch)

    provider.search('test query', topk=4)

    assert calls == [{'query': 'test query', 'count': 4}]


def test_bocha_search_public_api_dispatches(monkeypatch):
    provider = BochaSearch(api_key='key')
    calls = _patch_provider(provider, monkeypatch)

    provider.search('test query', topk=5, freshness='oneDay', summary=True)

    assert calls == [{
        'query': 'test query',
        'count': 5,
        'freshness': 'oneDay',
        'summary': True,
    }]


def test_wikipedia_search_public_api_dispatches(monkeypatch):
    provider = WikipediaSearch(base_url='https://zh.wikipedia.org')
    calls = _patch_provider(provider, monkeypatch)

    provider.search('test query', topk=6)

    assert calls == [{'query': 'test query', 'limit': 6}]


def test_arxiv_search_public_api_dispatches(monkeypatch):
    provider = ArxivSearch()
    calls = _patch_provider(provider, monkeypatch)

    result = provider.search('paper query', max_results=3, include_content=True, sort_by='submittedDate')

    assert calls == [{
        'query': 'paper query',
        'max_results': 3,
        'sort_by': 'submittedDate',
    }]
    assert result[0]['content'] == 'page content'


def test_lazymind_web_search_url_fetch_exists():
    import inspect
    assert inspect.isfunction(web_search_mod.url_fetch)
    assert web_search_mod.url_fetch.__name__ == 'url_fetch'
