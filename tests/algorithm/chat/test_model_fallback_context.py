"""HTTP regressions for one shared model history during recovery."""
import copy
import base64
import json
from dataclasses import replace
from unittest.mock import patch
from contextlib import ExitStack

import lazyllm
import pytest
from lazyllm.module.module import ModuleExecutionError
from lazyllm.module.llms.onlinemodule.base import ModelCallError

from fallback_test_support import VALID_SUMMARY
from fallback_test_support import EFFECT_RECEIPTS, record_fallback_effect
from fallback_test_support import candidate, fail, switches, request_tokens_for as _request_tokens
from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
)
from lazymind.chat.engine.agent_runtime.budget import build_context_budget
from lazymind.chat.engine.agent_runtime.model_fallback import FallbackLLM, create_fallback_state
from lazymind.chat.engine.agent_runtime.pruner import make_history_compactor
from lazymind.chat.engine.agent_runtime.summary_prompt import build_summary_user_prompt, get_summary_system_prompt
from lazymind.chat.engine.agent_runtime.summary_range import select_summary_range, validate_tool_pairing
from lazymind.config import config
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
from lazyllm.tools.agent import fc_register
from lazyllm.tools.agent.base import TOOL_OBSERVATION_KEY


@pytest.fixture
def provider(fallback_provider):
    lazyllm.globals['agentic_config'] = {'model_context': {'summary_text': 'committed-before-recovery'}}
    return fallback_provider


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
def test_summary_preserves_images_tools_and_original_execution_records(provider, tmp_path, b_failure):
    a = candidate(provider, 'model-a', window=32768)
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=8192)
    for item in (a, b, c):
        item.llm_config['llm']['vision'] = True
    fail(provider, 'model-a')
    provider.script['model-b'] = [
        {'message': {'role': 'assistant', 'content': 'invalid summary' if b_failure == 'invalid_summary'
                     else VALID_SUMMARY}},
        {'error': 'model_not_found', 'status': 404},
    ]
    provider.script['model-c'] = [{'message': {'role': 'assistant', 'content': 'continued with shared summary'}}]
    history = history_with_pairs()
    original = copy.deepcopy(history)
    workspace = {'history': history}
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
        wrapped = FallbackLLM(create_fallback_state(raw, options)).prompt(system)
        compact = wrapped.bind_history_compactor(make_history_compactor(
            max_input_tokens=32768, workspace=str(tmp_path), retain_message_metadata=True))
        projected, _ = compact(history, prefix={'system_prompt': system, 'tool_definitions': tools},
                               current_input='continue', runtime_state={})
        before_projection = copy.deepcopy(wrapped.state.model_history)
        before_context = copy.deepcopy(lazyllm.globals['agentic_config'])
        active_config = lazyllm.globals['agentic_config']
        observed_before_c = []

        def observe(request):
            if request['model'] == 'model-c':
                observed_before_c.append((copy.deepcopy(wrapped.state.model_history), copy.deepcopy(active_config),
                                          list(tmp_path.rglob('tool_spills/*'))))

        provider.on_request = observe
        budget = build_context_budget(8192)
        assert _request_tokens(raw.share(prompt=system), current_input, projected, [str(image)],
                               {'tools': tools}) > budget.effective_input_budget
        selected = select_summary_range(history, effective_input_budget=budget.effective_input_budget,
                                        keep_recent_ratio=0.1, min_recent_user_turns=1)
        summary_view = raw.share(prompt=get_summary_system_prompt(), stream=False)
        assert _request_tokens(summary_view, build_summary_user_prompt(selected.summary_messages), None, None,
                               {'max_tokens': 1024}) <= budget.effective_input_budget
        events = []
        if b_failure == 'invalid_summary':
            with pytest.raises(ModuleExecutionError, match='configured input budget'):
                wrapped(current_input, llm_chat_history=projected, lazyllm_files=[str(image)], tools=tools,
                        stream={'_stream_sink': events.append})
        else:
            assert wrapped(current_input, llm_chat_history=projected, lazyllm_files=[str(image)], tools=tools,
                           stream={'_stream_sink': events.append}) == 'continued with shared summary'
    assert [request['model'] for request in provider.requests] == (
        ['model-a', 'model-b'] if b_failure == 'invalid_summary'
        else ['model-a', 'model-b', 'model-b', 'model-c'])
    b_summary = provider.requests[1]
    assert b_summary['stream'] is False
    assert not b_summary.get('tools')
    assert b_summary['max_tokens'] <= budget.reserved_output_tokens
    assert 'ORIGINAL-OLDER-FACT' in str(b_summary['messages'])
    if b_failure == 'generation_failure':
        c_request = provider.requests[-1]
        assert c_request['tools'] == tools
        assert c_request['messages'] == provider.requests[-2]['messages']
        assert json.dumps(VALID_SUMMARY)[1:-1] in json.dumps(c_request['messages'])
        assert 'effect-receipt: once' in str(c_request['messages'])
        assert image_data in str(c_request['messages'])
        assert 'CURRENT-MULTIMODAL-INPUT' in str(c_request['messages'])
        assert validate_tool_pairing(c_request['messages'])[0]
        assert observed_before_c[0][0] != before_projection
        assert observed_before_c[0][1:] == (before_context, [])
        assert switches(events)[0]['to']['candidate_id'] == c.candidate_id
        assert switches(events)[0]['context_compacted'] is True
    else:
        assert wrapped.state.model_history == before_projection
        assert observed_before_c == []
        assert switches(events) == []
    assert history == original
    assert lazyllm.globals['agentic_config'] == before_context
    assert not list(tmp_path.rglob('tool_spills/*'))


