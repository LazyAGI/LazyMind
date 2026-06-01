import time
from typing import List, Optional, Set, Tuple
from lazyllm import LOG, Document, ModuleBase
from lazyllm.tools.rag import DocNode

_RPC_RETRIES = 2
_RPC_RETRY_DELAY = 0.3


def _get_doc_id(node: DocNode) -> Optional[str]:
    return (node.global_metadata or {}).get('docid')


def _estimate_tokens(text: str) -> int:
    return max(1, len(text) // 4)


def _node_sort_key(node: DocNode) -> Tuple:
    return (node.metadata.get('index') or 0, node.uid)


def _get_node_type(node: DocNode) -> Optional[str]:
    try:
        md = getattr(node, 'metadata', None) or {}
        if isinstance(md, dict):
            t = md.get('type') or md.get('node_type')
            if isinstance(t, str) and t:
                return t
    except Exception:
        pass
    try:
        t = getattr(node, 'type', None)
        return t if isinstance(t, str) and t else None
    except Exception:
        return None


def _relevance_key(n: DocNode) -> Tuple:
    return (-(getattr(n, 'relevance_score', 0.0) or 0.0), n.uid)


class ContextExpansionComponent(ModuleBase):
    def __init__(self, document: Document, token_budget: int = 3000,
                 score_decay: float = 0.98, max_seeds: Optional[int] = None,
                 max_new_nodes_per_seed: int = 2,
                 return_trace: bool = False, **kwargs):
        super().__init__(return_trace=return_trace, **kwargs)
        self.document = document
        self.token_budget = token_budget
        self.score_decay = score_decay
        self.max_seeds = max_seeds
        self.max_new_nodes_per_seed = max(1, int(max_new_nodes_per_seed))

    def _fetch_neighbors(self, node: DocNode, existing_uids: Set[str]) -> List[DocNode]:
        doc_id = _get_doc_id(node)
        if not doc_id:
            return []
        span = (-2, 2) if (_get_node_type(node) or '').lower() == 'table' else (-1, 1)
        window = None
        for attempt in range(_RPC_RETRIES + 1):
            try:
                window = self.document.get_window_nodes(node, span=span, merge=False)
                break
            except Exception as e:
                if attempt < _RPC_RETRIES:
                    time.sleep(_RPC_RETRY_DELAY)
                else:
                    LOG.warning('[CtxExpand] All RPC attempts failed uid=%s: %s', node.uid, e)
                    return []
        window = window if isinstance(window, list) else ([window] if window else [])
        neighbors = [
            n for n in window
            if n.uid != node.uid and n.uid not in existing_uids and _get_doc_id(n) == doc_id
        ]
        neighbors.sort(key=_node_sort_key)
        return neighbors

    def forward(self, nodes: List[DocNode], **kwargs) -> List[DocNode]:
        if not nodes:
            return nodes
        seeds = sorted(nodes, key=_relevance_key)
        if self.max_seeds is not None and self.max_seeds > 0:
            seeds = seeds[: self.max_seeds]
        existing_uids: Set[str] = {n.uid for n in nodes}
        all_added: List[DocNode] = []
        added_tokens = 0
        for seed in seeds:
            is_table = (_get_node_type(seed) or '').lower() == 'table'
            if added_tokens >= self.token_budget and not is_table:
                continue
            neighbors = self._fetch_neighbors(seed, existing_uids)
            seed_score = getattr(seed, 'relevance_score', 0.0) or 0.0
            added_for_seed = 0
            cap = max(self.max_new_nodes_per_seed, 4) if is_table else self.max_new_nodes_per_seed
            for nb in neighbors:
                if added_for_seed >= cap:
                    break
                nb_tok = _estimate_tokens(nb.text or '')
                if not is_table and added_tokens + nb_tok > self.token_budget:
                    continue
                existing_uids.add(nb.uid)
                try:
                    nb.relevance_score = seed_score * self.score_decay
                except (AttributeError, TypeError):
                    pass
                all_added.append(nb)
                if not is_table:
                    added_tokens += nb_tok
                added_for_seed += 1
        if all_added:
            LOG.info('[CtxExpand] Expanded +%d nodes (+%d tokens)', len(all_added), added_tokens)
        result = list(nodes) + all_added
        result.sort(key=_relevance_key)
        return result
