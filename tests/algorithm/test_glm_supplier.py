from pathlib import Path
from unittest.mock import MagicMock

import lazyllm
import pytest
from lazyllm.components.formatter import decode_query_with_filepaths
from lazyllm.module.module import ModuleExecutionError
from lazyllm.module.llms.onlinemodule.map_model_type import get_model_type
from lazyllm.module.llms.onlinemodule.supplier import glm


@pytest.fixture
def transport(monkeypatch):
    request = MagicMock()
    response = request.return_value.__enter__.return_value
    response.status_code = 200
    response.headers = {'Content-Type': 'audio/wav'}
    response.content = b'RIFF0000WAVEaudio'
    monkeypatch.setattr(glm.requests, 'request', request)
    download = MagicMock()
    media = download.return_value.__enter__.return_value
    media.headers = {'Content-Type': 'application/octet-stream'}
    media.content = b'0000ftypvideo'
    monkeypatch.setattr(glm.requests, 'get', download)
    return request, response, download


def model(name, **kwargs):
    return lazyllm.OnlineModule(model=name, source='glm', api_key='test-key',
                                url='https://provider.example/api/paas/v4', **kwargs)


def consume_files(result):
    files = decode_query_with_filepaths(result)['files']
    assert files
    data = [Path(path).read_bytes() for path in files]
    for path in files:
        Path(path).unlink()
    return data


@pytest.mark.parametrize('name,kind', [
    ('GLM-5.3', 'llm'), ('GLM-5.3-Flash', 'vlm'), ('glm-5.3-flashx-legal', 'vlm'),
    ('GLM-4.6V-Flash', 'vlm'), ('GLM-5V-Turbo', 'vlm'), ('AutoGLM-Phone', 'vlm'),
    ('GLM-Image', 'sd'), ('CogView-4-250304', 'sd'), ('GLM-ASR-2512', 'stt'), ('GLM-TTS', 'tts'),
    ('cogvideox-3', 'text2video'), ('viduq1-text', 'text2video'), ('vidu2-reference', 'text2video'),
])
def test_glm_routes_current_models(name, kind):
    assert get_model_type(name) == kind
    selected = model(name)
    if kind == 'vlm':
        assert selected.type == 'VLM'
    elif kind in ('tts', 'stt', 'text2video', 'sd'):
        assert selected.type == 'MultiModal'


def test_glm_chat_retains_custom_base_path():
    assert model('glm-5.3')._chat_url == 'https://provider.example/api/paas/v4/chat/completions'


@pytest.mark.parametrize('name', ['cogview-4', 'glm-image'])
def test_glm_images_normalize_parameters_without_sdk(transport, name):
    request, response, download = transport
    response.json.return_value = {'data': [{'url': 'https://cdn.example/image.png'}]}
    result = model(name)('mountain', image_size='1280x1280', batch_size=2, stream_output=False, priority=1)
    assert len(consume_files(result)) == 2
    assert request.call_count == 2
    assert request.call_args.kwargs['json'] == {
        'model': 'cogview-4-250304' if name == 'cogview-4' else name,
        'prompt': 'mountain', 'size': '1280x1280',
    }
    assert 'headers' not in download.call_args.kwargs


def test_glm_speech_returns_encoded_file_and_filters_runtime_options(transport):
    request, _, _ = transport
    assert consume_files(model('glm-tts')('Hello', stream_output=False, priority=2))[0].startswith(b'RIFF')
    assert request.call_args.args[1].endswith('/api/paas/v4/audio/speech')
    payload = request.call_args.kwargs['json']
    assert payload['voice'] == 'female'
    assert payload['stream'] is False
    assert 'priority' not in payload and 'stream_output' not in payload


def test_glm_transcription_closes_file_and_sends_multipart(transport, tmp_path):
    request, response, _ = transport
    response.json.return_value = {'text': 'recognized'}
    audio = tmp_path / 'sample.wav'
    audio.write_bytes(b'RIFF0000WAVEaudio')
    assert model('glm-asr-2512')(None, files=[str(audio)]) == 'recognized'
    args = request.call_args.kwargs
    assert args['data']['model'] == 'glm-asr-2512'
    assert 'Content-Type' not in args['headers']
    assert args['files']['file'][1].closed


