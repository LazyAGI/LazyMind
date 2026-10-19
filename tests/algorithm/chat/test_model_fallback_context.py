"""HTTP regressions for candidate-local context adaptation."""
import copy
import base64
import json
from dataclasses import replace
from unittest.mock import patch
from contextlib import ExitStack

import lazyllm
import pytest

from test_context_summary_compression import VALID_SUMMARY
from test_model_fallback import ScriptedProvider, EFFECT_RECEIPTS, record_fallback_effect
from test_model_fallback_native import candidate, fail, switches
from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
)
from lazymind.chat.engine.agent_runtime.budget import build_context_budget
from lazymind.chat.engine.agent_runtime.model_fallback import FallbackLLM, create_fallback_state, _request_tokens
from lazymind.chat.engine.agent_runtime.pruner import make_history_compactor
from lazymind.chat.engine.agent_runtime.summary_prompt import build_summary_user_prompt, get_summary_system_prompt
from lazymind.chat.engine.agent_runtime.summary_range import select_summary_range, validate_tool_pairing
from lazymind.config import config
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
from lazyllm.tools.agent import fc_register
from lazyllm.tools.agent.base import TOOL_OBSERVATION_KEY


@pytest.fixture
def provider():
    server = ScriptedProvider()
    old_agent = lazyllm.locals.get('_lazyllm_agent')
    old_config = lazyllm.globals.get('agentic_config')
    lazyllm.locals['_lazyllm_agent'] = {}
    lazyllm.globals['agentic_config'] = {'model_context': {'summary_text': 'committed-before-recovery'}}
    yield server
    lazyllm.locals['_lazyllm_agent'] = old_agent
    lazyllm.globals['agentic_config'] = old_config
    server.close()


def compression_settings():
    stack = ExitStack()
    for key, value in {'context_compression_enabled': True, 'context_summary_compression_enabled': True,
                       'context_compression_reserved_output_tokens': 1024,
                       'context_summary_keep_recent_ratio': 0.1}.items():
        stack.enter_context(config.temp(key, value))
    return stack


def history_with_pairs():
    return [
        {'role': 'user', 'content': 'Keep the original task and its constraints.', 'history_seq': 1},
        {'role': 'assistant', 'content': 'ORIGINAL-OLDER-FACT ' + 'old evidence ' * 1800, 'history_seq': 2},
        {'role': 'user', 'content': 'Keep the completed effect.', 'history_seq': 3},
        {'role': 'assistant', 'content': '', 'history_seq': 4,
         'tool_calls': [{'id': 'completed-effect', 'type': 'function',
                         'function': {'name': 'record_fallback_effect', 'arguments': '{"value":"once"}'}}]},
        {'role': 'tool', 'name': 'record_fallback_effect', 'tool_call_id': 'completed-effect',
         'content': 'effect-receipt: once', 'history_seq': 5},
        {'role': 'user', 'content': 'latest user constraint', 'history_seq': 6},
    ]


