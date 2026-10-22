"""Exercise fallback using the existing native SDK adapter, without SDK changes."""
import asyncio
import copy
import json
import socket
import threading
from types import SimpleNamespace
from unittest.mock import patch

import lazyllm
import pytest

from fallback_test_support import EFFECT_RECEIPTS, record_fallback_effect, candidate, wrapper, succeed, fail, switches
from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
)
from lazymind.chat.engine.agent_runtime.executor import _cfg
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext


@pytest.mark.parametrize(('current_source', 'next_source'), [('claude', 'openai'), ('openai', 'claude')])
def test_native_boundary_success_is_confirmed_without_sdk_lifecycle(provider, current_source, next_source):
    a = candidate(provider, 'model-a', source=current_source)
    b = candidate(provider, 'model-b', source=next_source)
    fail(provider, 'model-a')
    succeed(provider, 'model-b')
    events = []
    assert wrapper(a, [b])('start', stream={'_stream_sink': events.append}) == 'done:model-b'
    assert [item['model'] for item in provider.requests] == ['model-a', 'model-b']
    assert switches(events) == [{
        'switch_index': 1,
        'from': {'candidate_id': a.candidate_id, 'source': 'own', 'model_id': 'model-a', 'provider_group_id': 'model-a'},
        'to': {'candidate_id': b.candidate_id, 'source': 'own', 'model_id': 'model-b', 'provider_group_id': 'model-b'},
        'reason_code': 'not_found', 'from_max_input_tokens': 32768, 'to_max_input_tokens': 32768,
        'context_compacted': False,
    }]
    for item in events:
        event = item.get('runtime_event') or {}
        if event.get('type') == 'model_call_finished':
            assert not {'candidate_id', 'switch_index', 'fallback_code'} & event['data'].keys()


def test_native_healthy_reports_a_real_sdk_outcome_without_a_switch(provider):
    a = candidate(provider, 'native-a', source='claude')
    succeed(provider, 'native-a')
    events = []
    with patch('lazymind.chat.engine.agent_runtime.model_fallback._make_llm') as factory:
        assert wrapper(a, [candidate(provider, 'model-b')])(
            'start', stream={'_stream_sink': events.append},
        ) == 'done:native-a'
    assert not factory.called
    assert switches(events) == []
    runtime = [event['runtime_event'] for event in events if event.get('tag') == 'runtime_event']
    assert [event['type'] for event in runtime] == ['model_call_started', 'model_call_finished']
    assert runtime[-1]['data']['kind'] == 'finish'
    assert runtime[-1]['data']['finish'] == 'stop'


def test_native_swallowed_sink_error_stops_before_commit_or_another_candidate(provider):
    a = candidate(provider, 'model-a')
    b = candidate(provider, 'native-b', source='claude')
    fail(provider, 'model-a')
    succeed(provider, 'native-b')
    wrapped = wrapper(a, [b, candidate(provider, 'model-c')])
    events = []

    def sink(event):
        if event.get('tag') == 'text':
            raise RuntimeError('native sink failed locally')
        events.append(event)

    with pytest.raises(Exception, match='native sink failed locally'):
        wrapped('start', stream={'_stream_sink': sink})
    assert wrapped.state.current.candidate_id == a.candidate_id
    assert switches(events) == []
    assert [item['model'] for item in provider.requests] == ['model-a', 'native-b']


@pytest.mark.parametrize('local_failure', ['formatter', 'sink'])
def test_executor_local_failure_after_native_success_overrides_old_sdk_failure(provider, local_failure):
    from lazymind.chat.runtime_events import RunOutcome
    from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'native-b', source='claude')
    c = candidate(provider, 'model-c')
    fail(provider, 'model-a')
    succeed(provider, 'native-b')
    raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='llm',
                                  url=provider.url, api_key='fake-fallback-key', stream=False)
    options = AgentExecutionOptions(
        llm_config=a.llm_config, model_candidates=(a, b, c), current_model_candidate_id=a.candidate_id,
        enable_builtin_tools=False, skills=False, preload_all_tools=True,
        workspace_permission=WorkspaceContext(local_runtime=False),
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT, prompt=PromptBuilder.for_role(AgentRole.CHAT).input('start', source='user').build(),
        tools=[record_fallback_effect], execution_options=options,
    )
    collected = []
    executor = AgentExecutor()

    def broken_formatter(_response):
        raise ValueError('local formatting broke')

    def broken_sink(event):
        if event.get('tag') == 'text':
            raise RuntimeError('local native sink broke')
        lazyllm.FileSystemQueue().enqueue(json.dumps(event))

    async def drive(agent):
        async for item in executor.stream_agent(agent, plan):
            collected.append(item)

    with _cfg.temp('context_compression_enabled', False):
        agent = executor.create_agent(raw, plan)
        agent.build_agent()
        view = executor.runtime_llm(agent)
        if local_failure == 'formatter':
            view._formatter = broken_formatter
        else:
            view._stream = {'_stream_sink': broken_sink}
        with pytest.raises(Exception, match='local'):
            asyncio.run(drive(agent))
    events = [event for kind, event in collected if kind == 'event']
    translator = AgentEventFrameTranslator(query='start', run_id='local-failure')
    for event in events:
        translator.feed(event)
    terminal = translator.run.finish(outcome=RunOutcome.FAILED)['data']
    assert terminal['reason'] == 'runtime_failure'
    assert terminal['code'] == 'runtime_failure'
    assert [item['model'] for item in provider.requests] == ['model-a', 'native-b']
    assert switches(events) == []
    assert agent._model_fallback_state.current.candidate_id == a.candidate_id


