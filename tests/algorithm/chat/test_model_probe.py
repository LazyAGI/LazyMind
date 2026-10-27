import asyncio
import json

import httpx
import pytest


def run_probe(kind='llm', source='OpenAI', response=None, status=200, **values):
    from lazymind.chat.model_probe import ProbeInput, probe_model

    requests = []

    def handle(request):
        requests.append(request)
        if isinstance(response, Exception):
            raise response
        if isinstance(response, bytes):
            return httpx.Response(status, content=response, headers={'content-type': 'audio/wav'})
        return httpx.Response(status, json=response)

    async def invoke():
        async with httpx.AsyncClient(transport=httpx.MockTransport(handle)) as client:
            result = await probe_model(ProbeInput(
                model=values.pop('model', 'fixture-model'), model_type=kind, source=source,
                url=values.pop('url', 'https://provider.example/v1/'),
                api_key='secret-that-must-not-leak', **values,
            ), client=client)
        return result, requests

    return asyncio.run(invoke())


@pytest.mark.parametrize(('kind', 'suffix', 'response'), [
    ('llm', '/chat/completions', {'choices': [{'message': {'content': 'OK'}, 'finish_reason': 'stop'}]}),
    ('vlm', '/chat/completions', {'choices': [{'message': {'content': 'OK'}, 'finish_reason': 'stop'}]}),
    ('embed', '/embeddings', {'data': [{'embedding': [0.1, 0.2]}]}),
    ('multimodal_embedding', '/embeddings', {'data': [{'embedding': [0.1, 0.2]}]}),
    ('rerank', '/rerank', {'results': [{'index': 0, 'relevance_score': 0.8}]}),
    ('text2image', '/images/generations', {'data': [{'url': 'https://media.example/result.png'}]}),
    ('image_editing', '/images/edits', {'data': [{'b64_json': 'iVBORw0KGgo='}]}),
    ('stt', '/audio/transcriptions', {'text': ''}),
    ('tts', '/audio/speech', b'RIFF1234WAVEaudio'),
])
def test_each_type_sends_one_minimal_inference_request(kind, suffix, response):
    source = {
        'multimodal_embedding': 'SiliconFlow', 'text2image': 'SenseNova',
        'image_editing': 'SenseNova', 'stt': 'GLM', 'tts': 'SiliconFlow',
    }.get(kind, 'OpenAI')
    values = {'model': 'FunAudioLLM/CosyVoice2-0.5B'} if kind == 'tts' else {}
    result, requests = run_probe(kind, source=source, response=response, **values)
    assert result['status'] == 'passed'
    assert result['code'] == 'ok'
    assert result['model_type'] == kind
    assert result['elapsed_ms'] >= 0
    assert len(requests) == 1
    assert requests[0].method == 'POST'
    assert requests[0].url.path.endswith(suffix)
    assert requests[0].headers['Authorization'] == 'Bearer secret-that-must-not-leak'
    assert 'secret-that-must-not-leak' not in json.dumps(result)


def test_chat_length_finish_is_reachable_not_a_false_failure():
    result, _ = run_probe(response={'choices': [{'message': {'content': None}, 'finish_reason': 'length'}]})
    assert result['status'] == 'passed'


@pytest.mark.parametrize(('source', 'url', 'response'), [
    ('OpenAI', 'https://provider.example/v1/', {'data': [{'embedding': [0.1, 0.2, 0.3]}]}),
    ('Qwen', 'https://provider.example/api/v1/', {'output': {'embeddings': [{'embedding': [0.1, 0.2, 0.3]}]}}),
    ('SenseNova', 'https://provider.example/v1/llm/', {'embeddings': [{'embedding': [0.1, 0.2, 0.3]}]}),
])
def test_embedding_probe_returns_measured_dimensions_only(source, url, response):
    result, _ = run_probe('embed', source=source, url=url, response=response)
    assert result['status'] == 'passed'
    assert result['embedding_dimensions'] == 3
    assert 'embedding' not in result
    assert 'data' not in result


def test_embedding_probe_rejects_inconsistent_vector_dimensions():
    result, _ = run_probe('embed', response={'data': [{'embedding': [0.1]}, {'embedding': [0.1, 0.2]}]})
    assert result['status'] == 'failed'
    assert 'embedding_dimensions' not in result


