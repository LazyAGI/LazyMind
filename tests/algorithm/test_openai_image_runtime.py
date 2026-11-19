"""OpenAI image generation contracts, with only the HTTP transport replaced."""

import base64
import json
from io import BytesIO
from pathlib import Path

import lazyllm
import pytest
import requests
from PIL import Image

from lazyllm.components.formatter import decode_query_with_filepaths
from lazymind.chat.model_probe.contracts import ProbeInput
from lazymind.chat.model_probe.profiles import build_plan
from lazymind.chat.model_probe.runtime_support import supports_runtime_type


MODELS = [
    'gpt-image-1-mini', 'gpt-image-1.5', 'gpt-image-2', 'gpt-image-2.5-flare',
    'gpt-image-2.5-sunburst', 'gpt-image-2.6-flare', 'gpt-image-2.6-sunburst', 'chatgpt-image-latest',
]


def response(payload, status=200):
    result = requests.Response()
    result.status_code = status
    result.url = 'https://api.openai.com/v1/images/generations'
    result._content = json.dumps(payload).encode()
    result._content_consumed = True
    return result


@pytest.fixture(autouse=True)
def isolated_runtime(monkeypatch, tmp_path):
    def forbidden(*args, **kwargs):
        raise AssertionError('Tests must not call a live provider')

    monkeypatch.setattr(requests.Session, 'send', forbidden)
    previous_temp_dir = lazyllm.config['temp_dir']
    lazyllm.config['temp_dir'] = str(tmp_path)
    try:
        yield
    finally:
        lazyllm.config['temp_dir'] = previous_temp_dir


def image_bytes(fmt='PNG'):
    buffer = BytesIO()
    Image.new('RGB', (16, 16), 'blue').save(buffer, format=fmt)
    return buffer.getvalue()


@pytest.mark.parametrize('model', MODELS)
def test_models_route_to_images_api_and_save_real_images(monkeypatch, model):
    content = image_bytes()
    captured = {}

    def post(url, **kwargs):
        captured.update(url=url, **kwargs)
        return response({'data': [{'b64_json': base64.b64encode(content).decode()}] * 2})

    monkeypatch.setattr(requests, 'post', post)
    module = lazyllm.OnlineModule(model=model, source='openai', api_key='test-key',
                                  url='https://api.openai.com/v1')
    result = module('a blue square', image_size='1536x1024', batch_size=2, priority=1, stream_output=False)
    files = decode_query_with_filepaths(result)['files']
    assert len(files) == 2 and len(set(files)) == 2
    assert all(Path(path).read_bytes() == content and path.endswith('.png') for path in files)
    assert captured['url'] == 'https://api.openai.com/v1/images/generations'
    assert captured['headers']['Authorization'] == 'Bearer test-key'
    assert captured['json'] == {'model': model, 'prompt': 'a blue square', 'n': 2, 'size': '1536x1024'}
    assert captured['timeout'] == 600
    assert supports_runtime_type('openai', 'text2image')
    plan = build_plan(ProbeInput(model, 'text2image', 'OpenAI', 'https://api.openai.com/v1/'))
    assert plan.endpoint == 'images/generations' and plan.json['model'] == model
    assert not supports_runtime_type('openai', 'image_editing')


@pytest.mark.parametrize('fmt, suffix', [('PNG', '.png'), ('JPEG', '.jpg'), ('WEBP', '.webp')])
def test_output_options_and_runtime_overrides(monkeypatch, fmt, suffix):
    content = image_bytes(fmt)
    captured = {}

    def post(url, **kwargs):
        captured.update(url=url, **kwargs)
        return response({'data': [{'b64_json': base64.b64encode(content).decode()}]})

    monkeypatch.setattr(requests, 'post', post)
    module = lazyllm.OnlineModule(model=MODELS[0], source='openai', api_key='test-key')
    result = module('square', model=MODELS[3], url='https://gateway.example/tenant/v1/',
                    size='1024x1536', quality='high', output_format=fmt.lower(),
                    output_compression=0, background='opaque', moderation='auto', request_timeout=240)
    path = decode_query_with_filepaths(result)['files'][0]
    assert Path(path).read_bytes() == content and path.endswith(suffix)
    assert captured['url'] == 'https://gateway.example/tenant/v1/images/generations'
    assert captured['timeout'] == 240
    assert captured['json'] == {
        'model': MODELS[3], 'prompt': 'square', 'n': 1, 'size': '1024x1536', 'quality': 'high',
        'output_format': fmt.lower(), 'output_compression': 0, 'background': 'opaque', 'moderation': 'auto',
    }