def test_native_exposed_network_failure_recovers_without_parsing_error_text(provider):
    a = candidate(provider, 'native-a', source='claude')
    b = candidate(provider, 'model-b')
    succeed(provider, 'model-b')
    with socket.socket() as unavailable:
        unavailable.bind(('127.0.0.1', 0))
        unavailable.listen(1)
        unavailable.settimeout(2)

        def disconnect():
            with unavailable.accept()[0]:
                pass

        # Accept and close a real connection so every OS reports a transport failure.
        thread = threading.Thread(target=disconnect, daemon=True)
        thread.start()
        a.llm_config['llm']['base_url'] = f'http://127.0.0.1:{unavailable.getsockname()[1]}/v1/'
        events = []
        try:
            assert wrapper(a, [b])('start', stream={'_stream_sink': events.append}) == 'done:model-b'
        finally:
            thread.join(timeout=2)
    assert [item['model'] for item in provider.requests] == ['model-b']
    assert switches(events)[0]['reason_code'] == 'transport_error'


def test_native_exposed_half_stream_failure_rolls_back_then_recovers(provider):
    a = candidate(provider, 'native-a', source='claude')
    provider.script['native-a'] = [{'message': {'role': 'assistant', 'content': 'visible ' * 300},
                                  'disconnect': True}]
    succeed(provider, 'model-b')
    events = []
    assert wrapper(a, [candidate(provider, 'model-b')])(
        'start', stream={'_stream_sink': events.append},
    ) == 'done:model-b'
    assert any(item.get('tag') == 'text' and item.get('delta') for item in events)
    assert [item['model'] for item in provider.requests] == ['native-a', 'model-b']
    boundaries = [(event['model_output']['call_index'], event['model_output']['phase'])
                  for event in events if (event.get('model_output') or {}).get('phase')]
    assert boundaries == [(1, 'begin'), (1, 'rollback'), (2, 'begin'), (2, 'commit')]
    assert switches(events)[0]['to']['candidate_id'] == 'own:model-b'


def test_success_index_counts_commits_and_does_not_expose_failed_attempts_as_switches(provider):
    a, b, c = [candidate(provider, model) for model in ['model-a', 'model-b', 'model-c']]
    fail(provider, 'model-a')
    fail(provider, 'model-b')
    succeed(provider, 'model-c')
    events = []
    wrapped = wrapper(a, [b, c])
    assert wrapped('start', stream={'_stream_sink': events.append}) == 'done:model-c'
    assert len(switches(events)) == 1
    assert switches(events)[0]['switch_index'] == 1
    assert switches(events)[0]['from']['model_id'] == 'model-a'
    assert switches(events)[0]['to']['model_id'] == 'model-c'
    assert wrapped('again', stream={'_stream_sink': events.append}) == 'done:model-c'
    assert len(switches(events)) == 1


@pytest.mark.parametrize('original_window', [32768, None])
def test_bootstrap_orders_effective_budgets_and_preserves_original_switch_metadata(provider, original_window):
    b = candidate(provider, 'native-b', source='claude', window=65536)
    c = candidate(provider, 'model-c', window=32768)
    d = candidate(provider, 'model-d', window=131072)
    fail(provider, 'native-b')
    succeed(provider, 'model-c')
    succeed(provider, 'model-d')
    initial = {'from': {'candidate_id': 'own:deleted-a', 'source': 'own', 'model_id': 'deleted-a',
                        'provider_group_id': 'old-group'}, 'reason_code': 'model_config_unavailable',
               'from_max_input_tokens': original_window}
    events = []
    result = wrapper(b, [c, d], initial=initial)('start', stream={'_stream_sink': events.append})
    expected = 'model-d'
    assert result == 'done:' + expected
    assert [item['model'] for item in provider.requests] == ['native-b', expected]
    assert len(switches(events)) == 1
    assert switches(events)[0]['from'] == initial['from']
    assert switches(events)[0]['to']['model_id'] == expected
    assert switches(events)[0]['reason_code'] == 'model_config_unavailable'
    assert switches(events)[0]['from_max_input_tokens'] == original_window


