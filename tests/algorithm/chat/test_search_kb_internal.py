import sys
import types
from importlib import import_module


def _stub_vocab():
    if "lazymind.review.service.registry" not in sys.modules:
        stub = types.ModuleType("lazymind.review.service.registry")
        stub.get_vocab_manager = lambda user_id: (lambda q: q)
        sys.modules["lazymind.review.service.registry"] = stub


_stub_vocab()

search_kb_mod = import_module("lazymind.chat.engine.tools.algo.search_kb")


def test_search_text_uses_tmp_retriever_without_reranker():
    class DummyNode:
        def __init__(self, score):
            self.score = score
            self.relevance_score = None

    nodes = [DummyNode(0.8), DummyNode(0.3)]

    result = search_kb_mod.search_text(
        {"query": "hello", "user_id": "u1", "files": ["tmp.md"]},
        retrievers=[],
        retriever_topk=9,
        rerank_topk=5,
        tmp_retriever=lambda files, query, **kwargs: nodes,
        reranker=None,
        adaptive_k=lambda xs: xs,
        ctx_expand=lambda xs: xs,
    )

    assert result is nodes
    assert result[0].relevance_score == 0.8
    assert result[1].relevance_score == 0.3


def test_search_text_uses_kb_retrievers_with_filters(monkeypatch):
    captured = {}

    class DummyNode:
        def __init__(self):
            self.score = 0.8
            self.relevance_score = None

    class DummyRetriever:
        def __call__(self, query, *, filters=None, topk=None):
            captured["query"] = query
            captured["filters"] = filters
            captured["topk"] = topk
            return [DummyNode()]

    monkeypatch.setattr(search_kb_mod, "RRFFusion", lambda top_k: (lambda nodes: list(nodes[0])))

    result = search_kb_mod.search_text(
        {"query": "hello", "user_id": "u1", "files": [], "filters": {"scope": "kb"}},
        retrievers=[DummyRetriever()],
        retriever_topk=11,
        rerank_topk=7,
        tmp_retriever=lambda files, query: [],
        reranker=None,
        adaptive_k=lambda xs: xs,
        ctx_expand=lambda xs: xs,
    )

    assert len(result) == 1
    assert captured == {
        "query": "hello",
        "filters": {"scope": "kb"},
        "topk": 11,
    }