def effect_call(value='once', identifier='actual-effect'):
    return {'message': {'role': 'assistant', 'content': '', 'tool_calls': [{
        'id': identifier, 'index': 0, 'type': 'function',
        'function': {'name': 'record_fallback_effect', 'arguments': json.dumps({'value': value})},
    }]}, 'finish': 'tool_calls'}


@pytest.mark.parametrize('fallback', [False, True])
@pytest.mark.parametrize('style', ['legacy_tuple', 'legacy_list', 'combined', 'structured'])
def test_history_callback_protocol_matches_the_normal_agent(provider, tmp_path, fallback, style):
    def legacy_tuple(history, keep):
        return list(history), []

    def legacy_list(history, keep):
        return list(history)

    def combined(history, keep, current_round_messages=None, **kwargs):
        return [*history, *(current_round_messages or [])]

    def structured(history, keep, *, current_round_messages=None):
        return list(history), list(current_round_messages or [])

    compactor = {'legacy_tuple': legacy_tuple, 'legacy_list': legacy_list,
                 'combined': combined, 'structured': structured}[style]
    a = candidate(provider, 'model-a')
    provider.script['model-a'] = ([] if style == 'legacy_tuple' else [effect_call()]) + [
        {'message': {'role': 'assistant', 'content': 'protocol compatible'}}]
    plan = agent_plan(tmp_path, a, [])
    plan = replace(plan, history=[])
    plan.execution_options = replace(
        plan.execution_options, history_compactor=compactor,
        model_candidates=(a,) if fallback else (), current_model_candidate_id=a.candidate_id if fallback else '')
    raw = lazyllm.OnlineChatModule(
        source='openai', model='model-a', type='llm', url=provider.url, api_key='fixture-key', stream=False)
    with compression_settings(), config.temp('agentic_workspace', str(tmp_path)):
        agent = AgentExecutor().create_agent(raw, plan)
        assert agent(plan.prompt.current_input) == 'protocol compatible'
    assert len(provider.requests) == (1 if style == 'legacy_tuple' else 2)
    assert EFFECT_RECEIPTS == ([] if style == 'legacy_tuple' else ['once'])
    messages = provider.requests[-1]['messages']
    assert [message['tool_call_id'] for message in messages if message['role'] == 'tool'] \
        == ([] if style == 'legacy_tuple' else ['actual-effect'])
    assert validate_tool_pairing(messages) == (True, 'ok')


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