@pytest.mark.parametrize('b_failure', ['invalid_summary', 'generation_failure'])
@pytest.mark.parametrize('c_window', [None, 8192])
def test_failed_short_candidate_preserves_original_context_for_next_candidate(provider, tmp_path, b_failure, c_window):
    a = candidate(provider, 'model-a', window=32768)
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=c_window)
    for item in (a, b, c):
        item.llm_config['llm']['vision'] = True
    fail(provider, 'model-a')
    provider.script['model-b'] = [
        {'message': {'role': 'assistant', 'content': 'invalid summary' if b_failure == 'invalid_summary'
                     else VALID_SUMMARY}},
        {'error': 'model_not_found', 'status': 404},
    ]
    provider.script['model-c'] = ([{'message': {'role': 'assistant', 'content': VALID_SUMMARY}}]
                                if c_window else []) + [
        {'message': {'role': 'assistant', 'content': 'continued from original'}},
    ]
    history = history_with_pairs()
    original = copy.deepcopy(history)
    projection = {}
    workspace = {'history': history, '_history_projection_state': projection}
    lazyllm.locals['_lazyllm_agent']['workspace'] = workspace
    raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='vlm', url=provider.url,
                                  api_key='fake-fallback-key', stream=False)
    image_data = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jFZkAAAAASUVORK5CYII='
    image = tmp_path / 'input.png'
    image.write_bytes(base64.b64decode(image_data))
    current_input = 'continue' if b_failure == 'invalid_summary' else {'input': [{
        'role': 'user', 'content': [{'type': 'text', 'text': 'CURRENT-MULTIMODAL-INPUT'},
                                  {'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,' + image_data}}],
    }]}
    system = 'Fixed task constraints. ' * 600
    tools = [{'type': 'function', 'function': {'name': 'record_fallback_effect', 'description': 'record effect',
                                               'parameters': {'type': 'object', 'properties': {}}}}]
    options = AgentExecutionOptions(llm_config=a.llm_config, model_candidates=(a, b, c),
                                    current_model_candidate_id=a.candidate_id, workspace=str(tmp_path))
    with compression_settings():
        projected, _ = make_history_compactor(max_input_tokens=32768, workspace=str(tmp_path))(
            history, prefix={'system_prompt': system, 'tool_definitions': tools},
            current_input='continue', runtime_state=projection,
        )
        before_projection = copy.deepcopy(projection)
        before_context = copy.deepcopy(lazyllm.globals['agentic_config'])
        active_config = lazyllm.globals['agentic_config']
        observed_before_c = []

        def observe(request):
            if request['model'] == 'model-c':
                observed_before_c.append((copy.deepcopy(projection), copy.deepcopy(active_config),
                                          list(tmp_path.rglob('tool_spills/*'))))

        provider.on_request = observe
        wrapped = FallbackLLM(create_fallback_state(raw, options)).prompt(system)
        budget = build_context_budget(8192)
        assert _request_tokens(raw.share(prompt=system), current_input, projected, [str(image)],
                               {'tools': tools}) > budget.effective_input_budget
        selected = select_summary_range(history, effective_input_budget=budget.effective_input_budget,
                                        keep_recent_ratio=0.1, min_recent_user_turns=1)
        summary_view = raw.share(prompt=get_summary_system_prompt(), stream=False)
        assert _request_tokens(summary_view, build_summary_user_prompt(selected.summary_messages), None, None,
                               {'max_tokens': 1024}) <= budget.effective_input_budget
        events = []
        assert wrapped(current_input, llm_chat_history=projected, lazyllm_files=[str(image)], tools=tools,
                       stream={'_stream_sink': events.append}) == 'continued from original'
    expected = ['model-a', 'model-b', 'model-c'] if b_failure == 'invalid_summary' else [
        'model-a', 'model-b', 'model-b', 'model-c',
    ]
    if c_window:
        expected.append('model-c')
    assert [request['model'] for request in provider.requests] == expected
    b_summary = provider.requests[1]
    assert b_summary['stream'] is False
    assert not b_summary.get('tools')
    assert b_summary['max_tokens'] <= budget.reserved_output_tokens
    assert 'ORIGINAL-OLDER-FACT' in str(b_summary['messages'])
    c_request = provider.requests[-1]
    assert c_request['tools'] == tools
    if c_window:
        assert provider.requests[-2]['messages'] == b_summary['messages']
        assert json.dumps(VALID_SUMMARY)[1:-1] in json.dumps(c_request['messages'])
    else:
        assert 'ORIGINAL-OLDER-FACT' in str(c_request['messages'])
        assert 'effect-receipt: once' in str(c_request['messages'])
    assert image_data in str(c_request['messages'])
    if b_failure == 'generation_failure':
        assert 'CURRENT-MULTIMODAL-INPUT' in str(c_request['messages'])
    assert validate_tool_pairing(c_request['messages'])[0]
    assert history == original
    assert observed_before_c == [(before_projection, before_context, [])] * (2 if c_window else 1)
    if not c_window:
        assert lazyllm.globals['agentic_config'] == before_context
    assert not list(tmp_path.rglob('tool_spills/*'))
    assert switches(events)[0]['to']['candidate_id'] == c.candidate_id
    assert switches(events)[0]['context_compacted'] is bool(c_window)


def effect_call():
    return {'message': {'role': 'assistant', 'content': '', 'tool_calls': [{
        'id': 'actual-effect', 'index': 0, 'type': 'function',
        'function': {'name': 'record_fallback_effect', 'arguments': '{"value":"once"}'},
    }]}, 'finish': 'tool_calls'}