def test_embedding_probe_uses_saved_query_prefix_without_leaking_it():
    result, requests = run_probe('embed', embedding={'query_prefix': 'query: ', 'passage_prefix': 'passage: ',
                                                   'batch_size': 2},
                                 response={'data': [{'embedding': [0.1, 0.2]}]})
    assert result['status'] == 'passed'
    assert json.loads(requests[0].content)['input'] == 'query: test'
    assert 'query: ' not in json.dumps(result)


def test_embedding_probe_applies_advanced_transport_to_same_minimal_request():
    result, requests = run_probe('embed', request_advanced={
        'headers': {'X-Tenant': 'sample'}, 'query': {'version': '42'}, 'extra_body': {'user': 'sample'},
    }, response={'data': [{'embedding': [0.1, 0.2]}]})
    assert result['status'] == 'passed'
    assert requests[0].headers['X-Tenant'] == 'sample'
    assert requests[0].url.params['version'] == '42'
    assert json.loads(requests[0].content) == {'model': 'fixture-model', 'input': 'test', 'user': 'sample'}
    assert 'sample' not in json.dumps(result)


@pytest.mark.parametrize('vision', [False, True])
def test_vlm_probe_respects_explicit_vision_gate(vision):
    result, requests = run_probe('vlm', vision=vision, response={'choices': [{'message': {'content': 'OK'}}]})
    assert result['status'] == 'passed'
    content = json.loads(requests[0].content)['messages'][0]['content']
    assert isinstance(content, list) is vision


def test_probe_feature_gates_override_gateway_extra_body():
    result, requests = run_probe(features={'tool_calling': False, 'reasoning': False}, request_advanced={
        'extra_body': {'tools': [{'type': 'function'}], 'reasoning_effort': 'high',
                       'chat_template_kwargs': {'enable_thinking': True}},
    }, response={'choices': [{'message': {'content': 'OK'}}]})
    assert result['status'] == 'passed'
    body = json.loads(requests[0].content)
    assert 'tools' not in body
    assert 'reasoning_effort' not in body
    assert body['chat_template_kwargs'] == {}


def test_embedding_probe_does_not_apply_chat_feature_gates():
    result, requests = run_probe('embed', features={'tool_calling': False, 'reasoning': False},
                                 response={'data': [{'embedding': [0.1, 0.2]}]})
    assert result['status'] == 'passed'
    assert json.loads(requests[0].content) == {'model': 'fixture-model', 'input': 'test'}


@pytest.mark.parametrize(('kind', 'response'), [
    ('stt', {'text': 'transcript'}),
    ('text2image', {'data': [{'url': 'https://media.example/image.png'}]}),
])
def test_probe_rejects_advanced_settings_for_native_sdk_runtime_transports(kind, response):
    result, requests = run_probe(kind, 'GLM', request_advanced={'headers': {'X-Tenant': 'sample'}},
                                 response=response)
    assert result['status'] == 'unsupported'
    assert result['code'] == 'unsupported_provider'
    assert requests == []


@pytest.mark.parametrize(('kind', 'response'), [
    ('stt', {'text': 'transcript'}),
    ('text2image', {'data': [{'url': 'https://media.example/image.png'}]}),
])
def test_native_sdk_profiles_still_probe_without_advanced_options(kind, response):
    result, requests = run_probe(kind, 'GLM', response=response)
    assert result['status'] == 'passed'
    assert len(requests) == 1


def test_reasoning_openai_chat_uses_completion_budget_without_temperature():
    result, requests = run_probe(model='o3-mini', url='https://api.openai.com/v1/',
                                 response={'choices': [{'message': {'content': 'OK'}}]})
    body = json.loads(requests[0].content)
    assert result['status'] == 'passed'
    assert 'max_completion_tokens' in body
    assert 'max_tokens' not in body
    assert 'temperature' not in body


def test_anthropic_uses_messages_and_its_authentication_protocol():
    result, requests = run_probe(source='Claude', response={'content': [{'type': 'text', 'text': 'OK'}]})
    assert result['status'] == 'passed'
    request = requests[0]
    assert request.url.path.endswith('/messages')
    assert request.headers['x-api-key'] == 'secret-that-must-not-leak'
    assert 'authorization' not in request.headers
    assert json.loads(request.content)['max_tokens'] > 0


