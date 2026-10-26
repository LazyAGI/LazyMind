"""Use the current Chat's authoritative handoff, without model-authored IDs."""
from lazymind.chat.workflow.workflow_manager import _client
from lazymind.chat.engine.subagent.context import require_context


def academic_handoff_context():
    """Read the complete original paper handoff and requirements for this Chat."""
    api = _client()
    response = api.transport.request('GET', api.base_url + '/academic/chat-context', headers=api._headers(), params={'conversation_id': require_context().conversation_id}, timeout=30)
    response.raise_for_status()
    data = response.json()
    return data.get('data', data)


def academic_handoff_submit():
    """Submit and publish the frozen handoff; return its handoff receipt."""
    context = academic_handoff_context()
    api = _client()
    response = api.transport.request('POST', api.base_url + '/academic/mock-handoffs/' + context['handoff_id'] + ':submit', headers=api._headers(), json={}, timeout=30)
    response.raise_for_status()
    data = response.json()
    result = data.get('data', data)
    from lazymind.chat.engine.subagent.tools import _save_artifact
    import json
    if 'handoff_receipt' in require_context().output_slots:
        body = '## 任务交接\n\n' + result['feedback'] + '\n\n```json\n' + json.dumps(result, ensure_ascii=False, indent=2) + '\n```'
        saved = _save_artifact('handoff_receipt', body, content_type='text', source_tool='academic_handoff_submit', internal_publish=True)
    elif 'ppt_request' in require_context().output_slots:
        saved = _save_artifact('ppt_request', result, content_type='json', source_tool='academic_handoff_submit', internal_publish=True)
    else:
        raise ValueError('this step has no declared handoff output')
    return {'status': result.get('status', 'awaiting_downstream'), 'kind': result['kind'], 'paper_count': len(result['input']['papers']),
            'feedback': result['feedback'], 'artifact': saved}


def academic_read_paper(paper_id: str = '', cursor: str = ''):
    """Read a selected paper's full text, or continue using its unchanged cursor."""
    if bool(paper_id) == bool(cursor):
        raise ValueError('Supply paper_id or cursor, but not both')
    context = academic_handoff_context()
    payload = {'handoff_id': context['handoff_id']}
    payload.update({'cursor': cursor} if cursor else {'paper_id': paper_id})
    api = _client()
    response = api.transport.request('POST', api.base_url + '/academic/paper-contents:read',
                                     headers=api._headers(), json=payload, timeout=90)
    response.raise_for_status()
    data = response.json()
    return data.get('data', data)