@pytest.mark.parametrize('name,files', [
    ('cogvideox-3', []), ('cogvideox-flash', []), ('viduq1-text', []),
    ('viduq1-image', ['https://cdn.example/first.png']),
    ('vidu2-start-end', ['https://cdn.example/first.png', 'https://cdn.example/last.png']),
    ('vidu2-reference', ['https://cdn.example/ref.png']),
])
def test_glm_video_creates_once_polls_and_downloads(transport, name, files):
    request, response, download = transport
    response.json.side_effect = [
        {'id': 'task/id', 'task_status': 'PROCESSING'}, {'task_status': 'PROCESSING'},
        {'task_status': 'SUCCESS', 'video_result': [{'url': 'https://cdn.example/video.mp4'}]},
    ]
    result = model(name)('mountain', files=files, size='1280x720', poll_interval=0,
                         stream_output=False, priority=1)
    assert consume_files(result) == [b'0000ftypvideo']
    assert [call.args[0] for call in request.call_args_list] == ['POST', 'GET', 'GET']
    assert request.call_args.args[1].endswith('/async-result/task%2Fid')
    payload = request.call_args_list[0].kwargs['json']
    assert payload['model'] == name
    assert 'stream_output' not in payload and 'priority' not in payload
    if files:
        assert payload['image_url'] == (files[0] if len(files) == 1 else files)
    assert 'headers' not in download.call_args.kwargs


@pytest.mark.parametrize('result', [{'task_status': 'FAIL'}, {'task_status': 'SUCCESS'}, {'task_status': 'unknown'}])
def test_glm_video_failure_does_not_create_another_task(transport, result):
    request, response, _ = transport
    response.json.side_effect = [{'id': 'task'}, result]
    with pytest.raises(ModuleExecutionError, match='GLM video task'):
        model('cogvideox-3')('test', poll_interval=0)
    assert request.call_count == 2


def test_glm_video_timeout_is_bounded(transport, monkeypatch):
    request, response, _ = transport
    response.json.return_value = {'id': 'task'}
    monkeypatch.setattr(glm.time, 'monotonic', MagicMock(side_effect=[0, 2]))
    with pytest.raises(ModuleExecutionError, match='timed out'):
        model('cogvideox-3')('test', timeout=1)
    assert request.call_count == 1


def test_glm_video_rejects_wrong_input_mode_before_request(transport):
    with pytest.raises(ModuleExecutionError, match='require two images'):
        model('vidu2-start-end')('test', files=['https://cdn.example/one.png'])
    transport[0].assert_not_called()


@pytest.mark.parametrize('role,name', [('tts', 'glm-tts'), ('stt', 'glm-asr-2512'),
                                       ('text2video', 'cogvideox-3'), ('text2image', 'glm-image')])
def test_glm_dynamic_roles_use_request_model_endpoint_and_key(transport, role, name, tmp_path):
    from lazyllm.common.globals import init_session
    from lazymind.model_config import inject_model_config

    request, response, _ = transport
    audio = tmp_path / 'audio.wav'
    audio.write_bytes(b'RIFF0000WAVEaudio')
    runtime_role = {'text2video': 'video_generator', 'text2image': 'image_generator',
                    'stt': 'speech_to_text'}.get(role, role)
    init_session()
    try:
        inject_model_config({role: {'source': 'glm', 'model': name, 'api_key': 'first-key',
                                    'base_url': 'https://first.example/v4'}})
        selected = lazyllm.AutoModel(model=runtime_role)
        for key, host in [('first-key', 'first'), ('second-key', 'second')]:
            inject_model_config({role: {'source': 'glm', 'model': name, 'api_key': key,
                                        'base_url': f'https://{host}.example/v4'}})
            if role == 'stt':
                response.json.side_effect = None
                response.json.return_value = {'text': 'ok'}
                assert selected(None, files=[str(audio)]) == 'ok'
            else:
                if role == 'text2video':
                    response.json.side_effect = [
                        {'id': 'task'}, {'task_status': 'SUCCESS',
                                         'video_result': [{'url': 'https://cdn.example/video.mp4'}]},
                    ]
                elif role == 'text2image':
                    response.json.return_value = {'data': [{'url': 'https://cdn.example/image.png'}]}
                consume_files(selected('hello', poll_interval=0))
            assert request.call_args.args[1].startswith(f'https://{host}.example/v4/')
            assert request.call_args.kwargs['headers']['Authorization'] == f'Bearer {key}'
    finally:
        lazyllm.globals.clear()