@pytest.mark.parametrize('mode', ['normal', 'healthy_fallback', 'switch_after_tool'])
@pytest.mark.parametrize('historical', [False, True])
def test_first_exposure_spill_preserves_normal_behavior_and_old_history(provider, tmp_path, mode, historical):
    fallback = mode != 'normal'
    switched = mode == 'switch_after_tool'
    payload = 'UNCOMPACTED_RESULT ' * 2000
    calls = []

    def large_receipt(value: str) -> str:
        """Return one large tool result.

        Args:
            value: Requested receipt.
        """
        calls.append(value)
        return payload

    a = candidate(provider, 'model-a', window=131072)
    tool_call = {'id': 'large-1', 'index': 0, 'type': 'function',
                 'function': {'name': 'large_receipt', 'arguments': '{"value":"once"}'}}
    provider.script['model-a'] = ([] if historical else [
        {'message': {'role': 'assistant', 'content': '', 'tool_calls': [tool_call]}, 'finish': 'tool_calls'},
        effect_call('after-large'),
    ]) + [{'message': {'role': 'assistant', 'content': 'healthy completed'}}]
    if switched:
        provider.script['model-b'] = provider.script['model-a'][(0 if historical else 1):]
        provider.script['model-a'] = provider.script['model-a'][:(0 if historical else 1)] + [
            {'error': 'model_not_found', 'status': 404}]
    original = [
        {'role': 'user', 'content': 'Original task'},
        {'role': 'assistant', 'content': '', 'tool_calls': [tool_call]},
        {'role': 'tool', 'name': 'large_receipt', 'tool_call_id': 'large-1', 'content': payload},
        {'role': 'user', 'content': 'Continue'},
    ] if historical else []
    plan = replace(agent_plan(tmp_path, a, [], tools=[large_receipt, record_fallback_effect]), history=original)
    candidates = (a, candidate(provider, 'model-b', window=131072)) if switched else (a,)
    plan.execution_options = replace(
        plan.execution_options, model_candidates=candidates if fallback else (),
        current_model_candidate_id=a.candidate_id if fallback else '')
    raw = lazyllm.OnlineChatModule(
        source='openai', model='model-a', type='llm', url=provider.url, api_key='fixture-key', stream=False)
    with compression_settings(), config.temp('context_summary_compression_enabled', False), \
            config.temp('context_compression_spill_bytes', 4096), config.temp('agentic_workspace', str(tmp_path)):
        agent = AgentExecutor().create_agent(raw, plan)
        assert agent(plan.prompt.current_input, llm_chat_history=original) == 'healthy completed'
    assert len(provider.requests) == (1 if historical else 3) + int(switched)
    if switched:
        assert agent._model_fallback_state.current.model_id == 'model-b'
    assert calls == ([] if historical else ['once'])
    assert EFFECT_RECEIPTS == ([] if historical else ['after-large'])
    sent = [message['content'] for request in provider.requests for message in request['messages']
            if message.get('tool_call_id') == 'large-1']
    files = list(tmp_path.rglob('tool_spills/*'))
    if historical:
        assert sent == [payload] * (1 + int(switched))
        assert files == []
    else:
        assert len(sent) == 2 + int(switched) and all(content == sent[0] for content in sent)
        assert len(sent[0]) < 4096 and 'workspace://tool_spills/' in sent[0]
        assert len(files) == 1 and payload in files[0].read_text()
        raw_history = lazyllm.locals['_lazyllm_agent']['history']
        assert sum(message.get('tool_call_id') == 'large-1' for message in raw_history) == 1
        assert payload in str(raw_history)
    assert all(validate_tool_pairing(request['messages'])[0] for request in provider.requests)


