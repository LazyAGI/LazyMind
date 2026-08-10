import json
from copy import deepcopy

import lazyllm
import pytest
from lazyllm.tools.tools.search import ArxivSearch, BingSearch, BochaSearch, GoogleSearch, WikipediaSearch

from lazymind.chat.engine.tools import web_search as web_search_mod
from lazymind.chat.engine.tools.infra import enable_search_result_citations
from lazymind.chat.service.utils.citations import (
    CITATION_REFS_KEY,
    register_external_search_result,
    reset_citation_state,
)


@pytest.fixture(autouse=True)
def reset_web_tool_state():
    previous = lazyllm.globals.get('agentic_config')
    state = {}
    reset_citation_state(state)
    lazyllm.globals['agentic_config'] = {'citation_state': state}
    try:
        yield state
    finally:
        lazyllm.globals['agentic_config'] = previous or {}


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
    assert all(item['result']['page_ref'] for item in result['results'] if item['success'])


def test_url_fetch_registers_sources_and_follows_page_links(monkeypatch, reset_web_tool_state):
    search = register_external_search_result({
        'title': 'Root from search',
        'url': 'https://example.test/root',
    }, reset_web_tool_state)
    pages = {
        'https://example.test/root': {
            'title': 'Root',
            'content': 'Root content',
            'links': [{'id': 1, 'text': 'Child', 'target_url': 'https://example.test/child'}],
        },
        'https://example.test/child': {
            'title': 'Child',
            'content': 'Child content',
            'links': [{'id': 1, 'text': 'Root', 'target_url': 'https://example.test/root'}],
        },
    }

    def fake_fetch(url):
        page = deepcopy(pages[url])
        page.update({'status': 'ok', 'source_status': 'ok', 'url': url, 'final_url': url})
        return page

    monkeypatch.setattr(web_search_mod, 'fetch_url_content', fake_fetch)
    url_fetch = enable_search_result_citations(web_search_mod.url_fetch)
    root = url_fetch(url='https://example.test/root')['result']
    child = url_fetch(page_ref=root['page_ref'], link_id=1)['result']

    assert root['citation_index'] == search['citation_index'] == '1.1'
    assert root['source_action'] == 'supplemented_source'
    assert child['citation_index'] == '2.1'
    assert child['source_action'] == 'new_source'
    assert child['parent_page_ref'] == root['page_ref']
    assert child['depth'] == 1
    assert len(reset_web_tool_state[CITATION_REFS_KEY]) == 2
    with pytest.raises(ValueError, match='navigation_cycle'):
        web_search_mod.url_fetch(page_ref=child['page_ref'], link_id=1)


def test_restore_web_navigation_state_supports_cross_turn_click(monkeypatch):
    history = [{
        'role': 'tool',
        'name': 'url_fetch',
        'content': json.dumps({
            'success': True,
            'tool': 'url_fetch',
            'result': {
                'url': 'https://example.test/root',
                'final_url': 'https://example.test/root',
                'page_ref': 'page_previous',
                'parent_page_ref': None,
                'via_link_id': None,
                'depth': 0,
                'links': [{'id': 1, 'target_url': 'https://example.test/child'}],
            },
        }),
    }]
    lazyllm.globals['agentic_config']['web_navigation_state'] = (
        web_search_mod.restore_web_navigation_state(history)
    )
    monkeypatch.setattr(web_search_mod, 'fetch_url_content', lambda url: {
        'status': 'ok',
        'source_status': 'ok',
        'url': url,
        'final_url': url,
        'content': 'Child content',
        'links': [],
    })

    child = web_search_mod.url_fetch(page_ref='page_previous', link_id=1)['result']

    assert child['final_url'] == 'https://example.test/child'
    assert child['parent_page_ref'] == 'page_previous'
    assert child['depth'] == 1
