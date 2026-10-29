"""Provider request compatibility and real Agent initialization regressions."""
import base64
import copy
import json
import random
import struct
import zlib
from contextlib import ExitStack

import lazyllm
import pytest

from fallback_test_support import EFFECT_RECEIPTS, record_fallback_effect
from fallback_test_support import candidate, fail, succeed, switches
from fallback_test_support import VALID_SUMMARY
from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
)
from lazymind.chat.engine.agent_runtime.budget import build_context_budget
from lazymind.chat.engine.agent_runtime.model_fallback import FallbackLLM, create_fallback_state
from lazymind.chat.engine.agent_runtime.summarizer import _call_summarizer_llm
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
from lazymind.config import config
from lazyllm.tools.agent.toolError import ToolExecutionError


@pytest.fixture
def provider(fallback_provider, tmp_path):
    lazyllm.globals['agentic_config'] = {'user_id': 'compatibility-test', 'conversation_id': 'test'}
    with config.temp('agentic_workspace', str(tmp_path)):
        yield fallback_provider


def raw_model(item, *, vision=False):
    role = item.llm_config['llm']
    return lazyllm.OnlineChatModule(
        source=role['source'], model=role['model'], type='vlm' if vision else 'llm',
        url=role['base_url'], api_key=role['api_key'], stream=False,
        static_params=copy.deepcopy(role.get('static_params') or {}),
    )


def wrapped(current, alternatives, *, vision=False):
    options = AgentExecutionOptions(
        llm_config=current.llm_config, model_candidates=(current, *alternatives),
        current_model_candidate_id=current.candidate_id,
    )
    return FallbackLLM(create_fallback_state(raw_model(current, vision=vision), options))


def reasoning_response(request):
    if request['model'] == 'o3':
        if 'max_tokens' in request:
            return {'error': 'unsupported_parameter', 'status': 400, 'type': 'invalid_request_error'}
        return {'message': {'role': 'assistant', 'content': 'reasoning completed'}}


@pytest.mark.parametrize('as_fallback', [False, True])
@pytest.mark.parametrize('configured_limit', [None, 128, 65536])
@pytest.mark.parametrize('window,reserved', [(131072, 32768), (32768, 16384)])
def test_reasoning_output_limit_is_legal_for_initial_and_fallback_models(
    provider, as_fallback, configured_limit, window, reserved,
):
    target = candidate(provider, 'o3', window=window)
    if configured_limit is not None:
        target.llm_config['llm']['static_params'] = {'max_completion_tokens': configured_limit}
    current = candidate(provider, 'model-a') if as_fallback else target
    fail(provider, 'model-a')
    provider.on_request = reasoning_response
    model = wrapped(current, [target] if as_fallback else [])
    assert model('complete this request') == 'reasoning completed'
    expected = ['model-a', 'o3'] if as_fallback else ['o3']
    assert [request['model'] for request in provider.requests] == expected
    request = provider.requests[-1]
    assert 'max_tokens' not in request
    if configured_limit is None:
        assert 'max_completion_tokens' not in request
    else:
        assert request['max_completion_tokens'] == min(configured_limit, reserved)
    assert target.llm_config['llm'].get('static_params', {}) == (
        {'max_completion_tokens': configured_limit} if configured_limit is not None else {}
    )


@pytest.mark.parametrize('as_fallback', [False, True])
@pytest.mark.parametrize('summary', [False, True])
def test_unconfigured_output_limit_keeps_provider_default(provider, as_fallback, summary):
    target = candidate(provider, 'gpt-4o', window=131072)
    current = candidate(provider, 'model-a') if as_fallback else target
    fail(provider, 'model-a')

    def respond(request):
        if request['model'] == 'gpt-4o':
            # This model accepts defaults but rejects the internal 32768 reserve.
            if 'max_tokens' in request or 'max_completion_tokens' in request:
                return {'error': 'invalid_value', 'status': 400}
            return {'message': {'role': 'assistant', 'content': 'completed'}}

    provider.on_request = respond
    if summary:
        assert _call_summarizer_llm(raw_model(target), 'Summarize.', 'Short history.',
                                   candidate_budget=build_context_budget(131072)) == 'completed'
    else:
        assert wrapped(current, [target] if as_fallback else [])('Complete.') == 'completed'
    assert provider.requests[-1]['model'] == 'gpt-4o'
    assert 'max_tokens' not in provider.requests[-1]
    assert 'max_completion_tokens' not in provider.requests[-1]


