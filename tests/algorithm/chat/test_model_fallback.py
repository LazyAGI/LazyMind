"""Real FunctionCall / HTTP regression for call-local model fallback.

Only the external provider is controlled. The fixture never selects a fallback
or replays an agent, so provider request counts expose missing recovery.
"""
from __future__ import annotations

import asyncio
import base64
from dataclasses import replace
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import lazyllm
from lazyllm.module.llms.onlinemodule.base import ModelCallError
import pytest
from fallback_test_support import EFFECT_RECEIPTS, record_fallback_effect

from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
)
from lazymind.chat.engine.agent_runtime.executor import _cfg
from lazymind.chat.engine.agent_runtime import executor as executor_module
from lazymind.chat.engine.agent_runtime import model_fallback as fallback_module
from lazymind.chat.engine.agent_runtime.models import FallbackRunState, ModelCandidate
from lazymind.chat.engine.agent_runtime.model_fallback import FallbackLLM, create_fallback_state
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext


@pytest.mark.usefixtures('fallback_provider')
class ModelFallbackHTTPTests(unittest.TestCase):
    def candidate(self, model, window=32768, *, group=None):
        return ModelCandidate(
            candidate_id='own:' + model, source='own', model_id=model,
            provider_group_id=group or model, credential_revision=1, max_input_tokens=window,
            llm_config=self.provider.config(model),
        )

    def wrapper(self, candidates, *, current_window=32768, current_vision=False):
        current = self.candidate('model-a', current_window)
        current.llm_config['llm']['vision'] = current_vision
        options = AgentExecutionOptions(
            llm_config=self.provider.config('model-a'), max_input_tokens=32768,
            model_candidates=(current, *candidates), current_model_candidate_id=current.candidate_id,
        )
        llm = lazyllm.OnlineChatModule(
            source='openai', model='model-a', type='vlm' if current_vision else 'llm', url=self.provider.url,
            api_key='fake-fallback-key', stream=False,
        )
        return FallbackLLM(create_fallback_state(llm, options))

    def fail_model(self, model, *, code='model_not_found', status=404, type='invalid_request_error'):
        self.provider.script[model] = [{'error': code, 'status': status, 'type': type}]

    def succeed(self, model, content='done'):
        self.provider.script[model] = [{'message': {'role': 'assistant', 'content': content}}]

    def test_healthy_path_does_not_construct_or_request_an_alternative(self):
        self.succeed('model-a', 'healthy')
        wrapper = self.wrapper([self.candidate('model-b')])
        with patch.object(fallback_module, '_make_llm', wraps=fallback_module._make_llm) as factory:
            self.assertEqual(wrapper('start'), 'healthy')
        self.assertEqual(factory.call_count, 0)
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a'])

    def test_effective_budget_order_and_unknown_attempts_are_finite(self):
        candidates = [self.candidate('unknown-1', None), self.candidate('long-2', 131072),
                      self.candidate('short', 16384), self.candidate('same', 32768),
                      self.candidate('long-1', 65536), self.candidate('unknown-2', None)]
        for candidate in [self.candidate('model-a'), *candidates]:
            self.fail_model(candidate.model_id)
        from lazymind.config import config
        with config.temp('model_fallback_max_candidates', 7), \
                patch.object(fallback_module.random, 'shuffle', side_effect=lambda values: values.reverse()) as shuffle:
            wrapper = self.wrapper(candidates)
            with self.assertRaises(Exception) as failure:
                wrapper('start', stream=True)
        self.assertEqual(shuffle.call_count, 1)
        self.assertEqual([item['model'] for item in self.provider.requests],
                         ['model-a', 'long-2', 'unknown-2', 'unknown-1', 'long-1', 'same', 'short'])
        self.assertEqual(getattr(failure.exception, 'fallback_code', None), 'model_fallback_exhausted')

    def test_unknown_current_uses_known_windows_largest_first(self):
        self.fail_model('model-a')
        self.fail_model('long')
        self.succeed('small')
        wrapper = self.wrapper([self.candidate('small', 32768), self.candidate('long', 65536)],
                               current_window=None)
        self.assertEqual(wrapper('start'), 'done')
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'long', 'small'])

    def test_unknown_token_limit_stops_without_compression_or_another_candidate(self):
        self.fail_model('model-a')
        self.fail_model('unknown-1', code='context_length_exceeded', status=400)
        self.succeed('unknown-2')
        with patch.object(fallback_module.random, 'shuffle', return_value=None):
            wrapper = self.wrapper([self.candidate('unknown-1', None), self.candidate('unknown-2', None)])
        with self.assertRaises(ModelCallError) as failure:
            wrapper('preserve all text')
        self.assertEqual(failure.exception.terminal.failure.code.value, 'token_limit')
        self.assertEqual([item['model'] for item in self.provider.requests],
                         ['model-a', 'unknown-1'])

    def test_local_fit_skips_http_and_larger_candidate_receives_the_full_input(self):
        self.succeed('large')
        wrapper = self.wrapper([self.candidate('large', 32768)], current_window=256)
        events = []
        text = '完整输入不丢失' * 200
        self.assertEqual(wrapper(text, stream={'_stream_sink': events.append}), 'done')
        self.assertEqual([item['model'] for item in self.provider.requests], ['large'])
        self.assertIn(text, json.dumps(self.provider.requests[-1], ensure_ascii=False))
        calls = [item['runtime_event'] for item in events if item.get('runtime_event')
                 and item['runtime_event']['type'] == 'model_call_finished']
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]['data']['attempt_count'], 1)
        switches = [event['runtime_event']['data'] for event in events
                    if (event.get('runtime_event') or {}).get('type') == 'model_switched']
        self.assertEqual(switches[0]['reason_code'], 'model_context_not_fit')
        self.assertNotIn('own:model-a', wrapper.state.failed_candidates)

    def test_all_local_fit_rejections_have_no_fabricated_model_calls(self):
        from lazymind.chat.runtime_events import RunOutcome
        from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

        wrapper = self.wrapper([self.candidate('same', 256)], current_window=256)
        events = []
        with self.assertRaises(Exception) as failure:
            wrapper('长输入' * 1000, stream={'_stream_sink': events.append})
        self.assertEqual(self.provider.requests, [])
        self.assertIsNone(AgentExecutor._find_model_terminal(failure.exception))
        translator = AgentEventFrameTranslator(query='q', run_id='fit-run')
        for event in events:
            self.assertEqual(translator.feed(event), [])
        terminal = translator.run.finish(outcome=RunOutcome.FAILED)
        self.assertEqual(terminal['data']['code'], 'model_context_not_fit')
        self.assertEqual(translator.model_events, [])

    def test_real_failure_then_local_fit_exhaustion_preserves_real_call_count(self):
        from lazymind.chat.runtime_events import RunOutcome
        from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

        self.fail_model('model-a')
        candidate = self.candidate('model-b')
        candidate.llm_config['llm']['static_params'] = {'large_fixed_parameter': 'overflow ' * 100000}
        wrapper = self.wrapper([candidate])
        events = []
        with self.assertRaises(Exception):
            wrapper('start', stream={'_stream_sink': events.append})
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a'])
        translator = AgentEventFrameTranslator(query='q', run_id='mixed-run')
        for event in events:
            translator.feed(event)
        finished = [event for event in translator.model_events if event['type'] == 'model_call_finished']
        self.assertEqual(len(finished), 1)
        self.assertEqual(finished[0]['data']['attempt_count'], 1)
        self.assertEqual(translator.run.finish(outcome=RunOutcome.FAILED)['data']['code'], 'model_fallback_exhausted')

    def test_cancellation_stops_before_the_next_provider_request(self):
        self.fail_model('model-a')
        wrapper = self.wrapper([self.candidate('model-b')])
        def cancel(_output):
            return bool(self.provider.requests)

        wrapper._cancel_check = cancel
        with self.assertRaises(asyncio.CancelledError):
            wrapper('start')
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a'])

    def test_files_and_history_are_explicitly_forwarded_to_the_new_module(self):
        self.fail_model('model-a')
        self.succeed('model-b')
        current, alternative = self.candidate('model-a'), self.candidate('model-b')
        for candidate in (current, alternative):
            candidate.llm_config['llm']['vision'] = True
        options = AgentExecutionOptions(model_candidates=(current, alternative),
                                        current_model_candidate_id=current.candidate_id)
        raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='vlm', url=self.provider.url,
                                      api_key='fake-fallback-key', stream=False)
        wrapper = FallbackLLM(create_fallback_state(raw, options))
        history = [{'role': 'user', 'content': 'prior text'}]
        with tempfile.TemporaryDirectory() as directory:
            picture = Path(directory) / 'picture.png'
            picture.write_bytes(base64.b64decode(
                'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jY9kAAAAASUVORK5CYII=',
            ))
            lazyllm.locals['chat_history'][wrapper._module_id] = history
            lazyllm.locals['lazyllm_files'][wrapper._module_id] = [str(picture)]
            try:
                self.assertEqual(wrapper('inspect picture'), 'done')
            finally:
                lazyllm.locals['lazyllm_files'].pop(wrapper._module_id, None)
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'model-b'])
        self.assertEqual(self.provider.requests[0]['messages'], self.provider.requests[1]['messages'])
        self.assertIn('data:image/png;base64,', json.dumps(self.provider.requests[1]))
        self.assertEqual(history, [{'role': 'user', 'content': 'prior text'}])

    def test_unknown_candidate_preserves_input_and_remains_unknown_after_success(self):
        self.fail_model('model-a')
        self.succeed('unknown')
        wrapper = self.wrapper([self.candidate('unknown', None)])
        history = [{'role': 'user', 'content': 'preserve me'}]
        self.assertEqual(wrapper('current', llm_chat_history=history), 'done')
        self.assertIsNone(wrapper.state.current.max_input_tokens)
        self.assertEqual(self.provider.requests[0]['messages'], self.provider.requests[1]['messages'])
        self.assertEqual(history, [{'role': 'user', 'content': 'preserve me'}])

    def test_dynamic_current_model_uses_the_existing_request_configuration(self):
        from lazyllm.module.llms.model_config_inject import inject_model_config

        self.fail_model('model-a')
        self.succeed('model-b')
        config = lazyllm.globals['config']
        missing = object()
        old_models = config.get('dynamic_model_configs', missing)
        old_key = config.get('openai_api_key', missing)
        try:
            inject_model_config(self.provider.config('model-a'))
            current, candidate = self.candidate('model-a'), self.candidate('model-b')
            options = AgentExecutionOptions(model_candidates=(current, candidate),
                                            current_model_candidate_id=current.candidate_id)
            raw = lazyllm.OnlineChatModule(source='dynamic', name='llm', dynamic_auth=True,
                                          type='llm', stream=False)
            wrapper = FallbackLLM(create_fallback_state(raw, options))
            self.assertEqual(wrapper.share('unchanged system')('current input'), 'done')
        finally:
            for key, value in [('dynamic_model_configs', old_models), ('openai_api_key', old_key)]:
                if value is missing:
                    config.pop(key, None)
                else:
                    config[key] = value
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'model-b'])

    def test_visual_history_skips_text_only_candidates_while_text_attachments_do_not(self):
        self.fail_model('model-a')
        self.succeed('text-only')
        self.succeed('visual')
        visual = self.candidate('visual')
        visual.llm_config['llm']['vision'] = True
        wrapper = self.wrapper([self.candidate('text-only'), visual], current_vision=True)
        self.assertEqual(wrapper('q', llm_chat_history=[{'role': 'user', 'content': [
            {'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,ZmFrZQ=='}},
        ]}]), 'done')
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'visual'])
        self.provider.requests.clear()
        wrapper = self.wrapper([self.candidate('text-only')])
        self.assertEqual(wrapper('text attachment is described here', lazyllm_files=['notes.txt']), 'done')
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'text-only'])

    def test_new_visual_input_does_not_get_dropped_by_a_text_only_committed_model(self):
        self.fail_model('model-a')
        self.succeed('text-only')
        self.succeed('visual')
        visual = self.candidate('visual')
        visual.llm_config['llm']['vision'] = True
        wrapper = self.wrapper([self.candidate('text-only'), visual])
        self.assertEqual(wrapper('text request'), 'done')
        self.assertEqual(wrapper('image request', llm_chat_history=[{'role': 'user', 'content': [
            {'type': 'image_url', 'image_url': {'url': 'data:image/png;base64,ZmFrZQ=='}},
        ]}]), 'done')
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'text-only', 'visual'])

    def test_shared_views_keep_prompt_formatter_and_stream_independent(self):
        self.fail_model('model-a')
        self.succeed('model-b', 'value')
        wrapper = self.wrapper([self.candidate('model-b')])
        first_events, second_events = [], []
        first = wrapper.share(prompt='first system', format=lambda value: 'first:' + value['content'],
                              stream={'_stream_sink': first_events.append})
        second = wrapper.share(prompt='second system', format=lambda value: 'second:' + value['content'],
                               stream={'_stream_sink': second_events.append})
        self.assertEqual(first('q1'), 'first:value')
        self.assertEqual(second('q2'), 'second:value')
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'model-b', 'model-b'])
        self.assertIn('first system', self.provider.requests[1]['messages'][0]['content'])
        self.assertNotIn('second system', self.provider.requests[1]['messages'][0]['content'])
        self.assertIn('second system', self.provider.requests[2]['messages'][0]['content'])
        self.assertTrue(first_events)
        self.assertTrue(second_events)

    def test_sink_failure_never_requests_another_model(self):
        self.succeed('model-a')
        wrapper = self.wrapper([self.candidate('model-b')])

        def broken_sink(event):
            if event.get('tag') == 'text':
                raise RuntimeError('local text sink broke')

        with self.assertRaisesRegex(Exception, 'local text sink broke') as failure:
            wrapper('start', stream={'_stream_sink': broken_sink})
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a'])
        self.assertIsNone(AgentExecutor._find_model_terminal(failure.exception))

    def test_formatter_and_success_sink_failures_do_not_publish_or_retry_success(self):
        self.fail_model('model-a')
        self.succeed('model-b')
        wrapper = self.wrapper([self.candidate('model-b'), self.candidate('model-c')])
        events = []

        def broken_formatter(_response):
            raise ValueError('local formatting broke')

        with self.assertRaisesRegex(Exception, 'local formatting broke'):
            wrapper.share(format=broken_formatter)('start', stream={'_stream_sink': events.append})
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'model-b'])
        successes = [item for item in events
                     if (item.get('runtime_event') or {}).get('type') == 'model_switched']
        self.assertEqual(successes, [])
        self.assertEqual(wrapper.state.current.model_id, 'model-a')

    def test_success_event_sink_failure_does_not_retry_or_execute_tools(self):
        self.fail_model('model-a')
        self.succeed('model-b')
        wrapper = self.wrapper([self.candidate('model-b'), self.candidate('model-c')])

        def broken_sink(event):
            if (event.get('runtime_event') or {}).get('type') == 'model_switched':
                raise RuntimeError('local success sink broke')

        with self.assertRaisesRegex(Exception, 'local success sink broke'):
            wrapper('start', stream={'_stream_sink': broken_sink})
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'model-b'])
        self.assertEqual(EFFECT_RECEIPTS, [])

    def test_local_commit_failure_does_not_publish_success_or_try_another_candidate(self):
        self.fail_model('model-a')
        self.succeed('model-b')
        wrapper = self.wrapper([self.candidate('model-b'), self.candidate('model-c')])
        events = []
        original_setattr = FallbackRunState.__setattr__

        def fail_commit(state, name, value):
            if name == 'current' and value.model_id == 'model-b':
                raise RuntimeError('local state commit failed')
            original_setattr(state, name, value)

        with patch.object(FallbackRunState, '__setattr__', fail_commit), self.assertRaisesRegex(
            Exception, 'local state commit failed',
        ):
            wrapper('start', stream={'_stream_sink': events.append})
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'model-b'])
        self.assertFalse(any((item.get('runtime_event') or {}).get('type') == 'model_switched'
                             for item in events))

    def test_local_runtime_failure_overrides_an_earlier_provider_failure(self):
        from lazymind.chat.runtime_events import RunOutcome
        from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

        translator = AgentEventFrameTranslator(query='q', run_id='local-failure')
        translator.feed({'tag': 'runtime_event', 'runtime_event': {
            'schema_version': 1, 'event_id': 'failed-a', 'type': 'model_call_finished',
            'data': {'model_call_id': 'a', 'attempt_count': 1, 'kind': 'failure',
                     'failure': {'origin': 'http', 'code': 'not_found'}},
        }})
        self.assertEqual(translator.feed({'tag': 'model_fallback_failed', 'fallback_code': 'runtime_failure'}), [])
        terminal = translator.run.finish(outcome=RunOutcome.FAILED)
        self.assertEqual(terminal['data']['reason'], 'runtime_failure')
        self.assertEqual(terminal['data']['code'], 'runtime_failure')

    def test_raw_helper_uses_committed_model_without_consuming_candidates(self):
        self.fail_model('model-a')
        self.succeed('model-b')
        wrapper = self.wrapper([self.candidate('model-b'), self.candidate('model-c')])
        self.assertEqual(wrapper('start'), 'done')
        self.fail_model('model-b')
        with self.assertRaises(Exception):
            wrapper.auxiliary_call('summary system', 'summary input')
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'model-b', 'model-b'])
        self.assertEqual(wrapper.state.current.model_id, 'model-b')

    def test_executor_stream_and_translator_bind_success_to_the_replacement(self):
        from lazymind.chat.runtime_events import RunOutcome
        from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
        from lazymind.chat.service.chat_request import ChatRequest

        request = ChatRequest(message={'query': 'record once'}, runtime={
            'model_fallback_current_id': 'own:model-a',
            'model_fallback_candidates': [vars(self.candidate('model-a')), vars(self.candidate('model-b'))],
        })
        options = AgentExecutionOptions(
            llm_config=self.provider.config('model-a'),
            current_model_candidate_id=request.runtime.model_fallback_current_id,
            model_candidates=tuple(request.runtime.model_fallback_candidates),
            max_input_tokens=32768, enable_builtin_tools=False, skills=False, preload_all_tools=True,
            workspace_permission=WorkspaceContext(local_runtime=False), max_retries=3,
        )
        plan = AgentRunPlan(
            role=AgentRole.CHAT,
            prompt=PromptBuilder.for_role(AgentRole.CHAT).input('record once', source='user').build(),
            tools=[record_fallback_effect], execution_options=options,
        )
        llm = lazyllm.OnlineChatModule(source='openai', model='model-a', type='llm', url=self.provider.url,
                                     api_key='fake-fallback-key', stream=False)

        async def collect():
            return [item async for item in AgentExecutor().stream(llm, plan)]

        with _cfg.temp('context_compression_enabled', False), patch.object(
            executor_module, 'refine_unavailable_model_event', side_effect=AssertionError('stale A refine'),
        ):
            collected = asyncio.run(collect())
        self.assertIn(('final', 'completed via B'), collected)
        self.assertEqual(EFFECT_RECEIPTS, ['once'])
        translator = AgentEventFrameTranslator(query='record once', run_id='same-run')
        frames = []
        for kind, item in collected:
            if kind == 'event':
                frames.extend(translator.feed(item))
        events = [frame['runtime_event'] for frame in frames if frame.get('runtime_event')]
        switched = next(event for event in events if event['type'] == 'model_switched')
        successful = next(event for event in events if event['type'] == 'model_call_finished'
                          and event['data'].get('finish') == 'stop')
        self.assertEqual(switched['run_id'], 'same-run')
        self.assertEqual(switched['data']['to']['candidate_id'], 'own:model-b')
        self.assertEqual(switched['data']['switch_index'], 1)
        self.assertGreater(events.index(switched), events.index(successful))
        self.assertFalse({'candidate_id', 'switch_index', 'fallback_code'} & successful['data'].keys())
        self.assertFalse({'model_call_id', 'phase', 'status'} & switched['data'].keys())
        self.assertEqual(translator.run.finish(outcome=RunOutcome.SUCCEEDED)['data']['status'], 'completed')
        self.assertNotIn('fake-fallback-key', json.dumps(events))
        self.assertNotIn(self.provider.url, json.dumps(events))

    def test_real_function_call_final_fit_recovers_before_workspace_cleanup(self):
        self.succeed('model-b')
        current, candidate = self.candidate('model-a', 4096), self.candidate('model-b', 32768)
        options = AgentExecutionOptions(
            model_candidates=(current, candidate), current_model_candidate_id=current.candidate_id,
            max_input_tokens=4096, enable_builtin_tools=False, skills=False, preload_all_tools=True,
            workspace_permission=WorkspaceContext(local_runtime=False), max_retries=3,
        )
        plan = AgentRunPlan(
            role=AgentRole.CHAT, prompt=PromptBuilder.for_role(AgentRole.CHAT).input('start', source='user').build(),
            tools=[record_fallback_effect], execution_options=options,
        )
        raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='llm', url=self.provider.url,
                                     api_key='fake-fallback-key', stream=False)
        lazyllm.globals['agentic_config'] = {'enable_tool_retrieval': True, 'user_id': 'fake', 'conversation_id': 'fit'}
        with tempfile.TemporaryDirectory() as workspace, _cfg.temp('agentic_workspace', workspace), \
                _cfg.temp('context_compression_enabled', False):
            agent = AgentExecutor().create_agent(raw, plan)
            actual_input = '保留完整输入' * 1000
            self.assertEqual(agent(actual_input), 'done')
        self.assertEqual([item['model'] for item in self.provider.requests], ['model-b'])
        self.assertIn(actual_input, json.dumps(self.provider.requests[0], ensure_ascii=False))
        self.assertEqual(EFFECT_RECEIPTS, [])

    def test_reusing_an_executor_creates_independent_run_state(self):
        self.fail_model('model-a')
        self.succeed('model-b')
        current, candidate = self.candidate('model-a'), self.candidate('model-b')
        options = AgentExecutionOptions(
            model_candidates=(current, candidate), current_model_candidate_id=current.candidate_id,
            enable_builtin_tools=False, skills=False, workspace_permission=WorkspaceContext(local_runtime=False),
        )
        plan = AgentRunPlan(
            role=AgentRole.CHAT, prompt=PromptBuilder.for_role(AgentRole.CHAT).input('start', source='user').build(),
            tools=[record_fallback_effect], execution_options=options,
        )
        raw = lazyllm.OnlineChatModule(source='openai', model='model-a', type='llm', url=self.provider.url,
                                     api_key='fake-fallback-key', stream=False)
        executor = AgentExecutor()
        with _cfg.temp('context_compression_enabled', False):
            first = executor.create_agent(raw, plan)
            second = executor.create_agent(raw, plan)
            self.assertEqual(first(plan.prompt.current_input), 'done')
            self.assertEqual(second(plan.prompt.current_input), 'done')
        self.assertEqual([item['model'] for item in self.provider.requests],
                         ['model-a', 'model-b', 'model-a', 'model-b'])

    def test_explicit_invalid_single_key_skips_its_group_but_generic_401_does_not(self):
        for code, expected in [('invalid_api_key', ['model-a', 'other']),
                               ('unknown_auth_error', ['model-a', 'model-b'])]:
            with self.subTest(code=code):
                self.provider.requests.clear()
                self.fail_model('model-a', code=code, status=401, type='authentication_error')
                self.succeed('model-b')
                self.succeed('other')
                wrapper = self.wrapper([self.candidate('model-b', group='model-a'), self.candidate('other')])
                self.assertEqual(wrapper('start'), 'done')
                self.assertEqual([item['model'] for item in self.provider.requests], expected)

    def test_only_explicit_unsupported_call_codes_recover_from_invalid_requests(self):
        for code in ['unsupported_tools', 'tools_not_supported', 'unsupported_function_call', 'invalid_parameters']:
            with self.subTest(code=code):
                self.provider.requests.clear()
                self.fail_model('model-a', code=code, status=400)
                self.succeed('model-b')
                wrapper = self.wrapper([self.candidate('model-b')])
                events = []
                if code == 'invalid_parameters':
                    with self.assertRaises(Exception):
                        wrapper('start', stream={'_stream_sink': events.append})
                    self.assertEqual([item['model'] for item in self.provider.requests], ['model-a'])
                else:
                    self.assertEqual(wrapper('start', stream={'_stream_sink': events.append}), 'done')
                    self.assertEqual([item['model'] for item in self.provider.requests], ['model-a', 'model-b'])
                    switched = next(item['runtime_event'] for item in events
                                    if (item.get('runtime_event') or {}).get('type') == 'model_switched')
                    self.assertEqual(switched['data']['reason_code'], 'provider_rejected')

    def test_provider_script_really_distinguishes_model_and_stage(self):
        def request(model, messages):
            data = json.dumps({'model': model, 'messages': messages}).encode()
            return urlopen(Request(self.provider.url + 'chat/completions', data=data,
                                   headers={'Content-Type': 'application/json'}), timeout=2)

        with request('model-a', [{'role': 'user', 'content': 'start'}]) as response:
            first = json.load(response)
        self.assertEqual(first['choices'][0]['finish_reason'], 'tool_calls')
        self.assertEqual(first['choices'][0]['message']['tool_calls'][0]['function']['name'],
                         'record_fallback_effect')
        with self.assertRaises(HTTPError) as failed:
            request('model-a', [{'role': 'tool', 'content': 'effect-receipt: once'}])
        self.assertEqual(failed.exception.code, 404)
        failed.exception.close()
        with request('model-b', [{'role': 'tool', 'content': 'effect-receipt: once'}]) as response:
            replacement = json.load(response)
        self.assertEqual(replacement['choices'][0]['message']['content'], 'completed via B')
        self.assertEqual([item['model'] for item in self.provider.requests],
                         ['model-a', 'model-a', 'model-b'])

    def test_tool_result_survives_model_failure_without_replaying_tool(self):
        options = AgentExecutionOptions(
            llm_config=self.provider.config('model-a'), max_input_tokens=32768,
            enable_builtin_tools=False, skills=False, max_retries=3, preload_all_tools=True,
            workspace_permission=WorkspaceContext(local_runtime=False),
        )
        options = replace(options, model_candidates=({
            'source': 'own', 'model_id': 'model-b', 'provider_group_id': 'group-b',
            'credential_revision': 1, 'credential_count': 1,
            'max_input_tokens': 32768,
            'llm_config': self.provider.config('model-b'),
        },))
        plan = AgentRunPlan(
            role=AgentRole.CHAT,
            prompt=PromptBuilder.for_role(AgentRole.CHAT).input('record once then finish', source='user').build(),
            tools=[record_fallback_effect], execution_options=options,
        )
        llm = lazyllm.OnlineChatModule(
            source='openai', model='model-a', url=self.provider.url,
            api_key='fake-fallback-key', stream=False,
        )
        error = None
        result = None
        with _cfg.temp('context_compression_enabled', False):
            agent = AgentExecutor().create_agent(llm, plan)
            try:
                result = agent(plan.prompt.current_input, llm_chat_history=plan.history)
            except Exception as exc:
                error = exc

        # These checks distinguish a model defect from import/fixture/tool setup failure.
        self.assertEqual(EFFECT_RECEIPTS, ['once'])
        models = [item['model'] for item in self.provider.requests]
        self.assertGreaterEqual(len(models), 2)
        self.assertEqual(models[:2], ['model-a', 'model-a'])
        self.assertTrue(any(item.get('role') == 'tool'
                            and 'effect-receipt: once' in item.get('content', '')
                            for item in self.provider.requests[1]['messages']))
        self.assertIsNone(error, f'recovery escaped the model call: {type(error).__name__}: {error}; '
                                 f'provider_requests={models}; effect_receipts={EFFECT_RECEIPTS}')
        self.assertEqual(result, 'completed via B')
        self.assertEqual(models, ['model-a', 'model-a', 'model-b'])


if __name__ == '__main__':
    unittest.main(verbosity=2)
