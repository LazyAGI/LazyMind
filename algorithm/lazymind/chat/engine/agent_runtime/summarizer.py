from __future__ import annotations

import copy
from dataclasses import asdict
from typing import Any, Callable, Optional

import lazyllm

from lazymind.config import config

from .budget import usage_ratio
from .model_calls import ContextCapacityError, request_tokens, configured_output_limit
from lazyllm.module.llms.onlinemodule.base import ModelCallControl, ModelCallError, ModelFinish
from .context_estimator import estimate_tokens
from .message_fields import model_facing_history, estimate_message_tokens
from .models import ContextBudget, CompressionTrigger, SummaryEvent
from .summary_prompt import (
    build_summary_user_prompt,
    get_summary_system_prompt,
    has_required_profile_blocks,
    wrap_summary_for_projection,
)
from .summary_range import (
    RUNTIME_SUMMARY_KIND,
    SummaryRange,
    is_runtime_summary_message,
    select_summary_range,
    validate_tool_pairing,
    extract_summary_markdown,
)
from .telemetry import append_event

from .active_context import (
    collect_summary_runtime_state,
    merge_model_context_sidecar,
    resolve_summary_profile,
    sidecar_fields,
)

try:
    from lazyllm.tools.agent.base import _write_agent_data
except Exception:  # pragma: no cover - optional at import time in unit tests
    _write_agent_data = None


def _estimate_history_tokens(history: list[dict[str, Any]]) -> int:
    return sum(estimate_message_tokens(message) for message in history)


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


def _call_summarizer_llm(llm: Any, system_prompt: str, user_prompt: str, *,
                         candidate_budget: Optional[ContextBudget] = None, control=None, timeout=None) -> str:
    if llm is None:
        raise ValueError('summarizer llm is required')
    if candidate_budget is not None:
        view = llm.share(prompt=system_prompt, format=_llm_response_text, stream=False, copy_static_params=True)

        def check_request(request):
            if request_tokens(request) > candidate_budget.effective_input_budget:
                raise ContextCapacityError('The summary request does not fit this model.')

        call_control = ModelCallControl(cancel_check=control.cancel_check if control is not None else None,
                                        request_check=check_request)
        kwargs = {'timeout': timeout} if timeout is not None else {}
        try:
            return _llm_response_text(view(
                user_prompt, tools=[], max_retries=1, output_limit=configured_output_limit(view, kwargs),
                call_control=call_control, **kwargs))
        except ModelCallError as exc:
            if exc.terminal.kind == 'finish' and exc.terminal.finish == ModelFinish.LENGTH:
                raise ContextCapacityError('The summary exceeded its output limit.') from exc
            raise
    prompt = f'{system_prompt}\n\n{user_prompt}'
    if hasattr(llm, 'share'):
        return _llm_response_text(llm.share(stream=False)(prompt))
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
    expected_prefix: list[dict[str, Any]],
    budget: ContextBudget,
    before_total: int,
    after_total: int,
    non_history_tokens: int,
    profile: Optional[str] = None,
    runtime_state: Optional[dict[str, Any]] = None,
) -> tuple[bool, str, int]:
    if not (summary_markdown or '').strip():
        return False, 'empty_summary', 0

    if not has_required_profile_blocks(summary_markdown, profile, runtime_state):
        return False, 'missing_required_sections', 0

    summary_tokens = estimate_tokens(summary_markdown)
    replaced_tokens = _estimate_history_tokens(replaced_span)
    if summary_tokens >= replaced_tokens:
        return False, 'summary_not_shorter', summary_tokens

    if projected[:len(expected_prefix)] != expected_prefix:
        return False, 'prefix_modified', summary_tokens

    if expected_tail:
        actual_tail = projected[len(projected) - len(expected_tail):]
        if actual_tail != expected_tail:
            return False, 'tail_modified', summary_tokens
    elif len(projected) != len(expected_prefix) + 1:
        return False, 'tail_modified', summary_tokens

    ok, reason = validate_tool_pairing(projected)
    if not ok:
        return False, f'tool_pairing_{reason}', summary_tokens

    if after_total <= budget.target_tokens:
        return True, 'ok', summary_tokens

    tail_tokens = _estimate_history_tokens(expected_tail)
    prefix_tokens = _estimate_history_tokens(expected_prefix)
    fixed_tokens = non_history_tokens + prefix_tokens + tail_tokens
    target_unreachable = fixed_tokens >= budget.target_tokens
    if not target_unreachable:
        return False, 'target_not_reached', summary_tokens

    # Recovery is measured against the reachable floor, excluding immutable context.
    overshoot = max(1, before_total - max(budget.target_tokens, fixed_tokens))
    reclaimed = max(0, before_total - after_total)
    required_recovery = float(config['context_summary_required_overshoot_reclaim_ratio'])
    if reclaimed < overshoot * required_recovery:
        return False, 'insufficient_overshoot_recovery', summary_tokens
    max_output_ratio = float(config['context_summary_max_output_to_replaced_ratio'])
    if replaced_tokens <= 0 or summary_tokens / replaced_tokens > max_output_ratio:
        return False, 'summary_compression_ratio_too_weak', summary_tokens

    return True, 'ok', summary_tokens


