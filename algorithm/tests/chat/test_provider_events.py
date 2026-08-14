import asyncio
import json
from types import SimpleNamespace

import lazyllm
import pytest

from lazymind.chat.engine.agent_runtime.executor import AgentExecutor
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
from lazymind.chat.service.utils import streaming


@pytest.mark.parametrize('tag', ['provider_status', 'model_retry', 'model_transport_error'])
def test_provider_events_are_preserved_as_structured_frames(tag):
    translator = AgentEventFrameTranslator(query='test')
    event = {
        'tag': tag,
        'model_call_id': 'call_1',
        'http_status': 429 if tag == 'provider_status' else None,
        'finish_reason': None,
    }
    if tag == 'provider_status': event['error_body'] = '{"error":"rate limited"}'
    if tag == 'model_retry': event.update(retry_index=1, max_retries=5, delay_ms=1000)
    if tag == 'model_transport_error': event.update(error_type='ReadTimeout', error_message='timed out')

    frames = translator.feed(event)

    assert frames == [{
        'think': None,
        'text': None,
        'sources': [],
        tag: {key: value for key, value in event.items() if key != 'tag'},
    }]


def test_provider_error_body_is_emitted_but_redacted_from_logs(monkeypatch):
    logs = []
    monkeypatch.setattr(streaming.LOG, 'debug', logs.append)
    frame = {
        'think': None,
        'text': None,
        'sources': [],
        'provider_status': {
            'model_call_id': 'call_1',
            'http_status': 429,
            'finish_reason': None,
            'error_body': 'secret provider response',
        },
    }

    emitted = streaming.log_and_emit_frame(frame, 0.1, 'query', 'session')

    assert 'secret provider response' not in logs[0]
    assert '"error_body_length": 24' in logs[0]
    assert json.loads(emitted)['data']['provider_status']['error_body'] == 'secret provider response'


@pytest.mark.asyncio
async def test_agent_executor_drains_provider_event_before_future_exception():
    class FailingAgent:
        def __call__(self, *args, **kwargs):
            lazyllm.FileSystemQueue().enqueue(json.dumps({
                'tag': 'provider_status',
                'model_call_id': 'call_1',
                'http_status': 500,
                'finish_reason': None,
                'error_body': 'upstream failed',
            }))
            raise RuntimeError('safe failure')

    plan = SimpleNamespace(history=None, prompt=SimpleNamespace(current_input='test'))
    received = []

    with pytest.raises(RuntimeError, match='safe failure'):
        async for item in AgentExecutor().stream_agent(FailingAgent(), plan):
            received.append(item)
            await asyncio.sleep(0)

    assert received == [('event', {
        'tag': 'provider_status',
        'model_call_id': 'call_1',
        'http_status': 500,
        'finish_reason': None,
        'error_body': 'upstream failed',
    })]
