import pytest
from types import SimpleNamespace

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


def test_request_selected_kb_ids_skip_catalog_validation(monkeypatch):
    def unexpected_get_core_api(path, params=None):
        raise AssertionError('request-selected knowledge bases should not reload the catalog')

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.get_core_api', unexpected_get_core_api)
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'request-kb'}}

    assert KBToolkit._kb_ids() == ['request-kb']


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
    lazyllm.globals['agentic_config'] = {'filters': {}}

    assert KBToolkit._kb_ids(['kb-second']) == ['kb-second']
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


def _node(uid, *, parent=None, number=1, kb_id='kb-one', docid='doc-one'):
    return SimpleNamespace(
        uid=uid,
        text=f'text-{uid}',
        metadata={},
        global_metadata={'kb_id': kb_id, 'docid': docid},
        group='block',
        number=number,
        _parent=parent,
    )


def test_parent_node_derives_kb_id_from_target_node(monkeypatch):
    current = _node('node-one', parent='parent-one')
    parent = _node('parent-one')
    calls = []

    class FakeDocument:
        def get_nodes(self, **kwargs):
            calls.append(kwargs)
            return [current] if kwargs.get('uids') == ['node-one'] else [parent]

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.DOCUMENT', FakeDocument())

    result = KBToolkit().kb_get_parent_node('node-one')

    assert result['result']['items'][0]['uid'] == 'parent-one'
    assert calls == [
        {'uids': ['node-one']},
        {'uids': ['parent-one'], 'kb_id': 'kb-one'},
    ]


def test_window_nodes_derive_scope_and_position_from_target_node(monkeypatch):
    seed = _node('node-one', number=3)
    previous = _node('node-zero', number=2)
    following = _node('node-two', number=4)
    calls = []

    class FakeDocument:
        def get_nodes(self, **kwargs):
            calls.append(('get_nodes', kwargs))
            return [seed]

        def get_window_nodes(self, node, span, merge):
            calls.append(('get_window_nodes', node.uid, span, merge))
            return [previous, seed, following]

    monkeypatch.setattr('lazymind.chat.engine.tools.kb.DOCUMENT', FakeDocument())

    result = KBToolkit().kb_get_window_nodes('node-one', before=1, after=1)

    assert [item['uid'] for item in result['result']['items']] == [
        'node-zero', 'node-one', 'node-two',
    ]
    assert calls == [
        ('get_nodes', {'uids': ['node-one']}),
        ('get_window_nodes', 'node-one', (-1, 1), False),
    ]
