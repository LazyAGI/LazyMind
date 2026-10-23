"""Temporary-file retrieval: TempDocRetriever plus rerank and adaptive-k.

Does not call knowledge-base context expansion.
"""
from __future__ import annotations

from collections import OrderedDict
from functools import lru_cache
from threading import RLock
from typing import Any, List, Optional

from lazyllm import AutoModel
from lazyllm.tools.rag import Reranker, TempDocRetriever

from lazymind.chat.engine.tools.algo.kb_adaptive_topk import AdaptiveKComponent
from lazymind.chat.engine.tools.temporary_retrieval_session import (
    initialize_temporary_retrieval,
    temporary_retrieval_scope,
)
from lazymind.config import EMBED_MAIN
from lazymind.model_config import is_model_role_available
from lazymind.parsing.engine.transform import GeneralParser

_TEMP_NODE_GROUP_NAME = 'block'
_TEMP_NODE_GROUP_MAX_LENGTH = 2048
_TEMP_NODE_GROUP_SPLIT_BY = '\n'
_RERANKER_MODULE = 'ModuleReranker'
_RERANKER_MODEL = 'reranker'

_MAX_CACHED_CONVERSATIONS = 32
_session_retrievers: OrderedDict[str, TempDocRetriever] = OrderedDict()
_session_lock = RLock()


class _ConversationTempDocRetriever(TempDocRetriever):
    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        # LazyLLM's class-level lru_cache is shared across all instances, and
        # its destructor clears that cache. Give each conversation its own.
        self._get_retrievers = lru_cache(maxsize=8)(self._get_retrievers_impl)
        self.retrieval_lock = RLock()


def _is_reranker_enabled() -> bool:
    return is_model_role_available(_RERANKER_MODEL)


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
    return sorted(nodes or [], key=lambda node: node.relevance_score, reverse=True)


def embed_available() -> bool:
    """Whether this conversation chose embedding, not the current model setting."""
    return initialize_temporary_retrieval()


def _make_retriever(*, use_embed: bool) -> TempDocRetriever:
    retriever = _ConversationTempDocRetriever(embed=AutoModel(model=EMBED_MAIN) if use_embed else None)
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


def _ensure_retriever() -> TempDocRetriever:
    scope = temporary_retrieval_scope()
    with _session_lock:
        retriever = _session_retrievers.pop(scope, None)
        if retriever is None:
            retriever = _make_retriever(use_embed=embed_available())
        _session_retrievers[scope] = retriever
        if len(_session_retrievers) > _MAX_CACHED_CONVERSATIONS:
            _session_retrievers.popitem(last=False)
        return retriever


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
    retriever = _ensure_retriever()
    with retriever.retrieval_lock:
        reranker = _build_reranker()
        nodes = retriever(files, query, topk=retriever_topk)
        ranked = reranker(nodes, query=query, topk=rerank_topk) if reranker else _pass_through_rerank(nodes)
        adaptive_k = AdaptiveKComponent(
            bias=2, gap_tau=0.2,
            get_token_len=lambda n: max(1, len(getattr(n, 'text', '') or '') // 4),
            max_tokens=2048,
        )
        return adaptive_k(ranked or [], k_max=k_max)
