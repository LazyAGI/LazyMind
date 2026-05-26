from httpx import ConnectError
from lazyllm.module import ModuleBase

from chat.tools import web_search as web_search_mod


class _FakeProvider(ModuleBase):
    def __init__(self, *, items=None, error=None, content=''):
        super().__init__()
        self._items = list(items or [])
        self._error = error
        self._content = content
        self.calls = []

    def forward(self, query: str, **kwargs):
        self.calls.append((query, kwargs))
        if self._error is not None:
            raise self._error
        return list(self._items)

    def get_content(self, item):
        return self._content or f"content:{item.get('title', '')}"


def test_search_provider_dispatches_lazyllm_search_kwargs():
    google = _FakeProvider()
    bing = _FakeProvider()
    bocha = _FakeProvider()
    wikipedia = _FakeProvider()

    assert web_search_mod._search_provider(google, 'google', 'g', 3) == []
    assert web_search_mod._search_provider(bing, 'bing', 'b', 4) == []
    assert web_search_mod._search_provider(bocha, 'bocha', 'o', 5) == []
    assert web_search_mod._search_provider(wikipedia, 'wikipedia', 'w', 6) == []

    assert google.calls == [('g', {'date_restrict': '', 'raise_on_error': True})]
    assert bing.calls == [('b', {'count': 4, 'raise_on_error': True})]
    assert bocha.calls == [('o', {'count': 5, 'summary': False, 'raise_on_error': True})]
    assert wikipedia.calls == [('w', {'limit': 6, 'raise_on_error': True})]


def test_web_search_auto_falls_through_runtime_error_and_empty_results(monkeypatch):
    providers = {
        'google': _FakeProvider(error=ConnectError('google down')),
        'bing': _FakeProvider(items=[]),
        'wikipedia': _FakeProvider(items=[{
            'title': 'Wiki',
            'url': 'https://example.com/wiki',
            'snippet': 'wiki snippet',
            'source': 'wikipedia',
        }], content='wiki content'),
    }

    monkeypatch.setattr(web_search_mod, '_candidate_sources', lambda _requested: ['google', 'bing', 'wikipedia'])
    monkeypatch.setattr(web_search_mod, '_provider_available', lambda source: source in providers or source == 'wikipedia')
    monkeypatch.setattr(web_search_mod, '_build_provider', lambda source, _lang: providers[source])

    result = web_search_mod.web_search('test query', source='auto', include_content=True)

    assert result == {
        'success': True,
        'tool': 'web_search',
        'result': {
            'status': 'ok',
            'query': 'test query',
            'requested_source': 'auto',
            'resolved_source': 'wikipedia',
            'tried_sources': ['google', 'bing', 'wikipedia'],
            'lang': 'zh',
            'total': 1,
            'items': [{
                'title': 'Wiki',
                'url': 'https://example.com/wiki',
                'snippet': 'wiki snippet',
                'source': 'wikipedia',
                'content': 'wiki content',
            }],
        },
    }


def test_web_search_explicit_source_keeps_no_results_without_fallback(monkeypatch):
    provider = _FakeProvider(items=[])

    monkeypatch.setattr(web_search_mod, '_build_provider', lambda _source, _lang: provider)

    result = web_search_mod.web_search('test query', source='google')

    assert result == {
        'success': True,
        'tool': 'web_search',
        'result': {
            'status': 'no_results',
            'query': 'test query',
            'requested_source': 'google',
            'resolved_source': 'google',
            'tried_sources': ['google'],
            'lang': 'zh',
            'total': 0,
            'items': [],
        },
    }
    assert provider.calls == [('test query', {'date_restrict': '', 'raise_on_error': True})]


def test_arxiv_search_dispatches_through_module_call(monkeypatch):
    provider = _FakeProvider(items=[{
        'title': 'Arxiv',
        'url': 'https://arxiv.org/abs/1234.5678',
        'snippet': 'paper snippet',
        'source': 'arxiv',
    }])

    monkeypatch.setattr(web_search_mod, 'ArxivSearch', lambda **_kwargs: provider)

    result = web_search_mod.arxiv_search('paper query', max_results=3, sort_by='submittedDate')

    assert result == {
        'success': True,
        'tool': 'arxiv_search',
        'result': {
            'status': 'ok',
            'query': 'paper query',
            'source': 'arxiv',
            'sort_by': 'submittedDate',
            'total': 1,
            'items': [{
                'title': 'Arxiv',
                'url': 'https://arxiv.org/abs/1234.5678',
                'snippet': 'paper snippet',
                'source': 'arxiv',
            }],
        },
    }
    assert provider.calls == [('paper query', {
        'max_results': 3,
        'sort_by': 'submittedDate',
        'raise_on_error': True,
    })]
