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
    assert result['total'] == 1
    assert result['items'][0]['docid'] == 'doc_be9d0c894bf623ffc82aa3f9a073fb96'


def test_kb_tmp_search_core_flow(monkeypatch, tmp_path):
    notes = tmp_path / 'tmp-a.md'
    notes.write_text('omega clause\n', encoding='utf-8')
    monkeypatch.setattr(kb, '_tmp_agentic_config', lambda: {
        'user_id': 'user-007',
        'files': [str(notes)],
        'history_files_per_turn': {},
    })
    monkeypatch.setattr(
        'lazymind.chat.engine.tools.local_file.store.workspace_for_request',
        lambda *_args, **_kwargs: str(tmp_path),
    )

    result = kb.kb_tmp_search(grep_patterns=['omega'])

    assert result['success'] is True
    assert result['result']['total'] == 1
    assert result['result']['hits'][0]['target'] == 'tmp-a.md'


def test_temp_kb_runtime_registers_block_group(monkeypatch):
    from lazymind.chat.engine.tools.algo import search_temp

    calls = []

    class FakeTempDocRetriever:
        def __init__(self, embed):
            calls.append(('init', embed))

        def create_node_group(self, **kwargs):
            calls.append(('create_node_group', kwargs))
            return self

        def add_subretriever(self, group, **kwargs):
            calls.append(('add_subretriever', group, kwargs))
            return self

    monkeypatch.setattr(search_temp, 'AutoModel', lambda model: f'model:{model}')
    monkeypatch.setattr(search_temp, 'TempDocRetriever', FakeTempDocRetriever)
    monkeypatch.setattr(search_temp, '_vector_retriever', None)
    monkeypatch.setattr(search_temp, '_bm25_retriever', None)

    search_temp._make_retriever(use_embed=True)

    assert calls[0] == ('init', f'model:{search_temp.EMBED_MAIN}')
    assert calls[1][0] == 'create_node_group'
    assert calls[1][1]['name'] == 'block'
    assert calls[2][0] == 'add_subretriever'
    assert calls[2][1] == 'block'
