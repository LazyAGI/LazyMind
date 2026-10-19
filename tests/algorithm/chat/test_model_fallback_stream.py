"""Host output boundaries around real, observable HTTP/SSE failures."""
import asyncio
import copy
import json
from unittest.mock import patch

import lazyllm
import pytest

from test_model_fallback import ScriptedProvider, EFFECT_RECEIPTS, record_fallback_effect
from test_model_fallback_native import candidate
from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
)
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
from lazymind.chat.runtime_events import RunOutcome
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
from lazymind.config import config


@pytest.fixture
def provider():
    server = ScriptedProvider()
    old_agent = lazyllm.locals.get('_lazyllm_agent')
    old_config = lazyllm.globals.get('agentic_config')
    lazyllm.locals['_lazyllm_agent'] = {}
    EFFECT_RECEIPTS.clear()
    yield server
    lazyllm.locals['_lazyllm_agent'] = old_agent
    lazyllm.globals['agentic_config'] = old_config
    server.close()


def sse_frame(delta, *, finish=None, usage=None, padding=''):
    result = {'id': 'controlled', 'choices': [{'index': 0, 'delta': delta, 'finish_reason': finish}]}
    if usage is not None:
        result['usage'] = usage
    if padding:
        result['padding'] = padding
    return result


def broken_output():
    return {'message': {'role': 'assistant', 'content': ''}, 'disconnect': True, 'frames': [
        sse_frame({'reasoning_content': 'BAD-THOUGHT ' * 60},
                  usage={'prompt_tokens': 10, 'completion_tokens': 3}),
        sse_frame({'content': 'BAD-BODY [[1.1]]\n:::export{title="bad draft" filename="bad.md"}\nBAD-DRAFT\n<thi'}),
        sse_frame({}, padding='x' * 4096),
    ]}


def make_agent(provider, translator, *, alternatives=None):
    a, b = candidate(provider, 'model-a'), candidate(provider, 'model-b')
    options = AgentExecutionOptions(
        llm_config=a.llm_config, model_candidates=(a, *(alternatives if alternatives is not None else [b])),
        current_model_candidate_id=a.candidate_id, enable_builtin_tools=False, skills=False,
        preload_all_tools=True, workspace_permission=WorkspaceContext(local_runtime=False),
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('perform one effect', source='user').build(),
        tools=[record_fallback_effect], execution_options=options,
    )
    raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='llm', url=provider.url,
                                  api_key='fake-fallback-key', stream=False)
    lazyllm.globals['agentic_config'] = {'citation_state': translator.citation_state}
    executor = AgentExecutor()
    return executor, executor.create_agent(raw, plan), plan


async def drive(executor, agent, plan, translator, frames, events):
    final = None
    async for kind, payload in executor.stream_agent(agent, plan):
        if kind == 'event':
            events.append(payload)
            frames.extend(translator.feed(payload))
        else:
            final = payload
    frames.extend(translator.finish(final))
    return final


