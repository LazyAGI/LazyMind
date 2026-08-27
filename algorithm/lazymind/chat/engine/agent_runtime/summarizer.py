from __future__ import annotations

import copy
import json
from dataclasses import asdict
from typing import Any, Callable, Optional

import lazyllm

from lazymind.config import config

from .budget import usage_ratio
from .context_estimator import estimate_tokens
from .message_fields import model_facing_history, model_facing_message
from .models import ContextBudget, CompressionTrigger, SummaryEvent
from .summary_prompt import (
    build_summary_user_prompt,
    get_summary_system_prompt,
    has_required_summary_sections,
    wrap_summary_for_projection,
)
from .summary_range import (
    RUNTIME_SUMMARY_KIND,
    is_runtime_summary_message,
    select_summary_range,
    validate_tool_pairing,
)
from .telemetry import append_event

try:
    from lazyllm.tools.agent.base import _write_agent_data
except Exception:  # pragma: no cover - optional at import time in unit tests
    _write_agent_data = None


def _estimate_history_tokens(history: list[dict[str, Any]]) -> int:
    return sum(
        estimate_tokens(json.dumps(model_facing_message(message), ensure_ascii=False, default=str))
        for message in history
    )


def _llm_response_text(resp: Any) -> str:
    if resp is None:
        return ''
    if isinstance(resp, str):
        return resp.strip()
    if isinstance(resp, dict):
        content = resp.get('content')
        if isinstance(content, str):
            return content.strip()
        return str(content or '').strip()
    return str(resp).strip()


def _call_summarizer_llm(llm: Any, system_prompt: str, user_prompt: str) -> str:
    """Invoke the shared chat LLM once without streaming."""
    if llm is None:
        raise ValueError('summarizer llm is required')
    prompt = f'{system_prompt}\n\n{user_prompt}'
    if hasattr(llm, 'share'):
        summarize_llm = llm.share(stream=False)
        return _llm_response_text(summarize_llm(prompt))
    return _llm_response_text(llm(prompt))


def _build_summary_message(
    summary_markdown: str,
    *,
    replaced_message_count: int,
    summary_tokens: int,
) -> dict[str, Any]:
    return {
        'role': 'user',
        'content': wrap_summary_for_projection(summary_markdown),
        '_lazymind_meta': {
            'kind': RUNTIME_SUMMARY_KIND,
            'version': 1,
            'replaced_message_count': replaced_message_count,
            'summary_tokens': summary_tokens,
        },
    }


def _validate_summary(
    summary_markdown: str,
    *,
    replaced_span: list[dict[str, Any]],
    projected: list[dict[str, Any]],
    expected_tail: list[dict[str, Any]],
    budget: ContextBudget,
    before_total: int,
    after_total: int,
    non_history_tokens: int,
) -> tuple[bool, str, int, int]:
    if not (summary_markdown or '').strip():
        return False, 'empty_summary', 0, 0

    if not has_required_summary_sections(summary_markdown):
        return False, 'missing_required_sections', 0, 0

    summary_tokens = estimate_tokens(summary_markdown)
    replaced_tokens = _estimate_history_tokens(replaced_span)
    if summary_tokens >= replaced_tokens:
        return False, 'summary_not_shorter', summary_tokens, replaced_tokens

    if expected_tail:
        actual_tail = projected[len(projected) - len(expected_tail):]
        if actual_tail != expected_tail:
            return False, 'tail_modified', summary_tokens, replaced_tokens
    elif len(projected) != 1:
        return False, 'tail_modified', summary_tokens, replaced_tokens

    ok, reason = validate_tool_pairing(projected)
    if not ok:
        return False, f'tool_pairing_{reason}', summary_tokens, replaced_tokens

    if after_total <= budget.target_tokens:
        return True, 'ok', summary_tokens, replaced_tokens

    tail_tokens = _estimate_history_tokens(expected_tail)
    target_unreachable = non_history_tokens + tail_tokens >= budget.target_tokens
    if not target_unreachable:
        return False, 'target_not_reached', summary_tokens, replaced_tokens

    overshoot = max(1, before_total - budget.target_tokens)
    reclaimed = max(0, before_total - after_total)
    required_recovery = float(config['context_summary_required_overshoot_reclaim_ratio'])
    if reclaimed < overshoot * required_recovery:
        return False, 'insufficient_overshoot_recovery', summary_tokens, replaced_tokens
    max_output_ratio = float(config['context_summary_max_output_to_replaced_ratio'])
    if replaced_tokens <= 0 or summary_tokens / replaced_tokens > max_output_ratio:
        return False, 'summary_compression_ratio_too_weak', summary_tokens, replaced_tokens

    return True, 'ok', summary_tokens, replaced_tokens


