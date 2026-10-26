from types import SimpleNamespace

import pytest

from lazymind.chat.engine.tools.infra import kb_ownership


def test_ownership_filters_text_images_and_missing_ids(monkeypatch):
    calls = []
    def authorize(path, payload, **kwargs):
        calls.append((path, payload, kwargs))
        return {'response': {'items': [{'knowledge_id': 'kb', 'document_id': 'active'}]}}
    monkeypatch.setattr(kb_ownership, 'post_core_api', authorize)
    nodes = [
        SimpleNamespace(global_metadata={'kb_id': 'kb', 'docid': 'active'}, text='current'),
        SimpleNamespace(global_metadata={'kb_id': 'kb', 'docid': 'deleted'}, text='old'),
        {'global_metadata': {'kb_id': 'kb', 'docid': 'deleted'}, 'group': 'image'},
        {'text': 'unowned'},
    ]
    assert kb_ownership.filter_active_nodes(nodes, user_id='owner') == [nodes[0]]
    assert calls[0][0] == '/documents:filterActive'
    assert calls[0][2] == {'user_id': 'owner'}
    assert len(calls[0][1]['items']) == 2


def test_ownership_failure_is_not_permissive(monkeypatch):
    def unavailable(*args, **kwargs):
        raise RuntimeError('backfill unavailable')
    monkeypatch.setattr(kb_ownership, 'post_core_api', unavailable)
    with pytest.raises(RuntimeError, match='backfill unavailable'):
        kb_ownership.filter_active_nodes([{'kb_id': 'kb', 'doc_id': 'doc'}])


def test_ownership_is_rechecked_after_delete_not_cached(monkeypatch):
    response = {'response': {'items': [{'knowledge_id': 'kb', 'document_id': 'doc'}]}}
    monkeypatch.setattr(kb_ownership, 'post_core_api', lambda *a, **k: response)
    nodes = [{'kb_id': 'kb', 'doc_id': 'doc'}]
    assert kb_ownership.filter_active_nodes(nodes) == nodes
    response['response']['items'] = []
    assert kb_ownership.filter_active_nodes(nodes) == []
