from typing import Any, Callable, List, Optional

from lazyllm import Document, parallel
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


DOCUMENT = Document(url=f'{_cfg["agentic_kb_url"]}/_call', name=_cfg['algo_id'])

_adaptive_k = AdaptiveKComponent(
    bias=2, gap_tau=0.2, get_token_len=_adaptive_get_token_len, max_tokens=2048,
)
_ctx_expand = ContextExpansionComponent(
    document=DOCUMENT, token_budget=1500, score_decay=0.97, max_seeds=1,
)


def _search_text(
    expanded: str,
    retrieve_fn: Callable[[str], Any],
    reranker: Optional[Reranker],
    rerank_topk: int,
    k_max: int,
) -> List[Any]:
    nodes = retrieve_fn(expanded)
    ranked = reranker(nodes, query=expanded, topk=rerank_topk) if reranker else _pass_through_rerank(nodes)
    merged = _adaptive_k(ranked or [], k_max=k_max)
    return _ctx_expand(merged)


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
    query = payload['query']
    expanded = get_vocab_manager(payload['user_id'])(query)

    def _kb_retrieve(expanded: str):
        results = parallel(*retrievers)(
            expanded, filters=payload.get('filters') or {}, topk=retriever_topk
        )
        nodes = tuple(r for r in results if r)
        return RRFFusion(top_k=50)(nodes)

    text_nodes = _search_text(expanded, _kb_retrieve, reranker, rerank_topk, k_max)

    if image_retriever is None:
        return text_nodes

    image_nodes = list(image_retriever(query, filters=payload.get('filters') or {}, topk=image_topk) or [])
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
    query = payload['query']
    expanded = get_vocab_manager(payload['user_id'])(query)

    def _tmp_retrieve(expanded: str):
        return tmp_retriever(payload.get('files') or [], expanded, topk=retriever_topk)

    return _search_text(expanded, _tmp_retrieve, reranker, rerank_topk, k_max)