def test_glm_catalog_models_resolve_to_real_suppliers():
    import yaml
    catalog_path = Path(__file__).parents[2] / 'backend/core/config/model_catalog.yaml'
    catalog = yaml.safe_load(catalog_path.read_text(encoding='utf-8'))
    provider = next(item for item in catalog['model_providers']['suppliers'] if item['name'] == 'GLM')
    for item in provider['models']:
        selected = model(item['name'], type=item['type'])
        assert selected.series == 'glm'
        if item.get('vision') or item['type'] == 'vlm':
            assert selected.type == 'VLM'


def test_glm_new_visual_models_send_images_and_canonical_name(monkeypatch):
    post = MagicMock()
    response = post.return_value.__enter__.return_value
    response.status_code = 200
    response.text = ('{"choices":[{"index":0,"finish_reason":"stop",'
                     '"message":{"role":"assistant","content":"mountain"}}]}')
    response.headers = {}
    monkeypatch.setattr(glm.requests, 'post', post)
    selected = model('GLM-5.3-Flash', stream=False)
    assert selected('Describe', lazyllm_files=['https://cdn.example/image.png']) == 'mountain'
    payload = post.call_args.kwargs['json']
    assert payload['model'] == 'glm-5.3-flash'
    assert payload['messages'][-1]['content'][1]['type'] == 'image_url'
    assert payload['messages'][-1]['content'][1]['image_url']['url'] == 'https://cdn.example/image.png'


@pytest.mark.parametrize('name,endpoint', [('Embedding-3', 'embeddings'), ('Rerank', 'rerank')])
def test_glm_embedding_and_rerank_keep_base_path_and_api_model(monkeypatch, name, endpoint):
    post = MagicMock()
    response = post.return_value.__enter__.return_value
    response.status_code = 200
    response.json.return_value = {'data': [{'embedding': [0.1, 0.2]}],
                                  'results': [{'index': 0, 'relevance_score': 0.9}]}
    monkeypatch.setattr(glm.requests, 'post', post)
    selected = model(name)
    if endpoint == 'rerank':
        assert selected('query', documents=['document'], top_n=1) == [(0, 0.9)]
    else:
        assert selected('query') == [0.1, 0.2]
    assert post.call_args.args == ('https://provider.example/api/paas/v4/' + endpoint,)
    assert post.call_args.kwargs['json']['model'] == name.lower()


@pytest.mark.parametrize('download', [False, True])
@pytest.mark.parametrize('failure', ['http', 'transport'])
def test_glm_transport_errors_propagate_without_supplier_wrapping(transport, download, failure):
    request, response, media_request = transport
    error = glm.requests.HTTPError('provider failure') if failure == 'http' else glm.requests.Timeout('timeout')
    selected_request = media_request if download else request
    if failure == 'http':
        selected_request.return_value.__enter__.return_value.raise_for_status.side_effect = error
    else:
        selected_request.side_effect = error
    with pytest.raises(type(error)) as caught:
        if download:
            glm._glm_download('https://cdn.example/media', 'image')
        else:
            glm._glm_request('POST', 'https://provider.example', 'images/generations', {}, json={})
    assert caught.value is error
    assert selected_request.call_count == 1
