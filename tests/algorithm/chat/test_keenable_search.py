from types import SimpleNamespace

import httpx
import lazyllm
import pytest

from lazyllm.tools.agent.toolsManager import InstanceToolGroup

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


def _keenable():
    return next(engine for engine in _WEB_SEARCH_ENGINE_INSTANCES if engine.source_name == 'keenable')


def test_keenable_is_last_web_search_engine_and_inactive_by_default():
    sources = [engine.source_name for engine in _WEB_SEARCH_ENGINE_INSTANCES]
    assert sources[-1] == 'keenable'
    assert sources[:4] == ['google', 'bing', 'bocha', 'tavily']
    assert _instance_is_active(_keenable()) is False
    assert 'web_search' not in {cfg.name for cfg in filter_tools(DEFAULT_TOOLS)}


def test_keenable_toolgroup_follows_per_request_selection():
    group = InstanceToolGroup(_keenable())

    lazyllm.globals.config['dynamic_tool_auth'] = {}
    assert group.should_skip() is True
    assert _instance_is_active(_keenable()) is False

    lazyllm.globals.config['dynamic_tool_auth'] = {'keenable': 'public'}
    assert group.should_skip() is False
    assert _instance_is_active(_keenable()) is True

    lazyllm.globals.config['dynamic_tool_auth'] = {'keenable': 'real-key'}
    assert group.should_skip() is False
    assert _instance_is_active(_keenable()) is True

    lazyllm.globals.config['dynamic_tool_auth'] = {}
    assert group.should_skip() is True
    assert _instance_is_active(_keenable()) is False


def test_keenable_becomes_active_when_selected_without_a_key():
    lazyllm.globals.config['dynamic_tool_auth'] = {'keenable': 'public'}
    assert _instance_is_active(_keenable()) is True
    assert 'web_search' in {cfg.name for cfg in filter_tools(DEFAULT_TOOLS)}
    group = next(item for item in get_all_tool_groups() if item['name'] == 'web_search')
    methods = {method['name']: method['active'] for method in group['methods']}
    assert methods['KeenableSearch'] is True
    assert methods['TavilySearch'] is False


def test_keenable_public_request_omits_api_key_header(monkeypatch):
    captured = {}

    def fake_request(method, url, **kwargs):
        captured['method'] = method
        captured['url'] = url
        captured['json'] = kwargs.get('json')
        captured['headers'] = dict(kwargs.get('headers') or {})
        return SimpleNamespace(
            ok=True,
            status_code=200,
            raise_for_status=lambda: None,
            json=lambda: {'results': [
                {'title': 'Example', 'url': 'https://example.test', 'snippet': 'A result'},
                {'title': 'Fallback', 'url': 'https://example.test/desc', 'description': 'from description'},
                {'title': 'Dropped', 'url': '', 'snippet': 'no url'},
            ]},
        )

    monkeypatch.setattr(httpx, 'request', fake_request)
    lazyllm.globals.config['dynamic_tool_auth'] = {'keenable': 'public'}
    results = KeenableSearch().search('open web search', max_results=51)
    assert captured['method'] == 'POST'
    assert captured['url'] == 'https://api.keenable.ai/v1/search/public'
    assert captured['json'] == {'query': 'open web search', 'max_results': 50}
    assert captured['headers'].get('X-Keenable-Title') == 'LazyMind'
    assert 'X-API-Key' not in captured['headers']
    assert results == [
        {'title': 'Example', 'url': 'https://example.test', 'snippet': 'A result', 'source': 'keenable'},
        {
            'title': 'Fallback',
            'url': 'https://example.test/desc',
            'snippet': 'from description',
            'source': 'keenable',
        },
    ]


def test_keenable_keyed_request_sends_the_user_key(monkeypatch):
    captured = {}

    def fake_request(method, url, **kwargs):
        captured['url'] = url
        captured['headers'] = dict(kwargs.get('headers') or {})
        captured['follow_redirects'] = kwargs.get('follow_redirects')
        return SimpleNamespace(
            ok=True,
            status_code=200,
            raise_for_status=lambda: None,
            json=lambda: {'results': [
                {'title': 'Keyed', 'url': 'https://example.test/keyed', 'snippet': 'ok'},
            ]},
        )

    monkeypatch.setattr(httpx, 'request', fake_request)
    results = KeenableSearch(api_key='secret-key').search('query')
    assert captured['url'] == 'https://api.keenable.ai/v1/search'
    assert captured['headers'].get('X-API-Key') == 'secret-key'
    assert captured['follow_redirects'] is False
    assert results[0]['url'] == 'https://example.test/keyed'
