"""An open paper is supplied input even when it is not a chat upload."""
import pytest

from lazymind.chat.engine.prompts.task_profile import resolve_task_profile
from lazymind.chat.service.chat_request import ChatRequest
from lazymind.chat.service.chat_service import _task_profile_inputs


@pytest.mark.parametrize('context,surface,available', [
    ({'dataset_id': 'kb-1', 'document_id': 'doc-1'}, 'knowledge_document_preview', True),
    ({'dataset_id': 'kb-1'}, 'knowledge_document_preview', False),
    ({'document_id': 'doc-1'}, 'knowledge_document_preview', False),
    ({'dataset_id': 'kb-1', 'document_id': '  '}, 'knowledge_document_preview', False),
    ({'dataset_id': 'kb-1', 'document_id': ['doc-1']}, 'knowledge_document_preview', False),
    ({'dataset_id': 'kb-1', 'document_id': 'doc-1'}, '', False),
])
def test_preview_document_is_available_to_request_assessment(context, surface, available):
    request = ChatRequest.model_validate({
        'message': {'query': '请用两句话概括这篇论文的核心创新，并给出原文依据。'},
        'conversation': {'surface': surface},
        'retrieval': {'filters': {'kb_id': 'kb-1', 'docid': 'doc-1'}},
        'document_context': context,
    })
    inputs = _task_profile_inputs(request)
    assert inputs['has_attachments'] is available
    profile = resolve_task_profile(**inputs, enable_llm_fallback=False)
    missing = [issue for issue in profile.request_assessment.issues if issue.issue_type == 'missing_input']
    assert bool(missing) is not available
