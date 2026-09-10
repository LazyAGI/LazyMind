import json

import pytest
from lazymind.chat.service import llm_task
from lazymind.chat.service.llm_task import LLMTaskRequest


@pytest.fixture(autouse=True)
def model_context(monkeypatch):
    monkeypatch.setattr(llm_task, 'inject_model_config', lambda _: None)
    monkeypatch.setattr(llm_task, 'get_model_role_runtime_identity', lambda _: {'model': 'test'})


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
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_: model)
    result = llm_task.run_llm_task(request(llm_config={'llm': {'max_input_tokens': 32000}}))
    assert result.status == 'succeeded'
    assert result.output['intent_status'] == 'ready'
    assert len(calls) == 1
    assert calls[0][1]['max_retries'] == 1
    assert 'max_tokens' not in calls[0][1]
    assert 'max_completion_tokens' not in calls[0][1]
    assert result.usage['model_calls'] == 1


@pytest.mark.parametrize('raw', [
    output()[:-2], output(extra='not allowed'),
    '{"title":123,"initial_intent_summary":"x","intent_status":"ready","missing_context":[]}',
    '{"title":"x","initial_intent_summary":"x","intent_status":"empty","missing_context":[]}'])
def test_invalid_output_is_not_repaired_or_retried(monkeypatch, raw):
    calls = []

    def model(*_, **__):
        calls.append(1)
        return raw
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_: model)
    result = llm_task.run_llm_task(request())
    assert result.status == 'failed'
    assert result.error_code == 'invalid_output'
    assert not result.retryable
    assert len(calls) == 1


def test_long_input_is_forwarded_without_estimated_capacity_rejection(monkeypatch):
    text = '资料内容\n' * 20000 + '最后要求：计算年度销售额'
    req = LLMTaskRequest(task_type='conversation.describe_opening', input={'text': text},
                         llm_config={'llm': {'max_input_tokens': 4096}})
    seen = []

    def model(prompt, **_):
        seen.append(prompt)
        return output()
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **_: model)
    result = llm_task.run_llm_task(req)
    assert result.status == 'succeeded'
    assert text.replace('\n', '\\n') in seen[0]
    assert len(seen) == 1
