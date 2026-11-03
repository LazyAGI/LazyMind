"""Adapter and retrieval contracts; all HTTP is replaced at the transport edge."""
import importlib
from threading import RLock

import lazyllm
import pytest
from lazyllm.tools.rag import DocNode

from lazymind.chat.model_probe.runtime_support import RUNTIME_TYPES_BY_SOURCE, supports_runtime_type
from lazymind.model_config import get_dynamic_role_slot_map, inject_model_config


@pytest.fixture(autouse=True)
def disable_unrelated_trace_export():
    # These tests exercise inference transports, not a telemetry backend.
    importlib.import_module('lazyllm.tracing.collect.configs')
    enabled = lazyllm.config['trace_enabled']
    lazyllm.config['trace_enabled'] = False
    try:
        yield
    finally:
        lazyllm.config['trace_enabled'] = enabled


@pytest.mark.parametrize(('kind', 'registry'), [
    ('llm', 'chat'), ('vlm', 'chat'), ('embed', 'embed'), ('rerank', 'rerank'),
    ('text2image', 'text2image'), ('text2video', 'text2video'), ('tts', 'tts'), ('stt', 'stt'),
])
def test_support_table_matches_actual_registered_adapters(kind, registry):
    registered = getattr(lazyllm.online, registry)
    for source in RUNTIME_TYPES_BY_SOURCE:
        assert supports_runtime_type(source, kind) == (source in registered), (source, kind)


@pytest.mark.parametrize(('source', 'root', 'suffix'), [
    ('openai', '/v1/', '/v1/rerank'),
    ('qwen', '/', '/api/v1/services/rerank/text-rerank/text-rerank'),
    ('glm', '/api/paas/v4/', '/api/paas/v4/rerank'),
    ('siliconflow', '/v1/', '/v1/rerank'),
    ('aiping', '/api/v1/', '/api/v1/rerank'),
    ('siliconflow', '/v1', '/v1/rerank'),
    ('siliconflow', '/gateway/v3/', '/gateway/v3/rerank'),
    ('siliconflow', '/gateway/v4', '/gateway/v4/rerank'),
    ('siliconflow', '/tenant%2Fname/v4/', '/tenant%2Fname/v4/rerank'),
    ('siliconflow', '/gateway/v4/rerank', '/gateway/v4/rerank'),
    ('siliconflow', '/gateway/v4/rerank/', '/gateway/v4/rerank'),
])
@pytest.mark.parametrize('consumer', ['knowledge', 'temporary'])
def test_configured_reranker_reorders_real_retrieval_nodes(monkeypatch, tmp_path, source, root, suffix, consumer):
    from lazymind.chat.engine.tools import kb
    from lazymind.chat.engine.tools.algo import search_temp

    search_kb = importlib.import_module('lazymind.chat.engine.tools.algo.search_kb')
    config = tmp_path / 'runtime_models.yaml'
    config.write_text('reranker:\n  source: dynamic\n  dynamic_auth: true\n  type: rerank\n')
    monkeypatch.setenv('LAZYMIND_MODEL_CONFIG_PATH', str(config))
    get_dynamic_role_slot_map.cache_clear()
    lazyllm.init_session()
    base_url = 'https://rerank.example' + root
    expected_url = 'https://rerank.example' + suffix
    selected = {'reranker': {
        'source': source, 'model': 'fixture-rerank', 'api_key': 'fixture-key',
        'base_url': base_url, 'type': 'rerank',
    }}
    inject_model_config(selected)
    assert selected['reranker']['base_url'] == base_url
    requests = []

    class Response:
        status_code = 200

        def __enter__(self):
            return self

        def __exit__(self, *_args):
            return False

        def json(self):
            result = {'results': [{'index': 1, 'relevance_score': 0.9}]}
            return {'output': result} if source == 'qwen' else result

    def post(url, **kwargs):
        requests.append((url, kwargs))
        return Response()

    monkeypatch.setattr('requests.post', post)
    nodes = [DocNode(text='irrelevant evidence'), DocNode(text='relevant evidence')]
    try:
        if consumer == 'knowledge':
            monkeypatch.setattr(search_kb, '_ctx_expand', lambda values: values)
            result = search_kb._search_text('needle', lambda _query: nodes, kb._build_reranker(), 1, 1)
        else:
            class Retriever:
                retrieval_lock = RLock()

                def __call__(self, files, query, **kwargs):
                    assert files == ['fixture.txt'] and query == 'needle'
                    return nodes

            monkeypatch.setattr(search_temp, '_ensure_retriever', Retriever)
            result = search_temp.retrieve_temp_nodes(['fixture.txt'], 'needle', rerank_topk=1, k_max=1)
        assert len(result) == 1
        assert result[0].text == 'relevant evidence'
        assert result[0].relevance_score == 0.9
        assert len(requests) == 1
        url, kwargs = requests[0]
        assert url == expected_url
        assert kwargs['headers']['Authorization'] == 'Bearer fixture-key'
        body = kwargs['json']
        inputs = body['input'] if source == 'qwen' else body
        params = body['parameters'] if source == 'qwen' else body
        assert inputs['query'] == 'needle'
        assert inputs['documents'] == ['irrelevant evidence', 'relevant evidence']
        assert params['top_n'] == 1
        assert body['model'] == 'fixture-rerank'
    finally:
        lazyllm.globals.clear()
        get_dynamic_role_slot_map.cache_clear()
