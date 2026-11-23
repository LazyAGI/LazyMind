import importlib.util
import base64
import json
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.infra import image_workflow_support as tools


@pytest.fixture
def unconfigured(monkeypatch):
    for engine_type in (tools.GoogleSearch, tools.BingSearch, tools.BochaSearch,
                        tools.TavilySearch, tools.KeenableSearch):
        monkeypatch.setattr(engine_type, '__key_source__', lambda self: '')
    network = Mock(side_effect=AssertionError('No network request expected'))
    monkeypatch.setattr(tools.requests, 'request', network)
    monkeypatch.setattr(tools.requests, 'get', network)
    return network


@pytest.mark.parametrize('entry', ['legacy', 'batch', 'ppt'])
def test_missing_search_configuration_blocks_with_setup_link(unconfigured, entry):
    if entry == 'ppt':
        path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
        spec = importlib.util.spec_from_file_location('ppt_search_configuration_tools', path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        call = module.ppt_search_web_images
    else:
        call = tools.image_search_tool if entry == 'legacy' else tools.image_search_and_validate
    with pytest.raises(ToolExecutionError) as error:
        call('真实建筑照片')
    marker, payload = str(error.value).split(' ', 1)
    assert marker == 'MEDIA_CAPABILITY_DEPENDENCY_MISSING'
    data = json.loads(payload)
    assert data['status'] == 'blocked'
    assert data['missing'][0]['id'] == 'web_search'
    assert data['missing'][0]['settings_url'] == '/settings?section=knowledge&tool=web-search'
    assert 'Tavily' in data['message']
    unconfigured.assert_not_called()


def test_configured_search_with_no_results_is_empty_not_missing(unconfigured, monkeypatch):
    monkeypatch.setattr(tools.TavilySearch, '__key_source__', lambda self: 'test-key')
    search = Mock(return_value=[])
    monkeypatch.setattr(tools.TavilySearch, 'search', search)
    result = tools.image_search_and_validate('no matching photo')
    assert result['status'] == 'empty'
    assert result['candidate_count'] == 0
    assert search.called


def test_provided_image_does_not_require_search(unconfigured, monkeypatch):
    url = 'https://example.com/photo.jpg'
    monkeypatch.setattr(tools, '_validate_image_candidate', lambda value: {
        'status': 'ok', 'original_url': value, 'url': value, 'width': 1000, 'height': 1000,
    })
    result = tools.image_search_and_validate('photo', target_valid=1, candidate_urls=[url])
    assert result['status'] == 'ok'
    assert result['selected'][0]['url'] == url
    unconfigured.assert_not_called()


def _ppt_tools():
    from lazymind.workflow_toolkit import load_workflow_package_tools
    root = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow'
    files = {path: base64.b64encode((root / path).read_bytes()).decode()
             for path in ('workflow.yaml', 'scripts/tools.py')}
    loaded = load_workflow_package_tools(
        {'files': files}, ['ppt_search_web_images', 'ppt_request_search_configuration'],
        'ppt-workflow', 'keen-search-test', package_root=root,
    )
    return SimpleNamespace(**loaded)


def test_ppt_search_uses_default_keenable_without_setup_and_does_not_accept_wiki_as_photo(monkeypatch):
    import httpx
    import lazyllm
    from lazyllm.tools.tools.search import WikipediaSearch
    from lazymind.chat.engine.tools import keenable_search

    monkeypatch.setattr(keenable_search, '_PUBLIC_RETRY_AT', 0.0)
    old_auth = lazyllm.globals.config['dynamic_tool_auth']
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    calls = []

    def request(method, url, **kwargs):
        calls.append(url)
        return httpx.Response(429, request=httpx.Request(method, url), headers={'Retry-After': '60'})

    monkeypatch.setattr(httpx, 'request', request)
    monkeypatch.setattr(httpx, 'get', lambda url, **kwargs: httpx.Response(
        200, request=httpx.Request('GET', url), json={'query': {'pages': {}}},
    ))
    monkeypatch.setattr(WikipediaSearch, 'search', lambda *a, **k: [
        {'title': 'Solar power', 'url': 'https://en.wikipedia.org/wiki/Solar_power', 'source': 'wikipedia'},
    ])
    try:
        ppt = _ppt_tools()
        result = ppt.ppt_search_web_images('solar panel photo')
        assert calls == ['https://api.keenable.ai/v1/search/public']
        assert result['urls'] == []
        assert result['reason'] == 'rate_limited'
        assert result['search_diagnostics']['fallback'] == 'wikipedia'
        assert result['settings_url'] == '/settings?section=knowledge&tool=web-search'
        # Required evidence explicitly asks for setup; the same failure is optional above.
        with pytest.raises(ToolExecutionError) as error:
            ppt.ppt_request_search_configuration('rate_limited: required solar plant photograph')
        from lazymind.chat.service.component.event_translator import _capability_dependency_from_value
        card = _capability_dependency_from_value(str(error.value))
        assert card['status'] == 'blocked'
        assert card['missing'][0]['id'] == 'web_search'
        assert '原任务' in card['message']
    finally:
        lazyllm.globals.config['dynamic_tool_auth'] = old_auth


def test_ppt_default_search_returns_direct_image_candidates(monkeypatch):
    import httpx
    import lazyllm
    from lazymind.chat.engine.tools import keenable_search

    monkeypatch.setattr(keenable_search, '_PUBLIC_RETRY_AT', 0.0)
    old_auth = lazyllm.globals.config['dynamic_tool_auth']
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    monkeypatch.setattr(httpx, 'request', lambda method, url, **kwargs: httpx.Response(
        200, request=httpx.Request(method, url), json={'results': [
            {'title': 'Photo', 'url': 'https://example.test/photo.jpg'},
            {'title': 'Article', 'url': 'https://example.test/article'},
        ]},
    ))
    try:
        result = _ppt_tools().ppt_search_web_images('plant photo')
        assert result['urls'] == ['https://example.test/photo.jpg']
        assert result['count'] == 1
    finally:
        lazyllm.globals.config['dynamic_tool_auth'] = old_auth
