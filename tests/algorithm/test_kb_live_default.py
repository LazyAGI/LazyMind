from types import SimpleNamespace

from lazymind.chat.engine.tools import kb


DEFAULT_AGENTIC_CONFIG = {
    'kb_id': 'ds_9e96150bb1ceeec7d96055638072b8a9',
    'kb_api_key': 'test-key',
}
SEED_KEYWORD = '铁路路基设计规范'


def test_kb_search_default_kb_branch(monkeypatch):
    calls = []

    def fake_ppl_search(payload, *, document, retriever_topk=20, rerank_topk=20, k_max=10):
        calls.append(
            {
                'document': document,
                'retriever_topk': retriever_topk,
                'rerank_topk': rerank_topk,
                'k_max': k_max,
            }
        )

        calls.append({'payload': payload})
        return [
            SimpleNamespace(
                uid='seed-node',
                number=3,
                group='block',
                _parent='parent-node',
                relevance_score=0.9,
                text='铁路路基设计规范',
                metadata={'file_name': '39-铁路路基设计规范  TB10001-2016.pdf'},
                global_metadata={
                    'docid': 'doc_be9d0c894bf623ffc82aa3f9a073fb96',
                    'kb_id': DEFAULT_AGENTIC_CONFIG['kb_id'],
                },
            )
        ]

    monkeypatch.setattr(kb, 'ppl_search', fake_ppl_search)
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = DEFAULT_AGENTIC_CONFIG
    try:
        result = kb.kb().search(SEED_KEYWORD)
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert calls[0] == {
        'document': kb._DEFAULT_KB_DOCUMENT,
        'retriever_topk': 20,
        'rerank_topk': 20,
        'k_max': 10,
    }
    assert calls[1] == {
        'payload': {
            'query': SEED_KEYWORD,
            'filters': {'kb_id': DEFAULT_AGENTIC_CONFIG['kb_id']},
            'files': [],
            'image_files': [],
            'user_id': '',
        }
    }
    assert result['success'] is True
    assert result['tool'] == 'kb_search'
    assert result['result']['total'] == 1
    assert result['result']['items'][0]['docid'] == 'doc_be9d0c894bf623ffc82aa3f9a073fb96'


def test_kb_search_forwards_explicit_retriever_and_rerank_topk(monkeypatch):
    calls = []

    def fake_ppl_search(payload, *, document, retriever_topk=20, rerank_topk=20, k_max=10):
        calls.append(
            {
                'document': document,
                'retriever_topk': retriever_topk,
                'rerank_topk': rerank_topk,
                'k_max': k_max,
            }
        )
        calls.append({'payload': payload})
        return []

    monkeypatch.setattr(kb, 'ppl_search', fake_ppl_search)
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = DEFAULT_AGENTIC_CONFIG
    try:
        result = kb.kb().search(SEED_KEYWORD, retriever_topk=9, rerank_topk=4, k_max=3)
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert calls[0] == {
        'document': kb._DEFAULT_KB_DOCUMENT,
        'retriever_topk': 9,
        'rerank_topk': 4,
        'k_max': 3,
    }
    assert result['success'] is True
    assert result['tool'] == 'kb_search'


def test_kb_get_parent_node_by_uid(monkeypatch):
    calls = []
    document_kwargs = {}

    class FakeNode:
        def __init__(self, uid, number, group, parent, text, docid, kb_id):
            self.uid = uid
            self.number = number
            self.group = group
            self._parent = parent
            self.text = text
            self.metadata = {}
            self.global_metadata = {'docid': docid, 'kb_id': kb_id}

    class FakeDocument:
        def __init__(self, **kwargs):
            document_kwargs.update(kwargs)

        def get_nodes(self, uids=None, doc_ids=None, group=None, kb_id=None, numbers=None):
            calls.append({
                'uids': uids,
                'doc_ids': doc_ids,
                'group': group,
                'kb_id': kb_id,
                'numbers': numbers,
            })
            nodes = {
                'child-node': FakeNode('child-node', 7, 'line', 'parent-node', 'child text', 'doc-1', DEFAULT_AGENTIC_CONFIG['kb_id']),
                'parent-node': FakeNode('parent-node', 3, 'block', None, 'parent text', 'doc-1', DEFAULT_AGENTIC_CONFIG['kb_id']),
            }
            uid = uids[0] if uids else None
            node = nodes.get(uid)
            return [node] if node else []

    monkeypatch.setattr(kb.lazyllm.tools.rag, 'Document', lambda **kwargs: FakeDocument(**kwargs))
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = DEFAULT_AGENTIC_CONFIG
    try:
        result = kb.kb().get_parent_node('child-node')
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert result['success'] is True
    assert result['tool'] == 'kb_get_parent_node'
    assert result['result']['node_id'] == 'child-node'
    assert result['result']['parent_id'] == 'parent-node'
    assert result['result']['current_node']['uid'] == 'child-node'
    assert result['result']['total'] == 1
    assert result['result']['items'][0]['uid'] == 'parent-node'
    assert result['result']['items'][0]['text'] == 'parent text'
    assert document_kwargs == {
        'url': kb._DEFAULT_KB_URL,
        'name': kb._DEFAULT_KB_NAME,
    }
    assert calls == [
        {
            'uids': ['child-node'],
            'doc_ids': None,
            'group': None,
            'kb_id': DEFAULT_AGENTIC_CONFIG['kb_id'],
            'numbers': None,
        },
        {
            'uids': ['parent-node'],
            'doc_ids': None,
            'group': None,
            'kb_id': DEFAULT_AGENTIC_CONFIG['kb_id'],
            'numbers': None,
        },
    ]


if __name__ == '__main__':
    test_kb_search_default_kb_branch()