def agent_plan(tmp_path, a, alternatives, *, retrieval=False, tools=None):
    options = AgentExecutionOptions(
        llm_config=a.llm_config, max_input_tokens=a.max_input_tokens,
        model_candidates=(a, *alternatives), current_model_candidate_id=a.candidate_id,
        workspace=str(tmp_path), enable_builtin_tools=False, skills=False, keep_full_turns=1,
        required_tool_names=('record_fallback_effect',), preload_all_tools=not retrieval,
        workspace_permission=WorkspaceContext(local_runtime=False),
    )
    prompt = PromptBuilder.for_role(AgentRole.CHAT).system(
        'fixed', 'Fixed constraints', 'Fixed task constraints. ' * 600, source='test',
    ).input('perform the effect once then continue', source='user').build()
    return AgentRunPlan(role=AgentRole.CHAT, prompt=prompt, tools=tools or [record_fallback_effect],
                        history=history_with_pairs(), execution_options=options)


def test_real_agent_keeps_completed_effect_and_tool_input_after_short_generation_failure(provider, tmp_path):
    a, b, c = candidate(provider, 'model-a'), candidate(provider, 'model-b', window=8192), \
        candidate(provider, 'model-c', window=None)
    provider.script['model-a'] = [effect_call(), {'error': 'model_not_found', 'status': 404}]
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
                                {'error': 'model_not_found', 'status': 404}]
    provider.script['model-c'] = [{'message': {'role': 'assistant', 'content': 'continued after one effect'}}]
    raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='llm', url=provider.url,
                                  api_key='fake-fallback-key', stream=False)
    plan = agent_plan(tmp_path, a, [b, c])
    plan.execution_options = replace(plan.execution_options,
                                     model_context_provider=lambda: 'CURRENT-RUNTIME-NOTICE')
    EFFECT_RECEIPTS.clear()
    with compression_settings(), config.temp('agentic_workspace', str(tmp_path)):
        agent = AgentExecutor().create_agent(raw, plan)
        assert agent(plan.prompt.current_input, llm_chat_history=plan.history) == 'continued after one effect'
    assert EFFECT_RECEIPTS == ['once']
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-a', 'model-b', 'model-b', 'model-c']
    assert provider.requests[-1]['messages'] == provider.requests[1]['messages']
    assert provider.requests[-1]['tools'] == provider.requests[1]['tools']
    assert validate_tool_pairing(provider.requests[-1]['messages'])[0]
    assert provider.requests[-1]['messages'][-2]['tool_call_id'] == 'actual-effect'
    assert provider.requests[-1]['messages'][-1]['content'] == 'CURRENT-RUNTIME-NOTICE'
    assert provider.requests[0]['messages'][-1]['content'] == 'CURRENT-RUNTIME-NOTICE'


def optional_first(topic: str) -> str:
    return topic


def optional_second(topic: str) -> str:
    return topic


for optional in (optional_first, optional_second):
    optional.__doc__ = ('Optional contextual capability. ' * 200) + '\n\nArgs:\n    topic: Requested topic.\n'
    fc_register(host_file='NONE')(optional)


