"""Explicit provider request shapes. Unknown combinations never become chat probes.

OpenRouter dedicated endpoints: https://openrouter.ai/docs/api/reference/overview
Other native shapes follow the corresponding checked-in LazyLLM supplier adapters.
"""
from urllib.parse import urlsplit

from .contracts import ProbeInput, ProbePlan, UnsupportedProbe
from .fixtures import AUDIO, AUDIO_BASE64, IMAGE, IMAGE_DATA_URL
from .runtime_support import runtime_source, supports_runtime_type

MODEL_TYPES = {'llm', 'vlm', 'embed', 'multimodal_embedding', 'rerank', 'text2image',
               'image_editing', 'text2video', 'tts', 'stt'}
TYPE_ALIASES = {'embedding': 'embed', 'reranker': 'rerank', 'cross_modal_embed': 'multimodal_embedding',
                'embed_image': 'multimodal_embedding', 'asr': 'stt'}
COMPATIBLE = {'openai', 'vllm', 'openrouter', 'deepseek', 'kimi', 'ppio', 'glm', 'aiping', 'siliconflow'}
PROMPT = 'Reply with OK.'
# These pairs are documented by OpenRouter; voices are model-specific, so an
# unrecognized TTS model must not inherit a different provider's default.
# https://openrouter.ai/docs/guides/overview/multimodal/tts
# https://openrouter.ai/blog/tutorials/text-to-speech/
OPENROUTER_TTS_VOICES = {
    'mistralai/voxtral-mini-tts-2603': 'en_paul_neutral',
    'x-ai/grok-voice-tts-1.0': 'eve',
    'microsoft/mai-voice-2': 'en-US-Harper:MAI-Voice-2',
    'microsoft/mai-voice-2.1': 'en-US-Harper:MAI-Voice-2.1',
    'openai/gpt-4o-mini-tts-2025-12-15': 'alloy',
    'google/gemini-3.8-flash-lite-tts': 'Kore',
}


def normalize_type(value: str) -> str:
    value = value.strip().lower()
    return TYPE_ALIASES.get(value, value)


def _chat(request: ProbeInput, source: str) -> ProbePlan:
    visual = request.vision or request.model_type == 'vlm'
    if source in {'anthropic', 'claude'}:
        content = [{'type': 'text', 'text': PROMPT}]
        if visual:
            content.insert(0, {'type': 'image', 'source': {
                'type': 'base64', 'media_type': 'image/png', 'data': IMAGE_DATA_URL.split(',', 1)[1],
            }})
        endpoint = 'messages' if urlsplit(request.url).path.rstrip('/').endswith('/v1') else 'v1/messages'
        return ProbePlan(endpoint, 'anthropic', json={
            'model': request.model, 'messages': [{'role': 'user', 'content': content}],
            'max_tokens': 16, 'stream': False,
        }, auth='anthropic')
    content = PROMPT if not visual else [
        {'type': 'text', 'text': PROMPT}, {'type': 'image_url', 'image_url': {'url': IMAGE_DATA_URL}},
    ]
    body = {'model': request.model, 'messages': [{'role': 'user', 'content': content}], 'stream': False}
    # OpenAI reasoning models reject legacy max_tokens; other compatible APIs
    # generally use max_tokens. Avoid optional temperature/stop on either profile.
    model = request.model.lower().rsplit('/', 1)[-1]
    reasoning = source == 'openai' and (model.startswith(('o1', 'o3', 'o4', 'gpt-5')))
    body['max_completion_tokens' if reasoning else 'max_tokens'] = 32
    return ProbePlan('chat/completions', 'chat', json=body)