def test_real_agent_rolls_back_only_failed_call_then_continues_after_one_tool(provider):
    provider.script['model-a'] = [
        {'message': {'role': 'assistant', 'content': ''}, 'frames': [
            sse_frame({'content': 'COMMITTED-COMMENTARY\n'}),
            sse_frame({'tool_calls': [{'id': 'effect-call', 'index': 0, 'type': 'function',
                                     'function': {'name': 'record_fallback_effect',
                                                  'arguments': '{"value":"once"}'}}]},
                      finish='tool_calls', usage={'prompt_tokens': 7, 'completion_tokens': 2}),
        ]},
        broken_output(),
    ]
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': 'B CLEAN ANSWER'},
                                 'usage': {'prompt_tokens': 11, 'completion_tokens': 7}}]
    translator = AgentEventFrameTranslator(query='test', run_id='same-run', enable_exports=True)
    translator.citation_state['_citation_sources']['1.1'] = {'index': '1.1', 'title': 'Real prior source'}
    frames, events = [], []
    error = None
    with config.temp('context_compression_enabled', False):
        executor, agent, plan = make_agent(provider, translator)
        try:
            final = asyncio.run(drive(executor, agent, plan, translator, frames, events))
        except Exception as exc:
            error = exc
            final = None
    assert any('BAD-BODY' in str(frame.get('text') or '') for frame in frames)
    assert error is None
    assert final == 'B CLEAN ANSWER'
    assert EFFECT_RECEIPTS == ['once']
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-a', 'model-b']
    assert provider.requests[-1]['messages'] == provider.requests[1]['messages']
    boundaries = [(frame['model_output']['call_index'], frame['model_output'].get('phase'))
                  for frame in frames if (frame.get('model_output') or {}).get('phase')]
    assert boundaries == [(1, 'begin'), (1, 'commit'), (2, 'begin'), (2, 'rollback'), (3, 'begin'), (3, 'commit')]
    assert all(frame['model_output']['run_id'] == 'same-run' for frame in frames if frame.get('model_output'))
    bad_frames = [frame for frame in frames if 'BAD-' in str(frame.get('text') or frame.get('think') or '')]
    assert all(frame['model_output']['call_index'] == 2 for frame in bad_frames)
    assert any('COMMITTED-COMMENTARY' in str(frame.get('text') or '') for frame in frames)
    assert any('effect-receipt: once' in str(frame.get('text') or '') and not frame.get('model_output')
               for frame in frames)
    switch = next(frame for frame in frames if (frame.get('runtime_event') or {}).get('type') == 'model_switched')
    assert switch['model_output'] == {'run_id': 'same-run', 'call_index': 3, 'phase': 'commit'}
    assert translator.citation_state['_citation_sources']['1.1']['title'] == 'Real prior source'
    assert translator.citation_state['_cited_source_indices'] == []
    assert translator.citation_plugin.streamed_indices == ()
    assert translator.text_scanner.buf == ''
    assert 'BAD-' not in ''.join(translator.export_stream.raw)
    terminal = translator.finish_run(outcome=RunOutcome.SUCCEEDED, usage=agent._model_fallback_state.usage_snapshot,
                                      prefer_usage_snapshot=True)
    assert terminal['runtime_event']['data']['status'] == 'completed'
    assert terminal['performance_metrics']['model_steps'] == 3
    assert terminal['performance_metrics']['output_tokens'] == 12
    assert 'export_snapshot' not in terminal


def test_old_raw_sink_keeps_its_identity_and_late_sdk_observation_is_only_accounted(provider, monkeypatch):
    import requests

    provider.script['model-a'] = [broken_output()]
    provider.script['model-b'] = [{'message': {'role': 'assistant', 'content': ''}}]
    translator = AgentEventFrameTranslator(query='test', run_id='late-run', enable_exports=True)
    frames, events, old_sinks, delayed = [], [], [], []
    with config.temp('context_compression_enabled', False):
        executor, agent, plan = make_agent(provider, translator)
        agent.build_agent()
        raw = agent._model_fallback_state.raw_llm
        original_share = raw.share

        def share(**kwargs):
            old_sinks.append(kwargs['stream']['_stream_sink'])
            return original_share(**kwargs)

        monkeypatch.setattr(raw, 'share', share)

        def deliver(event):
            value = event.get('runtime_event') or {}
            if value.get('type') == 'model_call_finished' and value['data'].get('kind') == 'failure' and not delayed:
                delayed.append(copy.deepcopy(event))
                return
            lazyllm.FileSystemQueue().enqueue(json.dumps(event))

        executor.runtime_llm(agent)._stream = {'_stream_sink': deliver}
        original_post = requests.post

        def post(url, **kwargs):
            if kwargs['json']['model'] == 'model-b':
                old_sinks[0]({'tag': 'text', 'delta': 'LATE-A-TEXT'})
                old_sinks[0]({'tag': 'think', 'delta': 'LATE-A-THINK'})
                old_sinks[0](delayed[0])
            return original_post(url, **kwargs)

        monkeypatch.setattr(requests, 'post', post)
        assert asyncio.run(drive(executor, agent, plan, translator, frames, events)) == ''
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b']
    late = [event for event in events if str(event.get('delta') or '').startswith('LATE-A-')]
    assert len(late) == 2
    assert all(event['model_output']['call_index'] == 1 for event in late)
    assert not any('LATE-A-' in str(frame.get('text') or frame.get('think') or '') for frame in frames)
    assert translator.metrics.model_steps == 2
    assert translator.run.semantic_output is False


