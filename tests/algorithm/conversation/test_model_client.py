import json
from unittest.mock import Mock

import pytest
from lazyllm.module.llms.onlinemodule.base import (
    ModelCallError, ModelCallTerminal, ModelFailure, ModelFailureCode,
    ModelFailureOrigin, ModelFinish,
)

from lazymind.conversation import model_client
from lazymind.conversation.conversation_title import generate_title
from lazymind.conversation.schemas import TitleRequest


def test_concurrent_requests_isolate_config_usage_and_restore_session(monkeypatch):
    import threading
    from concurrent.futures import ThreadPoolExecutor

    import lazyllm

    barrier = threading.Barrier(2)

    def model(prompt, **options):
        selected = lazyllm.globals['config']['dynamic_model_configs']['llm']['chat']
        name = selected['model']
        barrier.wait(timeout=5)
        assert lazyllm.globals['config']['dynamic_model_configs']['llm']['chat']['model'] == name
        lazyllm.globals['usage'].update({'prompt_tokens': len(name)})
        return json.dumps({'title': name, 'initial_intent_summary': name,
                           'intent_status': 'ready', 'missing_context': []})

    mock_model = Mock(side_effect=model)
    mock_model.formatter.return_value = mock_model
    monkeypatch.setattr(model_client, 'AutoModel', lambda **_: mock_model)

    def call(name):
        before = (lazyllm.globals._sid, lazyllm.locals._sid)
        lazyllm.globals['usage'].update({'prompt_tokens': 999})
        request = TitleRequest(llm_config={'llm': {'source': 'openai', 'model': name}})
        result = generate_title(request)
        assert (lazyllm.globals._sid, lazyllm.locals._sid) == before
        assert lazyllm.globals['usage']['prompt_tokens'] == 999
        lazyllm.globals.clear()
        lazyllm.locals.clear()
        return result

    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(call, ['first', 'second-model']))
    assert results[0].task_id != results[1].task_id
    for name, result in zip(['first', 'second-model'], results):
        assert result.status == 'succeeded', result
        assert result.output['title'] == name
        assert result.usage['model_id']['model'] == name
        assert result.usage['provider_usage']['prompt_tokens'] == len(name)


@pytest.mark.parametrize('streaming', [False, True])
def test_model_return_separates_reasoning_from_json(provider, provider_message, streaming):
    config, calls = provider
    payload = {'summary': '保留 <think>示例</think> 标签'}
    reasoning = '示例是 {"summary":"思考中的 JSON"}，实际结果另见正文。'
    provider_message.update(content=json.dumps(payload, ensure_ascii=False), reasoning_content=reasoning)
    events = []

    def operation(request):
        content = model_client.call_model(
            request, '生成会话摘要', response_format={'type': 'json_object'},
            stream_output={'_stream_sink': events.append} if streaming else False,
        )
        return {'body': json.loads(content)}, {}

    result = model_client.execute(TitleRequest(llm_config=config), operation)

    assert result.status == 'succeeded', result
    assert result.output['body'] == payload
    assert len(calls) == 1
    assert calls[0]['stream'] == streaming
    if streaming:
        assert any(event.get('tag') == 'think' and event.get('delta') == reasoning for event in events)
    else:
        assert events == []


@pytest.mark.parametrize('code,status,retryable,expected', [
    (ModelFailureCode.REQUEST_TIMEOUT, 408, True, 'request_timeout'),
    (ModelFailureCode.RATE_LIMITED, 429, True, 'rate_limited'),
    (ModelFailureCode.SERVICE_UNAVAILABLE, 503, True, 'service_unavailable'),
    (ModelFailureCode.AUTHENTICATION_FAILED, 401, False, 'authentication_failed'),
    (ModelFailureCode.INVALID_REQUEST, 400, False, 'invalid_request'),
])
def test_provider_failure_classification(code, status, retryable, expected):
    error = ModelCallError('provider failed', ModelCallTerminal(
        'call', 1, 'failed', False,
        failure=ModelFailure(ModelFailureOrigin.HTTP, code, provider_http_status=status)))
    classified = model_client.call_error(error)
    assert classified.code == expected
    assert classified.retryable == retryable
    assert classified.calls == 1


def test_length_finish_is_failure_even_when_partial_json_looks_valid():
    error = ModelCallError('length', ModelCallTerminal('call', 1, 'incomplete', True, finish=ModelFinish.LENGTH))
    assert model_client.call_error(error).code == 'output_too_large'
    error = ModelCallError('capacity', ModelCallTerminal(
        'call', 1, 'failed', False,
        failure=ModelFailure(ModelFailureOrigin.HTTP, ModelFailureCode.TOKEN_LIMIT,
                             provider_error_code='context_length_exceeded', provider_http_status=400)))
    assert model_client.call_error(error).code == 'input_too_large'


def test_model_configuration_failure_does_not_count_as_a_model_call(monkeypatch):
    def create_model(**_):
        raise ValueError('model is not configured')

    monkeypatch.setattr(model_client, 'AutoModel', create_model)
    result = generate_title(TitleRequest(input={'text': '解释倒排索引'},
                                         llm_config={'llm': {'source': 'openai', 'model': 'test'}}))
    assert result.status == 'failed'
    assert result.error_code == 'model_configuration'
    assert not result.retryable
    assert result.usage['model_calls'] == 0


@pytest.mark.parametrize('code,status,retryable', [
    (ModelFailureCode.RATE_LIMITED, 200, True),
    (ModelFailureCode.PROVIDER_OVERLOADED, 200, True),
    (ModelFailureCode.QUOTA_EXHAUSTED, 429, False),
    (ModelFailureCode.BALANCE_EXHAUSTED, 429, False),
])
def test_failure_semantics_take_precedence_over_http_status(code, status, retryable):
    error = ModelCallError('provider failed', ModelCallTerminal(
        'call', 1, 'failed', False,
        failure=ModelFailure(ModelFailureOrigin.HTTP, code, provider_http_status=status)))
    failure = model_client.call_error(error)
    assert failure.code == code.value
    assert failure.retryable is retryable
