"""
Additional tests for pipeline builder helpers.

These tests are kept in a separate file because importing
lazymind.chat.engine.pipelines.get_ppl_search triggers a circular import
(lazymind.review.vocab.evolution → lazymind.chat.engine.pipelines) when the full vocab package
is loaded.  We break the cycle by injecting a lightweight stub for
lazymind.review.service.registry into sys.modules before the real import happens.
"""
import sys
import types


def _stub_vocab():
    """Inject a minimal lazymind.review.service.registry stub to prevent circular import.

    Only stubs modules that haven't been loaded yet.  lazymind.review.vocab.evolution is NOT
    stubbed here because the circular import (lazymind.review.vocab.evolution → lazymind.chat.engine.pipelines)
    has been resolved with a lazy import inside the
    class constructors.  Stubbing vocab.evolution would leave an empty module
    object in sys.modules and break any later test that imports real symbols
    from it (e.g. ActionPlanningModule).
    """
    if 'lazymind.review.service.registry' not in sys.modules:
        stub = types.ModuleType('lazymind.review.service.registry')
        stub.get_vocab_manager = lambda user_id: (lambda q: q)
        sys.modules['lazymind.review.service.registry'] = stub
_stub_vocab()

from lazymind.chat.engine.tools.internal import ppl_search as ppl_search_mod

retriever_mod = ppl_search_mod


# ---------------------------------------------------------------------------
# _build_default_retriever_configs — fixed route shape and embed_keys propagation
# ---------------------------------------------------------------------------

def test_build_default_retriever_configs_uses_embed_keys(monkeypatch):
    monkeypatch.setattr(retriever_mod, 'get_text_embed_keys', lambda: ['embed_main', 'embed_sparse'])

    configs = retriever_mod.build_default_retriever_configs()

    assert len(configs) == 2
    names = [c['group_name'] for c in configs]
    assert 'line' in names
    assert 'block' in names
    for cfg in configs:
        assert cfg['embed_keys'] == ['embed_main', 'embed_sparse']
        assert 'topk' not in cfg


def test_build_default_retriever_configs_falls_back_to_embed_main(monkeypatch):
    monkeypatch.setattr(retriever_mod, 'get_text_embed_keys', lambda: [])

    configs = retriever_mod.build_default_retriever_configs()

    for cfg in configs:
        assert cfg['embed_keys'] == [retriever_mod.EMBED_MAIN]


def test_build_default_retriever_configs_line_has_block_target(monkeypatch):
    monkeypatch.setattr(retriever_mod, 'get_text_embed_keys', lambda: ['embed_main'])

    configs = retriever_mod.build_default_retriever_configs()

    line_cfg = next(c for c in configs if c['group_name'] == 'line')
    assert line_cfg.get('target') == 'block'
    block_cfg = next(c for c in configs if c['group_name'] == 'block')
    assert 'target' not in block_cfg
