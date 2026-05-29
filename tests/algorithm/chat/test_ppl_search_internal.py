import sys
import types


def _stub_vocab():
    if "lazymind.review.service.registry" not in sys.modules:
        stub = types.ModuleType("lazymind.review.service.registry")
        stub.get_vocab_manager = lambda user_id: (lambda q: q)
        sys.modules["lazymind.review.service.registry"] = stub


_stub_vocab()

from lazymind.chat.engine.tools.internal import ppl_search as ppl_search_mod


def test_build_reranker_returns_component_instead_of_lambda(monkeypatch):
    captured = {}

    class FakeReranker:
        def __init__(self, name, model):
            captured["args"] = {"name": name, "model": model}

    monkeypatch.setattr(ppl_search_mod, "get_enabled_role_config_path", lambda role: "/tmp/config.yml")
    monkeypatch.setattr(ppl_search_mod, "AutoModel", lambda model, config=False: f"model:{model}:{config}")
    monkeypatch.setattr(ppl_search_mod, "Reranker", FakeReranker)

    reranker = ppl_search_mod.build_reranker()

    assert isinstance(reranker, FakeReranker)
    assert captured["args"] == {
        "name": "ModuleReranker",
        "model": "model:reranker:/tmp/config.yml",
    }


def test_build_reranker_returns_none_when_role_disabled(monkeypatch):
    monkeypatch.setattr(ppl_search_mod, "get_enabled_role_config_path", lambda role: None)

    assert ppl_search_mod.build_reranker() is None


def test_search_text_passes_through_when_reranker_disabled():
    class DummyNode:
        def __init__(self, score):
            self.score = score
            self.relevance_score = None

    nodes = [DummyNode(0.8), DummyNode(0.3)]

    result = ppl_search_mod.search_text(
        {"query": "hello", "user_id": "u1", "files": ["tmp.md"]},
        retrievers=[],
        retriever_topk=9,
        rerank_topk=5,
        tmp_retriever=lambda files, query: nodes,
        reranker=None,
        adaptive_k=lambda xs: xs,
        ctx_expand=lambda xs: xs,
    )

    assert result is nodes
    assert result[0].relevance_score == 0.8
    assert result[1].relevance_score == 0.3


def test_search_text_passes_dynamic_retriever_topk(monkeypatch):
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

    monkeypatch.setattr(ppl_search_mod, "RRFFusion", lambda top_k: (lambda nodes: list(nodes[0])))

    result = ppl_search_mod.search_text(
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


def test_search_text_passes_dynamic_rerank_topk():
    captured = {}

    class DummyNode:
        def __init__(self):
            self.score = 0.8
            self.relevance_score = None

    class DummyReranker:
        def __call__(self, nodes, *, query="", topk=None):
            captured["query"] = query
            captured["topk"] = topk
            return nodes

    result = ppl_search_mod.search_text(
        {"query": "hello", "user_id": "u1", "files": ["tmp.md"]},
        retrievers=[],
        retriever_topk=11,
        rerank_topk=4,
        tmp_retriever=lambda files, query: [DummyNode()],
        reranker=DummyReranker(),
        adaptive_k=lambda xs: xs,
        ctx_expand=lambda xs: xs,
    )

    assert len(result) == 1
    assert captured == {"query": "hello", "topk": 4}


def test_get_reranker_reuses_cached_instance(monkeypatch):
    calls = {"automodel": [], "reranker": []}

    class FakeReranker:
        def __init__(self, name, model):
            calls["reranker"].append((name, model))

    monkeypatch.setattr(ppl_search_mod, "get_enabled_role_config_path", lambda role: "/tmp/config.yml")
    monkeypatch.setattr(ppl_search_mod, "AutoModel", lambda model, config=False: calls["automodel"].append((model, config)) or "rerank-model")
    monkeypatch.setattr(ppl_search_mod, "Reranker", FakeReranker)
    ppl_search_mod._RERANKER_CACHE.clear()

    first = ppl_search_mod.get_reranker()
    second = ppl_search_mod.get_reranker()

    assert first is second
    assert calls["automodel"] == [("reranker", "/tmp/config.yml")]
    assert calls["reranker"] == [("ModuleReranker", "rerank-model")]


def test_get_kb_retrievers_reuses_cached_instances(monkeypatch):
    calls = []

    class FakeRetriever:
        def __init__(self, document, **cfg):
            calls.append((document, cfg))

    monkeypatch.setattr(ppl_search_mod, "Retriever", FakeRetriever)
    ppl_search_mod._KB_RETRIEVER_CACHE.clear()

    document = object()

    first = ppl_search_mod.get_kb_retrievers(document)
    second = ppl_search_mod.get_kb_retrievers(document)

    assert first is second
    assert len(calls) == 2


def test_get_tmp_retriever_reuses_cached_instance(monkeypatch):
    calls = {"automodel": [], "subretriever": []}

    class FakeTempDocRetriever:
        def __init__(self, embed):
            calls["embed"] = embed

        def add_subretriever(self, name, topk):
            calls["subretriever"].append((name, topk))

    monkeypatch.setattr(ppl_search_mod, "get_config_path", lambda: "/tmp/runtime_models.yaml")
    monkeypatch.setattr(ppl_search_mod, "AutoModel", lambda model, config=False: calls["automodel"].append((model, config)) or "embed-model")
    monkeypatch.setattr(ppl_search_mod, "TempDocRetriever", FakeTempDocRetriever)
    ppl_search_mod._TMP_RETRIEVER_CACHE.clear()

    first = ppl_search_mod.get_tmp_retriever()
    second = ppl_search_mod.get_tmp_retriever()

    assert first is second
    assert calls["automodel"] == [("embed_main", "/tmp/runtime_models.yaml")]
    assert calls["subretriever"] == [("block", 20)]
