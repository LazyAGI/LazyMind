from typing import Any, List, Optional

from lazyllm import Document
from lazyllm.tools.rag import Reranker, Retriever, TempDocRetriever
from lazyllm.tools.rag.rank_fusion.reciprocal_rank_fusion import RRFFusion

from lazymind.chat.engine.tools.algo.kb_adaptive_topk import AdaptiveKComponent
from lazymind.chat.engine.tools.algo.kb_context_expansion import ContextExpansionComponent
from lazymind.review.service.registry import get_vocab_manager


def _adaptive_get_token_len(n: Any) -> int:
    txt = getattr(n, 'text', '') or ''
    return max(1, len(txt) // 4)


def _pass_through_rerank(nodes):
    for node in nodes or []:
        if getattr(node, 'relevance_score', None) is None:
            node.relevance_score = getattr(node, 'score', None) or getattr(node, 'similarity_score', None) or 0.0
    return nodes


def search_text(
    payload: dict,
    *,
    retrievers: List[Retriever],
    retriever_topk: int,
    rerank_topk: int,
    tmp_retriever: Optional[TempDocRetriever],
    reranker: Optional[Reranker],
    adaptive_k: AdaptiveKComponent,
    ctx_expand: ContextExpansionComponent,
):
    query = get_vocab_manager(payload['user_id'])(payload['query'])
    files = (payload or {}).get('files')
    if files:
        if tmp_retriever is None:
            raise ValueError('tmp_retriever is required when payload.files is set')
        nodes = tmp_retriever(files, query, topk=retriever_topk)
    else:
        filters = payload.get('filters') or {}
        nodes = tuple(
            result for result in (
                retriever(query, filters=filters, topk=retriever_topk) for retriever in retrievers
            ) if result
        )
        nodes = RRFFusion(top_k=50)(nodes)

    nodes = reranker(nodes, query=query, topk=rerank_topk) if reranker is not None else _pass_through_rerank(nodes)
    nodes = adaptive_k(nodes)
    return ctx_expand(nodes)


def search_kb(
    payload: dict,
    *,
    document: Document,
    retrievers: List[Retriever],
    tmp_retriever: Optional[TempDocRetriever],
    reranker: Optional[Reranker],
    image_retriever: Optional[Retriever],
    retriever_topk: int = 20,
    rerank_topk: int = 20,
    k_max: int = 10,
    image_topk: int = 3,
):
    adaptive_k = AdaptiveKComponent(
        bias=2,
        k_max=k_max,
        gap_tau=0.2,
        get_token_len=_adaptive_get_token_len,
        max_tokens=2048,
    )
    ctx_expand = ContextExpansionComponent(
        document=document,
        token_budget=1500,
        score_decay=0.97,
        max_seeds=1,
    )

    text_nodes = search_text(
        payload,
        retrievers=retrievers,
        retriever_topk=retriever_topk,
        rerank_topk=rerank_topk,
        tmp_retriever=tmp_retriever,
        reranker=reranker,
        adaptive_k=adaptive_k,
        ctx_expand=ctx_expand,
    )

    if image_retriever is None:
        return text_nodes

    if (payload or {}).get('files'):
        return text_nodes

    image_nodes = image_retriever(payload['query'], filters=payload.get('filters') or {}, topk=image_topk)
    return list(text_nodes or []) + list(image_nodes or [])
