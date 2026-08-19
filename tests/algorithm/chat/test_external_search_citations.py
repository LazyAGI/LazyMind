import time

import lazyllm
from lazyllm.tools.agent.toolsManager import ToolManager
from lazyllm.tools.tools.search import SearchBase

from lazymind.chat.engine.tools.infra import CitationResultMiddleware
from lazymind.chat.service.utils.citations import CITATION_REFS_KEY, reset_citation_state


class FakeSearch(SearchBase):
    __public_apis__ = ['search', 'get_content', 'get_contents', 'meta_search']

    def __init__(self):
        super().__init__(source_name='fake', skip_auth=True)
        self.last_results = []

    def search(self, query: str, limit: int = 5):
        """Return deterministic external search results for citation tests."""
        if query == 'slow':
            time.sleep(0.04)
        results = [{
            'title': f'Result for {query}',
            'url': f'https://example.test/{query}',
            'snippet': f'Snippet for {query}',
            'source': 'fake',
        }][:limit]
        self.last_results = results
        return results

    def get_content(self, item: dict, offset: int = 0):
        """Return fetched content with the unchanged provider identity."""
        return {
            'title': item.get('title', ''),
            'url': item.get('url', ''),
            'snippet': item.get('snippet', ''),
            'source': item.get('source', ''),
            'extra': dict(item.get('extra') or {}),
            'content': f"Fetched {item['title']} from {offset}",
        }

    def get_contents(self, items: list[dict]):
        """Return fetched content for multiple provider results."""
        return [self.get_content(item) for item in items]

    def meta_search(self, query: str = ''):
        """Return fake metadata search results."""
        return {'items': self.search(query), 'total_count': 1}


def _setup_manager(provider=None):
    state = {}
    reset_citation_state(state)
    lazyllm.globals['agentic_config'] = {'citation_state': state}
    provider = provider or FakeSearch()
    manager = CitationResultMiddleware(ToolManager([provider]))
    return state, provider, manager


def _call(manager, name, arguments):
    result = manager({'function': {'name': name, 'arguments': arguments}})[0]
    assert result['ok'] is True
    return result['value']


def test_search_and_meta_search_register_without_changing_envelopes_or_provider_objects():
    state, provider, manager = _setup_manager()

    results = _call(manager, 'FakeSearch_search', {'query': 'agents'})
    raw_results = provider.last_results
    meta = _call(manager, 'FakeSearch_meta_search', {'query': 'agents'})

    assert isinstance(results, list)
    assert meta['total_count'] == 1
    assert results[0]['ref'] == meta['items'][0]['ref'] == '[[1.1]]'
    assert 'ref' not in raw_results[0]
    assert state[CITATION_REFS_KEY]['1.1']['content'] == 'Snippet for agents'


def test_collect_only_registers_sources_without_exposing_citation_refs():
    state, provider, manager = _setup_manager()
    lazyllm.globals['agentic_config']['citation_mode'] = 'collect_only'

    results = _call(manager, 'FakeSearch_search', {'query': 'subagent'})

    assert results == provider.last_results
    assert 'ref' not in results[0]
    assert 'citation_index' not in results[0]
    assert state[CITATION_REFS_KEY]['1.1']['title'] == 'Result for subagent'


def test_ranked_duplicates_are_preserved_and_share_registry_ref():
    class DuplicateSearch(FakeSearch):
        def search(self, query: str, limit: int = 5):
            result = super().search(query, limit)[0]
            results = [result, {
                **result,
                'url': f"{result['url']}#duplicate",
                'snippet': 'Lower-ranked snippet',
                'score': 0.5,
            }]
            self.last_results = results
            return results

    state, _, manager = _setup_manager(DuplicateSearch())
    results = _call(manager, 'DuplicateSearch_search', {'query': 'agents'})

    assert len(results) == 2
    assert [item['snippet'] for item in results] == [
        'Snippet for agents', 'Lower-ranked snippet',
    ]
    assert results[0]['ref'] == results[1]['ref'] == '[[1.1]]'
    assert len(state[CITATION_REFS_KEY]) == 1


def test_tavily_extra_images_are_attached_to_parent_source_only():
    class ImageSearch(FakeSearch):
        def search(self, query: str, limit: int = 5):
            return [{
                'title': 'Article',
                'url': 'https://example.test/article',
                'snippet': 'Article snippet',
                'source': 'tavily',
                'extra': {
                    'images': [
                        {'url': 'https://images.example.test/result.jpg'},
                        'https://images.example.test/result.jpg',
                    ],
                },
            }, {
                'title': 'summary',
                'url': '',
                'snippet': 'Summary without an evidence URL',
                'source': 'tavily',
                'extra': {'images': ['https://images.example.test/orphan.jpg']},
            }]

    state, _, manager = _setup_manager(ImageSearch())
    results = _call(manager, 'ImageSearch_search', {'query': 'agents'})

    assert len(results) == 2
    assert results[0]['ref'] == '[[1.1]]'
    assert 'ref' not in results[1]
    assert state[CITATION_REFS_KEY] == {
        '1.1': {
            'source_type': 'external',
            'title': 'Article',
            'url': 'https://example.test/article',
            'content': 'Article snippet',
            'image_urls': ['https://images.example.test/result.jpg'],
        },
    }


def test_get_content_and_get_contents_return_identity_with_agent_visible_refs():
    state, provider, manager = _setup_manager()
    item = _call(manager, 'FakeSearch_search', {'query': 'agents'})[0]

    content = _call(manager, 'FakeSearch_get_content', {'item': item, 'offset': 3})
    batch = _call(manager, 'FakeSearch_get_contents', {'items': [item]})

    assert content['content'] == 'Fetched Result for agents from 3'
    assert content['ref'] == '[[1.1]]'
    assert batch[0]['content'] == 'Fetched Result for agents from 0'
    assert batch[0]['ref'] == '[[1.1]]'
    assert state[CITATION_REFS_KEY]['1.1']['content'] == 'Fetched Result for agents from 0'


def test_get_content_replaces_stale_history_ref_with_current_request_ref():
    _, _, manager = _setup_manager()
    historical_item = _call(manager, 'FakeSearch_search', {'query': 'historical'})[0]
    assert historical_item['ref'] == '[[1.1]]'

    current_state = {}
    reset_citation_state(current_state)
    lazyllm.globals['agentic_config'] = {'citation_state': current_state}
    _call(manager, 'FakeSearch_search', {'query': 'current'})

    fetched = _call(manager, 'FakeSearch_get_content', {'item': historical_item})

    assert fetched['ref'] == '[[2.1]]'
    assert fetched['citation_index'] == '2.1'
    assert fetched['content'] == 'Fetched Result for historical from 0'
    assert current_state[CITATION_REFS_KEY]['2.1']['url'] == historical_item['url']


def test_parallel_tools_register_citations_in_original_tool_call_order():
    state, _, manager = _setup_manager()
    calls = [
        {'function': {'name': 'FakeSearch_search', 'arguments': {'query': 'slow'}}},
        {'function': {'name': 'FakeSearch_search', 'arguments': {'query': 'fast'}}},
    ]

    results = manager(calls)

    assert results[0]['value'][0]['ref'] == '[[1.1]]'
    assert results[1]['value'][0]['ref'] == '[[2.1]]'
    assert state[CITATION_REFS_KEY]['1.1']['url'] == 'https://example.test/slow'
    assert state[CITATION_REFS_KEY]['2.1']['url'] == 'https://example.test/fast'