@pytest.mark.parametrize('another_tool_round', [False, True])
@pytest.mark.parametrize('compactor_kind', ['default', 'workflow'])
def test_real_agent_keeps_completed_effect_and_tool_input_after_short_generation_failure(
    provider, tmp_path, another_tool_round, compactor_kind,
):
    a, b, c = candidate(provider, 'model-a'), candidate(provider, 'model-b', window=8192), \
        candidate(provider, 'model-c', window=8192)
    provider.script['model-a'] = [effect_call(), {'error': 'model_not_found', 'status': 404}]
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
                                {'error': 'model_not_found', 'status': 404}]
    provider.script['model-c'] = ([effect_call('after-switch', 'new-effect')] if another_tool_round else []) + [
        {'message': {'role': 'assistant', 'content': 'continued after one effect'}}]
    raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='llm', url=provider.url,
                                  api_key='fake-fallback-key', stream=False)
    plan = agent_plan(tmp_path, a, [b, c])
    plan.execution_options = replace(plan.execution_options,
                                     model_context_provider=lambda: 'CURRENT-RUNTIME-NOTICE')
    if compactor_kind == 'workflow':
        from lazymind.chat.engine.agent_runtime.workflow_compactor import make_workflow_history_compactor
        plan.execution_options = replace(
            plan.execution_options,
            history_compactor=make_workflow_history_compactor(max_input_tokens=a.max_input_tokens))
    EFFECT_RECEIPTS.clear()
    with compression_settings(), config.temp('agentic_workspace', str(tmp_path)):
        agent = AgentExecutor().create_agent(raw, plan)
        assert agent(plan.prompt.current_input, llm_chat_history=plan.history) == 'continued after one effect'
    assert EFFECT_RECEIPTS == (['once', 'after-switch'] if another_tool_round else ['once'])
    assert [request['model'] for request in provider.requests] == (
        ['model-a', 'model-a', 'model-b', 'model-b', 'model-c'] + (['model-c'] if another_tool_round else []))
    assert provider.requests[4]['messages'] == provider.requests[3]['messages']
    assert 'ORIGINAL-OLDER-FACT' not in str(provider.requests[-1]['messages'])
    assert provider.requests[-1]['tools'] == provider.requests[1]['tools']
    assert validate_tool_pairing(provider.requests[-1]['messages'])[0]
    assert provider.requests[4]['messages'][-2]['tool_call_id'] == 'actual-effect'
    assert provider.requests[-1]['messages'][-1]['content'] == 'CURRENT-RUNTIME-NOTICE'
    assert provider.requests[0]['messages'][-1]['content'] == 'CURRENT-RUNTIME-NOTICE'
    assert isinstance(agent._model_fallback_state.model_history, list)
    if another_tool_round:
        assert 'effect-receipt: after-switch' in str(provider.requests[-1]['messages'])
        raw_history = lazyllm.locals['_lazyllm_agent']['history']
        assert 'ORIGINAL-OLDER-FACT' in str(raw_history)
        assert sum(message.get('tool_call_id') == 'actual-effect' for message in raw_history) == 1
        assert sum(message.get('tool_call_id') == 'new-effect' for message in raw_history) == 1
        provider.script['model-c'].append({'message': {'role': 'assistant', 'content': 'new execution completed'}})
        assert agent('new task', llm_chat_history=[{'role': 'user', 'content': 'NEW-EXECUTION-CONTEXT'}]) \
            == 'new execution completed'
        assert 'NEW-EXECUTION-CONTEXT' in str(provider.requests[-1]['messages'])
        assert 'Ship summary compression.' not in str(provider.requests[-1]['messages'])
        assert 'effect-receipt' not in str(provider.requests[-1]['messages'])
        assert EFFECT_RECEIPTS == ['once', 'after-switch']


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

    def observe(request):
        workspace = lazyllm.locals['_lazyllm_agent'].get('workspace')
        if workspace and request['model'] == 'model-a':
            projected_states.append(workspace['_history_projection_state'])

    # Observe the producer's state on the same thread through request preparation.
    from lazyllm.module.llms.onlinemodule.base import PreparedModelRequest
    original_execute = PreparedModelRequest.execute

    def execute(request, **kwargs):
        observe(request.payload)
        return original_execute(request, **kwargs)

    monkeypatch.setattr(PreparedModelRequest, 'execute', execute)
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
        assert any('Ship summary compression.' in str(message) for message in state.model_history)
        controller = agent._tools_manager.retrieval
        assert controller.threshold_tokens == int(build_context_budget(32768).effective_input_budget * 0.1)
        loaded = controller.load(['optional_first'], [])
        assert loaded['status'] == 'ok'
        assert loaded['over_threshold'] is False
        assert loaded['used_tokens_estimate'] < build_context_budget(32768).effective_input_budget * 0.1
        with pytest.raises(Exception, match='固定上下文'):
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
    lazyllm.locals['_lazyllm_agent']['workspace'] = {'history': history}
    options = AgentExecutionOptions(llm_config=a.llm_config, model_candidates=tuple(candidates),
                                    current_model_candidate_id=a.candidate_id, workspace=str(tmp_path),
                                    keep_full_turns=keep_recent)
    wrapped = FallbackLLM(create_fallback_state(raw, options), cancel_check=cancel_check).prompt(system)
    compact = wrapped.bind_history_compactor(make_history_compactor(
        max_input_tokens=a.max_input_tokens, workspace=str(tmp_path), retain_message_metadata=True))
    projected, _ = compact(history, prefix={'system_prompt': system}, current_input='continue', runtime_state={})
    return wrapped, projected, wrapped.state