def test_explicit_completion_limit_wins_without_mutating_the_original_model(provider):
    current = candidate(provider, 'o3')
    current.llm_config['llm']['static_params'] = {'max_tokens': 1024}
    raw = raw_model(current)
    original = copy.deepcopy(raw._static_params)
    options = AgentExecutionOptions(llm_config=current.llm_config, model_candidates=(current,),
                                    current_model_candidate_id=current.candidate_id)
    model = FallbackLLM(create_fallback_state(raw, options))
    provider.on_request = reasoning_response
    assert model('complete', max_completion_tokens=256) == 'reasoning completed'
    assert provider.requests[-1]['max_completion_tokens'] == 256
    assert 'max_tokens' not in provider.requests[-1]
    assert raw._static_params == original


def test_candidate_summary_uses_the_same_reasoning_output_limit(provider):
    current = candidate(provider, 'o3')
    current.llm_config['llm']['static_params'] = {'max_completion_tokens': 128}
    raw = raw_model(current)
    provider.on_request = reasoning_response
    assert _call_summarizer_llm(raw, 'Summarize.', 'A short history.',
                                candidate_budget=build_context_budget(32768)) == 'reasoning completed'
    assert len(provider.requests) == 1
    assert provider.requests[0]['max_completion_tokens'] == 128
    assert 'max_tokens' not in provider.requests[0]
    assert raw._static_params == {'max_completion_tokens': 128}


@pytest.mark.parametrize('summary', [False, True])
def test_dynamic_reasoning_model_uses_its_role_configuration(provider, summary):
    from lazyllm.module.llms.model_config_inject import inject_model_config

    current = candidate(provider, 'o3')
    runtime = lazyllm.globals['config']
    old_models, old_key = runtime.get('dynamic_model_configs'), runtime.get('openai_api_key')
    provider.on_request = reasoning_response
    try:
        inject_model_config(current.llm_config)
        raw = lazyllm.OnlineChatModule(
            source='dynamic', name='llm', dynamic_auth=True, type='llm', stream=False,
        )
        if summary:
            result = _call_summarizer_llm(
                raw, 'Summarize.', 'Short history.', candidate_budget=build_context_budget(32768),
            )
        else:
            options = AgentExecutionOptions(llm_config=current.llm_config, model_candidates=(current,),
                                            current_model_candidate_id=current.candidate_id)
            result = FallbackLLM(create_fallback_state(raw, options))('Complete.')
        assert result == 'reasoning completed'
    finally:
        runtime['dynamic_model_configs'], runtime['openai_api_key'] = old_models, old_key
    assert len(provider.requests) == 1
    assert provider.requests[0]['model'] == 'o3'
    assert 'max_completion_tokens' not in provider.requests[0]
    assert 'max_tokens' not in provider.requests[0]


@pytest.mark.parametrize('native_limit', [None, 64])
def test_explicit_output_limit_survives_a_change_to_a_native_provider(provider, native_limit):
    a = candidate(provider, 'o3')
    b = candidate(provider, 'native-b', source='claude')
    if native_limit is not None:
        b.llm_config['llm']['static_params'] = {'max_tokens': native_limit}
    fail(provider, 'o3')

    def respond(request):
        if request['model'] == 'native-b':
            if 'max_completion_tokens' in request:
                return {'error': 'unsupported_parameter', 'status': 400}
            return {'message': {'role': 'assistant', 'content': 'native completed'}}

    provider.on_request = respond
    assert wrapped(a, [b])('Complete.', max_completion_tokens=128) == 'native completed'
    assert [request['model'] for request in provider.requests] == ['o3', 'native-b']
    assert provider.requests[-1]['max_tokens'] == min(native_limit or 128, 128)
    assert 'max_completion_tokens' not in provider.requests[-1]