@pytest.mark.parametrize('winner_source', ['openai', 'claude'])
def test_short_success_uses_new_budget_for_normal_summary_and_tool_loading(
    provider, tmp_path, monkeypatch, winner_source,
):
    import lazymind.chat.engine.agent_runtime.summarizer as summary_module

    a, b = candidate(provider, 'model-a'), candidate(provider, 'model-b', source=winner_source, window=8192)
    provider.script['model-a'] = [effect_call(), {'error': 'model_not_found', 'status': 404}]
    provider.script['model-b'] = [
        {'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
        {'message': {'role': 'assistant', 'content': 'short candidate finished'}},
        {'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
    ]
    cfg = lazyllm.globals['agentic_config']
    cfg.update(enable_tool_retrieval=True, user_id='test-user', conversation_id='short-context')
    raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='llm', url=provider.url,
                                  api_key='fake-fallback-key', stream=False)
    plan = agent_plan(tmp_path, a, [b], retrieval=True,
                      tools=[record_fallback_effect, optional_first, optional_second])
    projected_states = []
    old_pressure = 10**9

    def observe(request):
        workspace = lazyllm.locals['_lazyllm_agent'].get('workspace')
        if workspace and request['model'] == 'model-a':
            workspace['_history_projection_state']['next_pressure_tokens'] = old_pressure
            projected_states.append(workspace['_history_projection_state'])

    # Observe the producer's state on the same thread through request preparation.
    original_post = __import__('requests').post

    def post(url, **kwargs):
        observe(kwargs['json'])
        return original_post(url, **kwargs)

    monkeypatch.setattr('requests.post', post)
    summary_events = []
    original_log = summary_module._log_summary_event

    def log(event):
        summary_events.append(event)
        original_log(event)

    monkeypatch.setattr(summary_module, '_log_summary_event', log)
    EFFECT_RECEIPTS.clear()
    with compression_settings(), config.temp('agentic_workspace', str(tmp_path)):
        agent = AgentExecutor().create_agent(raw, plan)
        assert agent(plan.prompt.current_input, llm_chat_history=plan.history) == 'short candidate finished'
        state = agent._model_fallback_state
        expected_budget = build_context_budget(8192)
        assert state.current.candidate_id == b.candidate_id
        assert state.budget == expected_budget
        projection = projected_states[-1]
        assert projection['next_pressure_tokens'] < old_pressure
        controller = state.tool_retrieval
        loaded = controller.load(['optional_first'], [])
        assert loaded['status'] == 'ok'
        assert loaded['over_threshold'] is True
        assert loaded['used_tokens_estimate'] < build_context_budget(32768).effective_input_budget * 0.1
        with pytest.raises(Exception, match='loading threshold'):
            controller.load(['optional_second'], [])
        completed = copy.deepcopy(lazyllm.locals['_lazyllm_agent']['history'])
        completed.extend([{'role': 'assistant', 'content': 'NEW-ORDINARY-HISTORY ' + 'new facts ' * 2000},
                          {'role': 'user', 'content': 'new tail remains intact'}])
        agent._history_compactor(completed, 1, prefix={'system_prompt': agent._prompt},
                                 current_input='next call', runtime_state=projection)
    assert EFFECT_RECEIPTS == ['once']
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-a', 'model-b', 'model-b', 'model-b']
    assert 'NEW-ORDINARY-HISTORY' in str(provider.requests[-1]['messages'])
    assert not provider.requests[-1].get('tools')
    assert summary_events[-1].budget == expected_budget
    assert 'summary_text' in cfg['model_context']


def context_wrapper(provider, tmp_path, history, candidates, system, *, cancel_check=None, keep_recent=None):
    a = candidates[0]
    raw = lazyllm.OnlineChatModule(source='openai', model=a.model_id, type='llm', url=provider.url,
                                  api_key='fake-fallback-key', stream=False)
    projection = {}
    lazyllm.locals['_lazyllm_agent']['workspace'] = {
        'history': history, '_history_projection_state': projection,
    }
    projected, _ = make_history_compactor(max_input_tokens=a.max_input_tokens, workspace=str(tmp_path))(
        history, prefix={'system_prompt': system}, current_input='continue', runtime_state=projection,
    )
    options = AgentExecutionOptions(llm_config=a.llm_config, model_candidates=tuple(candidates),
                                    current_model_candidate_id=a.candidate_id, workspace=str(tmp_path),
                                    keep_full_turns=keep_recent)
    wrapped = FallbackLLM(create_fallback_state(raw, options), cancel_check=cancel_check).prompt(system)
    return wrapped, projected, projection


@pytest.mark.parametrize('too_large', ['fixed', 'summary_input'])
def test_unfit_fixed_or_summary_input_skips_candidate_without_http(provider, tmp_path, too_large):
    a = candidate(provider, 'model-a', window=131072)
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=None)
    fail(provider, 'model-a')
    provider.script['model-c'] = [{'message': {'role': 'assistant', 'content': 'full request accepted'}}]
    history = history_with_pairs()
    system = 'fixed constraints ' * (3000 if too_large == 'fixed' else 600)
    if too_large == 'summary_input':
        history[1]['content'] *= 5
    with compression_settings():
        wrapped, projected, _ = context_wrapper(provider, tmp_path, history, [a, b, c], system)
        assert wrapped('continue', llm_chat_history=projected) == 'full request accepted'
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-c']
    assert 'ORIGINAL-OLDER-FACT' in str(provider.requests[-1]['messages'])


