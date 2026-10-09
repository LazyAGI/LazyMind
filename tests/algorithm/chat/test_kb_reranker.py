import importlib
from concurrent.futures import ThreadPoolExecutor
from threading import Barrier
from types import SimpleNamespace
from unittest.mock import Mock

import lazyllm
import pytest

from lazymind.chat.engine.tools import kb
from lazymind.model_config import inject_model_config


@pytest.fixture
def runtime(monkeypatch, tmp_path):
    config = tmp_path / 'models.yaml'
    config.write_text('reranker:\n  source: dynamic\n  type: rerank\n')
    monkeypatch.setenv('LAZYMIND_MODEL_CONFIG_PATH', str(config))
    monkeypatch.setattr(kb, '_kb_retrievers', None)
    monkeypatch.setattr(kb, '_kb_image_retriever', None)
    monkeypatch.setattr(kb, 'Retriever', Mock(side_effect=lambda *a, **kw: object()))
    monkeypatch.setattr(kb, 'AutoModel', Mock(side_effect=lambda **kw: SimpleNamespace(
        model_name=lazyllm.globals['config']['dynamic_model_configs']['reranker']['embed']['model'],
    )))
    monkeypatch.setattr(kb, 'Reranker', Mock(side_effect=lambda *a, model: model))
    lazyllm.init_session()
    yield config
    lazyllm.globals.clear()


def configure(model):
    lazyllm.globals.clear()
    lazyllm.init_session()
    inject_model_config({'reranker': {'source': 'openai', 'model': model, 'api_key': 'test'}} if model else {})


@pytest.mark.parametrize('models', [
    [None, 'model-a', 'model-b', None],
    ['model-a', None, 'model-b'],
])
def test_reranker_follows_each_request_while_retrievers_are_reused(runtime, models):
    retrievers = []
    for model in models:
        configure(model)
        text, reranker, image = kb._ensure_kb_search_runtime()
        retrievers.append((text, image))
        assert (reranker.model_name if reranker else None) == model
    assert all(text is retrievers[0][0] and image is retrievers[0][1] for text, image in retrievers)
    assert kb.Reranker.call_count == sum(model is not None for model in models)


def test_concurrent_requests_do_not_share_reranker_selection(runtime):
    configure(None)
    kb._ensure_kb_search_runtime()
    barrier = Barrier(3)

    def request(model):
        try:
            configure(model)
            barrier.wait(timeout=5)
            reranker = kb._ensure_kb_search_runtime()[1]
            return reranker.model_name if reranker else None
        finally:
            lazyllm.globals.clear()

    with ThreadPoolExecutor(max_workers=3) as pool:
        assert list(pool.map(request, [None, 'model-a', 'model-b'])) == [None, 'model-a', 'model-b']


def test_undeclared_reranker_role_is_optional(runtime):
    runtime.write_text('llm:\n  source: dynamic\n  type: llm\n')
    configure(None)
    assert kb._ensure_kb_search_runtime()[1] is None
    kb.AutoModel.assert_not_called()


def test_static_reranker_does_not_require_request_config(runtime, monkeypatch):
    runtime.write_text('reranker:\n  source: openai\n  type: rerank\n  name: static-model\n')
    model = object()
    monkeypatch.setattr(kb, 'AutoModel', Mock(return_value=model))
    configure(None)
    assert kb._ensure_kb_search_runtime()[1] is model


def test_search_returns_evidence_without_reranking_and_propagates_configured_model_errors(monkeypatch):
    search = importlib.import_module('lazymind.chat.engine.tools.algo.search_kb')
    monkeypatch.setattr(search, '_ctx_expand', lambda nodes: nodes)
    nodes = [SimpleNamespace(text='retrieved evidence', score=0.8, relevance_score=None)]
    retrieve = Mock(return_value=nodes)
    assert search._search_text('query', retrieve, None, 20, 10) == nodes
    assert nodes[0].relevance_score == 0.8
    failing_reranker = Mock(side_effect=RuntimeError('provider unavailable'))
    with pytest.raises(RuntimeError, match='provider unavailable'):
        search._search_text('query', retrieve, failing_reranker, 20, 10)
