from types import SimpleNamespace

import httpx
import lazyllm
import pytest

from lazyllm.tools.agent.toolsManager import InstanceToolGroup, ToolManager

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
    from lazymind.chat.engine.tools import keenable_search
    keenable_search._PUBLIC_RETRY_AT = 0.0
    old_auth = lazyllm.globals.config['dynamic_tool_auth']
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    try:
        yield
    finally:
        lazyllm.globals.config['dynamic_tool_auth'] = old_auth


def _keenable():
    return next(engine for engine in _WEB_SEARCH_ENGINE_INSTANCES if engine.source_name == 'keenable')


def test_keenable_is_last_web_search_engine_and_active_by_default():
    sources = [engine.source_name for engine in _WEB_SEARCH_ENGINE_INSTANCES]
    assert sources[-1] == 'keenable'
    assert sources[:4] == ['google', 'bing', 'bocha', 'tavily']
    assert _instance_is_active(_keenable()) is True
    assert 'web_search' in {cfg.name for cfg in filter_tools(DEFAULT_TOOLS)}


def test_keenable_toolgroup_follows_per_request_selection():
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    ensure_lazyllm_tool_docs([_keenable()])
    group = InstanceToolGroup(_keenable())

    lazyllm.globals.config['dynamic_tool_auth'] = {}
    assert group.should_skip() is False
    assert _instance_is_active(_keenable()) is True

    lazyllm.globals.config['dynamic_tool_auth'] = {'tavily': 'user-key'}
    assert group.should_skip() is True
    assert _instance_is_active(_keenable()) is False

    lazyllm.globals.config['dynamic_tool_auth'] = {'keenable': 'public'}
    assert group.should_skip() is False
    assert _instance_is_active(_keenable()) is True

    lazyllm.globals.config['dynamic_tool_auth'] = {'keenable': 'real-key'}
    assert group.should_skip() is False
    assert _instance_is_active(_keenable()) is True

    lazyllm.globals.config['dynamic_tool_auth'] = {}
    assert group.should_skip() is False
    assert _instance_is_active(_keenable()) is True


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
        {'title': 'Fallback', 'url': 'https://example.test/desc', 'snippet': '', 'source': 'keenable'},
    ]


def test_keenable_keyed_request_sends_the_user_key(monkeypatch):
    captured = {}

    def fake_request(method, url, **kwargs):
        captured['url'] = url
        captured['headers'] = dict(kwargs.get('headers') or {})
        captured['follow_redirects'] = kwargs.get('follow_redirects', False)
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


def test_keenable_uses_the_per_request_key(monkeypatch):
    captured = {}

    def fake_request(method, url, **kwargs):
        captured['url'] = url
        captured['headers'] = dict(kwargs.get('headers') or {})
        return SimpleNamespace(
            ok=True,
            status_code=200,
            raise_for_status=lambda: None,
            json=lambda: {'results': []},
        )

    monkeypatch.setattr(httpx, 'request', fake_request)
    lazyllm.globals.config['dynamic_tool_auth'] = {'keenable': 'user-key'}
    assert _keenable().search('query') == []
    assert captured['url'] == 'https://api.keenable.ai/v1/search'
    assert captured['headers'].get('X-API-Key') == 'user-key'


def test_default_search_executes_without_configuration_and_switches_to_user_provider(monkeypatch):
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    from lazymind.chat.engine.tool_auth import inject_tool_config

    config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'web_search')
    ensure_lazyllm_tool_docs([config.tool])
    old_workspace = lazyllm.locals['_lazyllm_agent'].get('workspace') or {}
    lazyllm.locals['_lazyllm_agent']['workspace'] = {}
    requests = []

    def request(method, url, **kwargs):
        requests.append(url)
        return httpx.Response(200, request=httpx.Request(method, url), json={'results': [
            {'title': 'Default result', 'url': 'https://example.test', 'snippet': 'Evidence'},
        ]})

    monkeypatch.setattr(httpx, 'request', request)
    try:
        manager = ToolManager([{**config.tool, 'discoverable': True}])
        first = manager([{'id': '1', 'function': {'name': 'KeenableSearch_search',
                          'arguments': {'query': 'first'}}}])
        assert first[0]['ok'] is True
        assert requests == ['https://api.keenable.ai/v1/search/public']
        inject_tool_config({'tavily': 'user-key'})
        manager([{'id': 'activate', 'function': {'name': 'get_TavilySearch_methods', 'arguments': {}}}])
        descriptions = manager.tools_description
        assert any(item['function']['name'] == 'TavilySearch_search' for item in descriptions)
        assert not any(item['function']['name'] == 'KeenableSearch_search' for item in descriptions)
        result = manager([{'id': '2', 'function': {
            'name': 'TavilySearch_search', 'arguments': {'query': 'second'},
        }}])
        assert result[0]['ok'] is True
        assert requests == ['https://api.keenable.ai/v1/search/public', 'https://api.tavily.com/search']
    finally:
        lazyllm.locals['_lazyllm_agent']['workspace'] = old_workspace