def test_openrouter_uses_its_dedicated_image_endpoint():
    result, requests = run_probe('text2image', 'OpenRouter',
                                 response={'data': [{'url': 'https://result.example/image'}]})
    assert result['status'] == 'passed'
    assert requests[0].url.path == '/v1/images'


def test_siliconflow_image_probe_uses_native_request_and_response():
    result, requests = run_probe('text2image', 'SiliconFlow', response={
        'images': [{'url': 'https://media.example/image?signature=test'}],
    })
    assert result['status'] == 'passed'
    body = json.loads(requests[0].content)
    assert body == {'model': 'fixture-model', 'prompt': 'A blue square.'}
    assert requests[0].url.path == '/v1/images/generations'


def test_siliconflow_model_not_found_code_is_distinct_from_invalid_parameters():
    result, _ = run_probe(source='SiliconFlow', status=400, response={
        'code': 20012, 'message': 'secret-that-must-not-leak', 'data': None,
    })
    assert result['code'] == 'model_not_found'
    assert 'secret-that-must-not-leak' not in json.dumps(result)


@pytest.mark.parametrize(('model', 'voice'), [
    ('mistralai/voxtral-mini-tts-2603', 'en_paul_neutral'),
    ('x-ai/grok-voice-tts-1.0', 'eve'),
    ('microsoft/mai-voice-2', 'en-US-Harper:MAI-Voice-2'),
    ('microsoft/mai-voice-2.1', 'en-US-Harper:MAI-Voice-2.1'),
    ('openai/gpt-4o-mini-tts-2025-12-15', 'alloy'),
    ('google/gemini-3.8-flash-lite-tts', 'Kore'),
])
def test_openrouter_tts_uses_a_documented_model_voice_pair_and_mp3(model, voice):
    result, requests = run_probe('tts', 'OpenRouter', model=model, response=b'ID3test-mp3-audio')
    assert result['status'] == 'passed'
    assert len(requests) == 1
    assert requests[0].url.path == '/v1/audio/speech'
    body = json.loads(requests[0].content)
    assert body['model'] == model
    assert body['voice'] == voice
    assert body['response_format'] == 'mp3'
    assert body['input'] == 'OK'


@pytest.mark.parametrize('model', [
    'unknown-provider/new-tts', 'deepgram/flux-tts:free',
    'mistralai/voxtral-future-tts', 'google/gemini-unknown-tts',
])
def test_openrouter_tts_without_a_verified_voice_contract_makes_no_request(model):
    result, requests = run_probe('tts', 'OpenRouter', model=model, response=b'ID3test-mp3-audio')
    assert result['status'] == 'unsupported'
    assert result['code'] == 'unsupported_provider'
    assert requests == []


def test_glm_tts_probe_uses_hosted_speech_contract():
    result, requests = run_probe('tts', 'GLM', model='GLM-TTS', url='https://provider.example/api/paas/v4/',
                                 response=b'RIFF1234WAVEaudio')
    assert result['status'] == 'passed'
    assert len(requests) == 1
    assert requests[0].url.path == '/api/paas/v4/audio/speech'
    assert json.loads(requests[0].content) == {
        'model': 'glm-tts', 'input': 'OK', 'voice': 'female', 'response_format': 'wav', 'stream': False,
    }


@pytest.mark.parametrize(('model', 'image_count'), [
    ('cogvideox-3', 0), ('viduq1-text', 0), ('viduq1-image', 1),
    ('vidu2-start-end', 2), ('vidu2-reference', 1),
])
def test_glm_video_probe_uses_model_input_contract(model, image_count):
    result, requests = run_probe('text2video', 'GLM', model=model,
                                 url='https://provider.example/api/paas/v4/', response={'id': 'task-id'})
    assert result['status'] == 'accepted'
    assert len(requests) == 1
    assert requests[0].url.path == '/api/paas/v4/videos/generations'
    body = json.loads(requests[0].content)
    assert body['model'] == model
    assert body['prompt'] == 'A blue square.'
    image_url = body.get('image_url')
    assert (len(image_url) if isinstance(image_url, list) else int(image_url is not None)) == image_count