def boundary(index, phase=None, *, run_id='run'):
    result = {'run_id': run_id, 'call_index': index}
    if phase is not None:
        result['phase'] = phase
    return result


def test_private_checkpoint_restores_parser_and_ignores_stale_or_duplicate_frames():
    translator = AgentEventFrameTranslator(query='test', run_id='run', enable_exports=True)
    citation = translator.citation_state
    citation['_citation_sources']['1.1'] = {'index': '1.1', 'title': 'real source'}
    translator._pending_previews['real-tool'] = 'committed tool preview'
    export = translator.export_stream
    before_export = copy.deepcopy(vars(export))
    with patch.object(export, 'finish', side_effect=AssertionError('must not finish live parser')):
        translator.feed({'tag': 'model_output', 'model_output': boundary(1, 'begin')})
        translator.feed({'tag': 'text', 'delta': 'BAD [[1.1]]\n:::export{title="bad" filename="bad.md"}\nbody\n<thi',
                         'model_output': boundary(1)})
        translator.feed({'tag': 'think', 'delta': 'bad reasoning', 'model_output': boundary(1)})
        # These facts belong to tools/tasks, independently of model draft output.
        task = translator.feed({'tag': 'task_created', 'task_id': 'real-task'})[0]['task_created']
        assert task['task_id'] == 'real-task'
        assert translator.feed({'tag': 'artifact_created', 'artifact_key': 'real-file'})[0]['artifact_created']
        citation['_citation_sources']['2.1'] = {'index': '2.1', 'title': 'another real source'}
        translator.feed({'tag': 'runtime_event', 'runtime_event': {
            'schema_version': 1, 'event_id': 'sdk-failed', 'type': 'model_call_finished',
            'data': {'model_call_id': 'sdk-call', 'attempt_count': 1, 'kind': 'failure',
                     'has_semantic_output': True, 'failure': {'origin': 'transport', 'code': 'transport_error'}},
        }})
        restored = translator.feed({'tag': 'model_output', 'model_output': boundary(1, 'rollback')})
    assert restored[0]['model_output'] == boundary(1, 'rollback')
    assert translator.citation_state is citation
    assert set(citation['_citation_sources']) == {'1.1', '2.1'}
    assert citation['_cited_source_indices'] == []
    assert translator.citation_plugin.streamed_indices == ()
    assert translator.text_scanner.buf == ''
    assert translator.text_scanner.state == 'BODY'
    assert translator.export_stream is export
    assert vars(export) == before_export
    assert translator._pending_previews == {'real-tool': 'committed tool preview'}
    assert translator.run.semantic_output is False
    assert translator.metrics.model_steps == 1
    assert translator.feed({'tag': 'model_output', 'model_output': boundary(1, 'rollback')}) == []
    translator.feed({'tag': 'model_output', 'model_output': boundary(2, 'begin')})
    assert translator.feed({'tag': 'model_output', 'model_output': boundary(2, 'begin')}) == []
    current_terminal = translator.run.last_model_terminal
    translator.feed({'tag': 'runtime_event', 'model_output': boundary(1), 'runtime_event': {
        'schema_version': 1, 'event_id': 'late-sdk-failed', 'type': 'model_call_finished',
        'data': {'model_call_id': 'old-sdk-call', 'attempt_count': 1, 'kind': 'failure',
                 'has_semantic_output': True, 'failure': {'origin': 'transport', 'code': 'transport_error'}},
    }})
    assert translator.run.last_model_terminal is current_terminal
    assert translator.run.semantic_output is False
    assert translator.metrics.model_steps == 2
    for field in ('text', 'think'):
        assert translator.feed({'tag': field, 'delta': 'STALE [[1.1]]', 'model_output': boundary(1)}) == []
    assert translator.feed({'tag': 'model_output', 'model_output': boundary(1, 'begin')}) == []
    assert translator.feed({'tag': 'model_output', 'model_output': boundary(10, 'begin', run_id='old-run')}) == []
    translator.feed({'tag': 'text', 'delta': 'clean', 'model_output': boundary(2)})
    translator.feed({'tag': 'model_output', 'model_output': boundary(2, 'commit')})
    assert translator.feed({'tag': 'text', 'delta': 'LATE', 'model_output': boundary(2)}) == []


