"""Normal context management and immutable fallback input regressions."""
import copy


import json


from dataclasses import replace


from contextlib import ExitStack


import lazyllm


import pytest


from fallback_test_support import VALID_SUMMARY


from fallback_test_support import EFFECT_RECEIPTS, record_fallback_effect


from fallback_test_support import candidate, fail, succeed, switches, wrapper


from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
)


from lazymind.chat.engine.agent_runtime.budget import build_context_budget


from lazymind.chat.engine.agent_runtime.pruner import make_history_compactor


from lazymind.chat.engine.agent_runtime.summary_range import validate_tool_pairing


from lazymind.config import config


from lazymind.chat.engine.tools.workspace_context import WorkspaceContext


from lazyllm.tools.agent import fc_register


def test_unfit_candidate_is_skipped_without_summary_or_input_mutation(provider, tmp_path):
    a = candidate(provider, 'model-a', window=32768)
    b = candidate(provider, 'model-b', window=16384)
    c = candidate(provider, 'model-c', window=8192)
    b.llm_config['llm']['static_params'] = {'fixed_vendor_parameter': 'large ' * 100000}
    fail(provider, 'model-a')
    succeed(provider, 'model-c')
    model = wrapper(a, [b, c])
    history = [{'role': 'user', 'content': 'Keep these facts unchanged.'}]
    before = copy.deepcopy(history)
    events = []
    with compression_settings(), config.temp('model_fallback_max_candidates', 2):
        assert model('continue', llm_chat_history=history, stream={'_stream_sink': events.append}) == 'done:model-c'
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-c']
    assert provider.requests[0]['messages'] == provider.requests[1]['messages']
    assert history == before
    assert model.state.failed_candidates == {a.candidate_id}
    assert switches(events)[0]['context_compacted'] is False
    assert not list(tmp_path.rglob('tool_spills/*'))


def test_unfit_candidates_never_start_a_recovery_summary(provider, monkeypatch):
    from lazymind.chat.engine.agent_runtime import summarizer
    from lazymind.chat.engine.agent_runtime.model_calls import ContextCapacityError

    a, b = candidate(provider, 'model-a', window=256), candidate(provider, 'model-b', window=256)

    def unexpected(*args, **kwargs):
        pytest.fail('fallback started a context summary')

    monkeypatch.setattr(summarizer, 'prepare_summary_compression', unexpected)
    model = wrapper(a, [b])
    with compression_settings(), pytest.raises(ContextCapacityError) as failure:
        model('original request ' * 1000)
    assert failure.value.fallback_code == 'model_context_not_fit'
    assert provider.requests == []
    assert model.state.failed_candidates == set()


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


@pytest.mark.parametrize('has_image', [False, True])
@pytest.mark.parametrize('fallback_enabled', [False, True])
def test_precompression_snapshot_preserves_structured_agent_input(
    provider, tmp_path, has_image, fallback_enabled,
):
    from fallback_test_support import succeed

    a = candidate(provider, 'model-a')
    a.llm_config['llm']['vision'] = True
    succeed(provider, 'model-a')
    content = [{'type': 'text', 'text': 'supported wrapped input'}]
    if has_image:
        content.append({'type': 'image_url', 'image_url': {'url': 'https://images.example/fixture.png'}})
    query = {'input': [{'role': 'user', 'content': content}]}
    before = copy.deepcopy(query)
    options = AgentExecutionOptions(
        llm_config=a.llm_config, max_input_tokens=a.max_input_tokens,
        model_candidates=(a,) if fallback_enabled else (),
        current_model_candidate_id=a.candidate_id if fallback_enabled else '',
        workspace=str(tmp_path), enable_builtin_tools=False, skills=False, keep_full_turns=1,
        preload_all_tools=True, workspace_permission=WorkspaceContext(local_runtime=False),
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('continue', source='user').build(),
        tools=[record_fallback_effect], history=[], execution_options=options,
    )
    raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='vlm',
                                  url=provider.url, api_key='fixture-key', stream=False)
    with compression_settings(), config.temp('agentic_workspace', str(tmp_path)):
        agent = AgentExecutor().create_agent(raw, plan)
        assert agent(query, llm_chat_history=[]) == 'done:model-a'
    assert len(provider.requests) == 1
    assert provider.requests[0]['messages'][-1] == before['input'][0]
    assert query == before
    assert EFFECT_RECEIPTS == []


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


def optional_first(topic: str) -> str:
    return topic


def optional_second(topic: str) -> str:
    return topic


for optional in (optional_first, optional_second):
    optional.__doc__ = ('Optional contextual capability. ' * 200) + '\n\nArgs:\n    topic: Requested topic.\n'
    fc_register(host_file='NONE')(optional)


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


@pytest.mark.parametrize('another_tool_round', [False, True])
@pytest.mark.parametrize('compactor_kind', ['default', 'workflow'])
def test_real_agent_keeps_completed_effect_and_tool_input_after_candidate_generation_failure(
    provider, tmp_path, another_tool_round, compactor_kind,
):
    a, b, c = candidate(provider, 'model-a'), candidate(provider, 'model-b', window=32768), \
        candidate(provider, 'model-c', window=32768)
    provider.script['model-a'] = [effect_call(), {'error': 'model_not_found', 'status': 404}]
    provider.script['model-b'] = [{'error': 'model_not_found', 'status': 404}]
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
        ['model-a', 'model-a', 'model-b', 'model-c'] + (['model-c'] if another_tool_round else []))
    assert provider.requests[1]['messages'] == provider.requests[2]['messages'] == provider.requests[3]['messages']
    assert provider.requests[-1]['tools'] == provider.requests[1]['tools']
    assert validate_tool_pairing(provider.requests[-1]['messages'])[0]
    assert provider.requests[3]['messages'][-2]['tool_call_id'] == 'actual-effect'
    assert provider.requests[-1]['messages'][-1]['content'] == 'CURRENT-RUNTIME-NOTICE'
    assert provider.requests[0]['messages'][-1]['content'] == 'CURRENT-RUNTIME-NOTICE'
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


