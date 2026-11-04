"""Read and acknowledge the current Chat's authoritative paper handoff."""
from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.workflow.workflow_manager import _client
from lazymind.chat.service.academic_reading import academic_request, paper_read_payload, reading_observation


def _handoff_context(summary=False):
    api = _client()
    params = {'conversation_id': require_context().conversation_id}
    if summary:
        params['view'] = 'summary'
    return academic_request(api, 'GET', '/chat-context', params=params, timeout=30)


def academic_handoff_context():
    """Read the complete frozen business input for this Chat, without internal sources."""
    return _handoff_context()


def submit_handoff():
    """Acknowledge the server-owned frozen input without model-authored IDs."""
    context = _handoff_context(summary=True)
    api = _client()
    return academic_request(api, 'POST', '/mock-handoffs/' + context['handoff_id'] + ':submit', payload={}, timeout=30)


def academic_read_paper(paper_id: str = '', cursor: str = ''):
    """Read a selected paper's full text, or continue using its unchanged cursor."""
    payload = paper_read_payload(paper_id, cursor)
    context = _handoff_context(summary=True)
    return reading_observation(academic_request(
        _client(), 'POST', '/paper-contents:read', payload={**payload, 'handoff_id': context['handoff_id']}))
