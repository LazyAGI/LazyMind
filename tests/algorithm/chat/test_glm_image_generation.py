from unittest.mock import MagicMock

import pytest

from lazyllm.module.llms.onlinemodule.supplier import glm
from lazymind.chat.engine.tools.infra import image_generation_support as images
from lazymind.chat.service.external_tools import run_external_tools


PNG = b'\x89PNG\r\n\x1a\nimage'


def payload(key='test-image-key'):
    return {
        'llm_config': {'text2image': {
            'source': 'glm', 'model': 'cogview-4-250304', 'api_key': key,
            'base_url': 'https://provider.example/api/paas/v4/',
        }},
        'tool_name': 'image_generator',
        'arguments': {'prompt': 'A mountain', 'image_size': '1024x1024', 'batch_size': 2},
    }


@pytest.fixture
def provider(monkeypatch, tmp_path):
    request = MagicMock()
    response = request.return_value.__enter__.return_value
    response.status_code = 200
    response.json.return_value = {'data': [{'url': 'https://images.example/result.png'}]}
    monkeypatch.setattr(glm.requests, 'request', request)
    download = MagicMock()
    media = download.return_value.__enter__.return_value
    media.headers = {'Content-Type': 'image/png'}
    media.content = PNG
    monkeypatch.setattr(glm.requests, 'get', download)
    monkeypatch.setattr(images, '_upload_root', lambda: str(tmp_path))
    monkeypatch.setattr(images, 'static_file_url_from_any', lambda p: '/static-files/' + p.split('/')[-1])
    return request, response, download


def test_selected_glm_model_executes_through_real_external_registry(provider):
    request, _, download = provider
    catalog = run_external_tools(payload())['items']
    assert next(item for item in catalog if item['name'] == 'image_generator')['available']
    result = run_external_tools(payload(), execute=True)['result']
    assert len(result['images']) == 2
    assert all(item['image_url'].startswith('/static-files/') for item in result['images'])
    assert 'local_path' not in result
    assert all('local_path' not in item for item in result['images'])
    assert request.call_count == 2
    args, kwargs = request.call_args
    assert args == ('POST', 'https://provider.example/api/paas/v4/images/generations')
    assert kwargs['json'] == {'model': 'cogview-4-250304', 'prompt': 'A mountain', 'size': '1024x1024'}
    assert kwargs['headers']['Authorization'] == 'Bearer test-image-key'
    assert kwargs['allow_redirects'] is False
    assert 'headers' not in download.call_args.kwargs
    run_external_tools(payload('second-key'), execute=True)
    assert request.call_args.kwargs['headers']['Authorization'] == 'Bearer second-key'
    catalog = run_external_tools({})['items']
    assert not next(item for item in catalog if item['name'] == 'image_generator')['available']


@pytest.mark.parametrize('failure', ['status', 'empty', 'transport', 'download'])
def test_glm_failure_has_sanitized_diagnostics(provider, caplog, failure, monkeypatch):
    request, response, download = provider
    if failure == 'status':
        response.raise_for_status.side_effect = glm.requests.HTTPError('provider rejected request')
    elif failure == 'empty':
        response.json.return_value = {'data': []}
    elif failure == 'download':
        download.side_effect = RuntimeError('secret-key-and-private-prompt')
    else:
        request.side_effect = RuntimeError('secret-key-and-private-prompt')
    with pytest.raises(RuntimeError):
        run_external_tools(payload(), execute=True)
    assert 'stage=execution' in caplog.text
    diagnostic = caplog.text
    assert 'secret-key-and-private-prompt' not in diagnostic
    assert 'test-image-key' not in diagnostic