@pytest.mark.parametrize('last_fails', [False, True])
def test_one_summary_is_shared_after_a_candidates_generation_fails(provider, tmp_path, last_fails):
    a = candidate(provider, 'model-a')
    b, c = [candidate(provider, name, window=8192) for name in ('model-b', 'model-c')]
    fail(provider, 'model-a')
    provider.script['model-b'] = [
        {'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
        {'error': 'model_not_found', 'status': 404},
    ]
    provider.script['model-c'] = ([{'error': 'model_not_found', 'status': 404}] if last_fails else [
        {'message': {'role': 'assistant', 'content': 'shared history completed'}}])
    history = history_with_pairs()
    original = copy.deepcopy(history)
    with compression_settings():
        wrapped, projected, history_state = context_wrapper(
            provider, tmp_path, history, [a, b, c], 'Fixed task constraints. ' * 600)
        events = []
        if last_fails:
            with pytest.raises(ModelCallError):
                wrapped('continue', llm_chat_history=projected, stream={'_stream_sink': events.append})
        else:
            assert wrapped('continue', llm_chat_history=projected,
                           stream={'_stream_sink': events.append}) == 'shared history completed'
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b', 'model-b', 'model-c']
    assert provider.requests[2]['messages'] == provider.requests[3]['messages']
    assert 'ORIGINAL-OLDER-FACT' not in str(provider.requests[-1]['messages'])
    assert 'effect-receipt: once' in str(provider.requests[-1]['messages'])
    assert validate_tool_pairing(provider.requests[-1]['messages'])[0]
    assert any('Ship summary compression.' in str(message) for message in history_state.model_history)
    assert history == original
    if last_fails:
        assert switches(events) == []
        assert wrapped.state.current.candidate_id == a.candidate_id
    else:
        assert switches(events)[0]['context_compacted'] is True
    assert not list(tmp_path.rglob('tool_spills/*'))


def test_invalid_summary_is_not_repeated_for_the_next_candidate(provider, tmp_path):
    a = candidate(provider, 'model-a')
    b, c = [candidate(provider, name, window=8192) for name in ('model-b', 'model-c')]
    fail(provider, 'model-a')
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': 'invalid summary'}}]
    provider.script['model-c'] = [
        {'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
        {'message': {'role': 'assistant', 'content': 'should not be called'}},
    ]
    with compression_settings():
        wrapped, projected, history_state = context_wrapper(
            provider, tmp_path, history_with_pairs(), [a, b, c], 'Fixed task constraints. ' * 600)
        before = copy.deepcopy(history_state.model_history)
        with pytest.raises(ModuleExecutionError, match='configured input budget'):
            wrapped('continue', llm_chat_history=projected)
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b']
    assert history_state.model_history == before


def test_larger_remaining_candidate_is_tried_without_a_summary(provider, tmp_path):
    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=None)
    fail(provider, 'model-a')
    provider.script['model-b'] = [
        {'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
        {'message': {'role': 'assistant', 'content': 'unnecessary compression'}},
    ]
    provider.script['model-c'] = [{'message': {'role': 'assistant', 'content': 'original history completed'}}]
    with compression_settings():
        wrapped, projected, history_state = context_wrapper(
            provider, tmp_path, history_with_pairs(), [a, b, c], 'Fixed task constraints. ' * 600)
        before = copy.deepcopy(history_state.model_history)
        assert wrapped('continue', llm_chat_history=projected) == 'original history completed'
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-c']
    assert 'ORIGINAL-OLDER-FACT' in str(provider.requests[-1]['messages'])
    assert history_state.model_history == before


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


def test_summary_invalid_key_excludes_same_credential_without_another_summary(provider, tmp_path):
    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    sibling = replace(candidate(provider, 'same-key', window=8192), provider_group_id=b.provider_group_id)
    c = candidate(provider, 'model-c', window=8192)
    fail(provider, 'model-a')
    provider.script['model-b'] = [{'error': 'invalid_api_key', 'status': 401, 'type': 'authentication_error'}]
    provider.script['model-c'] = [{'message': {'role': 'assistant', 'content': 'other credential continued'}}]
    with compression_settings():
        wrapped, projected, _ = context_wrapper(provider, tmp_path, history_with_pairs(), [a, b, sibling, c],
                                                'Fixed task constraints. ' * 600)
        with pytest.raises(ModuleExecutionError, match='configured input budget'):
            wrapped('continue', llm_chat_history=projected)
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b']
    assert provider.requests[1]['stream'] is False
    assert not provider.requests[1].get('tools')
    assert (b.provider_group_id, b.credential_revision) in wrapped.state.failed_credentials


def test_fallback_never_plans_or_writes_tool_spills(provider, tmp_path, monkeypatch):
    from lazymind.chat.engine.agent_runtime import compactors

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    fail(provider, 'model-a')
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

    original = copy.deepcopy(history)
    with compression_settings():
        wrapped, projected, history_state = context_wrapper(provider, tmp_path, history, [a, b], 'fixed context')
        before = copy.deepcopy(history_state.model_history)

        def unexpected_spill(*args, **kwargs):
            pytest.fail('fallback must not plan or write a tool spill')

        monkeypatch.setattr(compactors, 'plan_tool_result_compaction', unexpected_spill)
        monkeypatch.setattr(compactors, '_write_spill', unexpected_spill)
        with pytest.raises(ModuleExecutionError, match='summary request does not fit'):
            wrapped('continue', llm_chat_history=projected)
    assert [request['model'] for request in provider.requests] == ['model-a']
    assert history_state.model_history == before
    assert history == original
    assert parsed.read_text() == payload
    assert not (tmp_path / 'tool_spills').exists()


def test_cancel_after_candidate_summary_stops_before_generation_and_other_candidates(provider, tmp_path):
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=8192)
    fail(provider, 'model-a')
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': VALID_SUMMARY}}]
    cancelled = []
    provider.on_request = lambda request: cancelled.append(True) if request['model'] == 'model-b' else None
    with compression_settings():
        wrapped, projected, history_state = context_wrapper(
            provider, tmp_path, history_with_pairs(), [a, b, c], 'Fixed task constraints. ' * 600,
            cancel_check=lambda _: bool(cancelled))
        before = copy.deepcopy(history_state.model_history)
        with pytest.raises(UserCancelledError):
            wrapped('continue', llm_chat_history=projected)
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b']
    assert history_state.model_history == before
    assert wrapped.state.current.candidate_id == a.candidate_id


@pytest.mark.parametrize('local_failure', ['sidecar', 'formatter', 'switch_event'])
def test_shared_summary_does_not_depend_on_sidecar_and_survives_local_failure(
    provider, tmp_path, monkeypatch, local_failure,
):
    import lazymind.chat.engine.agent_runtime.summarizer as summary_module

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=8192)
    fail(provider, 'model-a')
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
                                {'message': {'role': 'assistant', 'content': 'successful generation'}}]

    def local_error(*_args, **_kwargs):
        raise RuntimeError('local prepared result failed')

    if local_failure == 'sidecar':
        monkeypatch.setattr(summary_module, '_write_agent_data', local_error)
    with compression_settings():
        wrapped, projected, history_state = context_wrapper(
            provider, tmp_path, history_with_pairs(), [a, b, c], 'Fixed task constraints. ' * 600)
        if local_failure == 'formatter':
            wrapped = wrapped.share(format=local_error)
        before = copy.deepcopy(history_state.model_history)
        events = []

        def sink(event):
            if local_failure == 'switch_event' and (event.get('runtime_event') or {}).get('type') == 'model_switched':
                local_error()
            events.append(event)

        if local_failure == 'sidecar':
            assert wrapped('continue', llm_chat_history=projected,
                           stream={'_stream_sink': sink}) == 'successful generation'
        else:
            with pytest.raises(Exception, match='local prepared result failed'):
                wrapped('continue', llm_chat_history=projected, stream={'_stream_sink': sink})
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b', 'model-b']
    assert history_state.model_history != before
    assert wrapped.state.current.candidate_id == (a.candidate_id if local_failure == 'formatter' else b.candidate_id)
    assert lazyllm.globals['agentic_config']['model_context'] == {'summary_text': 'committed-before-recovery'}
    assert len(switches(events)) == (1 if local_failure == 'sidecar' else 0)