@pytest.fixture
def image_data():
    def chunk(name, data):
        return struct.pack('!I', len(data)) + name + data + struct.pack('!I', zlib.crc32(name + data))

    pixels = random.Random(841).randbytes(600 * 600 * 3)
    rows = b''.join(b'\0' + pixels[start:start + 1800] for start in range(0, len(pixels), 1800))
    png = (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('!2I5B', 600, 600, 8, 2, 0, 0, 0))
           + chunk(b'IDAT', zlib.compress(rows)) + chunk(b'IEND', b''))
    assert len(png) > 1000000
    return base64.b64encode(png).decode('ascii')


@pytest.mark.parametrize('compression', [False, True])
@pytest.mark.parametrize('source', ['openai', 'claude'])
def test_normal_image_size_does_not_exhaust_healthy_vision_candidates(provider, image_data, compression, source):
    a = candidate(provider, 'model-a', source=source, window=131072)
    b = candidate(provider, 'model-b', source=source, window=131072)
    for item in (a, b):
        item.llm_config['llm']['vision'] = True
    fail(provider, 'model-a')
    succeed(provider, 'model-b')
    history = [{'role': 'user', 'content': [
        {'type': 'text', 'text': 'Describe this attached image.'},
        {'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,' + image_data}},
    ]}]
    before = copy.deepcopy(history)
    events = []
    with config.temp('context_compression_enabled', compression):
        assert wrapped(a, [b], vision=True)(
            'Describe the image.', llm_chat_history=history, stream={'_stream_sink': events.append},
        ) == 'done:model-b'
    assert history == before
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b']
    assert all(image_data in json.dumps(request) for request in provider.requests)
    assert switches(events)[0]['context_compacted'] is False


@pytest.mark.parametrize('block_type', ['image', 'input_image'])
def test_native_image_blocks_are_not_counted_as_base64_text(provider, image_data, block_type):
    from fallback_test_support import request_tokens_for as _request_tokens

    model = raw_model(candidate(provider, 'native-image', source='claude', window=131072), vision=True)
    block = ({'type': 'image', 'source': {'type': 'base64', 'media_type': 'image/png', 'data': image_data}}
             if block_type == 'image' else {'type': 'input_image', 'image_url': 'data:image/png;base64,' + image_data})
    tokens = _request_tokens(model, 'Describe.', [{'role': 'user', 'content': [block]}], None, {})
    assert 1536 <= tokens < 10000


@pytest.mark.parametrize('block_type', ['image_url', 'input_image', 'image'])
def test_image_history_skips_text_candidates_before_http(provider, block_type):
    current, text, vision = [candidate(provider, name) for name in ('current', 'text', 'vision')]
    vision.llm_config['llm']['vision'] = True
    succeed(provider, 'vision')
    image = 'data:image/png;base64,ZmFrZQ=='
    block = ({'type': 'image', 'source': {'type': 'base64', 'media_type': 'image/png', 'data': 'ZmFrZQ=='}}
             if block_type == 'image' else
             {'type': block_type, 'image_url': {'url': image} if block_type == 'image_url' else image})
    history = [{'role': 'user', 'content': [block]}]
    original = copy.deepcopy(history)
    model = wrapped(current, [text, vision])
    assert model('Describe.', llm_chat_history=history) == 'done:vision'
    assert [request['model'] for request in provider.requests] == ['vision']
    assert any(message['content'] == [block] for message in provider.requests[0]['messages'])
    assert history == original


