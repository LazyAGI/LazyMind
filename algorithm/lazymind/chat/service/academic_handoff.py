"""Read and acknowledge the current Chat's authoritative paper handoff."""
from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.workflow.workflow_manager import _client


def _handoff_context(summary=False):
    api = _client()
    params = {'conversation_id': require_context().conversation_id}
    if summary:
        params['view'] = 'summary'
    response = api.transport.request(
        'GET', api.base_url + '/academic/chat-context', headers=api._headers(),
        params=params, timeout=30)
    response.raise_for_status()
    data = response.json()
    return data.get('data', data)


def academic_handoff_context():
    """Read the complete frozen business input for this Chat, without internal sources."""
    return _handoff_context()


def submit_handoff():
    """Acknowledge the server-owned frozen input without model-authored IDs."""
    context = _handoff_context(summary=True)
    api = _client()
    response = api.transport.request(
        'POST', api.base_url + '/academic/mock-handoffs/' + context['handoff_id'] + ':submit',
        headers=api._headers(), json={}, timeout=30)
    response.raise_for_status()
    data = response.json()
    return data.get('data', data)


def academic_read_paper(paper_id: str = '', cursor: str = ''):
    """Read a selected paper's full text, or continue using its unchanged cursor."""
    if bool(paper_id) == bool(cursor):
        raise ValueError('Supply paper_id or cursor, but not both')
    context = _handoff_context(summary=True)
    payload = {'handoff_id': context['handoff_id']}
    payload.update({'cursor': cursor} if cursor else {'paper_id': paper_id})
    api = _client()
    response = api.transport.request(
        'POST', api.base_url + '/academic/paper-contents:read',
        headers=api._headers(), json=payload, timeout=90)
    response.raise_for_status()
    data = response.json()
    return data.get('data', data)
