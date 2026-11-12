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
@pytest.mark.parametrize('advanced', [False, True])
def test_image_tool_registry_to_uploaded_image(monkeypatch, tmp_path, image_bytes, source, model, advanced):
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
    extras = ({'quality': 'high', 'background': 'transparent', 'output_format': 'png'}
              if source == 'openai' else {'negative_prompt': 'blur', 'guidance_scale': 7.5})
    if advanced:
        payload['llm_config']['text2image']['request_advanced'] = {
            'headers': {'X-Image-Option': 'private-option'},
            'query': {'image_option': 'private-option'},
            'extra_body': extras,
        }
    result = run_external_tools(payload, execute=True)['result']
    assert result['image_url'].startswith('/static-files/')
    assert 'local_path' not in result
    assert len(result['images']) == 1
    files = list((tmp_path / 'ai_generated').iterdir())
    assert len(files) == 1 and files[0].read_bytes() == image_bytes
    expected_url = 'https://provider.example/v1/images/generations'
    if advanced:
        expected_url += '?image_option=private-option'
        body = json.loads(requests_sent[0].body)
        assert all(body[key] == value for key, value in extras.items())
        assert requests_sent[0].headers['X-Image-Option'] == 'private-option'
        for download in requests_sent[1:]:
            assert 'X-Image-Option' not in download.headers
            assert 'image_option' not in download.url
    assert requests_sent[0].url == expected_url


@pytest.mark.parametrize('count', [0, -1, False, True, 1.9, '2'])
def test_image_tool_rejects_invalid_count_before_calling_provider(count):
    from lazymind.chat.engine.tools.infra.image_generation_support import run_image_model_instance
    from lazyllm.tools.agent import ToolExecutionError
    model = MagicMock()
    with pytest.raises(ToolExecutionError, match='positive integer'):
        run_image_model_instance(model, 'blue square', batch_size=count)
    model.assert_not_called()


@pytest.mark.parametrize('format,mime', [('JPEG', 'image/jpeg'), ('WEBP', 'image/webp'), ('PNG', 'image/png')])
def test_siliconflow_edit_preserves_reference_mime(monkeypatch, tmp_path, image_bytes, format, mime):
    reference = BytesIO()
    Image.new('RGB', (8, 8), 'red').save(reference, format=format)
    # Deliberately use an uninformative suffix: determine MIME from bytes.
    path = tmp_path / 'reference.bin'
    path.write_bytes(reference.getvalue())
    module = OnlineMultiModalModule(source='siliconflow', model='Kwai-Kolors/Kolors',
                                    type='image_editing', api_key='test-key')
    post = MagicMock(return_value={'images': [{'url': 'https://images.example/result.png'}]})
    monkeypatch.setattr(module, '_make_request', post)
    monkeypatch.setattr(module, '_get_image_data_from_url', lambda url: image_bytes)
    result = module('make it blue', files=[str(path)])
    for generated in decode_query_with_filepaths(result)['files']:
        Path(generated).unlink(missing_ok=True)
    uri = post.call_args.args[1]['image']
    assert uri.startswith(f'data:{mime};base64,')
    assert base64.b64decode(uri.split(',', 1)[1]) == reference.getvalue()


@pytest.mark.parametrize('destination', ['http://127.0.0.1/private.png', 'http://10.0.0.1/private.png'])
def test_siliconflow_rejects_redirect_to_internal_network(monkeypatch, destination):
    import ipaddress
    import requests
    module = OnlineMultiModalModule(source='siliconflow', model='Kwai-Kolors/Kolors',
                                    type='text2image', api_key='test-key')
    monkeypatch.setattr(module, '_is_internal_address',
                        lambda host: host != 'images.example' and ipaddress.ip_address(host).is_private)
    response = requests.Response()
    response.status_code = 302
    response.headers['Location'] = destination
    response._content = b''
    response._content_consumed = True
    get = MagicMock(return_value=response)
    monkeypatch.setattr(requests, 'get', get)
    with pytest.raises(ValueError, match='internal network'):
        module._get_image_data_from_url('https://images.example/result.png')
    assert get.call_count == 1
    assert get.call_args.kwargs['allow_redirects'] is False


def test_siliconflow_follows_valid_relative_redirect(monkeypatch, image_bytes):
    import requests
    module = OnlineMultiModalModule(source='siliconflow', model='Kwai-Kolors/Kolors',
                                    type='text2image', api_key='test-key')
    checked = []
    monkeypatch.setattr(module, '_validate_url_security', checked.append)
    redirect, final = requests.Response(), requests.Response()
    redirect.status_code = 302
    redirect.headers['Location'] = '/cdn/final.png'
    redirect._content = b''
    final.status_code = 200
    final._content = image_bytes
    final.headers['Content-Type'] = 'application/octet-stream'
    for response in (redirect, final):
        response._content_consumed = True
    get = MagicMock(side_effect=[redirect, final])
    monkeypatch.setattr(requests, 'get', get)
    assert module._get_image_data_from_url('https://images.example/result.png') == image_bytes
    assert checked == ['https://images.example/result.png', 'https://images.example/cdn/final.png']
    assert all(call.kwargs['allow_redirects'] is False for call in get.call_args_list)


def test_siliconflow_limits_redirect_loops(monkeypatch):
    import requests
    module = OnlineMultiModalModule(source='siliconflow', model='Kwai-Kolors/Kolors',
                                    type='text2image', api_key='test-key')
    monkeypatch.setattr(module, '_validate_url_security', lambda url: None)
    response = requests.Response()
    response.status_code = 302
    response.headers['Location'] = '/loop.png'
    response._content = b''
    response._content_consumed = True
    get = MagicMock(return_value=response)
    monkeypatch.setattr(requests, 'get', get)
    with pytest.raises(requests.TooManyRedirects):
        module._get_image_data_from_url('https://images.example/loop.png')
    assert get.call_count <= 6