@pytest.mark.parametrize('replacement_succeeds', [True, False])
def test_bootstrap_excludes_original_candidate_for_recovery_and_exhaustion(provider, replacement_succeeds):
    from lazymind.chat.runtime_events import RunOutcome
    from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'native-b', source='claude')
    c = candidate(provider, 'model-c')
    succeed(provider, 'model-a')  # Calling the excluded original would falsely succeed.
    fail(provider, 'native-b')
    (succeed if replacement_succeeds else fail)(provider, 'model-c')
    initial = {'from': {'candidate_id': a.candidate_id, 'source': a.source, 'model_id': a.model_id,
                        'provider_group_id': a.provider_group_id}, 'reason_code': 'model_config_unavailable',
               'from_max_input_tokens': a.max_input_tokens}
    options = AgentExecutionOptions(
        llm_config=b.llm_config, model_candidates=(b, a, c), current_model_candidate_id=b.candidate_id,
        initial_model_switch=initial, enable_builtin_tools=False, skills=False, preload_all_tools=True,
        workspace_permission=WorkspaceContext(local_runtime=False),
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT, prompt=PromptBuilder.for_role(AgentRole.CHAT).input('start', source='user').build(),
        tools=[record_fallback_effect], execution_options=options,
    )
    raw = lazyllm.OnlineChatModule(source='claude', model=b.model_id, type='llm',
                                  url=provider.url, api_key='fake-fallback-key', stream=False)
    executor = AgentExecutor()
    collected = []

    async def drive():
        async for item in executor.stream(raw, plan):
            collected.append(item)

    error = None
    with _cfg.temp('context_compression_enabled', False), patch.object(
        executor, 'create_agent', wraps=executor.create_agent,
    ) as create_agent:
        try:
            asyncio.run(drive())
        except Exception as exc:
            error = exc
    assert [item['model'] for item in provider.requests] == ['native-b', 'model-c']
    assert create_agent.call_count == 1
    translator = AgentEventFrameTranslator(query='start', run_id='bootstrap-run')
    frames = []
    for kind, event in collected:
        if kind == 'event':
            frames.extend(translator.feed(event))
    frames.append(translator.finish_run(
        outcome=RunOutcome.SUCCEEDED if error is None else RunOutcome.FAILED,
    ))
    events = [frame['runtime_event'] for frame in frames if frame.get('runtime_event')]
    terminals = [event for event in events if event['type'] == 'run_finished']
    assert len(terminals) == 1
    assert all(event['run_id'] == 'bootstrap-run' for event in events)
    successes = [event['data'] for event in events if event['type'] == 'model_switched']
    if replacement_succeeds:
        assert error is None
        assert ('final', 'done:model-c') in collected
        assert len(successes) == 1
        assert successes[0]['from'] == initial['from']
        assert successes[0]['to']['candidate_id'] == c.candidate_id
        assert successes[0]['switch_index'] == 1
        assert successes[0]['reason_code'] == 'model_config_unavailable'
        assert terminals[0]['data']['status'] == 'completed'
    else:
        assert error is not None
        assert not [item for item in collected if item[0] == 'final']
        assert successes == []
        assert terminals[0]['data']['status'] == 'failed'
        assert terminals[0]['data']['code'] == 'model_fallback_exhausted'
        assert [event['fallback_code'] for kind, event in collected if kind == 'event'
                and event.get('tag') == 'model_fallback_failed'] == ['model_fallback_exhausted']


def test_bootstrap_native_success_needs_no_sdk_started_or_finished(provider):
    b = candidate(provider, 'native-b', source='claude')
    a = candidate(provider, 'deleted-a')
    succeed(provider, 'native-b')
    succeed(provider, 'deleted-a')
    initial = {'from': {'candidate_id': 'own:deleted-a', 'source': 'own', 'model_id': 'deleted-a',
                        'provider_group_id': 'old-group'}, 'reason_code': 'model_config_unavailable',
               'from_max_input_tokens': 32768}
    wrapped = wrapper(b, [a], initial=initial)
    events = []
    assert wrapped('start', stream={'_stream_sink': events.append}) == 'done:native-b'
    assert len(switches(events)) == 1
    assert switches(events)[0]['from'] == initial['from']
    assert switches(events)[0]['to']['model_id'] == 'native-b'
    assert [item['runtime_event']['type'] for item in events
            if (item.get('runtime_event') or {}).get('type') in {'model_call_started', 'model_call_finished'}] == [
                'model_call_started', 'model_call_finished']
    assert wrapped('again', stream={'_stream_sink': events.append}) == 'done:native-b'
    assert len(switches(events)) == 1
    fail(provider, 'native-b')
    assert wrapper(b, [a])('new execution without bootstrap') == 'done:deleted-a'
    assert [item['model'] for item in provider.requests] == ['native-b', 'native-b', 'native-b', 'deleted-a']