def test_exhausted_half_streams_restore_empty_output_and_finish_once(provider):
    provider.script['model-a'] = [broken_output()]
    provider.script['model-b'] = [broken_output()]
    translator = AgentEventFrameTranslator(query='test', run_id='exhausted', enable_exports=True)
    frames, events = [], []
    with config.temp('context_compression_enabled', False):
        executor, agent, plan = make_agent(provider, translator)
        with pytest.raises(Exception):
            asyncio.run(drive(executor, agent, plan, translator, frames, events))
    terminal = translator.finish_run(outcome=RunOutcome.FAILED, usage=agent._model_fallback_state.usage_snapshot,
                                     prefer_usage_snapshot=True)
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b']
    assert EFFECT_RECEIPTS == []
    assert [(event['model_output']['call_index'], event['model_output']['phase'])
            for event in events if event.get('tag') == 'model_output'] == [
                (1, 'begin'), (1, 'rollback'), (2, 'begin'), (2, 'rollback')]
    assert [event['fallback_code'] for event in events if event.get('tag') == 'model_fallback_failed'] == [
        'model_fallback_exhausted']
    assert not [frame for frame in frames if (frame.get('runtime_event') or {}).get('type') == 'model_switched']
    assert terminal['runtime_event']['data']['code'] == 'model_fallback_exhausted'
    assert terminal['runtime_event']['data']['partial_output'] is False
    assert terminal['performance_metrics']['model_steps'] == 2
    assert terminal['performance_metrics']['output_tokens'] == 6
    assert 'export_snapshot' not in terminal
    with pytest.raises(RuntimeError, match='already has a terminal'):
        translator.finish_run(outcome=RunOutcome.FAILED)


def test_failed_partial_tool_call_never_executes_before_replacement_tool(provider):
    provider.script['model-a'] = [{'message': {'role': 'assistant', 'content': ''}, 'disconnect': True, 'frames': [
        sse_frame({'tool_calls': [{'id': 'partial', 'index': 0, 'type': 'function',
                                 'function': {'name': 'record_fallback_effect', 'arguments': '{"value":"BAD'}}]}),
        sse_frame({}, padding='x' * 4096),
    ]}]
    provider.script['model-b'] = [
        {'message': {'role': 'assistant', 'content': '', 'tool_calls': [{
            'id': 'real-tool', 'index': 0, 'type': 'function',
            'function': {'name': 'record_fallback_effect', 'arguments': '{"value":"once"}'},
        }]}, 'finish': 'tool_calls'},
        {'message': {'role': 'assistant', 'content': 'DONE AFTER ONE TOOL'}},
    ]
    translator = AgentEventFrameTranslator(query='test', run_id='partial-tool', enable_exports=True)
    frames, events = [], []
    with config.temp('context_compression_enabled', False):
        executor, agent, plan = make_agent(provider, translator)
        assert asyncio.run(drive(executor, agent, plan, translator, frames, events)) == 'DONE AFTER ONE TOOL'
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b', 'model-b']
    assert EFFECT_RECEIPTS == ['once']
    assert provider.requests[0]['messages'] == provider.requests[1]['messages']
    assert len([event for event in events if event.get('tag') == 'tool_calls']) == 1
    tool_position = next(index for index, event in enumerate(events) if event.get('tag') == 'tool_calls')
    commit_position = next(index for index, event in enumerate(events)
                           if event.get('model_output') == {'call_index': 2, 'phase': 'commit'})
    assert commit_position < tool_position
    assert not events[tool_position].get('model_output')
    assert not translator._pending_previews


