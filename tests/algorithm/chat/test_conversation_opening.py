import json

import pytest
from lazyllm.module.llms.onlinemodule.base.model_outcome import (
    ModelCallError, ModelCallTerminal, ModelFailure, ModelFailureCode,
    ModelFailureOrigin, ModelFinish,
)
from lazymind.chat.service import conversation_opening as opening
from lazymind.chat.service import llm_task
from lazymind.chat.service.llm_task import LLMTaskRequest


def request(**kwargs):
    return LLMTaskRequest(task_type='conversation.describe_opening', input={'text': '为 LazyMind 设计对话整理'}, **kwargs)


def output(**kwargs):
    return json.dumps(dict(title='对话整理方案', initial_intent_summary='为 LazyMind 设计对话整理方案。',
                           intent_status='ready', missing_context=[], **kwargs), ensure_ascii=False)


def test_complete_output_and_single_call(monkeypatch):
    calls = []
    def model(prompt, **options):
        calls.append((prompt, options))
        return output()
    monkeypatch.setattr(opening, 'AutoModel', lambda **_: model)
    monkeypatch.setattr(llm_task, 'inject_model_config', lambda _: None)
    monkeypatch.setattr(opening, 'get_model_role_runtime_identity', lambda _: {'model': 'test'})
    result = llm_task.run_llm_task(request(llm_config={'llm': {'max_input_tokens': 32000}}))
    assert result.status == 'succeeded'
    assert result.output['intent_status'] == 'ready'
    assert len(calls) == 1
    assert calls[0][1]['max_retries'] == 1
    assert 'max_tokens' not in calls[0][1]
    assert 'max_completion_tokens' not in calls[0][1]
    assert result.usage['model_calls'] == 1


@pytest.mark.parametrize('raw', [output()[:-2], output(extra='not allowed'),
    '{"title":123,"initial_intent_summary":"x","intent_status":"ready","missing_context":[]}',
    '{"title":"x","initial_intent_summary":"x","intent_status":"empty","missing_context":[]}'])
def test_invalid_output_is_not_repaired_or_retried(monkeypatch, raw):
    calls = []
    def model(*_, **__):
        calls.append(1)
        return raw
    monkeypatch.setattr(opening, 'AutoModel', lambda **_: model)
    with pytest.raises(opening.OpeningTaskError) as error:
        opening.describe_opening(request())
    assert error.value.code == 'invalid_output'
    assert not error.value.retryable
    assert len(calls) == 1


def test_long_input_is_forwarded_without_estimated_capacity_rejection(monkeypatch):
    text = '资料内容\n' * 20000 + '最后要求：计算年度销售额'
    req = LLMTaskRequest(input={'text': text}, llm_config={'llm': {'max_input_tokens': 4096}})
    seen = []
    def model(prompt, **_):
        seen.append(prompt)
        return output()
    monkeypatch.setattr(opening, 'AutoModel', lambda **_: model)
    monkeypatch.setattr(opening, 'get_model_role_runtime_identity', lambda _: {})
    opening.describe_opening(req)
    assert text.replace('\n', '\\n') in seen[0]
    assert len(seen) == 1


@pytest.mark.parametrize('code,status,retryable,expected', [
    (ModelFailureCode.REQUEST_TIMEOUT,408,True,'request_timeout'),
    (ModelFailureCode.RATE_LIMITED,429,True,'rate_limited'),
    (ModelFailureCode.SERVICE_UNAVAILABLE,503,True,'service_unavailable'),
    (ModelFailureCode.AUTHENTICATION_FAILED,401,False,'authentication_failed'),
    (ModelFailureCode.INVALID_REQUEST,400,False,'invalid_request'),
])
def test_provider_failure_classification(code, status, retryable, expected):
    error = ModelCallError('provider failed', ModelCallTerminal('call',1,'failed',False,
        failure=ModelFailure(ModelFailureOrigin.HTTP,code,provider_http_status=status)))
    classified = opening.opening_error(error)
    assert classified.code == expected
    assert classified.retryable == retryable
    assert classified.calls == 1


def test_length_finish_is_failure_even_when_partial_json_looks_valid():
    error = ModelCallError('length', ModelCallTerminal('call',1,'incomplete',True,finish=ModelFinish.LENGTH))
    assert opening.opening_error(error).code == 'output_too_large'
    error = ModelCallError('capacity', ModelCallTerminal('call',1,'failed',False,
        failure=ModelFailure(ModelFailureOrigin.HTTP,ModelFailureCode.INVALID_REQUEST,
                             provider_error_code='context_length_exceeded',provider_http_status=400)))
    assert opening.opening_error(error).code == 'input_too_large'
