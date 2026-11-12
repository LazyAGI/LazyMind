import base64
from io import BytesIO
from pathlib import Path
from unittest.mock import MagicMock

import pytest
from PIL import Image

from lazyllm import OnlineMultiModalModule
from lazyllm.components.formatter import decode_query_with_filepaths


@pytest.fixture
def image_bytes():
    output = BytesIO()
    Image.new('RGB', (8, 8), 'blue').save(output, format='PNG')
    return output.getvalue()


@pytest.mark.parametrize('source,model,endpoint', [
    ('siliconflow', 'Kwai-Kolors/Kolors', 'https://provider.example/v1/images/generations'),
    ('openai', 'gpt-image-1', 'https://provider.example/v1/images/generations'),
])
def test_native_image_generation_contract(monkeypatch, image_bytes, source, model, endpoint):
    import requests
    post = MagicMock()
    response = post.return_value
    response.__enter__.return_value = response
    response.json.return_value = ({'images': [{'url': 'https://images.example/result.png'}]}
                                  if source == 'siliconflow' else
                                  {'data': [{'b64_json': base64.b64encode(image_bytes).decode()}]})
    get = MagicMock()
    get.return_value.content = image_bytes
    get.return_value.headers = {'Content-Type': 'image/png'}
    monkeypatch.setattr(requests, 'post', post)
    monkeypatch.setattr(requests, 'get', get)
    # DNS is the only external dependency besides HTTP in the image downloader.
    monkeypatch.setattr('socket.gethostbyname_ex', lambda host: (host, [], ['8.8.8.8']))
    module = OnlineMultiModalModule(source=source, model=model, type='text2image', api_key='test-key',
                                    base_url='https://provider.example/v1')
    result = module('a blue square', image_size='1024x1024', batch_size=1, priority=0, stream_output=False)
    files = decode_query_with_filepaths(result)['files']
    try:
        assert len(files) == 1
        assert Path(files[0]).read_bytes() == image_bytes
    finally:
        for file in files:
            Path(file).unlink(missing_ok=True)
    assert post.call_args.args[0] == endpoint
    body = post.call_args.kwargs['json']
    assert body['model'] == model
    assert body['prompt'] == 'a blue square'
    assert body['image_size' if source == 'siliconflow' else 'size'] == '1024x1024'
    assert 'priority' not in body and 'stream_output' not in body
    if source == 'siliconflow':
        assert 'headers' not in get.call_args.kwargs


def test_openai_full_endpoint_is_not_duplicated(monkeypatch, image_bytes):
    import requests
    post = MagicMock()
    response = post.return_value.__enter__.return_value
    response.json.return_value = {'data': [{'b64_json': base64.b64encode(image_bytes).decode()}]}
    monkeypatch.setattr(requests, 'post', post)
    module = OnlineMultiModalModule(source='openai', model='gpt-image-1', type='text2image', api_key='test-key',
                                    base_url='https://provider.example/v1/images/generations')
    result = module('blue square')
    for file in decode_query_with_filepaths(result)['files']:
        Path(file).unlink(missing_ok=True)
    assert post.call_args.args[0] == 'https://provider.example/v1/images/generations'


@pytest.mark.parametrize('source,model', [('openai', 'gpt-image-1'), ('siliconflow', 'Kwai-Kolors/Kolors')])
def test_image_tool_registry_to_uploaded_image(monkeypatch, tmp_path, image_bytes, source, model):
    import json
    import requests
    from lazymind.chat.service.external_tools import run_external_tools
    from lazymind.chat.engine.tools.infra import image_generation_support as images
    requests_sent = []

    def send(session, request, **kwargs):
        requests_sent.append(request)
        response = requests.Response()
        response.status_code = 200
        response.request = request
        if request.method == 'POST':
            payload = ({'images': [{'url': 'https://images.example/blue.png'}]}
                       if source == 'siliconflow' else
                       {'data': [{'b64_json': base64.b64encode(image_bytes).decode()}]})
            response._content = json.dumps(payload).encode()
        else:
            assert 'Authorization' not in request.headers
            response._content = image_bytes
            response.headers['Content-Type'] = 'image/png'
        response._content_consumed = True
        response.raw = BytesIO()
        return response

    monkeypatch.setattr(requests.Session, 'send', send)
    monkeypatch.setattr('socket.gethostbyname_ex', lambda host: (host, [], ['8.8.8.8']))
    monkeypatch.setattr(images, '_upload_root', lambda: str(tmp_path))
    monkeypatch.setattr(images, 'static_file_url_from_any', lambda path: '/static-files/' + Path(path).name)
    payload = {'llm_config': {'text2image': {'source': source, 'model': model, 'api_key': 'test-key',
                                             'base_url': 'https://provider.example/v1'}},
               'tool_name': 'image_generator', 'arguments': {'prompt': 'blue square'}}
    result = run_external_tools(payload, execute=True)['result']
    assert result['image_url'].startswith('/static-files/')
    assert 'local_path' not in result
    assert len(result['images']) == 1
    files = list((tmp_path / 'ai_generated').iterdir())
    assert len(files) == 1 and files[0].read_bytes() == image_bytes
    assert requests_sent[0].url == 'https://provider.example/v1/images/generations'
