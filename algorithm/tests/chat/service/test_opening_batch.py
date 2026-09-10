import json

import pytest

from lazymind.chat.service import llm_task
from lazymind.chat.service.llm_task import LLMTaskRequest


def item(cid, status='ready'):
    return {'id': cid, 'title': '发送邮件', 'initial_intent_summary': '用指定邮箱向收件人发送邮件',
            'intent_status': status, 'missing_context': []}


@pytest.mark.parametrize('outputs,valid', [
    ([item('1'), item('0')], True),
    ([item('0'), item('0')], False),
    ([item('0')], False),
    ([item('0'), item('2')], False),
])
def test_opening_batch_checks_partition_and_accepts_reordering(monkeypatch, outputs, valid):
    monkeypatch.setattr(llm_task, 'inject_model_config', lambda _: None)
    monkeypatch.setattr(llm_task, 'get_model_role_runtime_identity', lambda _: {})
    monkeypatch.setattr(llm_task, '_call_model', lambda *args, **kwargs: json.dumps({'items': outputs}))
    request = LLMTaskRequest(task_type='conversation.describe_opening_batch', input={'data': {
        'items': [{'id': str(i), 'input': {'messages': [{'role': 'user', 'content': '帮我发送邮件'}]}}
                  for i in range(2)]}})
    result = llm_task.run_llm_task(request)
    assert result.status == ('succeeded' if valid else 'failed')
    if valid:
        assert result.output['items'] == outputs
    else:
        assert result.error_code == 'invalid_output'
