from __future__ import annotations

import json
from dataclasses import asdict, replace
from typing import Any, Callable, Optional

import lazyllm

from lazymind.config import config

from .budget import build_context_budget, needs_compression, usage_ratio
from .compactors import (
    ToolCompactionPlan,
    commit_tool_result_plan,
    compact_or_spill_tool_result,
    is_oversized_tool_result,
    plan_tool_result_compaction,
)
from .context_estimator import estimate_non_history_tokens, estimate_tokens
from .message_fields import TOOL_OBSERVATION_KEY, model_facing_message
from .models import (
    ContextBudget,
    CompressionTrigger,
    PruneEvent,
    ToolPruneDetail,
)
from .telemetry import append_event
from .projection_state import (
    clone_entries,
    commit_entries,
    mark_projection_sent,
    projection_tokens,
    reconcile_projection,
    render_projection,
    transition_metrics,
)


def _message_tokens(message: dict[str, Any]) -> int:
    return estimate_tokens(json.dumps(model_facing_message(message), ensure_ascii=False, default=str))


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
    min_reclaim = max(0, int(min_reclaim_tokens or 0))
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
                observation=message.get(TOOL_OBSERVATION_KEY),
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


def _entry_tool_indices(entries: list[dict[str, Any]]) -> list[int]:
    return [
        index for index, entry in enumerate(entries)
        if entry['message'].get('role') == 'tool'
    ]


def _plan_entry(
    entry: dict[str, Any],
    *,
    workspace: Optional[str],
) -> ToolCompactionPlan:
    message = entry['message']
    return plan_tool_result_compaction(
        str(message.get('name') or ''),
        message.get('content'),
        observation=message.get(TOOL_OBSERVATION_KEY),
        workspace=workspace,
    )


def _apply_entry_plans(
    entries: list[dict[str, Any]],
    plans: list[tuple[int, ToolCompactionPlan]],
) -> list[dict[str, Any]]:
    candidate = clone_entries(entries)
    for index, plan in plans:
        if plan.compactor == 'noop' or plan.after_tokens >= plan.before_tokens:
            continue
        candidate[index]['message']['content'] = plan.content
        candidate[index]['kind'] = 'spilled' if plan.compactor == 'spill' else 'compacted'
    return candidate


def _commit_surviving_spills(
    entries: list[dict[str, Any]],
    plans: list[tuple[int, ToolCompactionPlan]],
    *,
    workspace: Optional[str],
) -> list[dict[str, Any]]:
    if not workspace:
        return entries
    committed = clone_entries(entries)
    by_content = {
        plan.content: plan
        for _index, plan in plans
        if plan.compactor == 'spill'
    }
    for entry in committed:
        plan = by_content.get(entry['message'].get('content'))
        if plan is None:
            continue
        written = commit_tool_result_plan(plan, workspace=workspace)
        if written.compactor == 'noop':
            entry['message']['content'] = written.content
            entry['kind'] = 'full'
        else:
            entry['message']['content'] = written.content
    return committed


def _detail(index: int, entry: dict[str, Any], plan: ToolCompactionPlan) -> ToolPruneDetail:
    return ToolPruneDetail(
        tool_name=str(entry['message'].get('name') or 'unknown'),
        message_index=index,
        before_tokens=plan.before_tokens,
        after_tokens=plan.after_tokens,
        compactor=plan.compactor,
        spill_path=plan.spill_path,
        spill_bytes=plan.spill_bytes,
    )


def _event_with_metrics(
    event: PruneEvent,
    metrics: dict[str, Any],
) -> PruneEvent:
    return replace(
        event,
        first_changed_projection_index=metrics['first_changed_projection_index'],
        cache_disruption_tokens=metrics['cache_disruption_tokens'],
        changed_messages=metrics['changed_messages'],
        changed_model_visible=tuple(metrics['changed_model_visible']),
    )


def _pressure_rearm_tokens(after_total: int, budget: ContextBudget) -> int:
    hysteresis = max(1, budget.trigger_tokens - budget.target_tokens)
    return max(budget.trigger_tokens, after_total + hysteresis)


def _candidate_acceptance(
    metrics: dict[str, Any],
    *,
    budget: ContextBudget,
    remaining_rounds: Optional[int],
) -> tuple[bool, str]:
    before_total = metrics['estimated_before']
    after_total = metrics['estimated_after']
    if before_total > budget.effective_input_budget:
        if after_total <= budget.effective_input_budget:
            return True, 'context_safety'
        return False, 'context_safety_more_reclaim_needed'
    if after_total <= budget.target_tokens:
        return True, 'target_reached'
    configured_horizon = max(1, int(config['context_prune_cache_amortization_calls']))
    horizon = configured_horizon
    if remaining_rounds is not None:
        horizon = max(1, min(int(remaining_rounds), configured_horizon))
    benefit = metrics['reclaimed_tokens'] * horizon
    cost = (
        metrics['cache_disruption_tokens']
        * float(config['context_prune_cached_token_cost_ratio'])
    )
    if benefit >= cost:
        return True, 'cache_amortized'
    return False, 'cache_cost_exceeds_benefit'


