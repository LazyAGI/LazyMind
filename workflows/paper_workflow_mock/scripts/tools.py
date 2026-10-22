"""Use the current Chat's authoritative handoff, without model-authored IDs."""
from lazymind.chat.workflow.workflow_manager import _client
from lazymind.chat.engine.subagent.context import require_context


def academic_mock_context():
    """Read the complete original paper handoff and requirements for this Chat."""
    api = _client()
    response = api.transport.request('GET', api.base_url + '/academic/chat-context', headers=api._headers(), params={'conversation_id': require_context().conversation_id}, timeout=30)
    response.raise_for_status()
    data = response.json()
    return data.get('data', data)


def academic_mock_submit():
    """Submit and publish the frozen handoff; return a brief Mock receipt."""
    context = academic_mock_context()
    api = _client()
    response = api.transport.request('POST', api.base_url + '/academic/mock-handoffs/' + context['handoff_id'] + ':submit', headers=api._headers(), json={}, timeout=30)
    response.raise_for_status()
    data = response.json()
    result = data.get('data', data)
    from lazymind.chat.engine.subagent.tools import _save_artifact
    import json
    if 'mock_feedback' in require_context().output_slots:
        body = '## Mock 接口反馈\n\n' + result['feedback'] + '\n\n```json\n' + json.dumps(result, ensure_ascii=False, indent=2) + '\n```'
        saved = _save_artifact('mock_feedback', body, content_type='text', source_tool='academic_mock_submit', internal_publish=True)
    elif 'ppt_request' in require_context().output_slots:
        saved = _save_artifact('ppt_request', result, content_type='json', source_tool='academic_mock_submit', internal_publish=True)
    else:
        raise ValueError('this step has no declared Mock output')
    return {'mock': True, 'kind': result['kind'], 'paper_count': len(result['input']['papers']),
            'feedback': result['feedback'], 'artifact': saved}
