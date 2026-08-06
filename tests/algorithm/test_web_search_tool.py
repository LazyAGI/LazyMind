import json
from copy import deepcopy

import lazyllm
import pytest
from lazyllm.tools.tools.search import ArxivSearch, BingSearch, BochaSearch, GoogleSearch, WikipediaSearch

from lazymind.chat.engine.tools import web_search as web_search_mod
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


def test_url_fetch_supplements_search_source_and_registers_direct_page(monkeypatch, reset_web_tool_state):
    search = register_external_search_result({
        'title': 'Search title',
        'url': 'https://example.test/search-result',
        'snippet': 'Search snippet',
    }, reset_web_tool_state)
    monkeypatch.setattr(web_search_mod, 'fetch_url_content', lambda url: {
        'status': 'ok',
        'source_status': 'ok',
        'url': url,
        'final_url': url,
        'title': 'Fetched title',
        'content': 'Fetched content',
        'links': [],
    })

    fetched = web_search_mod.url_fetch(url='https://example.test/search-result')['result']
    direct = web_search_mod.url_fetch(url='https://example.test/direct')['result']

    assert fetched['citation_index'] == search['citation_index'] == '1.1'
    assert fetched['source_action'] == 'supplemented_source'
    assert direct['citation_index'] == '2.1'
    assert direct['source_action'] == 'new_source'


def test_url_fetch_deduplicates_page_links_by_normalized_url(monkeypatch):
    monkeypatch.setattr(web_search_mod, 'fetch_url_content', lambda url: {
        'status': 'ok',
        'source_status': 'ok',
        'url': url,
        'final_url': url,
        'content': 'Root content',
        'links': [
            {'id': 1, 'text': 'First', 'target_url': 'https://example.test/child?utm_source=one'},
            {'id': 2, 'text': 'Duplicate', 'target_url': 'https://example.test/child#section'},
            {'id': 3, 'text': 'Other', 'target_url': 'https://example.test/other'},
        ],
    })

    page = web_search_mod.url_fetch(url='https://example.test/root')['result']

    assert [(item['id'], item['text']) for item in page['links']] == [(1, 'First'), (2, 'Other')]


def test_url_fetch_supports_nested_page_local_clicks(monkeypatch, reset_web_tool_state):
    pages = {
        'https://example.test/root': {
            'title': 'Root',
            'content': 'Root content',
            'links': [{'id': 1, 'text': 'Child', 'target_url': 'https://example.test/child'}],
        },
        'https://example.test/child': {
            'title': 'Child',
            'content': 'Child content',
            'links': [{'id': 1, 'text': 'Grandchild', 'target_url': 'https://example.test/grandchild'}],
        },
        'https://example.test/grandchild': {
            'title': 'Grandchild',
            'content': 'Grandchild content',
            'links': [{'id': 1, 'text': 'Root', 'target_url': 'https://example.test/root'}],
        },
    }

    def fake_fetch(url):
        page = deepcopy(pages[url])
        page.update({'status': 'ok', 'source_status': 'ok', 'url': url, 'final_url': url})
        return page

    monkeypatch.setattr(web_search_mod, 'fetch_url_content', fake_fetch)
    root = web_search_mod.url_fetch(url='https://example.test/root')['result']
    assert len(reset_web_tool_state[CITATION_REFS_KEY]) == 1

    child = web_search_mod.url_fetch(page_ref=root['page_ref'], link_id=1)['result']
    grandchild = web_search_mod.url_fetch(page_ref=child['page_ref'], link_id=1)['result']

    assert child['parent_page_ref'] == root['page_ref']
    assert child['depth'] == 1
    assert grandchild['parent_page_ref'] == child['page_ref']
    assert grandchild['depth'] == 2
    assert len(reset_web_tool_state[CITATION_REFS_KEY]) == 3
    with pytest.raises(ValueError, match='navigation_cycle'):
        web_search_mod.url_fetch(page_ref=grandchild['page_ref'], link_id=1)


