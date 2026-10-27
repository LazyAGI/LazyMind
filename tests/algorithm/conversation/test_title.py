import json

import pytest
from lazymind.conversation.schemas import BatchTitleRequest, TitleRequest
from lazymind.conversation.conversation_title import generate_title, generate_titles


def request(**kwargs):
    kwargs.setdefault('llm_config', {'llm': {'source': 'openai', 'model': 'test'}})
    return TitleRequest(input={'text': '为 LazyMind 设计对话整理'}, **kwargs)


def output(**kwargs):
    return json.dumps(dict(title='对话整理方案', initial_intent_summary='为 LazyMind 设计对话整理方案。',
                           intent_status='ready', missing_context=[], **kwargs), ensure_ascii=False)


@pytest.mark.parametrize('reasoning', ['', '识别用户的主要任务。\n保留必要限定。'])
def test_complete_output_and_single_call(provider, provider_message, reasoning):
    config, calls = provider
    config['llm']['max_input_tokens'] = 32000
    provider_message.update(content=output(), reasoning_content=reasoning)
    result = generate_title(request(llm_config=config))
    assert result.status == 'succeeded'
    assert result.output['intent_status'] == 'ready'
    assert len(calls) == 1
    assert calls[0]['response_format'] == {'type': 'json_object'}
    assert 'max_tokens' not in calls[0]
    assert 'max_completion_tokens' not in calls[0]
    assert result.usage['model_calls'] == 1


@pytest.mark.parametrize('reasoning', ['', '准备输出摘要。'])
@pytest.mark.parametrize('raw', [
    output()[:-2], output(extra='not allowed'),
    '{"title":123,"initial_intent_summary":"x","intent_status":"ready","missing_context":[]}',
    '{"title":"x","initial_intent_summary":"x","intent_status":"empty","missing_context":[]}',
    '<think>正文中的非 JSON 前缀。</think>' + output(),
])
def test_invalid_output_is_not_repaired_or_retried(provider, provider_message, raw, reasoning):
    config, calls = provider
    provider_message.update(content=raw, reasoning_content=reasoning)
    result = generate_title(request(llm_config=config))
    assert result.status == 'failed'
    assert result.error_code == 'invalid_output'
    assert not result.retryable
    assert len(calls) == 1


@pytest.mark.parametrize('message', [
    {'reasoning_content': '只有思考，没有正文'},
    {'reasoning_content': '只有思考，没有正文', 'content': None},
    {'reasoning_content': '只有思考，没有正文', 'content': ''},
])
def test_missing_or_empty_content_is_invalid_output(provider, provider_message, message):
    config, calls = provider
    provider_message.update(message)

    result = generate_title(request(llm_config=config))

    assert result.status == 'failed'
    assert result.error_code == 'invalid_output'
    assert not result.retryable
    assert len(calls) == 1


def test_batch_titles_accept_reasoning_without_changing_summary_text(provider, provider_message):
    config, calls = provider
    summary = '解释 <think>示例</think> 标签的用途。'
    item = json.loads(output())
    item['initial_intent_summary'] = summary
    provider_message.update(
        content=json.dumps({'items': [dict(item, id='0'), dict(item, id='1')]}, ensure_ascii=False),
        reasoning_content='分析任务，可能的示例是 {"items":[]}，正文会讨论 </think> 标签。',
    )

    result = generate_titles(BatchTitleRequest(
        items=[{'id': str(i), 'input': {'text': '解释 think 标签'}} for i in range(2)],
        llm_config=config,
    ))

    assert result.status == 'succeeded'
    assert [item['id'] for item in result.output['items']] == ['0', '1']
    assert all(item['initial_intent_summary'] == summary for item in result.output['items'])
    assert result.usage['model_calls'] == 1
    assert len(calls) == 1


def test_long_input_is_forwarded_without_estimated_capacity_rejection(provider, provider_message):
    config, calls = provider
    config['llm']['max_input_tokens'] = 4096
    provider_message['content'] = output()
    text = '资料内容\n' * 20000 + '最后要求：计算年度销售额'
    req = TitleRequest(input={'text': text},
                       llm_config=config)
    result = generate_title(req)
    assert result.status == 'succeeded'
    assert len(calls) == 1
    assert text.replace('\n', '\\n') in '\n'.join(message['content'] for message in calls[0]['messages'])