def test_summary_invalid_key_excludes_same_credential_before_another_summary(provider, tmp_path):
    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    sibling = replace(candidate(provider, 'same-key', window=8192), provider_group_id=b.provider_group_id)
    c = candidate(provider, 'model-c', window=None)
    fail(provider, 'model-a')
    provider.script['model-b'] = [{'error': 'invalid_api_key', 'status': 401, 'type': 'authentication_error'}]
    provider.script['model-c'] = [{'message': {'role': 'assistant', 'content': 'other credential continued'}}]
    with compression_settings():
        wrapped, projected, _ = context_wrapper(provider, tmp_path, history_with_pairs(), [a, b, sibling, c],
                                                'Fixed task constraints. ' * 600)
        assert wrapped('continue', llm_chat_history=projected) == 'other credential continued'
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b', 'model-c']
    assert provider.requests[1]['stream'] is False
    assert not provider.requests[1].get('tools')
    assert (b.provider_group_id, b.credential_revision) in wrapped.state.failed_credentials


@pytest.mark.parametrize('write_fails', [False, True])
def test_frozen_spill_commit_and_io_error_never_retries(provider, tmp_path, monkeypatch, write_fails):
    import lazymind.chat.engine.agent_runtime.compactors as compactor_module

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=None)
    fail(provider, 'model-a')
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': 'spill candidate finished'}}]
    payload = 'FROZEN parsed resource\n' * 1000
    parsed = tmp_path / 'file-resources' / 'fr_sample' / 'parsed.md'
    parsed.parent.mkdir(parents=True)
    parsed.write_text(payload)
    visible = 'visible file excerpt\n' * 1800
    history = [
        {'role': 'user', 'content': 'keep the task'},
        {'role': 'assistant', 'content': '', 'tool_calls': [{
            'id': 'resource-call', 'function': {'name': 'read_file_resource', 'arguments': '{}'},
        }]},
        {'role': 'tool', 'name': 'read_file_resource', 'tool_call_id': 'resource-call', 'content': visible,
         TOOL_OBSERVATION_KEY: {'version': 1, 'ok': True, 'value': {'file_id': 'fr_sample', 'text': visible}}},
        {'role': 'user', 'content': 'keep the recent request'},
    ]

    def observe(request):
        if request['model'] == 'model-b':
            assert not (tmp_path / 'tool_spills').exists()
            parsed.write_text('CHANGED after preparation')

    provider.on_request = observe
    if write_fails:
        def fail_write(*_args, **_kwargs):
            raise OSError('local frozen spill write failed')
        monkeypatch.setattr(compactor_module, '_write_spill', fail_write)
    with compression_settings():
        wrapped, projected, projection = context_wrapper(provider, tmp_path, history, [a, b, c], 'fixed context')
        before = copy.deepcopy(projection)
        events = []
        if write_fails:
            with pytest.raises(Exception, match='local frozen spill write failed'):
                wrapped('continue', llm_chat_history=projected, stream={'_stream_sink': events.append})
            assert wrapped.state.current.candidate_id == a.candidate_id
            assert projection == before
            assert switches(events) == []
        else:
            assert wrapped('continue', llm_chat_history=projected,
                           stream={'_stream_sink': events.append}) == 'spill candidate finished'
            assert (tmp_path / 'tool_spills' / 'fr_sample.txt').read_text() == payload
            assert switches(events)[0]['context_compacted'] is True
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b']
    assert 'workspace://tool_spills/fr_sample.txt' in str(provider.requests[-1]['messages'])
    assert TOOL_OBSERVATION_KEY not in json.dumps(provider.requests[-1]['messages'])


def test_cancel_after_candidate_summary_stops_before_generation_and_other_candidates(provider, tmp_path):
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=None)
    fail(provider, 'model-a')
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': VALID_SUMMARY}}]
    cancelled = []
    provider.on_request = lambda request: cancelled.append(True) if request['model'] == 'model-b' else None
    with compression_settings():
        wrapped, projected, projection = context_wrapper(provider, tmp_path, history_with_pairs(), [a, b, c],
                                                          'Fixed task constraints. ' * 600,
                                                          cancel_check=lambda _: bool(cancelled))
        before = copy.deepcopy(projection)
        with pytest.raises(UserCancelledError):
            wrapped('continue', llm_chat_history=projected)
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b']
    assert projection == before
    assert wrapped.state.current.candidate_id == a.candidate_id


