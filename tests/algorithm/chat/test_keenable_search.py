from types import SimpleNamespace

import lazyllm
import pytest

from lazymind.chat.engine.tools.keenable_search import KeenableSearch
from lazymind.chat.service.component.tool_registry import (
    DEFAULT_TOOLS,
    _WEB_SEARCH_ENGINE_INSTANCES,
    _instance_is_active,
    filter_tools,
    get_all_tool_groups,
)


@pytest.fixture(autouse=True)
def reset_dynamic_tool_auth():
    old_auth = lazyllm.globals.config['dynamic_tool_auth']
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    try:
        yield
    finally:
        lazyllm.globals.config['dynamic_tool_auth'] = old_auth


def test_keenable_is_last_web_search_engine_and_inactive_by_default():
    sources = [engine.source_name for engine in _WEB_SEARCH_ENGINE_INSTANCES]
    assert sources[-1] == 'keenable'
    assert sources[:4] == ['google', 'bing', 'bocha', 'tavily']
    keenable = next(engine for engine in _WEB_SEARCH_ENGINE_INSTANCES if engine.source_name == 'keenable')
    assert _instance_is_active(keenable) is False
    assert 'web_search' not in {cfg.name for cfg in filter_tools(DEFAULT_TOOLS)}


def test_keenable_becomes_active_when_selected_without_a_key():
    lazyllm.globals.config['dynamic_tool_auth'] = {'keenable': 'public'}
    keenable = next(engine for engine in _WEB_SEARCH_ENGINE_INSTANCES if engine.source_name == 'keenable')
    assert _instance_is_active(keenable) is True
    assert 'web_search' in {cfg.name for cfg in filter_tools(DEFAULT_TOOLS)}
    group = next(item for item in get_all_tool_groups() if item['name'] == 'web_search')
    methods = {method['name']: method['active'] for method in group['methods']}
    assert methods['KeenableSearch'] is True
    assert methods['TavilySearch'] is False


def test_keenable_search_uses_public_endpoint_and_title_header():
    captured = {}

    def fake_request(method, url, **kwargs):
        captured['method'] = method
        captured['url'] = url
        captured['json'] = kwargs.get('json')
        captured['headers'] = kwargs.get('headers')
        return SimpleNamespace(json=lambda: {'results': [
            {'title': 'Example', 'url': 'https://example.test', 'snippet': 'A result'},
            {'title': 'Dropped', 'url': '', 'snippet': 'no url'},
        ]})

    lazyllm.globals.config['dynamic_tool_auth'] = {'keenable': 'public'}
    engine = KeenableSearch()
    engine._request = fake_request
    results = engine.search('open web search', max_results=51)
    assert captured['method'] == 'POST'
    assert captured['url'] == 'https://api.keenable.ai/v1/search/public'
    assert captured['json'] == {'query': 'open web search', 'max_results': 50}
    assert captured['headers'] == {'X-Keenable-Title': 'LazyMind'}
    assert results == [{
        'title': 'Example',
        'url': 'https://example.test',
        'snippet': 'A result',
        'source': 'keenable',
    }]


def test_keenable_search_uses_keyed_endpoint_for_a_real_key():
    captured = {}

    def fake_request(method, url, **kwargs):
        captured['url'] = url
        return SimpleNamespace(json=lambda: {'results': [
            {'title': 'Keyed', 'url': 'https://example.test/keyed', 'snippet': 'ok'},
        ]})

    engine = KeenableSearch(api_key='secret-key')
    engine._request = fake_request
    results = engine.search('query')
    assert captured['url'] == 'https://api.keenable.ai/v1/search'
    assert results[0]['url'] == 'https://example.test/keyed'