@pytest.mark.parametrize('text_backups,cancelled', [
    (0, False), (3, False), (3, True),
])
def test_ordinary_image_summary_has_its_own_timeout_and_does_not_spend_soft_budget(
    provider, tmp_path, monkeypatch, text_backups, cancelled,
):
    import base64
    import threading
    import time
    from lazyllm.components.formatter.formatterbase import encode_query_with_filepaths
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError

    a = candidate(provider, 'model-a', window=8192)
    a.llm_config['llm']['vision'] = True
    alternatives = [candidate(provider, f'text-{index}', window=8192) for index in range(text_backups)]
    stop = threading.Event()

    def respond(request):
        summary = not request.get('stream')
        time.sleep(0.7 if summary else 0.25)
        if cancelled and summary:
            stop.set()
        return {'message': {'role': 'assistant', 'content': VALID_SUMMARY if summary else 'visual completed'}}

    provider.on_request = respond
    image_data = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jFZkAAAAASUVORK5CYII='
    history = history_with_pairs()
    image = tmp_path / 'input.png'
    image.write_bytes(base64.b64decode(image_data))
    query = encode_query_with_filepaths('continue', [str(image)])
    before = copy.deepcopy(history)
    options = AgentExecutionOptions(
        llm_config=a.llm_config, max_input_tokens=a.max_input_tokens,
        model_candidates=(a, *alternatives), current_model_candidate_id=a.candidate_id,
        workspace=str(tmp_path), enable_builtin_tools=False, skills=False, keep_full_turns=1,
        required_tool_names=('record_fallback_effect',), preload_all_tools=True,
        extra_stop_condition=lambda _: stop.is_set(), workspace_permission=WorkspaceContext(local_runtime=False),
    )
    prompt = PromptBuilder.for_role(AgentRole.CHAT).system(
        'fixed', 'Fixed constraints', 'Fixed task constraints. ' * 600, source='test',
    ).input('continue', source='user').build()
    plan = AgentRunPlan(role=AgentRole.CHAT, prompt=prompt, tools=[record_fallback_effect],
                       history=history, execution_options=options)
    raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='vlm',
                                  url=provider.url, api_key='fixture-key', stream=False)
    with compression_settings(), config.temp('model_fallback_timeout_seconds', 0.1), \
            config.temp('agentic_workspace', str(tmp_path)):
        agent = AgentExecutor().create_agent(raw, plan)
        started = time.monotonic()
        if cancelled:
            with pytest.raises(UserCancelledError):
                agent(query, llm_chat_history=plan.history)
        else:
            assert agent(query, llm_chat_history=plan.history) == 'visual completed'
        elapsed = time.monotonic() - started
    assert [request['model'] for request in provider.requests] == ['model-a'] * (1 if cancelled else 2)
    assert [request['stream'] for request in provider.requests] == ([False] if cancelled else [False, True])
    if not cancelled:
        assert image_data in json.dumps(provider.requests[-1]['messages'])
    assert elapsed < 1.4
    assert history == before
    assert EFFECT_RECEIPTS == []


@pytest.mark.parametrize('winner_source', ['openai', 'claude'])
def test_short_success_uses_new_budget_for_normal_summary_and_tool_loading(
    provider, tmp_path, monkeypatch, winner_source,
):
    import lazymind.chat.engine.agent_runtime.summarizer as summary_module

    a, b = candidate(provider, 'model-a'), candidate(provider, 'model-b', source=winner_source, window=8192)
    provider.script['model-a'] = [effect_call(), {'error': 'model_not_found', 'status': 404}]
    provider.script['model-b'] = [
        {'message': {'role': 'assistant', 'content': 'short candidate finished'}},
        {'message': {'role': 'assistant', 'content': VALID_SUMMARY}},
    ]
    cfg = lazyllm.globals['agentic_config']
    cfg.update(enable_tool_retrieval=True, user_id='test-user', conversation_id='short-context')
    raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='llm', url=provider.url,
                                  api_key='fake-fallback-key', stream=False)
    plan = agent_plan(tmp_path, a, [b], retrieval=True,
                      tools=[record_fallback_effect, optional_first, optional_second])
    plan = replace(plan, history=[])
    projected_states = []

    def observe(request):
        workspace = lazyllm.locals['_lazyllm_agent'].get('workspace')
        if workspace and request['model'] == 'model-a':
            projected_states.append(workspace['_history_projection_state'])

    from lazyllm.module.llms.onlinemodule.base import ModelCallControl
    validate = ModelCallControl.validate_request

    def validate_request(control, request):
        observe(request.payload)
        return validate(control, request)

    monkeypatch.setattr(ModelCallControl, 'validate_request', validate_request)
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
        assert not any('Ship summary compression.' in str(request['messages']) for request in provider.requests)
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
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-a', 'model-b', 'model-b']
    assert 'NEW-ORDINARY-HISTORY' in str(provider.requests[-1]['messages'])
    assert not provider.requests[-1].get('tools')
    assert summary_events[-1].budget == expected_budget
    assert 'summary_text' in cfg['model_context']
