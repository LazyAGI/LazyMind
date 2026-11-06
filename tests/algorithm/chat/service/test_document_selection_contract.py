"""Selection explanations may need more of the active paper than one paragraph."""

from lazymind.chat.service import chat_service


def test_quick_explain_can_read_only_the_active_paper_for_missing_context():
    contract = getattr(chat_service, '_document_selection_contract', None)
    assert callable(contract)

    text = contract({'expand_paper_context': True})

    assert 'Selected text' in text
    assert 'Surrounding passage' in text
    assert 'KBToolkit_read_document' in text
    assert 'current document' in text
    assert 'without searching the knowledge base' not in text


def test_ordinary_selection_keeps_direct_answer_contract():
    contract = getattr(chat_service, '_document_selection_contract', None)
    assert callable(contract)

    text = contract({'expand_paper_context': 'true'})

    assert 'without searching the knowledge base' in text
    assert 'KBToolkit_read_document' not in text