@pytest.mark.parametrize('window', [65536, None])
@pytest.mark.parametrize('winner_source', ['claude', 'openai'])
def test_service_metrics_follow_winner_and_keep_mixed_real_usage(provider, monkeypatch, tmp_path, window, winner_source):
    from lazyllm.module import ModuleBase
    from lazyllm.tools.agent.functionCall import FunctionCall
    from lazymind.chat.service import chat_service
    from lazymind.chat.service.chat_request import ChatRequest
    from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

    a = candidate(provider, 'model-a')
    b = candidate(provider, 'native-b', source=winner_source, window=window)
    provider.script['model-a'] = [
        {'message': {'role': 'assistant', 'content': '', 'tool_calls': [{
            'id': 'effect-call', 'index': 0, 'type': 'function',
            'function': {'name': 'record_fallback_effect', 'arguments': '{"value":"once"}'},
        }]}, 'finish': 'tool_calls', 'usage': {'prompt_tokens': 7, 'completion_tokens': 2}},
        {'error': 'model_not_found', 'status': 404, 'type': 'invalid_request_error'},
    ]
    succeed(provider, 'native-b')
    provider.script['native-b'][0]['usage'] = {'prompt_tokens': 11, 'completion_tokens': 7}
    raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='llm',
                                  url=provider.url, api_key='fake-fallback-key', stream=False)
    monkeypatch.setattr(chat_service, 'AutoModel', lambda **kwargs: raw)
    monkeypatch.setattr(chat_service, 'apply_tool_supersession', lambda tools: [record_fallback_effect])
    monkeypatch.setattr(chat_service, 'chat_agent_workspace', lambda *_args: str(tmp_path))
    monkeypatch.setattr(chat_service, 'get_episode_store', lambda: SimpleNamespace(
        search=lambda *_a, **_kw: [], list_recent=lambda *_a, **_kw: [], render=lambda _episode: '',
    ))
    captured = []
    function_call_usages = []
    original_clear = ModuleBase._clear_usage

    def clear_usage(module):
        if isinstance(module, FunctionCall):
            function_call_usages.append(copy.deepcopy(lazyllm.globals['usage'].get(module._module_id)))
        return original_clear(module)

    monkeypatch.setattr(ModuleBase, '_clear_usage', clear_usage)
    original_finish = AgentEventFrameTranslator.finish_run

    def finish(translator, **kwargs):
        captured.append(copy.deepcopy(kwargs.get('usage')))
        return original_finish(translator, **kwargs)

    monkeypatch.setattr(AgentEventFrameTranslator, 'finish_run', finish)
    request = ChatRequest(
        message={'query': 'record once then finish'},
        conversation={'session_id': 'native-metrics-' + winner_source + str(window), 'run_id': 'metrics-run'},
        runtime={'llm_config': a.llm_config, 'model_fallback_current_id': a.candidate_id,
                 'model_fallback_candidates': [vars(a), vars(b)]},
        personalization={'use_memory': False},
        agent={'available_skills': [], 'enable_subagent': False,
               'disabled_tools': [config.name for config in chat_service.DEFAULT_TOOLS]},
        workflow={'enable_workflow': False},
    )
    EFFECT_RECEIPTS.clear()

    async def drive():
        response = await chat_service._handle_chat_impl(request)
        return [part async for part in response.body_iterator]

    with chat_service._cfg.temp('context_compression_enabled', False):
        chunks = asyncio.run(drive())
    payloads = [json.loads(part.decode() if isinstance(part, bytes) else part)['data'] for part in chunks]
    terminal = next(part for part in payloads
                    if (part.get('runtime_event') or {}).get('type') == 'run_finished')
    assert terminal['runtime_event']['data']['status'] == 'completed'
    assert EFFECT_RECEIPTS == ['once']
    assert [item['model'] for item in provider.requests] == ['model-a', 'model-a', 'native-b']
    metrics = terminal['performance_metrics']
    assert (metrics['model'], metrics.get('max_input_tokens'), metrics.get('output_tokens')) == (
        'native-b', window, 9,  # A's real 2 plus B's reported 7.
    )
    assert metrics['model_steps'] == 3
    assert 'model_ms' in metrics
    if metrics['model_ms'] > 0:
        assert metrics['tok_s'] == pytest.approx(9 / (metrics['model_ms'] / 1000))
    else:
        assert 'tok_s' not in metrics
    assert captured[-1]
    assert function_call_usages and all(function_call_usages)