@pytest.mark.parametrize('local_failure', ['sidecar', 'formatter'])
def test_prepared_success_stops_on_local_commit_or_formatter_failure(provider, tmp_path, monkeypatch, local_failure):
    import lazymind.chat.engine.agent_runtime.summarizer as summary_module

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=None)
    fail(provider, 'model-a')
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
                                {'message': {'role': 'assistant', 'content': 'successful generation'}}]

    def local_error(*_args, **_kwargs):
        raise RuntimeError('local prepared result failed')

    if local_failure == 'sidecar':
        monkeypatch.setattr(summary_module, '_write_agent_data', local_error)
    with compression_settings():
        wrapped, projected, projection = context_wrapper(provider, tmp_path, history_with_pairs(), [a, b, c],
                                                          'Fixed task constraints. ' * 600)
        if local_failure == 'formatter':
            wrapped = wrapped.share(format=local_error)
        before = copy.deepcopy(projection)
        events = []
        with pytest.raises(Exception, match='local prepared result failed'):
            wrapped('continue', llm_chat_history=projected, stream={'_stream_sink': events.append})
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b', 'model-b']
    assert projection == before
    assert wrapped.state.current.candidate_id == a.candidate_id
    assert switches(events) == []


@pytest.mark.parametrize('length_at', ['summary', 'generation'])
def test_candidate_summary_length_is_unfit_but_generation_length_still_stops(provider, tmp_path, length_at):
    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=None)
    fail(provider, 'model-a')
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': VALID_SUMMARY},
                                 'finish': 'length' if length_at == 'summary' else 'stop'}]
    if length_at == 'generation':
        provider.script['model-b'].append({'message': {'role': 'assistant', 'content': 'unfinished main output'},
                                         'finish': 'length'})
    provider.script['model-c'] = [{'message': {'role': 'assistant', 'content': 'continued with full context'}}]
    with compression_settings():
        wrapped, projected, projection = context_wrapper(provider, tmp_path, history_with_pairs(), [a, b, c],
                                                          'Fixed task constraints. ' * 600)
        before = copy.deepcopy(projection)
        events = []
        if length_at == 'summary':
            assert wrapped('continue', llm_chat_history=projected,
                           stream={'_stream_sink': events.append}) == 'continued with full context'
            assert [request['model'] for request in provider.requests] == ['model-a', 'model-b', 'model-c']
            assert wrapped.state.current.candidate_id == c.candidate_id
        else:
            with pytest.raises(Exception):
                wrapped('continue', llm_chat_history=projected, stream={'_stream_sink': events.append})
            assert [request['model'] for request in provider.requests] == ['model-a', 'model-b', 'model-b']
            assert projection == before
            assert wrapped.state.current.candidate_id == a.candidate_id
            assert switches(events) == []


def test_short_order_precedes_unknown_and_exhaustion_is_finite(provider, tmp_path):
    a = candidate(provider, 'model-a')
    candidates = [a, candidate(provider, 'unknown-one', window=None),
                  candidate(provider, 'short-small', window=8192), candidate(provider, 'same-window'),
                  candidate(provider, 'long-window', window=65536),
                  candidate(provider, 'short-large', window=16384), candidate(provider, 'unknown-two', window=None)]
    for item in candidates:
        fail(provider, item.model_id)
    with compression_settings(), patch(
        'lazymind.chat.engine.agent_runtime.model_fallback.random.shuffle', side_effect=lambda items: items.reverse(),
    ) as shuffle:
        wrapped, projected, _ = context_wrapper(provider, tmp_path, [], candidates, 'fixed')
        events = []
        with pytest.raises(Exception):
            wrapped('continue', llm_chat_history=projected, stream={'_stream_sink': events.append})
    assert shuffle.call_count == 1
    assert [request['model'] for request in provider.requests] == [
        'model-a', 'same-window', 'long-window', 'short-large', 'short-small', 'unknown-two', 'unknown-one',
    ]
    assert [event['fallback_code'] for event in events if event.get('tag') == 'model_fallback_failed'] == [
        'model_fallback_exhausted',
    ]
    assert switches(events) == []


def test_explicit_empty_summary_state_does_not_read_or_initialize_global_config(provider):
    from lazymind.chat.engine.agent_runtime.active_context import collect_summary_runtime_state

    lazyllm.globals['agentic_config'] = {'task_goal': 'a different global goal'}
    assert collect_summary_runtime_state({})['task_goal'] == ''
    assert collect_summary_runtime_state()['task_goal'] == 'a different global goal'
    lazyllm.globals['agentic_config'] = None
    assert collect_summary_runtime_state({})['task_goal'] == ''
    assert lazyllm.globals.get('agentic_config') is None