def test_glm_custom_video_model_keeps_provider_model_case():
    result, requests = run_probe('text2video', 'GLM', model='Private-Video-V1',
                                 response={'id': 'task-id'})
    assert result['status'] == 'accepted'
    assert json.loads(requests[0].content)['model'] == 'Private-Video-V1'


def test_glm_unknown_speech_and_video_modes_make_no_probe_request():
    for kind, model in [('tts', 'GLM-TTS-Clone'), ('text2video', 'vidu2-future-mode')]:
        result, requests = run_probe(kind, 'GLM', model=model, response={'id': 'task-id'})
        assert result['status'] == 'unsupported'
        assert requests == []


@pytest.mark.parametrize('source', ['OpenRouter'])
def test_video_is_only_accepted_and_never_polled_or_downloaded(source):
    result, requests = run_probe('text2video', source, response={'id': 'task-id', 'status': 'queued'})
    assert result['status'] == 'accepted'
    assert result['code'] == 'accepted'
    assert len(requests) == 1
    assert requests[0].url.path.endswith('/videos')


@pytest.mark.parametrize(('kind', 'source', 'code'), [
    ('', 'OpenAI', 'unknown_type'),
    ('future-task', 'OpenAI', 'unsupported_type'),
    ('embed', 'Claude', 'unsupported_provider'),
    ('multimodal_embedding', 'OpenRouter', 'unsupported_provider'),
    ('rerank', 'OpenRouter', 'unsupported_provider'),
    ('tts', 'Qwen', 'unsupported_provider'),
    ('llm', 'UnknownProvider', 'unsupported_provider'),
])
def test_unsupported_types_never_fall_back_to_chat_or_make_requests(kind, source, code):
    result, requests = run_probe(kind, source, response={})
    assert result['status'] == 'unsupported'
    assert result['code'] == code
    assert requests == []


@pytest.mark.parametrize('source', ['DeepSeek', 'Kimi', 'Claude', 'PPIO'])
@pytest.mark.parametrize('kind', ['embed', 'rerank', 'text2image', 'text2video', 'tts', 'stt'])
def test_missing_runtime_adapters_never_produce_false_positive_probes(source, kind):
    result, requests = run_probe(kind, source, response={'data': [{'embedding': [0.1]}]})
    assert result['status'] == 'unsupported'
    assert result['code'] == 'unsupported_provider'
    assert requests == []


@pytest.mark.parametrize('source', ['OpenAI', 'VLLM', 'LMDeploy'])
@pytest.mark.parametrize('kind', ['text2image', 'text2video', 'image_editing', 'tts', 'stt'])
def test_compatible_protocol_does_not_imply_missing_runtime_media_support(source, kind):
    result, requests = run_probe(kind, source, response={'id': 'task-id'})
    assert result['status'] == 'unsupported'
    assert result['code'] == 'unsupported_provider'
    assert requests == []


@pytest.mark.parametrize(('status', 'body', 'code'), [
    (400, {'error': {'message': 'secret-that-must-not-leak'}}, 'invalid_request'),
    (401, {}, 'authentication_failed'), (403, {}, 'permission_denied'),
    (404, {}, 'model_not_found'), (429, {}, 'rate_limited'),
    (429, {'error': {'code': 'insufficient_quota'}}, 'quota_exceeded'),
    (503, {}, 'upstream_error'), (302, {}, 'upstream_error'),
])
def test_http_failures_return_safe_stable_codes_without_retry(status, body, code):
    result, requests = run_probe(response=body, status=status)
    assert result['status'] == 'failed'
    assert result['code'] == code
    assert result['http_status'] == status
    assert len(requests) == 1
    assert 'secret-that-must-not-leak' not in json.dumps(result)


@pytest.mark.parametrize('response', [{}, {'choices': []}, {'choices': [{'message': {'content': ''}}]}])
def test_empty_chat_success_is_not_reported_as_available(response):
    result, _ = run_probe(response=response)
    assert result['status'] == 'failed'
    assert result['code'] in {'empty_response', 'invalid_response'}


