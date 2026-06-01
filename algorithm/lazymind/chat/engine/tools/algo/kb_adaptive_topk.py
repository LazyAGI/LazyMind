"""Adaptive top-k selection for KB retrieval nodes."""
from __future__ import annotations

from typing import Any, Callable, Dict, List, Optional, Sequence, Tuple

from lazyllm import LOG, ModuleBase


def _moving_average(xs: List[float], w: int) -> List[float]:
    """Centered moving average; w=1 means no smoothing."""
    if w <= 1 or len(xs) == 0:
        return xs[:]
    pad = w // 2
    ext_left = [xs[0]] * pad
    ext_right = [xs[-1]] * pad
    buf = ext_left + xs + ext_right
    out = []
    for i in range(len(xs)):
        window = buf[i:i + w]
        out.append(sum(window) / w)
    return out


def _clamp(x: int, lo: int, hi: int) -> int:
    return max(lo, min(x, hi))


def _fit_by_budget(
    nodes: Sequence[Any],
    get_token_len: Optional[Callable[[Any], int]],
    max_tokens: Optional[int],
) -> int:
    """Compute the maximum k that fits within the token budget."""
    if max_tokens is None or get_token_len is None or len(nodes) == 0:
        return 0
    acc = 0
    k = 0
    for n in nodes:
        t = int(get_token_len(n))
        if acc + t > max_tokens:
            break
        acc += t
        k += 1
    return max(k, 1)


def adaptive_k_select_from_nodes(
    nodes: Sequence[Any],
    *,
    get_score: Callable[[Any], float] = lambda n: n.relevance_score,
    get_token_len: Optional[Callable[[Any], int]] = None,
    assume_sorted_desc: bool = True,
    max_tokens: Optional[int] = None,
    bias: int = 2,
    search_pct: float = 1.0,
    k_min: int = 1,
    k_max: Optional[int] = None,
    gap_tau: Optional[float] = None,
    smooth_w: int = 1,
    default_k: int = 6,
) -> Tuple[List[Any], int, Dict]:
    """
    Adaptive k selection using DocNode.relevance_score:
    - Finds the position of the "maximum first-order gap" in the score sequence within the first search_pct range as the threshold, plus a buffer B;  # noqa: E501
    - Optional gap_tau: falls back to budget-driven or default_k when the maximum gap is not significant;
    - Finally applies a token budget for secondary truncation.

    Returns: (selected_nodes, k, diag)
    """
    N = len(nodes)
    if N == 0:
        return [], 0, dict(max_gap=0.0, argmax_idx=-1, scores_head=[], tokens_used=0, k_before_budget=0)

    if assume_sorted_desc:
        nodes_sorted = list(nodes)
    else:
        nodes_sorted = sorted(nodes, key=get_score, reverse=True)

    scores = [float(get_score(n)) for n in nodes_sorted]

    if N == 1:
        k = 1
        tokens_used = int(get_token_len(nodes_sorted[0])) if get_token_len else 0
        return nodes_sorted[:1], k, dict(
            max_gap=0.0, argmax_idx=0, scores_head=scores[:1],
            tokens_used=tokens_used, k_before_budget=1
        )

    s_sm = _moving_average(scores, smooth_w) if smooth_w > 1 else scores

    M = max(1, min(N - 1, int((N - 1) * search_pct)))
    gaps = [s_sm[i] - s_sm[i + 1] for i in range(M)]
    argmax_idx = max(range(M), key=lambda i: gaps[i])
    max_gap = gaps[argmax_idx] if M > 0 else 0.0

    if (gap_tau is not None) and (max_gap < gap_tau):
        k0 = default_k
        by_budget = _fit_by_budget(nodes_sorted, get_token_len, max_tokens)
        if by_budget > 0:
            k0 = by_budget
        k = _clamp(k0, k_min, k_max or N)
    else:
        k = argmax_idx + 1 + bias
        if k_max is not None:
            k = min(k, k_max)
        k = _clamp(k, k_min, N)

    k_before_budget = k

    if max_tokens is not None and get_token_len is not None:
        by_budget = _fit_by_budget(nodes_sorted, get_token_len, max_tokens)
        if by_budget > 0:
            k = min(k, by_budget)

    selected = nodes_sorted[:k]
    tokens_used = sum(int(get_token_len(n)) for n in selected) if get_token_len else 0

    diag = dict(
        max_gap=float(max_gap),
        argmax_idx=int(argmax_idx),
        scores_head=scores[:min(k, 5)],
        tokens_used=int(tokens_used),
        k_before_budget=int(k_before_budget),
        search_M=int(M),
    )
    return selected, k, diag


class AdaptiveKComponent(ModuleBase):
    def __init__(
        self,
        get_score: Callable[[Any], float] = lambda n: n.relevance_score,
        get_token_len: Optional[Callable[[Any], int]] = None,
        **kwargs,
    ):
        super().__init__()
        self.get_score = get_score
        self.get_token_len = get_token_len
        self.kwargs = kwargs or {}

    def forward(self, nodes: List[Any], **kwargs) -> List[Any]:
        self.kwargs.update(kwargs or {})
        selected, k, diag = adaptive_k_select_from_nodes(
            nodes,
            get_score=self.get_score,
            get_token_len=self.get_token_len,
            **self.kwargs
        )
        LOG.info(f'[AdaptiveKComponent] AdaptiveK selected {k} / {len(nodes)} nodes, diag={diag}')
        return selected
