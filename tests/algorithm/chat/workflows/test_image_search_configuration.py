import importlib.util
import json
from pathlib import Path
from unittest.mock import Mock

import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.infra import image_workflow_support as tools


@pytest.fixture
def unconfigured(monkeypatch):
    for engine_type in (tools.GoogleSearch, tools.BingSearch, tools.BochaSearch, tools.TavilySearch):
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