def stage_one_boundary_history():
    history = history_with_pairs()
    history[1]['content'] = 'older fact ' * 1070
    history[3]['tool_calls'][0]['function']['name'] = 'terminal'
    history[4]['name'] = 'terminal'
    history[4]['content'] = 'successful tool output\n' * 1000
    return history


@pytest.mark.parametrize('conservative_image_estimate', [False, True])
def test_stage_one_fit_skips_optional_summary_and_required_invalid_summary_excludes_candidate(
    provider, tmp_path, conservative_image_estimate,
):
    from lazymind.chat.engine.agent_runtime.compactors import plan_tool_result_compaction
    from lazymind.chat.engine.agent_runtime.message_fields import estimate_message_tokens

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=None)
    fail(provider, 'model-a')
    # This is valid main-generation text, but lacks every required summary section.
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': 'B GENERATED'}}]
    provider.script['model-c'] = [{'message': {'role': 'assistant', 'content': 'C GENERATED'}}]
    history = stage_one_boundary_history()
    if conservative_image_estimate:
        image = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jFZkAAAAASUVORK5CYII='
        history[0]['content'] = [
            {'type': 'text', 'text': history[0]['content']},
            {'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,' + image}},
        ]
        for item in (a, b, c):
            item.llm_config['llm']['vision'] = True
    system = 'Fixed task constraints. ' * 600
    with compression_settings():
        wrapped, projected, projection = context_wrapper(
            provider, tmp_path, history, [a, b, c], system, keep_recent=0,
        )
        budget = build_context_budget(8192)
        view = wrapped.state.raw_llm.share(prompt=system)
        call_kwargs = {'max_tokens': budget.reserved_output_tokens}
        assert _request_tokens(view, 'continue', projected, None, call_kwargs) > budget.effective_input_budget
        plan = plan_tool_result_compaction('terminal', history[4]['content'], workspace=str(tmp_path))
        stage_one = copy.deepcopy(projected)
        stage_one[4]['content'] = plan.content
        complete_tokens = _request_tokens(view, 'continue', stage_one, None, call_kwargs)
        proxy_tokens = _request_tokens(view, 'continue', [], None, call_kwargs) + sum(
            estimate_message_tokens(message) for message in stage_one
        )
        assert budget.trigger_tokens < complete_tokens
        if conservative_image_estimate:
            # The complete request now uses the same image allowance as history.
            assert complete_tokens > budget.effective_input_budget
            assert proxy_tokens > budget.effective_input_budget
        else:
            assert complete_tokens <= budget.effective_input_budget
            assert proxy_tokens <= budget.effective_input_budget
        before = copy.deepcopy(projection)
        events = []
        result = wrapped('continue', llm_chat_history=projected, stream={'_stream_sink': events.append})
    if conservative_image_estimate:
        assert [request['model'] for request in provider.requests] == ['model-a', 'model-b', 'model-c']
        assert result == 'C GENERATED'
        assert provider.requests[1]['stream'] is False
        assert 'successful tool output' in str(provider.requests[-1]['messages'])
        assert not (tmp_path / 'tool_spills').exists()
        assert projection['entries'] == before['entries']
        assert switches(events)[0]['to']['candidate_id'] == c.candidate_id
    else:
        assert [request['model'] for request in provider.requests] == ['model-a', 'model-b']
        assert result == 'B GENERATED'
        assert provider.requests[1]['stream'] is True
        assert 'Fixed task constraints.' in str(provider.requests[1]['messages'])
        assert switches(events)[0]['to']['candidate_id'] == b.candidate_id
        assert switches(events)[0]['context_compacted'] is True


def test_normal_compactor_keeps_optional_summary_and_stage_one_fallback(provider, tmp_path):
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': 'B GENERATED'}}]
    raw = lazyllm.OnlineChatModule(source='openai', model='model-b', type='llm', url=provider.url,
                                  api_key='fake-fallback-key', stream=False)
    with compression_settings():
        prior, current = make_history_compactor(max_input_tokens=8192, keep_recent=0,
                                                workspace=str(tmp_path), llm=raw)(
            stage_one_boundary_history(), prefix={'system_prompt': 'Fixed task constraints. ' * 600},
            current_input='continue', runtime_state={},
        )
    assert [request['model'] for request in provider.requests] == ['model-b']
    assert provider.requests[0]['stream'] is False
    assert 'compact runtime summary' in str(provider.requests[0]['messages'])
    assert 'B GENERATED' not in str(prior + current)
    assert 'workspace://tool_spills/terminal_' in str(prior + current)