@pytest.mark.parametrize('fault', ['formatter', 'commit', 'text_sink', 'switch_sink', 'rollback_sink'])
def test_local_failures_rollback_without_next_candidate_or_tool(provider, monkeypatch, fault):
    from lazymind.chat.engine.agent_runtime.models import FallbackRunState

    step = {'message': {'role': 'assistant', 'content': ''}, 'frames': [
        sse_frame({'reasoning_content': 'UNCOMMITTED THOUGHT'}),
        sse_frame({'content': 'UNCOMMITTED\n:::export{title="bad" filename="bad.md"}\nBAD DRAFT\n'}),
        sse_frame({'tool_calls': [{'id': 'must-not-run', 'index': 0, 'type': 'function',
                                 'function': {'name': 'record_fallback_effect',
                                              'arguments': '{"value":"BAD"}'}}]}, finish='tool_calls'),
    ]}
    provider.script['model-a'] = [step]
    expected_models = ['model-a']
    if fault == 'switch_sink':
        provider.script['model-a'] = [{'error': 'model_not_found', 'status': 404}]
        provider.script['model-b'] = [step]
        expected_models.append('model-b')
    translator = AgentEventFrameTranslator(query='test', run_id='local-' + fault, enable_exports=True)
    frames, events = [], []
    with config.temp('context_compression_enabled', False):
        executor, agent, plan = make_agent(provider, translator, alternatives=[
            candidate(provider, 'model-b'), candidate(provider, 'model-c')])
        agent.build_agent()
        view = executor.runtime_llm(agent)

        def broken_formatter(_response):
            raise ValueError('local formatter broke')

        if fault in {'formatter', 'rollback_sink'}:
            view._formatter = broken_formatter
        elif fault == 'commit':
            original_setattr = FallbackRunState.__setattr__

            def set_state(self, name, value):
                if self is agent._model_fallback_state and name == 'current':
                    raise RuntimeError('local commit broke')
                return original_setattr(self, name, value)

            monkeypatch.setattr(FallbackRunState, '__setattr__', set_state)

        def deliver(event):
            phase = (event.get('model_output') or {}).get('phase')
            if ((fault == 'text_sink' and event.get('tag') == 'text')
                    or (fault == 'switch_sink' and phase == 'commit')
                    or (fault == 'rollback_sink' and phase == 'rollback')):
                raise RuntimeError('local sink broke')
            lazyllm.FileSystemQueue().enqueue(json.dumps(event))

        view._stream = {'_stream_sink': deliver}
        with pytest.raises(Exception, match='local'):
            asyncio.run(drive(executor, agent, plan, translator, frames, events))
    assert [request['model'] for request in provider.requests] == expected_models
    assert EFFECT_RECEIPTS == []
    assert not any(event.get('tag') == 'tool_calls' for event in events)
    assert not any((frame.get('runtime_event') or {}).get('type') == 'model_switched' for frame in frames)
    terminal = translator.finish_run(outcome=RunOutcome.FAILED)
    assert terminal['runtime_event']['data']['code'] == 'runtime_failure'
    assert terminal['runtime_event']['data']['partial_output'] is False
    assert 'export_snapshot' not in terminal
    assert translator.text_scanner.buf == ''
    assert not translator.export_stream.raw
    if fault == 'rollback_sink':
        assert terminal['model_output'] == {'run_id': 'local-rollback_sink', 'call_index': 1, 'phase': 'rollback'}
        assert not terminal['text'] and not terminal['think'] and not terminal['sources']
    else:
        assert 'model_output' not in terminal


