from __future__ import annotations

import json
from dataclasses import asdict
from typing import Any, Callable, Optional

import lazyllm

from lazymind.config import config

from .budget import build_context_budget, needs_compression, usage_ratio
from .compactors import compact_or_spill_tool_result, is_oversized_tool_result
from .context_estimator import estimate_non_history_tokens, estimate_tokens
from .models import (
    ContextBudget,
    CompressionTrigger,
    PruneEvent,
    ToolPruneDetail,
)
from .telemetry import append_event


def _message_tokens(message: dict[str, Any]) -> int:
    return estimate_tokens(json.dumps(message, ensure_ascii=False, default=str))


def estimate_history_tokens(history: list[dict[str, Any]]) -> int:
    return sum(_message_tokens(message) for message in history)


def _tool_indices(history: list[dict[str, Any]]) -> list[int]:
    return [index for index, message in enumerate(history) if message.get('role') == 'tool']


def prune_tool_results(
    history: list[dict[str, Any]],
    *,
    keep_recent: int,
    budget: ContextBudget,
    trigger: CompressionTrigger,
    estimated_total_tokens: Optional[int] = None,
    force: bool = False,
    min_reclaim_tokens: Optional[int] = None,
    workspace: Optional[str] = None,
) -> tuple[list[dict[str, Any]], PruneEvent]:
    """Return a projected history with older tool results compacted.

    The input history is never mutated. Callers may discard the projected view
    on failure; the original session history remains authoritative.
    Oversized tool results are spilled even when they sit in the keep_recent window.
    """
    keep_recent = max(0, int(keep_recent))
    min_reclaim = int(
        min_reclaim_tokens
        if min_reclaim_tokens is not None
        else config['context_compression_min_reclaim_tokens']
    )
    before_total = (
        int(estimated_total_tokens)
        if estimated_total_tokens is not None
        else estimate_history_tokens(history)
    )
    ratio_before = usage_ratio(before_total, budget)
    tool_indices = _tool_indices(history)
    oversized = {
        index for index in tool_indices
        if is_oversized_tool_result(history[index].get('content'))
    }
    if not force and not needs_compression(before_total, budget) and not oversized:
        event = PruneEvent(
            trigger=trigger,
            decision='skipped',
            reason='below_trigger',
            estimated_before=before_total,
            estimated_after=before_total,
            reclaimed_tokens=0,
            budget=budget,
            usage_ratio_before=ratio_before,
            usage_ratio_after=ratio_before,
        )
        _log_event(event)
        return list(history), event

    cutoff = max(0, len(tool_indices) - keep_recent)
    to_compact = set(tool_indices[:cutoff]) | oversized
    if not to_compact:
        event = PruneEvent(
            trigger=trigger,
            decision='skipped',
            reason='no_old_tool_results',
            estimated_before=before_total,
            estimated_after=before_total,
            reclaimed_tokens=0,
            budget=budget,
            usage_ratio_before=ratio_before,
            usage_ratio_after=ratio_before,
        )
        _log_event(event)
        return list(history), event

    projected: list[dict[str, Any]] = []
    details: list[ToolPruneDetail] = []
    spilled = 0
    for index, message in enumerate(history):
        if index not in to_compact:
            projected.append(message)
            continue
        tool_name = str(message.get('name') or '')
        compacted, compactor, before_tokens, after_tokens, spill_path, spill_bytes = (
            compact_or_spill_tool_result(
                tool_name,
                message.get('content'),
                workspace=workspace,
            )
        )
        if compactor == 'noop' or after_tokens >= before_tokens:
            projected.append(message)
            continue
        projected.append(dict(message, content=compacted))
        if compactor == 'spill':
            spilled += 1
        details.append(ToolPruneDetail(
            tool_name=tool_name or 'unknown',
            message_index=index,
            before_tokens=before_tokens,
            after_tokens=after_tokens,
            compactor=compactor,
            spill_path=spill_path,
            spill_bytes=spill_bytes,
        ))

    after_history = estimate_history_tokens(projected)
    overhead = max(0, before_total - estimate_history_tokens(history))
    after_total = after_history + overhead
    reclaimed = max(0, before_total - after_total)
    ratio_after = usage_ratio(after_total, budget)

    if not details:
        event = PruneEvent(
            trigger=trigger,
            decision='skipped',
            reason='compactors_noop',
            estimated_before=before_total,
            estimated_after=before_total,
            reclaimed_tokens=0,
            budget=budget,
            usage_ratio_before=ratio_before,
            usage_ratio_after=ratio_before,
        )
        _log_event(event)
        return list(history), event

    if not spilled and not force and reclaimed < min_reclaim:
        event = PruneEvent(
            trigger=trigger,
            decision='abandoned',
            reason='reclaim_below_threshold',
            estimated_before=before_total,
            estimated_after=after_total,
            reclaimed_tokens=reclaimed,
            budget=budget,
            details=tuple(details),
            usage_ratio_before=ratio_before,
            usage_ratio_after=ratio_after,
        )
        _log_event(event)
        return list(history), event

    event = PruneEvent(
        trigger=trigger,
        decision='spilled' if spilled else 'pruned',
        reason='tool_result_spill' if spilled else 'deterministic_tool_prune',
        estimated_before=before_total,
        estimated_after=after_total,
        reclaimed_tokens=reclaimed,
        budget=budget,
        details=tuple(details),
        usage_ratio_before=ratio_before,
        usage_ratio_after=ratio_after,
    )
    _log_event(event)
    return projected, event