@pytest.mark.parametrize('vector', [[], [None], [True], ['1.0'], [10 ** 1000]])
def test_invalid_embedding_is_not_reported_as_available(vector):
    result, _ = run_probe('embed', response={'data': [{'embedding': vector}]})
    assert result['status'] == 'failed'
    assert result['code'] in {'empty_response', 'invalid_response'}


@pytest.mark.parametrize(('error', 'code'), [
    (httpx.ReadTimeout('secret-that-must-not-leak'), 'timeout'),
    (httpx.ConnectError('secret-that-must-not-leak'), 'connection_error'),
])
def test_transport_errors_are_safe(error, code):
    result, requests = run_probe(response=error)
    assert result['status'] == 'failed'
    assert result['code'] == code
    assert len(requests) == 1
    assert 'secret-that-must-not-leak' not in json.dumps(result)


def test_self_hosted_skip_auth_does_not_send_a_key():
    result, requests = run_probe(source='VLLM', skip_auth=True,
                                 response={'choices': [{'message': {'content': 'OK'}}]})
    assert result['status'] == 'passed'
    assert 'authorization' not in requests[0].headers


def test_invalid_base_url_never_makes_a_request():
    result, requests = run_probe(url='https://user:secret@provider.example/v1/?key=secret', response={})
    assert result['status'] == 'failed'
    assert result['code'] == 'invalid_request'
    assert requests == []


def test_sensenova_image_edit_uses_the_native_image_objects():
    result, requests = run_probe('image_editing', 'SenseNova',
                                 response={'data': [{'url': 'https://media.example/image.png'}]})
    assert result['status'] == 'passed'
    assert json.loads(requests[0].content)['images'][0]['image_url'].startswith('data:image/png;base64,')


def test_signed_image_reference_is_valid_and_never_downloaded():
    result, requests = run_probe('text2image', 'SenseNova', response={
        'data': [{'url': 'https://media.example/result.png?signature=secret-that-must-not-leak&expires=123'}],
    })
    assert result['status'] == 'passed'
    assert len(requests) == 1
    assert 'secret-that-must-not-leak' not in json.dumps(result)


def test_doubao_multimodal_embedding_uses_typed_image_url_input():
    result, requests = run_probe('multimodal_embedding', 'Doubao', response={'data': {'embedding': [1, 2]}})
    assert result['status'] == 'passed'
    image = json.loads(requests[0].content)['input'][0]
    assert image['type'] == 'image_url'
    assert image['image_url']['url'].startswith('data:image/png;base64,')


def test_kimi_default_origin_adds_its_v1_request_path():
    result, requests = run_probe(source='Kimi', url='https://api.moonshot.cn/',
                                 response={'choices': [{'message': {'content': 'OK'}}]})
    assert result['status'] == 'passed'
    assert requests[0].url.path == '/v1/chat/completions'


def test_qwen_default_origin_uses_its_native_embedding_endpoint():
    result, requests = run_probe('embed', 'Qwen', url='https://dashscope.aliyuncs.com/',
                                 response={'output': {'embeddings': [{'embedding': [1, 2]}]}})
    assert result['status'] == 'passed'
    assert requests[0].url.path == '/api/v1/services/embeddings/text-embedding/text-embedding'


def test_minimax_http_200_provider_error_is_not_a_success():
    result, requests = run_probe(source='MiniMax', response={
        'base_resp': {'status_code': 1004, 'status_msg': 'secret-that-must-not-leak'},
        'choices': [{'message': {'content': 'OK'}}],
    })
    assert result['status'] == 'failed'
    assert result['code'] == 'authentication_failed'
    assert len(requests) == 1
    assert 'secret-that-must-not-leak' not in json.dumps(result)


def test_total_deadline_cancels_actual_stream_and_closes_connection(monkeypatch):
    from lazymind.chat.model_probe import ProbeInput, probe_model, runner

    state = {'cancelled': False, 'closed': False}

    class EndlessStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            try:
                yield b'{'
                await asyncio.Event().wait()
            finally:
                state['cancelled'] = True

        async def aclose(self):
            state['closed'] = True

    async def invoke():
        monkeypatch.setattr(runner, 'TOTAL_TIMEOUT_SECONDS', 0.01)
        async with httpx.AsyncClient(transport=httpx.MockTransport(
            lambda request: httpx.Response(200, stream=EndlessStream()),
        )) as client:
            return await probe_model(ProbeInput('model', 'llm', 'VLLM', 'http://localhost/v1/', skip_auth=True),
                                     client=client)

    result = asyncio.run(invoke())
    assert result['code'] == 'timeout'
    assert state == {'cancelled': True, 'closed': True}


