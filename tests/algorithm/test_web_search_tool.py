from lazyllm.tools.tools.search import ArxivSearch, BingSearch, BochaSearch, GoogleSearch, WikipediaSearch

from lazymind.chat.engine.tools import web_search as web_search_mod


def test_lazyllm_search_public_apis_are_provider_specific():
    base_apis = ['search', 'get_content', 'get_contents']
    assert WikipediaSearch.__public_apis__ == base_apis
    assert ArxivSearch.__public_apis__ == base_apis
    assert GoogleSearch.__public_apis__ == base_apis
    assert BingSearch.__public_apis__ == base_apis
    assert BochaSearch.__public_apis__ == base_apis


def test_lazymind_web_search_url_fetch_exists():
    import inspect
    assert inspect.isfunction(web_search_mod.url_fetch)
    assert web_search_mod.url_fetch.__name__ == 'url_fetch'


def test_url_fetch_batches_multiple_urls_and_preserves_partial_failures(monkeypatch):
    def fake_fetch(url):
        if url.endswith('/bad'):
            raise RuntimeError('unavailable')
        return {'final_url': url, 'content': f'content:{url}'}

    monkeypatch.setattr(web_search_mod, 'fetch_url_content', fake_fetch)

    payload = web_search_mod.url_fetch(urls=[
        'https://example.test/one',
        'https://example.test/bad',
        'https://example.test/one',
        'https://example.test/two',
    ])

    result = payload['result']
    assert result['total'] == 3
    assert result['succeeded'] == 2
    assert result['failed'] == 1
    assert [item['url'] for item in result['results']] == [
        'https://example.test/one',
        'https://example.test/bad',
        'https://example.test/two',
    ]
    assert result['results'][1]['success'] is False
