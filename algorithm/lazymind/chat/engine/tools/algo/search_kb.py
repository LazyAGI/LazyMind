from typing import Any, List, Optional

from lazyllm import Document, warp
from lazyllm.tools.rag import Reranker, Retriever, TempDocRetriever
from lazyllm.tools.rag.rank_fusion.reciprocal_rank_fusion import RRFFusion

from lazymind.chat.engine.tools.algo.kb_adaptive_topk import AdaptiveKComponent
from lazymind.chat.engine.tools.algo.kb_context_expansion import ContextExpansionComponent
from lazymind.chat.engine.tools.infra import get_vocab_manager


def _adaptive_get_token_len(n: Any) -> int:
    txt = getattr(n, 'text', '') or ''
    return max(1, len(txt) // 4)


def _pass_through_rerank(nodes):
    for node in nodes or []:
        if getattr(node, 'relevance_score', None) is None:
            node.relevance_score = getattr(node, 'score', None) or getattr(node, 'similarity_score', None) or 0.0
    return nodes


def _merge_and_deduplicate(node_lists: List[List[Any]]) -> List[Any]:
    seen = {}
    for nodes in node_lists:
        for n in nodes:
            uid = getattr(n, 'uid', None) or id(n)
            score = getattr(n, 'relevance_score', 0.0) or 0.0
            if uid not in seen or score > (getattr(seen[uid], 'relevance_score', 0.0) or 0.0):
                seen[uid] = n
    return sorted(seen.values(), key=lambda n: getattr(n, 'relevance_score', 0.0) or 0.0, reverse=True)


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
    queries = payload['queries']
    files = (payload or {}).get('files')
    user_id = payload['user_id']
    filters = payload.get('filters') or {}

    if files and tmp_retriever is None:
        raise ValueError('tmp_retriever is required when payload.files is set')

    def _process_one(query: str):
        expanded_query = get_vocab_manager(user_id)(query)
        if files:
            nodes = tmp_retriever(files, expanded_query, topk=retriever_topk)
        else:
            nodes = tuple(
                result for result in (
                    retriever(expanded_query, filters=filters, topk=retriever_topk)
                    for retriever in retrievers
                ) if result
            )
            nodes = RRFFusion(top_k=50)(nodes)
        return reranker(nodes, query=expanded_query, topk=rerank_topk) if reranker else _pass_through_rerank(nodes)

    all_nodes = list(warp(_process_one, _concurrent=min(len(queries), 5))(*queries))

    merged = _merge_and_deduplicate(all_nodes)
    merged = adaptive_k(merged)
    return ctx_expand(merged)


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

    queries = payload['queries']
    filters = payload.get('filters') or {}

    def _image_one(query: str):
        image_nodes = image_retriever(query, filters=filters, topk=image_topk)
        if not image_nodes:
            return []
        if isinstance(image_nodes, (list, tuple)):
            return list(image_nodes)
        return [image_nodes]

    all_image_nodes = list(warp(_image_one, _concurrent=min(len(queries), 5))(*queries))

    merged_images = _merge_and_deduplicate(all_image_nodes)
    return list(text_nodes or []) + merged_images[:image_topk]
