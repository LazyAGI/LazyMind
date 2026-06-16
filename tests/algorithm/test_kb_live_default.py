from types import SimpleNamespace

from lazymind.chat.engine.tools import kb


DEFAULT_AGENTIC_CONFIG = {
    'kb_id': 'ds_9e96150bb1ceeec7d96055638072b8a9',
}
SEED_KEYWORD = '铁路路基设计规范'


def test_kb_search_core_flow(monkeypatch):
    captured = {}

    def fake_search_kb(
        payload,
        *,
        retrievers,
        reranker,
        image_retriever,
        retriever_topk=20,
        rerank_topk=20,
        k_max=10,
        image_topk=3,
    ):
        captured.update({
            'payload': payload,
            'retrievers': retrievers,
            'image_retriever': image_retriever,
        })
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

    monkeypatch.setattr(kb, 'search_kb', fake_search_kb)
    monkeypatch.setattr(kb.KBToolGroup, '_ensure_search_runtime', lambda self: None)
    monkeypatch.setattr(kb.KBToolGroup, '_retrievers', ['retriever'])
    monkeypatch.setattr(kb.KBToolGroup, '_reranker', 'reranker')
    monkeypatch.setattr(kb.KBToolGroup, '_image_retriever', 'image-retriever')
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = {
        'filters': {'kb_id': DEFAULT_AGENTIC_CONFIG['kb_id']},
        'user_id': 'user-007',
    }
    try:
        result = kb.KBToolGroup().kb_search(SEED_KEYWORD)
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert captured == {
        'payload': {
            'query': SEED_KEYWORD,
            'filters': {'kb_id': DEFAULT_AGENTIC_CONFIG['kb_id']},
            'user_id': 'user-007',
        },
        'retrievers': ['retriever'],
        'image_retriever': 'image-retriever',
    }
    assert result['success'] is True
    assert result['tool'] == 'kb_search'
    assert result['result']['total'] == 1
    assert result['result']['items'][0]['docid'] == 'doc_be9d0c894bf623ffc82aa3f9a073fb96'


def test_kb_tmp_search_core_flow(monkeypatch):
    captured = {}

    def fake_search_temp_files(
        payload,
        *,
        tmp_retriever,
        reranker,
        retriever_topk=20,
        rerank_topk=20,
        k_max=10,
    ):
        captured.update({
            'payload': payload,
            'tmp_retriever': tmp_retriever,
        })
        return []

    monkeypatch.setattr(kb, 'search_temp_files', fake_search_temp_files)
    monkeypatch.setattr(kb.TempKBToolGroup, '_ensure_search_runtime', lambda self: None)
    monkeypatch.setattr(kb.TempKBToolGroup, '_tmp_retriever', 'tmp-retriever')
    monkeypatch.setattr(kb.TempKBToolGroup, '_reranker', 'reranker')
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = {'user_id': 'user-007'}
    try:
        result = kb.TempKBToolGroup().kb_tmp_search(SEED_KEYWORD, files=['tmp-a.md'])
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert captured == {
        'payload': {
            'query': SEED_KEYWORD,
            'filters': {},
            'files': ['tmp-a.md'],
            'user_id': 'user-007',
        },
        'tmp_retriever': 'tmp-retriever',
    }
    assert result['success'] is True
    assert result['tool'] == 'kb_tmp_search'


def test_temp_kb_runtime_registers_block_group(monkeypatch):
    calls = []

    class FakeTempDocRetriever:
        def __init__(self, embed):
            calls.append(('init', embed))

        def create_node_group(self, **kwargs):
            calls.append(('create_node_group', kwargs))
            return self

        def add_subretriever(self, group):
            calls.append(('add_subretriever', group))
            return self

    monkeypatch.setattr(kb, 'AutoModel', lambda model: f'model:{model}')
    monkeypatch.setattr(kb, 'TempDocRetriever', FakeTempDocRetriever)
    monkeypatch.setattr(kb, '_is_reranker_enabled', lambda: False)
    monkeypatch.setattr(kb.TempKBToolGroup, '_tmp_retriever', None)
    monkeypatch.setattr(kb.TempKBToolGroup, '_reranker', None)

    kb.TempKBToolGroup()._ensure_search_runtime()

    assert calls[0] == ('init', f'model:{kb.EMBED_MAIN}')
    assert calls[1][0] == 'create_node_group'
    assert calls[1][1]['name'] == 'block'
    assert calls[1][1]['display_name'] == 'paragraph slice'
    assert calls[1][1]['group_type'] == kb.NodeGroupType.CHUNK
    assert calls[2] == ('add_subretriever', 'block')
