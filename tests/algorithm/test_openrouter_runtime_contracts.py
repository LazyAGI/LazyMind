"""Offline HTTP contracts for the OpenRouter adapters shipped with LazyMind."""

import json
from types import SimpleNamespace

import lazyllm
import pytest
import requests

from lazyllm.module.llms.onlinemodule.supplier import openrouter as supplier


def response(payload=None, *, status=200, content=None):
    result = requests.Response()
    result.status_code = status
    result.url = 'https://openrouter.ai/api/v1/test'
    result._content = content if content is not None else json.dumps(payload or {}).encode()
    result._content_consumed = True
    result.encoding = 'utf-8'
    result.headers['Content-Type'] = 'application/json'
    return result


@pytest.fixture(autouse=True)
def forbid_live_requests(monkeypatch):
    def forbidden(*_args, **_kwargs):
        raise AssertionError('Contract tests must not make live API calls')

    monkeypatch.setattr(requests, 'post', forbidden)
    monkeypatch.setattr(requests, 'get', forbidden)


def test_chat_uses_chat_completions_and_parses_openai_response(monkeypatch):
    captured = {}

    def post(url, **kwargs):
        captured.update(url=url, **kwargs)
        return response({'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': 'hello back'},
                                      'finish_reason': 'stop'}],
                         'usage': {'prompt_tokens': 2, 'completion_tokens': 2}})

    monkeypatch.setattr(requests, 'post', post)
    module = supplier.OpenRouterChat(api_key='test-only-key', stream=False)
    assert module.forward('hello', max_retries=1) == 'hello back'
    assert captured['url'] == 'https://openrouter.ai/api/v1/chat/completions'
    assert captured['headers']['Authorization'] == 'Bearer test-only-key'
    assert captured['json']['messages'][-1]['content'] == 'hello'
    assert captured['json']['stream'] is False


def test_text_embedding_uses_embeddings_and_returns_numeric_vector(monkeypatch):
    captured = {}

    def post(url, **kwargs):
        captured.update(url=url, **kwargs)
        return response({'data': [{'index': 0, 'embedding': [0.25, -0.5, 0.75]}]})

    monkeypatch.setattr(requests, 'post', post)
    module = supplier.OpenRouterEmbed(api_key='test-only-key')
    assert module.forward('semantic query') == [0.25, -0.5, 0.75]
    assert captured['url'] == 'https://openrouter.ai/api/v1/embeddings'
    assert captured['headers']['Authorization'] == 'Bearer test-only-key'
    assert captured['json']['input'] == ['semantic query']


@pytest.mark.parametrize('adapter, endpoint', [
    (supplier.OpenRouterText2Image, 'images'),
    (supplier.OpenRouterText2Video, 'videos'),
    (supplier.OpenRouterTTS, 'audio/speech'),
    (supplier.OpenRouterSTT, 'audio/transcriptions'),
])
def test_media_adapters_keep_bearer_auth_and_propagate_http_failures(monkeypatch, tmp_path, adapter, endpoint):
    captured = {}
    audio = tmp_path / 'sample.wav'
    audio.write_bytes(b'local-audio')

    def post(url, **kwargs):
        captured.update(url=url, **kwargs)
        return response({'error': {'message': 'unavailable'}}, status=503)

    monkeypatch.setattr(requests, 'post', post)
    module = adapter(api_key='test-only-key')
    with pytest.raises(requests.HTTPError, match='503'):
        module._forward(input=str(audio) if adapter is supplier.OpenRouterSTT else 'a request')
    assert captured['url'] == f'https://openrouter.ai/api/v1/{endpoint}'
    assert captured['headers']['Authorization'] == 'Bearer test-only-key'
    assert captured['timeout'] == 180


@pytest.mark.parametrize('status', ['failed', 'cancelled', 'expired'])
def test_video_stops_on_terminal_failure_without_fetching_content(monkeypatch, status):
    monkeypatch.setattr(requests, 'post', lambda *_args, **_kwargs: response({'id': 'job', 'status': status}))
    with pytest.raises(RuntimeError, match=f'video generation {status}'):
        supplier.OpenRouterText2Video(api_key='test-only-key')._forward(input='a request')


def test_video_rejects_missing_job_id_before_polling(monkeypatch):
    monkeypatch.setattr(requests, 'post', lambda *_args, **_kwargs: response({'status': 'pending'}))
    with pytest.raises(RuntimeError, match='no job ID'):
        supplier.OpenRouterText2Video(api_key='test-only-key')._forward(input='a request')


def test_video_poll_deadline_does_not_wait_or_fetch_after_timeout(monkeypatch):
    ticks = iter([100.0, 101.0])

    def forbidden_sleep(_seconds):
        raise AssertionError('Timed-out jobs must not sleep again')

    monkeypatch.setattr(supplier, 'time', SimpleNamespace(monotonic=lambda: next(ticks), sleep=forbidden_sleep))
    monkeypatch.setattr(requests, 'post', lambda *_args, **_kwargs: response({'id': 'job', 'status': 'pending'}))
    with pytest.raises(TimeoutError, match='timed out'):
        supplier.OpenRouterText2Video(api_key='test-only-key')._forward(input='a request', poll_timeout=0.5)


def test_video_surfaces_polling_http_error_instead_of_returning_a_file(monkeypatch):
    monkeypatch.setattr(supplier, 'time', SimpleNamespace(monotonic=lambda: 0.0, sleep=lambda _seconds: None))
    monkeypatch.setattr(requests, 'post', lambda *_args, **_kwargs: response({'id': 'job', 'status': 'pending'}))
    monkeypatch.setattr(requests, 'get', lambda *_args, **_kwargs: response(status=502))
    with pytest.raises(requests.HTTPError, match='502'):
        supplier.OpenRouterText2Video(api_key='test-only-key')._forward(input='a request')


def test_image_rejects_empty_generation_response(monkeypatch):
    monkeypatch.setattr(requests, 'post', lambda *_args, **_kwargs: response({'data': []}))
    with pytest.raises(RuntimeError, match='no images'):
        supplier.OpenRouterText2Image(api_key='test-only-key')._forward(input='a request')


def test_image_generation_does_not_accept_image_editing_inputs():
    with pytest.raises(ValueError, match='does not accept reference images'):
        supplier.OpenRouterText2Image(api_key='test-only-key')._forward(input='edit it', files=['reference.png'])


@pytest.mark.parametrize('options, message', [
    ({'input': 'https://example.test/audio.wav'}, 'local audio file'),
    ({'input': None}, 'local audio file'),
    ({'files': ['first.wav', 'second.wav']}, 'one audio file'),
])
def test_transcription_rejects_unsupported_file_inputs_before_http(options, message):
    with pytest.raises(ValueError, match=message):
        supplier.OpenRouterSTT(api_key='test-only-key')._forward(**options)


@pytest.mark.parametrize('task', ['rerank', 'image_editing', 'multimodalembed'])
def test_unimplemented_openrouter_tasks_have_no_registered_adapter(task):
    assert getattr(getattr(lazyllm.online, task), 'openrouter', None) is None
