"""Temporary-file retrieval: TempDocRetriever plus rerank and adaptive-k.

Does not call knowledge-base context expansion.
"""
from __future__ import annotations

from typing import Any, List, Optional

from lazyllm import AutoModel
from lazyllm.tools.rag import Reranker, TempDocRetriever

from lazymind.chat.engine.tools.algo.kb_adaptive_topk import AdaptiveKComponent
from lazymind.config import EMBED_MAIN
from lazymind.model_config import get_dynamic_role_slot_map, is_model_role_available
from lazymind.parsing.engine.transform import GeneralParser

_TEMP_NODE_GROUP_NAME = 'block'
_TEMP_NODE_GROUP_MAX_LENGTH = 2048
_TEMP_NODE_GROUP_SPLIT_BY = '\n'
_RERANKER_MODULE = 'ModuleReranker'
_RERANKER_MODEL = 'reranker'

_vector_retriever: Optional[TempDocRetriever] = None
_bm25_retriever: Optional[TempDocRetriever] = None
_adaptive_k = AdaptiveKComponent(
    bias=2, gap_tau=0.2,
    get_token_len=lambda n: max(1, len(getattr(n, 'text', '') or '') // 4),
    max_tokens=2048,
)


def _is_reranker_enabled() -> bool:
    import lazyllm
    role_slots = get_dynamic_role_slot_map()
    if 'reranker' not in role_slots:
        return True
    try:
        cfg = lazyllm.globals.config['dynamic_model_configs']
    except Exception:
        cfg = None
    role_cfg = cfg.get('reranker') if isinstance(cfg, dict) else None
    return isinstance(role_cfg, dict) and bool(role_cfg.get(role_slots['reranker']))


def _build_reranker() -> Optional[Reranker]:
    if not _is_reranker_enabled():
        return None
    return Reranker(_RERANKER_MODULE, model=AutoModel(model=_RERANKER_MODEL))


def _pass_through_rerank(nodes):
    for node in nodes or []:
        if getattr(node, 'relevance_score', None) is None:
            node.relevance_score = (
                getattr(node, 'score', None) or getattr(node, 'similarity_score', None) or 0.0
            )
    return nodes


def embed_available() -> bool:
    return is_model_role_available('embed_main')


def _make_retriever(*, use_embed: bool) -> TempDocRetriever:
    retriever = TempDocRetriever(embed=AutoModel(model=EMBED_MAIN) if use_embed else None)
    retriever.create_node_group(
        name=_TEMP_NODE_GROUP_NAME,
        transform=GeneralParser(
            max_length=_TEMP_NODE_GROUP_MAX_LENGTH,
            split_by=_TEMP_NODE_GROUP_SPLIT_BY,
        ),
    )
    retriever.add_subretriever(
        _TEMP_NODE_GROUP_NAME,
        similarity='cosine' if use_embed else 'bm25_chinese',
    )
    return retriever


def _ensure_retriever(*, use_embed: bool) -> TempDocRetriever:
    global _vector_retriever, _bm25_retriever
    if use_embed:
        if _vector_retriever is None:
            _vector_retriever = _make_retriever(use_embed=True)
        return _vector_retriever
    if _bm25_retriever is None:
        _bm25_retriever = _make_retriever(use_embed=False)
    return _bm25_retriever


def retrieve_temp_nodes(
    files: List[str],
    query: str,
    *,
    retriever_topk: int = 20,
    rerank_topk: int = 20,
    k_max: int = 10,
) -> List[Any]:
    if not files or not str(query or '').strip():
        return []
    retriever = _ensure_retriever(use_embed=embed_available())
    reranker = _build_reranker()
    nodes = retriever(files, query, topk=retriever_topk)
    ranked = reranker(nodes, query=query, topk=rerank_topk) if reranker else _pass_through_rerank(nodes)
    return _adaptive_k(ranked or [], k_max=k_max)
