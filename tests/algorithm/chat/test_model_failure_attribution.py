"""Keep the selected model's failure when an uncommitted fallback also fails."""
import pytest
from lazyllm.module.llms.onlinemodule.base import ModelCallError

from fallback_test_support import candidate, wrapper, broken_output
from lazymind.chat.runtime_events import RunOutcome
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator


@pytest.mark.parametrize(('error', 'status', 'error_type', 'expected'), [
    ('invalid_api_key', 401, 'authentication_error', 'authentication_failed'),
    ('permission_denied', 403, 'permission_error', 'permission_denied'),
    ('rate_limit_exceeded', 429, 'rate_limit_error', 'rate_limited'),
    ('insufficient_quota', 429, 'insufficient_quota', 'quota_exhausted'),
    ('credit_balance_exhausted', 402, 'insufficient_balance', 'balance_exhausted'),
    ('model_not_found', 404, 'not_found_error', 'not_found'),
    ('request_timeout', 408, 'timeout_error', 'request_timeout'),
    (None, None, None, 'transport_error'),
])
def test_uncommitted_candidate_error_does_not_replace_selected_model_failure(
    provider, error, status, error_type, expected,
):
    provider.script['model-a'] = [broken_output() if error is None else {
        'error': error, 'status': status, 'type': error_type,
    }]
    provider.script['model-b'] = [{'error': 'invalid_argument', 'status': 400, 'type': 'invalid_request_error'}]
    model = wrapper(candidate(provider, 'model-a'), [candidate(provider, 'model-b')])
    translator = AgentEventFrameTranslator(query='test', run_id='failure-attribution')
    events = []

    def collect(event):
        events.append(event)
        translator.feed(event)

    with pytest.raises(ModelCallError):
        model('test', stream={'_stream_sink': collect})
    observed = [event['runtime_event']['data'] for event in events
                if (event.get('runtime_event') or {}).get('type') == 'model_call_finished']
    assert observed[0]['failure']['code'] == expected
    assert observed[-1]['failure']['code'] == 'invalid_request'
    result = translator.finish_run(outcome=RunOutcome.FAILED)['runtime_event']['data']
    assert result['code'] == expected
    assert result['model_call_id'] == observed[0]['model_call_id']
    assert result['partial_output'] is False


@pytest.mark.parametrize('finish', ['length', 'content_filter', 'unknown'])
def test_uncommitted_incomplete_candidate_preserves_primary_failure(provider, finish):
    provider.script['model-a'] = [{'error': 'invalid_api_key', 'status': 401, 'type': 'authentication_error'}]
    provider.script['model-b'] = [{
        'message': {'role': 'assistant', 'content': 'uncommitted candidate text'}, 'finish': finish,
    }]
    model = wrapper(candidate(provider, 'model-a'), [candidate(provider, 'model-b')])
    translator = AgentEventFrameTranslator(query='test', run_id='incomplete-candidate')
    events = []

    def collect(event):
        events.append(event)
        translator.feed(event)

    with pytest.raises(ModelCallError):
        model('test', stream={'_stream_sink': collect})
    observed = [event['runtime_event']['data'] for event in events
                if (event.get('runtime_event') or {}).get('type') == 'model_call_finished']
    assert len(observed) == 2
    assert observed[0]['failure']['code'] == 'authentication_failed'
    assert observed[1]['kind'] == 'finish' and observed[1]['finish'] == finish
    result = translator.finish_run(outcome=RunOutcome.FAILED)['runtime_event']['data']
    assert result['code'] == 'authentication_failed'
    assert result['model_call_id'] == observed[0]['model_call_id']
    assert result.get('diagnostic_id') == observed[0]['failure'].get('diagnostic_id')
    assert result['partial_output'] is False
    assert not any(
        (event.get('runtime_event') or {}).get('type') == 'model_switched' for event in events
    )
