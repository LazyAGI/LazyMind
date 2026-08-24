from copy import deepcopy
from inspect import signature
import time

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


def test_url_fetch_registers_sources_and_follows_exact_target_url(monkeypatch, reset_web_tool_state):
    assert list(signature(web_search_mod.url_fetch).parameters) == ['url']
    search = register_external_search_result({
        'title': 'Root from search',
        'url': 'https://example.test/root',
    }, reset_web_tool_state)
    pages = {
        'https://example.test/root': {
            'title': 'Root',
            'content': 'Root content',
            'links': [{'text': 'Child', 'target_url': 'https://example.test/child'}],
        },
        'https://example.test/child': {
            'title': 'Child',
            'content': 'Child content',
            'links': [{'text': 'Root', 'target_url': 'https://example.test/root'}],
        },
    }

    def fake_fetch(url):
        page = deepcopy(pages[url])
        page.update({'status': 'ok', 'source_status': 'ok', 'url': url, 'final_url': url})
        return page

    monkeypatch.setattr(web_search_mod, 'fetch_url_content', fake_fetch)
    manager = CitationResultMiddleware(ToolManager([web_search_mod.url_fetch]))

    def fetch(url):
        result = manager({
            'function': {'name': 'url_fetch', 'arguments': {'url': url}},
        })[0]
        assert result['ok'] is True
        return result['value']

    root = fetch(url='https://example.test/root')
    child = fetch(url=root['links'][0]['target_url'])

    assert root['citation_index'] == search['citation_index'] == '1.1'
    assert child['citation_index'] == '2.1'
    assert root['links'] == [{
        'text': 'Child',
        'target_url': 'https://example.test/child',
    }]
    assert len(reset_web_tool_state[CITATION_REFS_KEY]) == 2
    assert [source['source_roles'] for source in materialize_source_views(reset_web_tool_state)] == [
        ['searched'], ['searched'],
    ]


def test_parallel_url_fetch_calls_register_in_original_tool_call_order(
    monkeypatch, reset_web_tool_state,
):
    def fake_fetch(url):
        if url.endswith('/slow'):
            time.sleep(0.04)
        return {
            'status': 'ok',
            'source_status': 'ok',
            'url': url,
            'final_url': url,
            'title': url.rsplit('/', 1)[-1],
            'content': f'Content for {url}',
            'links': [],
        }

    monkeypatch.setattr(web_search_mod, 'fetch_url_content', fake_fetch)
    manager = CitationResultMiddleware(ToolManager([web_search_mod.url_fetch]))
    calls = [
        {'function': {'name': 'url_fetch', 'arguments': {'url': 'https://example.test/slow'}}},
        {'function': {'name': 'url_fetch', 'arguments': {'url': 'https://example.test/fast'}}},
    ]

    results = manager(calls)

    assert results[0]['value']['ref'] == '[[1.1]]'
    assert results[1]['value']['ref'] == '[[2.1]]'
    assert [source['url'] for source in materialize_source_views(reset_web_tool_state)] == [
        'https://example.test/slow',
        'https://example.test/fast',
    ]
