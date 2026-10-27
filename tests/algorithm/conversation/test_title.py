import json

import pytest
from lazymind.conversation import model_client
from lazymind.conversation.schemas import BatchTitleRequest, TitleRequest
from lazymind.conversation.conversation_title import generate_title, generate_titles


@pytest.fixture(autouse=True)
def model_context(monkeypatch):
    monkeypatch.setattr(model_client, 'inject_model_config', lambda _: None)


def request(**kwargs):
    kwargs.setdefault('llm_config', {'llm': {'source': 'openai', 'model': 'test'}})
    return TitleRequest(input={'text': '为 LazyMind 设计对话整理'}, **kwargs)


def output(**kwargs):
    return json.dumps(dict(title='对话整理方案', initial_intent_summary='为 LazyMind 设计对话整理方案。',
                           intent_status='ready', missing_context=[], **kwargs), ensure_ascii=False)


@pytest.mark.parametrize('prefix', ['', '<think>识别用户的主要任务。\n保留必要限定。</think>'])
def test_complete_output_and_single_call(monkeypatch, prefix):
    calls = []

    def model(prompt, **options):
        calls.append((prompt, options))
        return prefix + output()
    monkeypatch.setattr(model_client, 'AutoModel', lambda **_: model)
    result = generate_title(
        request(llm_config={'llm': {'source': 'openai', 'model': 'test', 'max_input_tokens': 32000}}))
    assert result.status == 'succeeded'
    assert result.output['intent_status'] == 'ready'
    assert len(calls) == 1
    assert calls[0][1]['max_retries'] == 1
    assert 'max_tokens' not in calls[0][1]
    assert 'max_completion_tokens' not in calls[0][1]
    assert result.usage['model_calls'] == 1


@pytest.mark.parametrize('prefix', ['', '<think>准备输出摘要。</think>'])
@pytest.mark.parametrize('raw', [
    output()[:-2], output(extra='not allowed'),
    '{"title":123,"initial_intent_summary":"x","intent_status":"ready","missing_context":[]}',
    '{"title":"x","initial_intent_summary":"x","intent_status":"empty","missing_context":[]}'])
def test_invalid_output_is_not_repaired_or_retried(monkeypatch, raw, prefix):
    calls = []

    def model(*_, **__):
        calls.append(1)
        return prefix + raw
    monkeypatch.setattr(model_client, 'AutoModel', lambda **_: model)
    result = generate_title(request())
    assert result.status == 'failed'
    assert result.error_code == 'invalid_output'
    assert not result.retryable
    assert len(calls) == 1


def test_batch_titles_accept_reasoning_without_changing_summary_text(monkeypatch):
    summary = '解释 <think>示例</think> 标签的用途。'
    item = json.loads(output())
    item['initial_intent_summary'] = summary
    raw = '<think>分析任务，可能的示例是 {"items":[]}。</think>' + json.dumps(
        {'items': [dict(item, id='0'), dict(item, id='1')]}, ensure_ascii=False)
    monkeypatch.setattr(model_client, 'AutoModel', lambda **_: lambda *_, **__: raw)

    result = generate_titles(BatchTitleRequest(
        items=[{'id': str(i), 'input': {'text': '解释 think 标签'}} for i in range(2)],
        llm_config={'llm': {'source': 'openai', 'model': 'test'}},
    ))

    assert result.status == 'succeeded'
    assert [item['id'] for item in result.output['items']] == ['0', '1']
    assert all(item['initial_intent_summary'] == summary for item in result.output['items'])
    assert result.usage['model_calls'] == 1


def test_long_input_is_forwarded_without_estimated_capacity_rejection(monkeypatch):
    text = '资料内容\n' * 20000 + '最后要求：计算年度销售额'
    req = TitleRequest(input={'text': text},
                       llm_config={'llm': {'source': 'openai', 'model': 'test', 'max_input_tokens': 4096}})
    seen = []

    def model(prompt, **_):
        seen.append(prompt)
        return output()
    monkeypatch.setattr(model_client, 'AutoModel', lambda **_: model)
    result = generate_title(req)
    assert result.status == 'succeeded'
    assert text.replace('\n', '\\n') in seen[0]
    assert len(seen) == 1