@pytest.mark.parametrize('length_at', ['summary', 'generation'])
def test_candidate_summary_length_is_unfit_but_generation_length_still_stops(provider, tmp_path, length_at):
    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    c = candidate(provider, 'model-c', window=8192)
    fail(provider, 'model-a')
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': VALID_SUMMARY},
                                 'finish': 'length' if length_at == 'summary' else 'stop'}]
    if length_at == 'generation':
        provider.script['model-b'].append({'message': {'role': 'assistant', 'content': 'unfinished main output'},
                                         'finish': 'length'})
    provider.script['model-c'] = [{'message': {'role': 'assistant', 'content': 'continued with full context'}}]
    with compression_settings():
        wrapped, projected, history_state = context_wrapper(
            provider, tmp_path, history_with_pairs(), [a, b, c], 'Fixed task constraints. ' * 600)
        before = copy.deepcopy(history_state.model_history)
        events = []
        with pytest.raises(ModuleExecutionError if length_at == 'summary' else ModelCallError):
            wrapped('continue', llm_chat_history=projected, stream={'_stream_sink': events.append})
        assert [request['model'] for request in provider.requests] == (
            ['model-a', 'model-b'] if length_at == 'summary' else ['model-a', 'model-b', 'model-b'])
        assert (history_state.model_history == before) is (length_at == 'summary')
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
    with compression_settings(), config.temp('model_fallback_max_candidates', 7), patch(
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


def test_summary_and_generation_share_deadline_without_consuming_another_candidate(provider, tmp_path, monkeypatch):
    from lazyllm.module.llms.onlinemodule.base import PreparedModelRequest

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'model-b', window=8192)
    fail(provider, 'model-a')
    provider.script['model-b'] = [
        {'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
        {'message': {'role': 'assistant', 'content': 'continued'}},
    ]
    observed = []
    execute = PreparedModelRequest.execute

    def observe(request, *, control=None):
        observed.append((request.payload['model'], control.deadline))
        return execute(request, control=control)

    monkeypatch.setattr(PreparedModelRequest, 'execute', observe)
    with compression_settings(), config.temp('model_fallback_max_candidates', 2):
        model, projected, _ = context_wrapper(
            provider, tmp_path, history_with_pairs(), [a, b], 'Fixed task constraints. ' * 600)
        assert model('continue', llm_chat_history=projected) == 'continued'
    assert [model for model, _ in observed] == ['model-a', 'model-b', 'model-b']
    assert len({deadline for _, deadline in observed}) == 1
