import inspect

import lazyllm

from lazymind.chat.engine.tools.external_search import enable_search_result_citations
from lazymind.chat.service.utils.citations import CITATION_REFS_KEY, reset_citation_state


class FakeSearch:
    __public_apis__ = ['search', 'get_content', 'get_contents', 'meta_search', 'meta_catalog']

    def search(self, query: str, limit: int = 5):
        return [{
            'title': f'Result for {query}',
            'url': 'https://example.test/result',
            'snippet': 'Search snippet',
            'source': 'fake',
        }][:limit]

    def get_content(self, item: dict, offset: int = 0):
        return f"Fetched {item['title']} from {offset}"

    def get_contents(self, items: list):
        return [self.get_content(item) for item in items]

    def meta_search(self, query: str = ''):
        return {'items': self.search(query), 'total_count': 1}

    def meta_catalog(self):
        return {'fields': ['title']}


def _setup_state():
    state = {}
    reset_citation_state(state)
    lazyllm.globals['agentic_config'] = {'citation_state': state}
    return state


def test_search_adapter_preserves_provider_identity_and_signatures():
    provider = enable_search_result_citations(FakeSearch())

    assert type(provider).__name__ == 'FakeSearch'
    assert isinstance(provider, FakeSearch)
    assert str(inspect.signature(provider.search)) == '(query: str, limit: int = 5)'
    assert provider.__public_apis__ == FakeSearch.__public_apis__


def test_search_and_meta_search_register_results_without_changing_envelopes():
    state = _setup_state()
    provider = enable_search_result_citations(FakeSearch())

    results = provider.search('agents')
    meta = provider.meta_search('agents')

    assert isinstance(results, list)
    assert meta['total_count'] == 1
    assert results[0]['ref'] == meta['items'][0]['ref'] == '[[1.1]]'
    assert state[CITATION_REFS_KEY]['1.1']['content'] == 'Search snippet'
    assert provider.meta_catalog() == {'fields': ['title']}


def test_search_adapter_deduplicates_results_by_stable_ref():
    class DuplicateSearch(FakeSearch):
        def search(self, query: str, limit: int = 5):
            result = super().search(query, limit)[0]
            return [result, {**result, 'url': f"{result['url']}#duplicate"}]

    _setup_state()
    provider = enable_search_result_citations(DuplicateSearch())

    results = provider.search('agents')

    assert len(results) == 1
    assert results[0]['ref'] == '[[1.1]]'


def test_get_content_and_get_contents_supplement_existing_sources():
    state = _setup_state()
    provider = enable_search_result_citations(FakeSearch())
    item = provider.search('agents')[0]

    content = provider.get_content(item, offset=3)
    batch = provider.get_contents([item])

    assert content == {
        'content': 'Fetched Result for agents from 3',
        'citation_index': '1.1',
        'ref': '[[1.1]]',
    }
    assert batch == [{
        'content': 'Fetched Result for agents from 0',
        'citation_index': '1.1',
        'ref': '[[1.1]]',
    }]
    assert state[CITATION_REFS_KEY]['1.1']['content'] == 'Fetched Result for agents from 0'
