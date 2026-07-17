import pytest

import lazyllm
from lazymind.chat.engine.tools.kb import KBToolkit


def test_kb_toolkit_is_available_without_selected_kb():
    lazyllm.globals['agentic_config'] = {'filters': {}}
    toolkit = KBToolkit()
    assert 'list_knowledge_bases' in toolkit.__public_apis__
    with pytest.raises(ValueError, match='kb_ids is required'):
        toolkit._kb_ids()


def test_explicit_kb_ids_override_request_selection(monkeypatch):
    calls = []

    def fake_get_core_api(path, params=None):
        calls.append((path, params))
        return {
            'datasets': [
                {'dataset_id': 'explicit-kb'},
                {'dataset_id': 'request-kb'},
            ],
            'next_page_token': '',
        }

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.get_core_api', fake_get_core_api)
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'request-kb'}}
    assert KBToolkit._kb_ids(['explicit-kb']) == ['explicit-kb']
    assert KBToolkit._kb_ids() == ['request-kb']
    assert len(calls) == 1


def test_kb_ids_load_all_catalog_pages_and_cache_result(monkeypatch):
    calls = []

    def fake_get_core_api(path, params=None):
        calls.append((path, params))
        if not params.get('page_token'):
            return {
                'datasets': [{'dataset_id': 'kb-first'}],
                'next_page_token': 'page-2',
            }
        return {
            'datasets': [{'dataset_id': 'kb-second'}],
            'next_page_token': '',
        }

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.get_core_api', fake_get_core_api)
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'kb-second'}}

    assert KBToolkit._kb_ids() == ['kb-second']
    assert KBToolkit._kb_ids(['kb-first']) == ['kb-first']
    assert len(calls) == 2


def test_kb_ids_reject_unavailable_id(monkeypatch):
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.kb.get_core_api',
        lambda path, params=None: {
            'datasets': [{'dataset_id': 'readable-kb'}],
            'next_page_token': '',
        },
    )
    lazyllm.globals['agentic_config'] = {'filters': {}}

    with pytest.raises(ValueError, match='requested knowledge bases are unavailable'):
        KBToolkit._kb_ids(['unreadable-kb'])
