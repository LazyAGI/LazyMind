import threading
from copy import deepcopy

import lazyllm
import pytest
from lazyllm.tools.agent.toolsManager import ToolManager

from lazymind.chat.engine.tools import web_search as web_search_mod
from lazymind.chat.engine.tools.infra import CitationResultMiddleware
from lazymind.chat.service.utils.citations import (
    CITATION_REFS_KEY,
    materialize_source_views,
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


def test_url_fetch_batches_network_only_in_workers_and_registers_pages_in_order(
    monkeypatch, reset_web_tool_state,
):
    fetch_threads = []
    register_threads = []

    def fake_fetch(url):
        fetch_threads.append(threading.get_ident())
        if url.endswith('/bad'):
            raise RuntimeError('unavailable')
        return {'final_url': url, 'content': f'content:{url}'}

    original_register_page = web_search_mod._register_page

    def track_register_page(page):
        register_threads.append(threading.get_ident())
        return original_register_page(page)

    monkeypatch.setattr(web_search_mod, 'fetch_url_content', fake_fetch)
    monkeypatch.setattr(web_search_mod, '_register_page', track_register_page)

    manager = CitationResultMiddleware(ToolManager([web_search_mod.url_fetch]))
    execution = manager({
        'function': {
            'name': 'url_fetch',
            'arguments': {'urls': [
                'https://example.test/one',
                'https://example.test/bad',
                'https://example.test/one',
                'https://example.test/two',
            ]},
        },
    })[0]
    assert execution['ok'] is True
    payload = execution['value']

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
    assert len(set(register_threads)) == 1
    assert set(register_threads).isdisjoint(fetch_threads)
    assert [source['url'] for source in materialize_source_views(reset_web_tool_state)] == [
        'https://example.test/one', 'https://example.test/two',
    ]


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
    manager = CitationResultMiddleware(ToolManager([web_search_mod.url_fetch]))

    def fetch(**arguments):
        result = manager({
            'function': {'name': 'url_fetch', 'arguments': arguments},
        })[0]
        assert result['ok'] is True
        return result['value']['result']

    root = fetch(url='https://example.test/root')
    child = fetch(page_ref=root['page_ref'], link_id=1)

    assert root['citation_index'] == search['citation_index'] == '1.1'
    assert child['citation_index'] == '2.1'
    assert len(reset_web_tool_state[CITATION_REFS_KEY]) == 2
    assert [source['source_roles'] for source in materialize_source_views(reset_web_tool_state)] == [
        ['searched'], ['searched'],
    ]


def test_page_ref_expires_with_request_state_and_exact_url_can_be_refetched(monkeypatch):
    monkeypatch.setattr(web_search_mod, 'fetch_url_content', lambda url: {
        'status': 'ok',
        'source_status': 'ok',
        'url': url,
        'final_url': url,
        'content': f'Content for {url}',
        'links': [{'id': 1, 'target_url': 'https://example.test/child'}],
    })

    root = web_search_mod.url_fetch(url='https://example.test/root')['result']
    lazyllm.globals['agentic_config']['web_navigation_state'] = {}

    with pytest.raises(ValueError, match='page_ref is invalid or expired'):
        web_search_mod.url_fetch(page_ref=root['page_ref'], link_id=1)

    child = web_search_mod.url_fetch(url='https://example.test/child')['result']
    assert child['final_url'] == 'https://example.test/child'