@pytest.mark.parametrize('payload, message', [
    ({'data': []}, 'no images'),
    ({'data': [{'url': 'https://example.test/image.png'}]}, 'without b64_json'),
    ({'data': [{'b64_json': '!!!!'}]}, 'base64'),
    ({'data': [{'b64_json': base64.b64encode(b'not an image').decode()}]}, 'Invalid image data'),
])
def test_malformed_results_do_not_publish_files(monkeypatch, tmp_path, payload, message):
    monkeypatch.setattr(requests, 'post', lambda *args, **kwargs: response(payload))
    module = lazyllm.OnlineModule(model=MODELS[3], source='openai', api_key='test-key')
    with pytest.raises(Exception, match=message):
        module('square')
    assert not list(tmp_path.iterdir())


@pytest.mark.parametrize('status', [400, 401, 403, 429, 500])
def test_http_errors_propagate_without_retry(monkeypatch, status):
    calls = []

    def post(*args, **kwargs):
        calls.append(kwargs)
        return response({'error': {'message': 'provider rejected request'}}, status)

    monkeypatch.setattr(requests, 'post', post)
    module = lazyllm.OnlineModule(model=MODELS[3], source='openai', api_key='test-key')
    with pytest.raises(requests.HTTPError, match=str(status)):
        module.forward('square')
    assert len(calls) == 1


@pytest.mark.parametrize('options, message', [
    ({'batch_size': 0}, 'count'), ({'n': 11}, 'count'), ({'n': True}, 'count'),
    ({'files': ['reference.png']}, 'image editing'), ({'output_format': 'svg'}, 'output_format'),
])
def test_invalid_generation_options_are_rejected_before_http(options, message):
    module = lazyllm.OnlineModule(model=MODELS[3], source='openai', api_key='test-key')
    with pytest.raises(ValueError, match=message):
        module.forward('square', **options)


def test_timeout_propagates_without_retry(monkeypatch):
    calls = []

    def post(*args, **kwargs):
        calls.append(kwargs)
        raise requests.Timeout('image generation timed out')

    monkeypatch.setattr(requests, 'post', post)
    module = lazyllm.OnlineModule(model=MODELS[3], source='openai', api_key='test-key')
    with pytest.raises(requests.Timeout, match='timed out'):
        module.forward('square')
    assert len(calls) == 1


def test_dynamic_image_tool_returns_frontend_image_payload(monkeypatch, tmp_path):
    from lazymind.chat.engine.tools.infra.image_generation_support import run_image_model
    from lazymind.model_config import get_dynamic_role_slot_map, inject_model_config

    config = tmp_path / 'models.yaml'
    config.write_text('image_generator:\n  source: dynamic\n  dynamic_auth: true\n  type: text2image\n')
    monkeypatch.setenv('LAZYMIND_MODEL_CONFIG_PATH', str(config))
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    get_dynamic_role_slot_map.cache_clear()
    content = image_bytes()
    captured = {}

    def post(url, **kwargs):
        captured.update(url=url, **kwargs)
        return response({'data': [{'b64_json': base64.b64encode(content).decode()}]})

    monkeypatch.setattr(requests, 'post', post)
    lazyllm.init_session()
    try:
        inject_model_config({'text2image': {
            'source': 'openai', 'model': MODELS[3], 'api_key': 'dynamic-test-key',
            'base_url': 'https://api.openai.com/v1/',
        }})
        result = run_image_model('image_generator', 'a blue square', image_size='1024x1536')
        assert Path(result['local_path']).read_bytes() == content
        assert result['image_url'].startswith('/static-files/ai_generated/')
        assert result['image_url'] in result['image_markdown']
        assert len(result['images']) == 1
        assert captured['headers']['Authorization'] == 'Bearer dynamic-test-key'
        assert captured['json']['model'] == MODELS[3]
        assert captured['json']['size'] == '1024x1536'
    finally:
        lazyllm.globals.clear()
        get_dynamic_role_slot_map.cache_clear()