def build_plan(request: ProbeInput) -> ProbePlan:
    kind = normalize_type(request.model_type)
    if not kind:
        raise UnsupportedProbe('unknown_type')
    if kind not in MODEL_TYPES:
        raise UnsupportedProbe('unsupported_type')
    if not supports_runtime_type(request.source, kind):
        raise UnsupportedProbe('unsupported_provider')
    source = runtime_source(request.source)
    if kind in {'llm', 'vlm'}:
        if source not in COMPATIBLE | {'anthropic', 'claude', 'qwen', 'doubao', 'sensenova', 'minimax'}:
            raise UnsupportedProbe('unsupported_provider')
        plan = _chat(request, source)
        if source == 'kimi' and not urlsplit(request.url).path.rstrip('/').endswith('/v1'):
            plan.endpoint = 'v1/chat/completions'
        if source == 'qwen':
            path = urlsplit(request.url).path.rstrip('/')
            if not path:
                plan.endpoint = 'compatible-mode/v1/chat/completions'
            elif not path.endswith('/compatible-mode/v1'):
                raise UnsupportedProbe('unsupported_provider')
        return plan
    if source in {'anthropic', 'claude'}:
        raise UnsupportedProbe('unsupported_provider')
    if source == 'qwen':
        path = urlsplit(request.url).path.rstrip('/')
        if kind not in {'embed', 'rerank'} or (path and not path.endswith('/api/v1')):
            raise UnsupportedProbe('unsupported_provider')
        prefix = '' if path else 'api/v1/'
        if kind == 'embed':
            return ProbePlan(prefix + 'services/embeddings/text-embedding/text-embedding', 'qwen_embedding',
                             json={'model': request.model, 'input': {'texts': ['test']}})
        return ProbePlan(prefix + 'services/rerank/text-rerank/text-rerank', 'qwen_rerank', json={
            'model': request.model, 'input': {'query': 'test', 'documents': ['test']}, 'parameters': {'top_n': 1},
        })
    if source == 'sensenova':
        if kind == 'embed':
            if not urlsplit(request.url).path.rstrip('/').endswith('/v1/llm'):
                raise UnsupportedProbe('unsupported_provider')
            return ProbePlan('embeddings', 'sensenova_embedding', json={'model': request.model, 'input': 'test'})
        if kind not in {'text2image', 'image_editing'}:
            raise UnsupportedProbe('unsupported_provider')
        body = {'model': request.model, 'prompt': 'A blue square.', 'n': 1}
        if kind == 'image_editing':
            body['images'] = [{'image_url': IMAGE_DATA_URL}]
        return ProbePlan('images/edits' if kind == 'image_editing' else 'images/generations', 'image', json=body)
    if source not in COMPATIBLE | {'doubao'}:
        raise UnsupportedProbe('unsupported_provider')
    if source == 'openrouter' and kind in {'rerank', 'multimodal_embedding', 'image_editing'}:
        # Keep the same support boundary as LazyMind's current runtime adapter.
        raise UnsupportedProbe('unsupported_provider')
    if kind == 'embed':
        return ProbePlan('embeddings', 'embedding', json={'model': request.model, 'input': 'test'})
    if kind == 'multimodal_embedding':
        if source == 'siliconflow':
            return ProbePlan('embeddings', 'embedding',
                             json={'model': request.model, 'input': [{'image': IMAGE_DATA_URL}]})
        if source == 'doubao':
            return ProbePlan('embeddings/multimodal', 'embedding',
                             json={'model': request.model, 'input': [
                                 {'type': 'image_url', 'image_url': {'url': IMAGE_DATA_URL}},
                             ]})
        raise UnsupportedProbe('unsupported_provider')
    if kind == 'rerank':
        return ProbePlan('rerank', 'rerank', json={
            'model': request.model, 'query': 'test', 'documents': ['test'], 'top_n': 1,
        })
    if kind == 'text2image':
        if source == 'siliconflow':
            return ProbePlan('images/generations', 'siliconflow_image', json={
                'model': request.model, 'prompt': 'A blue square.',
            })
        return ProbePlan('images' if source == 'openrouter' else 'images/generations', 'image', json={
            'model': request.model, 'prompt': 'A blue square.', 'n': 1,
        })
    if kind == 'image_editing':
        if source == 'doubao':
            return ProbePlan('images/generations', 'image', json={
                'model': request.model, 'prompt': 'Make the square blue.', 'image': [IMAGE_DATA_URL], 'n': 1,
            })
        if source not in {'openai', 'vllm'}:
            raise UnsupportedProbe('unsupported_provider')
        return ProbePlan('images/edits', 'image',
                         data={'model': request.model, 'prompt': 'Make the square blue.', 'n': '1'},
                         files={'image': ('probe.png', IMAGE, 'image/png')})
    if kind == 'text2video':
        if source == 'openrouter':
            return ProbePlan('videos', 'video', json={'model': request.model, 'prompt': 'A blue square.'})
        if source in {'openai', 'vllm'}:
            return ProbePlan('videos', 'video', files={
                key: (None, value) for key, value in {
                    'model': request.model, 'prompt': 'A blue square.', 'seconds': '4', 'size': '720x1280',
                }.items()
            })
        if source == 'doubao':
            return ProbePlan('contents/generations/tasks', 'video', json={
                'model': request.model, 'content': [{'type': 'text', 'text': 'A blue square.'}],
                'duration': 2, 'resolution': '480p',
            })
        raise UnsupportedProbe('unsupported_provider')
    if kind == 'stt':
        if source == 'openrouter':
            return ProbePlan('audio/transcriptions', 'stt', json={
                'model': request.model, 'input_audio': {'data': AUDIO_BASE64, 'format': 'wav'},
                'response_format': 'json',
            })
        return ProbePlan('audio/transcriptions', 'stt', data={'model': request.model},
                         files={'file': ('probe.wav', AUDIO, 'audio/wav')})
    if kind == 'tts':
        body = {'model': request.model, 'input': 'OK', 'response_format': 'wav'}
        if source in {'openai', 'vllm'}:
            body['voice'] = 'alloy'
        elif source == 'siliconflow':
            if request.model not in {'fnlp/MOSS-TTSD-v0.5', 'FunAudioLLM/CosyVoice2-0.5B'}:
                raise UnsupportedProbe('unsupported_provider')
            body['voice'] = request.model + ':alex'
        elif source == 'openrouter':
            voice = OPENROUTER_TTS_VOICES.get(request.model)
            if voice is None:
                raise UnsupportedProbe('unsupported_provider')
            body.update(voice=voice, response_format='mp3')
        else:
            raise UnsupportedProbe('unsupported_provider')
        return ProbePlan('audio/speech', 'tts', json=body)
    raise UnsupportedProbe('unsupported_provider')