@pytest.mark.parametrize('status,reason', [
    (401, 'authentication_failed'), (402, 'quota_exhausted'),
    (429, 'rate_limited'), (503, 'temporarily_unavailable'),
])
def test_failure_falls_back_to_labelled_wikipedia_without_leaking_response(monkeypatch, status, reason):
    from lazyllm.tools.tools.search import WikipediaSearch

    def request(method, url, **kwargs):
        return httpx.Response(status, request=httpx.Request(method, url),
                              headers={'Retry-After': '120'}, text='sensitive-response')

    monkeypatch.setattr(httpx, 'request', request)
    monkeypatch.setattr(WikipediaSearch, 'search', lambda self, query, limit: [
        {'title': 'Background', 'url': 'https://en.wikipedia.org/wiki/Test', 'source': 'wikipedia'},
    ])
    result = KeenableSearch().search('query')
    assert result['status'] == 'partial'
    assert result['reason'] == reason
    assert result['items'][0]['source'] == 'wikipedia'
    assert '背景' in result['limitations']
    assert 'sensitive-response' not in str(result)


def test_rate_limit_cools_down_across_instances_but_user_key_bypasses_public_pool(monkeypatch):
    from lazyllm.tools.tools.search import WikipediaSearch
    from lazymind.chat.engine.tools import keenable_search
    calls = []
    now = [100.0]
    monkeypatch.setattr(keenable_search.time, 'monotonic', lambda: now[0])

    def request(method, url, **kwargs):
        calls.append(url)
        if url.endswith('/public'):
            return httpx.Response(429, request=httpx.Request(method, url), headers={'Retry-After': '120'})
        return httpx.Response(200, request=httpx.Request(method, url), json={'results': []})

    monkeypatch.setattr(httpx, 'request', request)
    monkeypatch.setattr(WikipediaSearch, 'search', lambda *a, **k: [])
    assert KeenableSearch().search('first')['status'] == 'unavailable'
    assert KeenableSearch().search('second')['reason'] == 'rate_limited'
    assert len(calls) == 1
    lazyllm.globals.config['dynamic_tool_auth'] = {'keenable': 'user-key'}
    assert KeenableSearch().search('keyed') == []
    assert calls[-1].endswith('/v1/search')
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    now[0] = 221.0
    KeenableSearch().search('after reset')
    assert len(calls) == 3


def test_network_failure_of_both_services_preserves_configuration_guidance(monkeypatch):
    from lazyllm.tools.tools.search import WikipediaSearch

    def offline(*a, **k):
        raise httpx.ConnectError('secret-transport-detail')

    monkeypatch.setattr(httpx, 'request', offline)
    monkeypatch.setattr(WikipediaSearch, 'search', offline)
    result = KeenableSearch().search('missing evidence')
    assert result['status'] == 'unavailable'
    assert result['items'] == []
    assert result['settings_url'] == '/settings?section=knowledge&tool=web-search'
    assert '原任务' in result['message']
    assert 'secret-transport-detail' not in str(result)


def test_empty_default_search_uses_background_fallback(monkeypatch):
    from lazyllm.tools.tools.search import WikipediaSearch
    monkeypatch.setattr(httpx, 'request', lambda method, url, **kw: httpx.Response(
        200, request=httpx.Request(method, url), json={'results': []},
    ))
    monkeypatch.setattr(WikipediaSearch, 'search', lambda self, query, limit: [
        {'title': 'Background', 'url': 'https://en.wikipedia.org/wiki/Test', 'source': 'wikipedia'},
    ])
    result = KeenableSearch().search('query')
    assert result['reason'] == 'no_results'
    assert result['status'] == 'partial'


def test_fallback_citations_and_limitations_survive_result_budget(monkeypatch):
    from lazyllm.tools.tools.search import WikipediaSearch
    from lazymind.chat.engine.tools.infra import CitationResultMiddleware
    from lazymind.chat.service.utils.citations import reset_citation_state
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    import json

    def offline(*a, **k):
        raise httpx.ConnectError('offline')

    monkeypatch.setattr(httpx, 'request', offline)
    monkeypatch.setattr(WikipediaSearch, 'search', lambda *a, **k: [
        {'title': f'Background {i}', 'url': f'https://en.wikipedia.org/wiki/Test_{i}',
         'snippet': 'x' * 700, 'source': 'wikipedia'} for i in range(40)
    ])
    state = {}
    reset_citation_state(state)
    old_config = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {'citation_state': state}
    try:
        engine = KeenableSearch()
        ensure_lazyllm_tool_docs([engine])
        manager = CitationResultMiddleware(ToolManager([engine]))
        response = manager({'function': {
            'name': 'KeenableSearch_search', 'arguments': {'query': 'query', 'max_results': 40},
        }})[0]
        result = response['value']
        assert response['ok'] is True
        assert result['items'][0]['ref'] == '[[1.1]]'
        assert 0 < len(result['items']) < 40
        assert result['reason'] == 'temporarily_unavailable'
        assert '背景' in result['limitations']
        assert result['settings_url'] == '/settings?section=knowledge&tool=web-search'
        assert len(json.dumps(response, ensure_ascii=False)) <= 16384
    finally:
        lazyllm.globals['agentic_config'] = old_config or {}


@pytest.mark.parametrize('kwargs', [{'query': ''}, {'max_results': 0}, {'max_results': True}])
def test_invalid_arguments_do_not_trigger_fallback(monkeypatch, kwargs):
    monkeypatch.setattr(httpx, 'request', lambda *a, **k: pytest.fail('network'))
    with pytest.raises(ValueError):
        KeenableSearch().search(**({'query': 'query'} | kwargs))