def _record_candidate(
    *,
    stage: str,
    decision: str,
    reason: str,
    metrics: dict[str, Any],
    changed_entries: int,
) -> None:
    append_event(
        'compression_candidate',
        stage=stage,
        decision=decision,
        reason=reason,
        changed_entries=changed_entries,
        **metrics,
    )


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
        current_round_messages = list(kwargs.get('current_round_messages') or [])
        combined = list(history) + current_round_messages
        if not config['context_compression_enabled']:
            return combined
        effective_keep = max(
            0,
            int(default_keep if keep_full_turns is None else keep_full_turns),
        )
        non_history_tokens = estimate_non_history_tokens(
            kwargs.get('prefix') or {},
            kwargs.get('current_input'),
        )
        state, _rebuilt = reconcile_projection(combined, kwargs.get('runtime_state'))
        stable = state['entries']
        before_total = non_history_tokens + projection_tokens(stable)

        mandatory_plans: list[tuple[int, ToolCompactionPlan]] = []
        for index, entry in enumerate(stable):
            message = entry['message']
            if (
                entry.get('kind') == 'full'
                and not entry.get('model_visible')
                and message.get('role') == 'tool'
                and is_oversized_tool_result(message.get('content'))
            ):
                plan = _plan_entry(entry, workspace=workspace)
                if plan.compactor != 'noop' and plan.after_tokens < plan.before_tokens:
                    mandatory_plans.append((index, plan))

        if mandatory_plans:
            candidate = _apply_entry_plans(stable, mandatory_plans)
            candidate = _commit_surviving_spills(
                candidate,
                mandatory_plans,
                workspace=workspace,
            )
            after_total = non_history_tokens + projection_tokens(candidate)
            metrics = transition_metrics(
                stable,
                candidate,
                before_total=before_total,
                after_total=after_total,
            )
            event = PruneEvent(
                trigger=trigger,
                decision=(
                    'spilled'
                    if any(plan.compactor == 'spill' for _, plan in mandatory_plans)
                    else 'pruned'
                ),
                reason='first_exposure_oversized_result',
                estimated_before=before_total,
                estimated_after=after_total,
                reclaimed_tokens=metrics['reclaimed_tokens'],
                budget=budget,
                usage_ratio_before=usage_ratio(before_total, budget),
                usage_ratio_after=usage_ratio(after_total, budget),
                details=tuple(
                    _detail(index, stable[index], plan)
                    for index, plan in mandatory_plans
                ),
            )
            event = _event_with_metrics(event, metrics)
            _log_event(event)
            commit_entries(state, candidate)
            stable = state['entries']
            before_total = after_total

        pressure_at = max(
            budget.trigger_tokens,
            int(state.get('next_pressure_tokens') or 0),
        )
        if (
            before_total < pressure_at
            and before_total <= budget.effective_input_budget
        ):
            mark_projection_sent(state)
            return render_projection(state['entries'])

        tool_indices = _entry_tool_indices(stable)
        protected = set(tool_indices[-effective_keep:]) if effective_keep else set()
        planned: list[tuple[int, ToolCompactionPlan]] = []
        for index in tool_indices:
            entry = stable[index]
            content = entry['message'].get('content')
            if entry.get('kind') != 'full' or not entry.get('model_visible'):
                continue
            if index in protected and not is_oversized_tool_result(content):
                continue
            plan = _plan_entry(entry, workspace=workspace)
            if plan.compactor != 'noop' and plan.after_tokens < plan.before_tokens:
                planned.append((index, plan))

        accepted_entries: Optional[list[dict[str, Any]]] = None
        accepted_plans: list[tuple[int, ToolCompactionPlan]] = []
        accepted_metrics: Optional[dict[str, Any]] = None
        accepted_reason = ''
        temporary_entries = stable
        for count in range(1, len(planned) + 1):
            batch = planned[:count]
            candidate = _apply_entry_plans(stable, batch)
            after_total = non_history_tokens + projection_tokens(candidate)
            metrics = transition_metrics(
                stable,
                candidate,
                before_total=before_total,
                after_total=after_total,
            )
            accepted, reason = _candidate_acceptance(
                metrics,
                budget=budget,
                remaining_rounds=kwargs.get('remaining_rounds'),
            )
            _record_candidate(
                stage='prune',
                decision='accepted' if accepted else 'rejected',
                reason=reason,
                metrics=metrics,
                changed_entries=count,
            )
            temporary_entries = candidate
            if accepted:
                accepted_entries = candidate
                accepted_plans = batch
                accepted_metrics = metrics
                accepted_reason = reason
                break

        if (
            accepted_entries is None
            and planned
            and before_total > budget.effective_input_budget
        ):
            best_effort = _apply_entry_plans(stable, planned)
            best_effort_total = non_history_tokens + projection_tokens(best_effort)
            if best_effort_total < before_total:
                accepted_entries = best_effort
                accepted_plans = planned
                accepted_metrics = transition_metrics(
                    stable,
                    best_effort,
                    before_total=before_total,
                    after_total=best_effort_total,
                )
                accepted_reason = 'context_safety_best_effort'
                _record_candidate(
                    stage='prune',
                    decision='accepted',
                    reason=accepted_reason,
                    metrics=accepted_metrics,
                    changed_entries=len(planned),
                )

        summary_input = accepted_entries or temporary_entries
        summary_input_total = non_history_tokens + projection_tokens(summary_input)
        summary_succeeded = False
        final_entries: Optional[list[dict[str, Any]]] = None
        if (
            config['context_summary_compression_enabled']
            and summary_input_total >= budget.trigger_tokens
        ):
            from .summarizer import apply_summary_compression
            from .summary_range import select_summary_range

            summary_history = [entry['message'] for entry in summary_input]
            selected = select_summary_range(
                summary_history,
                effective_input_budget=budget.effective_input_budget,
                keep_recent_ratio=float(config['context_summary_keep_recent_ratio']),
                min_recent_user_turns=int(config['context_summary_min_recent_user_turns']),
            )
            if selected is not None:
                summarized, summary_event = apply_summary_compression(
                    summary_history,
                    budget=budget,
                    trigger=trigger,
                    llm=llm,
                    summarizer=summarizer,
                    force=True,
                    estimated_total_tokens=summary_input_total,
                )
                if summary_event.decision == 'summarized':
                    replaced = summary_input[selected.replace_start:selected.replace_end]
                    summary_entry = {
                        'source_start': min(entry['source_start'] for entry in replaced),
                        'source_end': max(entry['source_end'] for entry in replaced),
                        'message': summarized[selected.replace_start],
                        'kind': 'summary',
                        'model_visible': False,
                    }
                    final_entries = (
                        clone_entries(summary_input[:selected.replace_start])
                        + [summary_entry]
                        + clone_entries(summary_input[selected.replace_end:])
                    )
                    final_total = non_history_tokens + projection_tokens(final_entries)
                    metrics = transition_metrics(
                        stable,
                        final_entries,
                        before_total=before_total,
                        after_total=final_total,
                    )
                    _record_candidate(
                        stage='summary',
                        decision='accepted',
                        reason=summary_event.reason,
                        metrics=metrics,
                        changed_entries=metrics['changed_messages'],
                    )
                    summary_succeeded = True

        if summary_succeeded and final_entries is not None:
            final_entries = _commit_surviving_spills(
                final_entries,
                planned,
                workspace=workspace,
            )
            final_total = non_history_tokens + projection_tokens(final_entries)
            commit_entries(
                state,
                final_entries,
                next_pressure_tokens=_pressure_rearm_tokens(final_total, budget),
            )
        elif accepted_entries is not None and accepted_metrics is not None:
            accepted_entries = _commit_surviving_spills(
                accepted_entries,
                accepted_plans,
                workspace=workspace,
            )
            accepted_total = non_history_tokens + projection_tokens(accepted_entries)
            committed_metrics = transition_metrics(
                stable,
                accepted_entries,
                before_total=before_total,
                after_total=accepted_total,
            )
            event = PruneEvent(
                trigger=trigger,
                decision=(
                    'spilled'
                    if any(plan.compactor == 'spill' for _, plan in accepted_plans)
                    else 'pruned'
                ),
                reason=accepted_reason,
                estimated_before=before_total,
                estimated_after=accepted_total,
                reclaimed_tokens=committed_metrics['reclaimed_tokens'],
                budget=budget,
                usage_ratio_before=usage_ratio(before_total, budget),
                usage_ratio_after=usage_ratio(accepted_total, budget),
                details=tuple(
                    _detail(index, stable[index], plan)
                    for index, plan in accepted_plans
                ),
            )
            _log_event(_event_with_metrics(event, committed_metrics))
            commit_entries(
                state,
                accepted_entries,
                next_pressure_tokens=_pressure_rearm_tokens(accepted_total, budget),
            )

        mark_projection_sent(state)
        return render_projection(state['entries'])

    return _compact
