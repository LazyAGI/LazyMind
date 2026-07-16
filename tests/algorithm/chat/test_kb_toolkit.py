import pytest

import lazyllm
from lazymind.chat.engine.tools.kb import KBToolkit


def test_kb_toolkit_is_available_without_selected_kb():
    lazyllm.globals['agentic_config'] = {'filters': {}}
    toolkit = KBToolkit()
    assert 'list_knowledge_bases' in toolkit.__public_apis__
    with pytest.raises(ValueError, match='kb_ids is required'):
        toolkit._kb_ids()


def test_explicit_kb_ids_override_request_selection():
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'request-kb'}}
    assert KBToolkit._kb_ids(['explicit-kb']) == ['explicit-kb']
    assert KBToolkit._kb_ids() == ['request-kb']