def test_response_cap_stops_reading_and_closes_connection(monkeypatch):
    from lazymind.chat.model_probe import ProbeInput, probe_model, runner

    state = {'chunks': 0, 'closed': False}

    class OversizedStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            for _ in range(10):
                state['chunks'] += 1
                yield b'x' * 16

        async def aclose(self):
            state['closed'] = True

    async def invoke():
        monkeypatch.setattr(runner, 'MAX_RESPONSE_BYTES', 20)
        async with httpx.AsyncClient(transport=httpx.MockTransport(
            lambda request: httpx.Response(200, stream=OversizedStream()),
        )) as client:
            return await probe_model(ProbeInput('model', 'llm', 'OpenAI', 'https://provider.example/v1/'), client=client)

    result = asyncio.run(invoke())
    assert result['code'] == 'invalid_response'
    assert state == {'chunks': 2, 'closed': True}


def test_unexpected_compression_is_rejected_before_decompressing_or_reading():
    from lazymind.chat.model_probe import ProbeInput, probe_model

    state = {'read': False, 'closed': False}

    class UnreadStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            state['read'] = True
            yield b'not-real-gzip'

        async def aclose(self):
            state['closed'] = True

    def handle(request):
        assert request.headers['accept-encoding'] == 'identity'
        return httpx.Response(200, headers={'content-encoding': 'gzip'}, stream=UnreadStream())

    async def invoke():
        async with httpx.AsyncClient(transport=httpx.MockTransport(handle)) as client:
            return await probe_model(ProbeInput('model', 'embed', 'OpenAI', 'https://provider.example/v1/'),
                                     client=client)

    result = asyncio.run(invoke())
    assert result['code'] == 'invalid_response'
    assert state == {'read': False, 'closed': True}


@pytest.mark.parametrize(('kind', 'response', 'code'), [
    ('tts', b'not actual audio', 'invalid_response'),
    ('llm', b'<html>secret-that-must-not-leak</html>', 'invalid_response'),
    ('embed', b'', 'empty_response'),
])
def test_non_json_or_invalid_audio_is_not_a_success(kind, response, code):
    source = 'SiliconFlow' if kind == 'tts' else 'OpenAI'
    values = {'model': 'FunAudioLLM/CosyVoice2-0.5B'} if kind == 'tts' else {}
    result, requests = run_probe(kind, source, response=response, **values)
    assert result['status'] == 'failed'
    assert result['code'] == code
    assert len(requests) == 1
    assert 'secret-that-must-not-leak' not in json.dumps(result)


@pytest.mark.parametrize(('source', 'url', 'kind', 'response', 'suffix'), [
    ('Qwen', 'https://proxy.example/custom/api/v1/', 'embed',
     {'output': {'embeddings': [{'embedding': [1, 2]}]}},
     '/custom/api/v1/services/embeddings/text-embedding/text-embedding'),
    ('Qwen', 'https://proxy.example/custom/api/v1/', 'rerank',
     {'output': {'results': [{'index': 0, 'relevance_score': 0.1}]}},
     '/custom/api/v1/services/rerank/text-rerank/text-rerank'),
    ('Doubao', 'https://proxy.example/custom/api/v3/', 'multimodal_embedding',
     {'data': {'embedding': [1, 2]}}, '/custom/api/v3/embeddings/multimodal'),
    ('SenseNova', 'https://proxy.example/v1/llm/', 'embed',
     {'embeddings': [{'embedding': [1, 2]}]}, '/v1/llm/embeddings'),
])
def test_native_profiles_keep_the_configured_origin_and_path(source, url, kind, response, suffix):
    result, requests = run_probe(kind, source, url=url, response=response)
    assert result['status'] == 'passed'
    assert requests[0].url.host == 'proxy.example'
    assert requests[0].url.path == suffix
