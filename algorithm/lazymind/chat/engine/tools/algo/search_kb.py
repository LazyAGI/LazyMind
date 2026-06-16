from typing import Any, Callable, List, Optional

from lazyllm import Document, warp
from lazyllm.tools.rag import Reranker, Retriever, TempDocRetriever
from lazyllm.tools.rag.rank_fusion.reciprocal_rank_fusion import RRFFusion

from lazymind.chat.engine.tools.algo.kb_adaptive_topk import AdaptiveKComponent
from lazymind.chat.engine.tools.algo.kb_context_expansion import ContextExpansionComponent
from lazymind.chat.engine.tools.infra import get_vocab_manager
from lazymind.config import config as _cfg


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


DOCUMENT = Document(url=f'{_cfg["agentic_kb_url"]}/_call', name=_cfg['algo_id'])

_adaptive_k = AdaptiveKComponent(
    bias=2, gap_tau=0.2, get_token_len=_adaptive_get_token_len, max_tokens=2048,
)
_ctx_expand = ContextExpansionComponent(
    document=DOCUMENT, token_budget=1500, score_decay=0.97, max_seeds=1,
)


def _search_text(
    queries: List[str],
    retrieve_fn: Callable[[str], tuple],
    reranker: Optional[Reranker],
    rerank_topk: int,
    k_max: int,
) -> List[Any]:
    def _process_one(query: str):
        nodes, expanded = retrieve_fn(query)
        return reranker(nodes, query=expanded, topk=rerank_topk) if reranker else _pass_through_rerank(nodes)

    all_nodes = list(warp(_process_one, _concurrent=min(len(queries), 5))(queries))
    merged = _merge_and_deduplicate(all_nodes)
    merged = _adaptive_k(merged, k_max=k_max)
    return _ctx_expand(merged)


def _search_image(
    queries: List[str],
    image_retriever: Retriever,
    filters: dict,
    image_topk: int,
) -> List[Any]:
    def _image_one(query: str):
        image_nodes = image_retriever(query, filters=filters, topk=image_topk)
        if not image_nodes:
            return []
        if isinstance(image_nodes, (list, tuple)):
            return list(image_nodes)
        return [image_nodes]

    all_nodes = list(warp(_image_one, _concurrent=min(len(queries), 5))(queries))
    return _merge_and_deduplicate(all_nodes)


def search_kb(
    payload: dict,
    *,
    retrievers: List[Retriever],
    reranker: Optional[Reranker],
    image_retriever: Optional[Retriever],
    retriever_topk: int = 20,
    rerank_topk: int = 20,
    k_max: int = 10,
    image_topk: int = 3,
):
    queries = payload['queries']

    def _kb_retrieve(query: str):
        expanded = get_vocab_manager(payload['user_id'])(query)
        nodes = tuple(
            result for result in (
                retriever(expanded, filters=payload.get('filters') or {}, topk=retriever_topk)
                for retriever in retrievers
            ) if result
        )
        nodes = RRFFusion(top_k=50)(nodes)
        return nodes, expanded

    text_nodes = _search_text(queries, _kb_retrieve, reranker, rerank_topk, k_max)

    if image_retriever is None:
        return text_nodes

    image_nodes = _search_image(queries, image_retriever, payload.get('filters') or {}, image_topk)
    return list(text_nodes or []) + image_nodes[:image_topk]


def search_temp_files(
    payload: dict,
    *,
    tmp_retriever: TempDocRetriever,
    reranker: Optional[Reranker],
    retriever_topk: int = 20,
    rerank_topk: int = 20,
    k_max: int = 10,
):
    queries = payload['queries']

    def _tmp_retrieve(query: str):
        expanded = get_vocab_manager(payload['user_id'])(query)
        nodes = tmp_retriever(payload.get('files') or [], expanded, topk=retriever_topk)
        return nodes, expanded

    return _search_text(queries, _tmp_retrieve, reranker, rerank_topk, k_max)