def apply_summary_compression(
    history: list[dict[str, Any]],
    *,
    budget: ContextBudget,
    trigger: CompressionTrigger,
    llm: Any = None,
    summarizer: Optional[Callable[[str, str], str]] = None,
    force: bool = False,
    estimated_total_tokens: Optional[int] = None,
) -> tuple[list[dict[str, Any]], SummaryEvent]:
    """Replace older turns with a rolling runtime summary when over target.

    The input history is never mutated. On any failure the original list copy
    is returned (Stage-1 projection remains authoritative for this attempt).
    """
    original = list(history)
    before_history = _estimate_history_tokens(original)
    before_total = (
        int(estimated_total_tokens)
        if estimated_total_tokens is not None
        else before_history
    )
    non_history_tokens = max(0, before_total - before_history)
    ratio_before = usage_ratio(before_total, budget)

    if not config['context_compression_enabled']:
        return _skip(original, budget, trigger, before_total, ratio_before, 'master_disabled')
    if not config['context_summary_compression_enabled']:
        return _skip(original, budget, trigger, before_total, ratio_before, 'strategy_disabled')

    if not force and before_total <= budget.target_tokens:
        return _skip(original, budget, trigger, before_total, ratio_before, 'at_or_below_target')

    keep_ratio = float(config['context_summary_keep_recent_ratio'])
    min_turns = int(config['context_summary_min_recent_user_turns'])
    selected = select_summary_range(
        original,
        effective_input_budget=budget.effective_input_budget,
        keep_recent_ratio=keep_ratio,
        min_recent_user_turns=min_turns,
    )
    if selected is None:
        return _abandon(original, budget, trigger, before_total, ratio_before, 'no_summary_range')

    system_prompt = get_summary_system_prompt()
    user_prompt = build_summary_user_prompt(selected.summary_messages)

    try:
        if summarizer is not None:
            summary_markdown = (summarizer(system_prompt, user_prompt) or '').strip()
        else:
            summary_markdown = _call_summarizer_llm(llm, system_prompt, user_prompt)
    except Exception as exc:  # noqa: BLE001 - never fail the agent path
        lazyllm.LOG.warning(f'[ContextSummary] summary_llm_failed err={exc}')
        return _abandon(
            original, budget, trigger, before_total, ratio_before, 'llm_failed',
            replaced_message_count=len(selected.summary_messages),
            tail_tokens=selected.tail_tokens,
        )

    replaced_span = original[selected.replace_start:selected.replace_end]
    summary_tokens_est = estimate_tokens(summary_markdown)
    summary_message = _build_summary_message(
        summary_markdown,
        replaced_message_count=len(replaced_span),
        summary_tokens=summary_tokens_est,
    )

    # Preserve any messages before replace_start (should be empty for v1 rolling).
    prefix = copy.deepcopy(original[:selected.replace_start])
    tail = copy.deepcopy(selected.tail)
    projected = prefix + [summary_message] + tail

    after_total = non_history_tokens + _estimate_history_tokens(projected)
    ok, reason, summary_tokens, _replaced_tokens = _validate_summary(
        summary_markdown,
        replaced_span=replaced_span,
        projected=projected,
        expected_tail=tail,
        budget=budget,
        before_total=before_total,
        after_total=after_total,
        non_history_tokens=non_history_tokens,
    )
    if not ok:
        return _abandon(
            original, budget, trigger, before_total, ratio_before, reason,
            replaced_message_count=len(replaced_span),
            tail_tokens=selected.tail_tokens,
            summary_tokens=summary_tokens,
        )

    # Strip internal meta for tool-pairing already validated with meta present;
    # keep meta in projected history so mid-turn rolling can detect it. Upstream
    # senders that reject unknown fields should strip via strip_lazymind_meta.
    reclaimed = max(0, before_total - after_total)
    ratio_after = usage_ratio(after_total, budget)
    event = SummaryEvent(
        trigger=trigger,
        decision='summarized',
        reason='rolling_summary',
        estimated_before=before_total,
        estimated_after=after_total,
        reclaimed_tokens=reclaimed,
        budget=budget,
        replaced_message_count=len(replaced_span),
        tail_tokens=selected.tail_tokens,
        summary_tokens=summary_tokens,
        usage_ratio_before=ratio_before,
        usage_ratio_after=ratio_after,
    )
    _log_summary_event(event)
    covered = _max_history_seq(replaced_span)
    if covered > 0:
        _emit_model_context_updated(summary_markdown, covered)
    return projected, event