@pytest.mark.parametrize('invalid_file', ['missing', 'directory'])
def test_local_image_preparation_failure_does_not_try_another_candidate(provider, tmp_path, invalid_file):
    from lazyllm.module.module import ModuleExecutionError

    current, text, vision = [candidate(provider, name) for name in ('current', 'text', 'vision')]
    for item in (current, vision):
        item.llm_config['llm']['vision'] = True
    picture = tmp_path / 'picture.png'
    if invalid_file == 'directory':
        picture.mkdir()
    model = wrapped(current, [text, vision], vision=True)
    events = []
    with pytest.raises(ModuleExecutionError):
        model('Describe.', lazyllm_files=[str(picture)], stream={'_stream_sink': events.append})
    assert provider.requests == []
    assert model.state.current.candidate_id == current.candidate_id
    assert model.state.failed_candidates == set()
    assert switches(events) == []


@pytest.mark.parametrize('normal_image', [False, True])
def test_image_input_is_not_rewritten_for_short_candidates(provider, image_data, normal_image):
    tiny = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jFZkAAAAASUVORK5CYII='
    image = image_data if normal_image else tiny
    a = candidate(provider, 'model-a', window=32768)
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=4096)
    for item in (a, b, c):
        item.llm_config['llm']['vision'] = True
    fail(provider, 'model-a')
    provider.script['model-b'] = [
        {'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
        {'message': {'role': 'assistant', 'content': 'B completed after summary'}},
    ]
    succeed(provider, 'model-c')
    history = [
        {'role': 'user', 'content': [
            {'type': 'text', 'text': 'Keep image and task.'},
            {'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,' + image}},
        ]},
        {'role': 'assistant', 'content': 'old evidence ' * 1950},
        {'role': 'user', 'content': 'latest request'},
    ]
    original = copy.deepcopy(history)
    model = wrapped(a, [b, c], vision=True)
    with ExitStack() as stack:
        for key, value in {
            'context_compression_enabled': True, 'context_summary_compression_enabled': True,
            'context_compression_reserved_output_tokens': 1024, 'context_summary_keep_recent_ratio': 0.1,
            'context_summary_min_recent_user_turns': 1,
        }.items():
            stack.enter_context(config.temp(key, value))
        from lazymind.chat.engine.agent_runtime.model_calls import ContextCapacityError
        with pytest.raises(ContextCapacityError) as failure:
            model('Continue.', llm_chat_history=history)
    assert failure.value.fallback_code == 'model_fallback_exhausted'
    assert [request['model'] for request in provider.requests] == ['model-a']
    assert model.state.failed_candidates == {a.candidate_id}
    assert image in json.dumps(provider.requests[-1])
    assert history == original


@pytest.mark.parametrize('window', [131072, None])
@pytest.mark.parametrize('limit', [1, 4])
def test_long_plan_input_reaches_a_larger_candidate_during_real_agent_initialization(provider, window, limit):
    a = candidate(provider, 'small', window=16384)
    b = candidate(provider, 'large', window=window)
    succeed(provider, 'large')
    text = '保留本轮完整输入和约束。' * 1000
    lazyllm.globals['agentic_config']['enable_tool_retrieval'] = True
    options = AgentExecutionOptions(
        llm_config=a.llm_config, model_candidates=(a, b), current_model_candidate_id=a.candidate_id,
        max_input_tokens=16384, enable_builtin_tools=False, skills=False, preload_all_tools=True,
        workspace_permission=WorkspaceContext(local_runtime=False),
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT, prompt=PromptBuilder.for_role(AgentRole.CHAT).input(text, source='user').build(),
        tools=[record_fallback_effect], execution_options=options,
    )
    with config.temp('context_compression_enabled', False), config.temp('model_fallback_max_candidates', limit):
        agent = AgentExecutor().create_agent(raw_model(a), plan)
        assert agent(plan.prompt.current_input) == 'done:large'
    assert [request['model'] for request in provider.requests] == ['large']
    assert text in json.dumps(provider.requests[0], ensure_ascii=False)
    assert agent._model_fallback_state.current.candidate_id == b.candidate_id
    assert EFFECT_RECEIPTS == []


def test_long_text_is_still_rejected_with_an_image(provider, image_data):
    current = candidate(provider, 'vision', window=131072)
    current.llm_config['llm']['vision'] = True
    history = [{'role': 'user', 'content': [
        {'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,' + image_data}},
    ]}]
    with config.temp('context_compression_enabled', False), pytest.raises(Exception, match='configured input budget'):
        wrapped(current, [], vision=True)('真正过长的文本' * 50000, llm_chat_history=history)
    assert provider.requests == []