def _log_event(event: PruneEvent) -> None:
    source_counts: dict[str, int] = {}
    for detail in event.details:
        source_counts[detail.compactor] = source_counts.get(detail.compactor, 0) + 1
    lazyllm.LOG.info(
        '[ContextCompression] '
        f'trigger={event.trigger} decision={event.decision} reason={event.reason} '
        f'before={event.estimated_before} after={event.estimated_after} '
        f'reclaimed={event.reclaimed_tokens} '
        f'ratio_before={event.usage_ratio_before:.3f} ratio_after={event.usage_ratio_after:.3f} '
        f'trigger_tokens={event.budget.trigger_tokens} target_tokens={event.budget.target_tokens} '
        f'budget={event.budget.effective_input_budget} sources={source_counts}'
    )
    spill_bits = [
        f'path={detail.spill_path} bytes={detail.spill_bytes}'
        for detail in event.details if detail.spill_path
    ]
    if spill_bits:
        lazyllm.LOG.info('[ContextCompression] spilled ' + '; '.join(spill_bits))
    append_event('prune', **prune_event_to_dict(event))


def prune_event_to_dict(event: PruneEvent) -> dict[str, Any]:
    return asdict(event)


def make_history_compactor(
    *,
    max_input_tokens: Any = None,
    llm_config: Optional[dict[str, Any]] = None,
    keep_recent: Optional[int] = None,
    trigger: CompressionTrigger = 'mid_turn',
    llm: Any = None,
    summarizer: Optional[Callable[[str, str], str]] = None,
    workspace: Optional[str] = None,
) -> Callable[..., list[dict[str, Any]]]:
    """Build a mid-turn history projector compatible with ReactAgent/FunctionCall."""

    budget = build_context_budget(max_input_tokens, llm_config=llm_config)
    default_keep = (
        keep_recent if keep_recent is not None else int(config['agentic_keep_full_turns'])
    )

    def _compact(
        history: list[dict[str, Any]],
        keep_full_turns: Optional[int] = None,
        **kwargs: Any,
    ) -> list[dict[str, Any]]:
        if not config['context_compression_enabled']:
            return list(history)
        effective_keep = default_keep if keep_full_turns is None else keep_full_turns
        non_history_tokens = estimate_non_history_tokens(
            kwargs.get('prefix') or {},
            kwargs.get('current_input'),
        )
        estimated_total = non_history_tokens + estimate_history_tokens(history)
        projected, _event = prune_tool_results(
            history,
            keep_recent=effective_keep,
            budget=budget,
            trigger=trigger,
            estimated_total_tokens=estimated_total,
            force=False,
            workspace=workspace,
        )
        projected_total = non_history_tokens + estimate_history_tokens(projected)
        if (
            config['context_summary_compression_enabled']
            and needs_compression(estimated_total, budget)
            and projected_total > budget.target_tokens
        ):
            from .summarizer import apply_summary_compression, strip_lazymind_meta
            projected, _summary_event = apply_summary_compression(
                projected,
                budget=budget,
                trigger=trigger,
                llm=llm,
                summarizer=summarizer,
                force=True,
            )
            return strip_lazymind_meta(projected)
        from .summarizer import strip_lazymind_meta
        return strip_lazymind_meta(projected)

    return _compact


def apply_pre_turn_pruning(
    history: list[dict[str, Any]],
    *,
    estimated_tokens: int,
    max_input_tokens: Any = None,
    llm_config: Optional[dict[str, Any]] = None,
    keep_recent: Optional[int] = None,
    llm: Any = None,
    summarizer: Optional[Callable[[str, str], str]] = None,
    workspace: Optional[str] = None,
) -> tuple[list[dict[str, Any]], Optional[PruneEvent]]:
    if not config['context_compression_enabled']:
        return list(history), None
    budget = build_context_budget(max_input_tokens, llm_config=llm_config)
    keep = keep_recent if keep_recent is not None else int(config['agentic_keep_full_turns'])
    projected, event = prune_tool_results(
        history,
        keep_recent=keep,
        budget=budget,
        trigger='pre_turn',
        estimated_total_tokens=estimated_tokens,
        force=False,
        workspace=workspace,
    )
    projected_total = (
        estimated_tokens
        - estimate_history_tokens(history)
        + estimate_history_tokens(projected)
    )
    if (
        config['context_summary_compression_enabled']
        and needs_compression(estimated_tokens, budget)
        and projected_total > budget.target_tokens
    ):
        from .summarizer import apply_summary_compression
        projected, _summary_event = apply_summary_compression(
            projected,
            budget=budget,
            trigger='pre_turn',
            llm=llm,
            summarizer=summarizer,
            force=True,
        )
    return projected, event
