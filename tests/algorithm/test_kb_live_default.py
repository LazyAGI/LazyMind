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
    monkeypatch.setattr(
        kb,
        '_ensure_kb_search_runtime',
        lambda: (['retriever'], 'reranker', 'image-retriever'),
    )
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = {
        'filters': {'kb_id': DEFAULT_AGENTIC_CONFIG['kb_id']},
        'user_id': 'user-007',
    }
    try:
        result = kb.KBToolkit().kb_search(SEED_KEYWORD)
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert captured == {
        'payload': {
            'query': SEED_KEYWORD,
            'filters': {'kb_id': [DEFAULT_AGENTIC_CONFIG['kb_id']]},
            'user_id': 'user-007',
        },
        'retrievers': ['retriever'],
        'image_retriever': 'image-retriever',
    }
    assert result['success'] is True
    assert result['tool'] == 'kb_search'
    assert result['result']['total'] == 1
    assert result['result']['items'][0]['docid'] == 'doc_be9d0c894bf623ffc82aa3f9a073fb96'