@pytest.mark.parametrize('excluded_large', [False, True])
def test_initialization_keeps_the_hard_limit_when_no_eligible_candidate_fits(provider, tmp_path, excluded_large):
    a = candidate(provider, 'small', window=16384)
    b = candidate(provider, 'other', window=131072 if excluded_large else 16384)
    lazyllm.globals['agentic_config']['enable_tool_retrieval'] = True
    initial = {'from': {'candidate_id': b.candidate_id}, 'reason_code': 'model_config_unavailable'} \
        if excluded_large else None
    options = AgentExecutionOptions(
        llm_config=a.llm_config, model_candidates=(a, b), current_model_candidate_id=a.candidate_id,
        initial_model_switch=initial, max_input_tokens=16384, enable_builtin_tools=False, skills=False,
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('完整本轮输入。' * 2000, source='user').build(),
        tools=[record_fallback_effect], execution_options=options,
    )
    before = {str(path): path.read_bytes() for path in tmp_path.rglob('*.json')}
    with config.temp('context_compression_enabled', False), pytest.raises(ToolExecutionError, match='固定上下文'):
        AgentExecutor().create_agent(raw_model(a), plan)
    assert {str(path): path.read_bytes() for path in tmp_path.rglob('*.json')} == before
    assert provider.requests == []


def test_dynamic_loading_cannot_borrow_an_unused_larger_candidates_budget(provider, tmp_path):
    def oversized(query: str) -> str:
        return query
    oversized.__doc__ = 'Large schema. ' * 10000 + '\n\nArgs:\n    query (str): Search keywords.\n'
    a = candidate(provider, 'small', window=16384)
    b = candidate(provider, 'large', window=131072)
    succeed(provider, 'small')
    lazyllm.globals['agentic_config']['enable_tool_retrieval'] = True
    options = AgentExecutionOptions(
        llm_config=a.llm_config, model_candidates=(a, b), current_model_candidate_id=a.candidate_id,
        max_input_tokens=16384, enable_builtin_tools=False, skills=False,
        workspace_permission=WorkspaceContext(local_runtime=False),
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT, prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Start.', source='user').build(),
        tools=[oversized], execution_options=options,
    )
    with config.temp('context_compression_enabled', False):
        agent = AgentExecutor().create_agent(raw_model(a), plan)
        assert agent(plan.prompt.current_input) == 'done:small'
    controller = agent._tools_manager.retrieval
    controller.threshold_tokens = 1000000
    before = {str(path): path.read_bytes() for path in tmp_path.rglob('*.json')}
    with pytest.raises(ToolExecutionError, match='固定上下文'):
        controller.load(['oversized'], [])
    assert {str(path): path.read_bytes() for path in tmp_path.rglob('*.json')} == before
    assert [request['model'] for request in provider.requests] == ['small']
    assert EFFECT_RECEIPTS == []


def test_unknown_candidate_does_not_allow_unbounded_tool_initialization(provider, tmp_path):
    a = candidate(provider, 'small', window=16384)
    b = candidate(provider, 'unknown', window=None)
    lazyllm.globals['agentic_config']['enable_tool_retrieval'] = True
    options = AgentExecutionOptions(
        llm_config=a.llm_config, model_candidates=(a, b), current_model_candidate_id=a.candidate_id,
        max_input_tokens=16384, enable_builtin_tools=False, skills=False)
    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('完整本轮输入。' * 20000, source='user').build(),
        tools=[record_fallback_effect], execution_options=options)
    before = {str(path): path.read_bytes() for path in tmp_path.rglob('*.json')}
    with pytest.raises(ToolExecutionError, match='固定上下文'):
        AgentExecutor().create_agent(raw_model(a), plan)
    assert provider.requests == []
    assert {str(path): path.read_bytes() for path in tmp_path.rglob('*.json')} == before