def prepare_summary_compression(
    history: list[dict[str, Any]],
    *,
    budget: ContextBudget,
    trigger: CompressionTrigger,
    llm: Any = None,
    summarizer: Optional[Callable[[str, str], str]] = None,
    force: bool = False,
    estimated_total_tokens: Optional[int] = None,
    selected: Optional[SummaryRange] = None,
) -> tuple[list[dict[str, Any]], SummaryEvent, Optional[SummaryRange]]:
    """Plan a validated summary without publishing events or model context."""
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

    if selected is None:
        selected = select_summary_range(
            original,
            effective_input_budget=budget.effective_input_budget,
            keep_recent_ratio=float(config['context_summary_keep_recent_ratio']),
            min_recent_user_turns=int(config['context_summary_min_recent_user_turns']),
        )
    if selected is None:
        return _abandon(original, budget, trigger, before_total, ratio_before, 'no_summary_range')

    runtime_state = collect_summary_runtime_state()
    profile = resolve_summary_profile(runtime_state)
    if (
        profile == 'workflow'
        and not runtime_state.get('artifact_coords')
        and not runtime_state.get('workflow_step_id')
    ):
        return _abandon(
            original, budget, trigger, before_total, ratio_before, 'workflow_missing_coords',
            replaced_message_count=len(selected.summary_messages),
            tail_tokens=selected.tail_tokens,
        )

    system_prompt = get_summary_system_prompt(profile)
    user_prompt = build_summary_user_prompt(
        selected.summary_messages, runtime_state=runtime_state,
    )

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

    # Keep authoritative task messages outside the replaceable summary range.
    prefix = copy.deepcopy(original[:selected.replace_start])
    tail = copy.deepcopy(selected.tail)
    projected = prefix + [summary_message] + tail

    after_total = non_history_tokens + _estimate_history_tokens(projected)
    ok, reason, summary_tokens = _validate_summary(
        summary_markdown,
        replaced_span=replaced_span,
        projected=projected,
        expected_tail=tail,
        expected_prefix=prefix,
        budget=budget,
        before_total=before_total,
        after_total=after_total,
        non_history_tokens=non_history_tokens,
        profile=profile,
        runtime_state=runtime_state,
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
    return projected, event, selected


def apply_summary_compression(
    history: list[dict[str, Any]],
    *,
    budget: ContextBudget,
    trigger: CompressionTrigger,
    llm: Any = None,
    summarizer: Optional[Callable[[str, str], str]] = None,
    force: bool = False,
    estimated_total_tokens: Optional[int] = None,
    selected: Optional[SummaryRange] = None,
) -> tuple[list[dict[str, Any]], SummaryEvent]:
    projected, event, selected = prepare_summary_compression(
        history, budget=budget, trigger=trigger, llm=llm, summarizer=summarizer,
        force=force, estimated_total_tokens=estimated_total_tokens, selected=selected,
    )
    _log_summary_event(event)
    if event.decision == 'summarized':
        _emit_model_context_updated(
            extract_summary_markdown(projected[selected.replace_start]),
            _max_history_seq(history[selected.replace_start:selected.replace_end]),
        )
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
        fields = sidecar_fields()
        _write_agent_data(
            'model_context_updated',
            summary_text=summary_markdown.strip(),
            covered_through_seq=int(covered_through_seq),
            version=1,
            **fields,
        )
        if isinstance(cfg, dict):
            cfg['model_context'] = merge_model_context_sidecar(
                {
                    'summary_text': summary_markdown.strip(),
                    'covered_through_seq': int(covered_through_seq),
                    'version': 1,
                },
                fields,
            )
    except Exception as exc:  # noqa: BLE001
        lazyllm.LOG.warning(f'[ContextSummary] model_context_emit_failed err={exc}')


def _skip(
    history: list[dict[str, Any]],
    budget: ContextBudget,
    trigger: CompressionTrigger,
    before_total: int,
    ratio_before: float,
    reason: str,
) -> tuple[list[dict[str, Any]], SummaryEvent, Optional[SummaryRange]]:
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
    return history, event, None


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
) -> tuple[list[dict[str, Any]], SummaryEvent, Optional[SummaryRange]]:
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
    return history, event, None


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