def strip_lazymind_meta(history: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Return a shallow-copied history without internal fields for upstream LLMs."""
    return model_facing_history(history)


def _max_history_seq(messages: list[dict[str, Any]]) -> int:
    max_seq = 0
    for message in messages:
        if is_runtime_summary_message(message):
            continue
        seq = message.get('history_seq')
        try:
            seq_i = int(seq)
        except (TypeError, ValueError):
            continue
        if seq_i > max_seq:
            max_seq = seq_i
    return max_seq


def _emit_model_context_updated(summary_markdown: str, covered_through_seq: int) -> None:
    if covered_through_seq <= 0 or not summary_markdown.strip():
        return
    if _write_agent_data is None:
        return
    cfg = lazyllm.globals.get('agentic_config') or {}
    prior = cfg.get('model_context') if isinstance(cfg, dict) else None
    prior_covered = 0
    if isinstance(prior, dict):
        try:
            prior_covered = int(prior.get('covered_through_seq') or 0)
        except (TypeError, ValueError):
            prior_covered = 0
    if covered_through_seq <= prior_covered:
        return
    try:
        _write_agent_data(
            'model_context_updated',
            summary_text=summary_markdown.strip(),
            covered_through_seq=int(covered_through_seq),
            version=1,
        )
        if isinstance(cfg, dict):
            cfg['model_context'] = {
                'summary_text': summary_markdown.strip(),
                'covered_through_seq': int(covered_through_seq),
                'version': 1,
            }
    except Exception as exc:  # noqa: BLE001
        lazyllm.LOG.warning(f'[ContextSummary] model_context_emit_failed err={exc}')


def _skip(
    history: list[dict[str, Any]],
    budget: ContextBudget,
    trigger: CompressionTrigger,
    before_total: int,
    ratio_before: float,
    reason: str,
) -> tuple[list[dict[str, Any]], SummaryEvent]:
    event = SummaryEvent(
        trigger=trigger,
        decision='skipped',
        reason=reason,
        estimated_before=before_total,
        estimated_after=before_total,
        reclaimed_tokens=0,
        budget=budget,
        usage_ratio_before=ratio_before,
        usage_ratio_after=ratio_before,
    )
    _log_summary_event(event)
    return history, event


def _abandon(
    history: list[dict[str, Any]],
    budget: ContextBudget,
    trigger: CompressionTrigger,
    before_total: int,
    ratio_before: float,
    reason: str,
    *,
    replaced_message_count: int = 0,
    tail_tokens: int = 0,
    summary_tokens: int = 0,
) -> tuple[list[dict[str, Any]], SummaryEvent]:
    event = SummaryEvent(
        trigger=trigger,
        decision='abandoned',
        reason=reason,
        estimated_before=before_total,
        estimated_after=before_total,
        reclaimed_tokens=0,
        budget=budget,
        replaced_message_count=replaced_message_count,
        tail_tokens=tail_tokens,
        summary_tokens=summary_tokens,
        usage_ratio_before=ratio_before,
        usage_ratio_after=ratio_before,
    )
    _log_summary_event(event)
    return history, event


def _log_summary_event(event: SummaryEvent) -> None:
    lazyllm.LOG.info(
        '[ContextSummary] '
        f'trigger={event.trigger} decision={event.decision} reason={event.reason} '
        f'before={event.estimated_before} after={event.estimated_after} '
        f'reclaimed={event.reclaimed_tokens} '
        f'replaced={event.replaced_message_count} tail_tokens={event.tail_tokens} '
        f'summary_tokens={event.summary_tokens} '
        f'ratio_before={event.usage_ratio_before:.3f} ratio_after={event.usage_ratio_after:.3f} '
        f'target_tokens={event.budget.target_tokens} '
        f'budget={event.budget.effective_input_budget}'
    )
    append_event('summary', **summary_event_to_dict(event))


def summary_event_to_dict(event: SummaryEvent) -> dict[str, Any]:
    return asdict(event)