@pytest.mark.parametrize('outcome', [RunOutcome.FAILED, RunOutcome.CANCELLED, RunOutcome.SUCCEEDED])
def test_finally_restores_open_output_with_one_pure_control_terminal(outcome):
    translator = AgentEventFrameTranslator(query='test', run_id='run', enable_exports=True)
    translator.feed({'tag': 'model_output', 'model_output': boundary(1, 'begin')})
    translator.feed({'tag': 'text', 'delta': 'BAD\n:::export{filename="bad.md"}\nbody\n<thi',
                     'model_output': boundary(1)})
    terminal = translator.finish_run(outcome=outcome)
    assert terminal['model_output'] == boundary(1, 'rollback')
    assert terminal['runtime_event']['type'] == 'run_finished'
    assert terminal['runtime_event']['data']['partial_output'] is False
    assert not terminal['text'] and not terminal['think'] and not terminal['sources']
    assert 'export_snapshot' not in terminal
    assert translator.text_scanner.buf == ''
    assert not translator.export_stream.raw


def test_main_length_rolls_back_only_incomplete_call_and_never_tries_another_model(provider):
    provider.script['model-a'] = [
        {'message': {'role': 'assistant', 'content': '', 'tool_calls': [{
            'id': 'real-tool', 'index': 0, 'type': 'function',
            'function': {'name': 'record_fallback_effect', 'arguments': '{"value":"once"}'},
        }]}, 'frames': [
            sse_frame({'content': 'COMMITTED COMMENTARY\n'}),
            sse_frame({'tool_calls': [{'id': 'real-tool', 'index': 0, 'type': 'function',
                                     'function': {'name': 'record_fallback_effect',
                                                  'arguments': '{"value":"once"}'}}]}, finish='tool_calls'),
        ]},
        {'message': {'role': 'assistant', 'content': 'INCOMPLETE DRAFT'}, 'finish': 'length'},
    ]
    translator = AgentEventFrameTranslator(query='test', run_id='length', enable_exports=True)
    frames, events = [], []
    with config.temp('context_compression_enabled', False):
        executor, agent, plan = make_agent(provider, translator)
        with pytest.raises(Exception):
            asyncio.run(drive(executor, agent, plan, translator, frames, events))
    terminal = translator.finish_run(outcome=RunOutcome.FAILED)
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-a']
    assert EFFECT_RECEIPTS == ['once']
    assert terminal['runtime_event']['data'] == {
        'status': 'interrupted', 'reason': 'model_incomplete', 'code': 'length', 'partial_output': True,
        'model_call_id': translator.run.last_model_terminal['model_call_id'],
    }
    assert [(event['model_output']['call_index'], event['model_output']['phase'])
            for event in events if event.get('tag') == 'model_output'] == [
                (1, 'begin'), (1, 'commit'), (2, 'begin'), (2, 'rollback')]
    assert 'INCOMPLETE' not in ''.join(translator.export_stream.raw)


def test_closed_finish_supplements_keep_legacy_unfenced_projection():
    translator = AgentEventFrameTranslator(query='test', run_id='run', enable_exports=True)
    translator.feed({'tag': 'model_output', 'model_output': boundary(1, 'begin')})
    translator.feed({'tag': 'model_output', 'model_output': boundary(1, 'commit')})
    frames = translator.finish('normal committed result')
    assert ''.join(frame['text'] for frame in frames) == 'normal committed result'
    assert all('model_output' not in frame for frame in frames)