def test_url_fetch_rejects_canonical_and_redirect_cycles(monkeypatch):
    pages = {
        'https://example.test/root': {
            'url': 'https://example.test/root',
            'final_url': 'https://example.test/root',
            'metadata': {'canonical_url': 'https://example.test/canonical'},
            'links': [{'id': 1, 'target_url': 'https://example.test/canonical'}],
        },
        'https://example.test/child': {
            'url': 'https://example.test/child',
            'final_url': 'https://example.test/root',
            'links': [],
        },
    }
    monkeypatch.setattr(web_search_mod, 'fetch_url_content', lambda url: {
        'status': 'ok', 'source_status': 'ok', 'content': 'content', **deepcopy(pages[url]),
    })

    root = web_search_mod.url_fetch(url='https://example.test/root')['result']
    with pytest.raises(ValueError, match='navigation_cycle'):
        web_search_mod.url_fetch(page_ref=root['page_ref'], link_id=1)

    root['links'][0]['target_url'] = 'https://example.test/child'
    navigation = lazyllm.globals['agentic_config']['web_navigation_state']
    navigation[root['page_ref']]['links'][1] = 'https://example.test/child'
    with pytest.raises(ValueError, match='navigation_cycle'):
        web_search_mod.url_fetch(page_ref=root['page_ref'], link_id=1)


def test_url_fetch_accepts_matching_redundant_click_url(monkeypatch):
    pages = {
        'https://example.test/root': {
            'title': 'Root',
            'content': 'Root content',
            'links': [{'id': 1, 'text': 'Child', 'target_url': 'https://example.test/child'}],
        },
        'https://example.test/child': {
            'title': 'Child',
            'content': 'Child content',
            'links': [],
        },
    }

    def fake_fetch(url):
        page = deepcopy(pages[url])
        page.update({'status': 'ok', 'source_status': 'ok', 'url': url, 'final_url': url})
        return page

    monkeypatch.setattr(web_search_mod, 'fetch_url_content', fake_fetch)
    root = web_search_mod.url_fetch(url='https://example.test/root')['result']
    child = web_search_mod.url_fetch(
        url='https://example.test/child#details',
        page_ref=root['page_ref'],
        link_id=1,
    )['result']

    assert child['parent_page_ref'] == root['page_ref']
    assert child['via_link_id'] == 1
    assert child['depth'] == 1
    with pytest.raises(ValueError, match='does not match'):
        web_search_mod.url_fetch(
            url='https://example.test/other',
            page_ref=root['page_ref'],
            link_id=1,
        )


def test_url_fetch_rejects_unpaired_or_batch_click_inputs():
    with pytest.raises(ValueError, match='required together'):
        web_search_mod.url_fetch(page_ref='page_missing')
    with pytest.raises(ValueError, match='cannot be combined'):
        web_search_mod.url_fetch(urls=['https://example.test'], page_ref='page_x', link_id=1)


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


def test_restore_web_navigation_state_skips_failed_and_invalid_results():
    history = [
        {'role': 'tool', 'name': 'url_fetch', 'content': 'not-json'},
        {
            'role': 'tool',
            'name': 'url_fetch',
            'content': json.dumps({'success': False, 'result': {'page_ref': 'page_failed'}}),
        },
        {'role': 'tool', 'name': 'kb_search', 'content': json.dumps({'page_ref': 'page_other_tool'})},
    ]

    assert web_search_mod.restore_web_navigation_state(history) == {}


def test_non_html_fetch_result_is_not_registered(monkeypatch, reset_web_tool_state):
    monkeypatch.setattr(web_search_mod, 'fetch_url_content', lambda url: {
        'status': 'ok',
        'source_status': 'non_html',
        'url': url,
        'final_url': url,
        'content': 'binary-like content',
        'links': [],
    })

    result = web_search_mod.url_fetch(url='https://example.test/file.pdf')['result']

    assert 'ref' not in result
    assert reset_web_tool_state[CITATION_REFS_KEY] == {}
