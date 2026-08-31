import asyncio
import json
from types import SimpleNamespace

import lazyllm
import pytest

from lazymind.chat.engine.agent_runtime.executor import AgentExecutor
from lazymind.chat.runtime_events import RunAccumulator
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator


def test_translator_binds_model_event_to_run():
    translator = AgentEventFrameTranslator(query='test', run_id='run-1')
    frames = translator.feed({
        'tag': 'runtime_event',
        'runtime_event': {
            'schema_version': 1,
            'event_id': 'event-1',
            'type': 'model_call_finished',
            'data': {
                'model_call_id': 'call-1',
                'attempt_count': 1,
                'kind': 'finish',
                'finish': 'tool_calls',
                'has_semantic_output': True,
            },
        },
    })

    assert frames[0]['runtime_event']['run_id'] == 'run-1'
    assert frames[0]['runtime_event']['type'] == 'model_call_finished'
    assert translator.run.last_model_terminal['model_call_id'] == 'call-1'


@pytest.mark.parametrize(('terminal', 'status', 'reason'), [
    ({'kind': 'finish', 'finish': 'length', 'has_semantic_output': True},
     'interrupted', 'model_incomplete'),
    ({'kind': 'finish', 'finish': 'content_filter', 'has_semantic_output': True},
     'interrupted', 'model_incomplete'),
    ({'kind': 'finish', 'finish': 'insufficient_system_resource', 'has_semantic_output': False},
     'interrupted', 'model_incomplete'),
    ({'kind': 'finish', 'finish': 'unknown', 'has_semantic_output': False},
     'interrupted', 'model_incomplete'),
    ({'kind': 'finish', 'finish': 'stop', 'has_semantic_output': True},
     'failed', 'runtime_failure'),
    ({'kind': 'finish', 'finish': 'tool_calls', 'has_semantic_output': True},
     'failed', 'runtime_failure'),
    ({'kind': 'failure',
      'failure': {'origin': 'transport', 'code': 'transport_error'},
      'has_semantic_output': True},
     'interrupted', 'model_failure'),
    ({'kind': 'failure', 'failure': {'origin': 'http', 'code': 'rate_limited'},
      'has_semantic_output': False},
     'failed', 'model_failure'),
    ({'kind': 'failure', 'failure': {'origin': 'provider', 'code': 'usage_limit_exceeded'},
      'has_semantic_output': False},
     'failed', 'model_failure'),
    ({'kind': 'failure', 'failure': {'origin': 'provider', 'code': 'concurrency_limited'},
      'has_semantic_output': False},
     'failed', 'model_failure'),
])
def test_run_accumulator_maps_model_terminal(terminal, status, reason):
    accumulator = RunAccumulator(run_id='run-1', last_model_terminal=terminal)
    event = accumulator.finish(succeeded=False)

    assert event['type'] == 'run_finished'
    assert event['data']['status'] == status
    assert event['data']['reason'] == reason


@pytest.mark.parametrize('finish', ['stop', 'tool_calls'])
def test_successful_model_terminal_does_not_mask_downstream_runtime_failure(finish):
    accumulator = RunAccumulator(run_id='run-1', last_model_terminal={
        'kind': 'finish',
        'finish': finish,
        'has_semantic_output': True,
    })

    assert accumulator.finish(succeeded=False)['data']['code'] == 'runtime_failure'


def test_run_accumulator_only_propagates_public_failure_fields():
    accumulator = RunAccumulator(run_id='run-1', last_model_terminal={
        'model_call_id': 'call-1',
        'kind': 'failure',
        'has_semantic_output': False,
        'failure': {
            'origin': 'http',
            'code': 'quota_exhausted',
            'provider_http_status': 429,
            'retry_after_ms': 2000,
            'diagnostic_id': 'diag-1',
        },
    })

    data = accumulator.finish(succeeded=False)['data']

    assert data == {
        'status': 'failed',
        'reason': 'model_failure',
        'code': 'quota_exhausted',
        'partial_output': False,
        'model_call_id': 'call-1',
        'diagnostic_id': 'diag-1',
    }


def test_run_accumulator_awaiting_user_input_is_completed():
    accumulator = RunAccumulator(run_id='run-1', ask_pending=True, semantic_output=True)
    event = accumulator.finish(succeeded=True)

    assert event['data'] == {
        'status': 'completed',
        'reason': 'awaiting_user_input',
        'partial_output': True,
    }


@pytest.mark.asyncio
async def test_agent_executor_drains_runtime_event_before_future_exception():
    class FailingAgent:
        def __call__(self, *args, **kwargs):
            lazyllm.FileSystemQueue().enqueue(json.dumps({
                'tag': 'runtime_event',
                'runtime_event': {
                    'schema_version': 1,
                    'event_id': 'event-1',
                    'type': 'model_call_finished',
                    'data': {
                        'model_call_id': 'call-1',
                        'attempt_count': 1,
                        'kind': 'failure',
                        'has_semantic_output': False,
                        'failure': {'origin': 'transport', 'has_semantic_output': False},
                    },
                },
            }))
            raise RuntimeError('safe failure')

    plan = SimpleNamespace(history=None, prompt=SimpleNamespace(current_input='test'))
    received = []

    with pytest.raises(RuntimeError, match='safe failure'):
        async for item in AgentExecutor().stream_agent(FailingAgent(), plan):
            received.append(item)
            await asyncio.sleep(0)

    assert len(received) == 1
    assert received[0][0] == 'event'
    assert received[0][1]['runtime_event']['type'] == 'model_call_finished'
