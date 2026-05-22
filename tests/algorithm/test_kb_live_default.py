from types import SimpleNamespace

from chat.tools import kb


DEFAULT_AGENTIC_CONFIG = {
    'kb_url': 'http://10.119.24.129:8056',
    'kb_name': 'general_algo',
    'kb_id': 'ds_9e96150bb1ceeec7d96055638072b8a9',
    'es_url': 'https://10.119.24.129:9200',
    'es_user': 'admin',
    'es_password': 'LazyRAG_OpenSearch123!',
}
SEED_KEYWORD = '铁路路基设计规范'


def test_kb_search_default_kb_branch(monkeypatch):
    calls = []

    def fake_get_ppl_search(url, retriever_configs=None, topk=20, k_max=10):
        calls.append(
            {
                'url': url,
                'retriever_configs': retriever_configs,
                'topk': topk,
                'k_max': k_max,
            }
        )

        def fake_search(payload):
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

        return fake_search

    monkeypatch.setattr(kb, 'get_ppl_search', fake_get_ppl_search)
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = DEFAULT_AGENTIC_CONFIG
    try:
        result = kb.kb_search(SEED_KEYWORD)
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert calls[0] == {
        'url': f"{DEFAULT_AGENTIC_CONFIG['kb_url']},{DEFAULT_AGENTIC_CONFIG['kb_name']}",
        'retriever_configs': None,
        'topk': 20,
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
    assert result['total'] == 1
    assert result['items'][0]['docid'] == 'doc_be9d0c894bf623ffc82aa3f9a073fb96'


def test_kb_get_parent_node_by_node_id(monkeypatch):
    calls = []

    def fake_opensearch_search(index, body, config):
        calls.append({'index': index, 'body': body, 'config': config})
        node_id = body['query']['bool']['must'][0]['bool']['should'][0]['ids']['values'][0]
        sources = {
            'child-node': {
                'uid': 'child-node',
                'number': 7,
                'group': 'line',
                'parent': 'parent-node',
                'doc_id': 'doc-1',
                'kb_id': DEFAULT_AGENTIC_CONFIG['kb_id'],
                'content': 'child text',
            },
            'parent-node': {
                'uid': 'parent-node',
                'number': 3,
                'group': 'block',
                'parent': None,
                'doc_id': 'doc-1',
                'kb_id': DEFAULT_AGENTIC_CONFIG['kb_id'],
                'content': 'parent text',
            },
        }
        source = sources.get(node_id)
        return {'hits': {'hits': [{'_id': node_id, '_source': source}] if source else []}}

    monkeypatch.setattr(kb, '_opensearch_search', fake_opensearch_search)
    original_config = kb.lazyllm.globals.get('agentic_config')
    kb.lazyllm.globals['agentic_config'] = DEFAULT_AGENTIC_CONFIG
    try:
        result = kb.kb_get_parent_node('child-node')
    finally:
        kb.lazyllm.globals['agentic_config'] = original_config or {}

    assert result['node_id'] == 'child-node'
    assert result['parent_id'] == 'parent-node'
    assert result['current_node']['uid'] == 'child-node'
    assert result['total'] == 1
    assert result['items'][0]['uid'] == 'parent-node'
    assert result['items'][0]['text'] == 'parent text'
    assert [call['index'] for call in calls] == ['col_block', 'col_block']


if __name__ == '__main__':
    test_kb_search_default_kb_branch()
