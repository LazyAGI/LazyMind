from __future__ import annotations

from typing import Any, Optional

import lazyllm
from lazyllm import AutoModel, LOG
from lazyllm.tools.fs.client import FS

from lazymind.model_config import inject_model_config
from lazymind.review.preference_organizer.compactor import (
    make_preference_organizer_compactor,
)
from lazymind.review.preference_organizer.prompts import (
    build_preference_organizer_prompt,
)
from lazymind.review.preference_organizer.schemas import (
    PreferenceOrganizerError,
    PreferenceOrganizerPassResult,
    PreferenceOrganizerResult,
    PreferenceOrganizerResultData,
)
from lazymind.review.preference_organizer.state import (
    load_preference_state,
    target_item_count,
    target_reached,
)
from lazymind.review.preference_organizer.tools import (
    ChangeBudget,
    PreferenceOrganizerAnalyzeTools,
    PreferenceOrganizerApplyTools,
    PreferenceOrganizerGate,
)


def organize_preferences(
    *,
    task_id: str,
    user_id: str,
    llm_config: Optional[dict[str, Any]] = None,
    target_items: int = 30,
    min_items: int = 20,
    hard_min_items: int = 15,
    max_items: int = 40,
    target_prompt_percent: int = 40,
    max_changes: int = 50,
    max_passes: int = 2,
    max_rounds_per_pass: int = 60,
) -> PreferenceOrganizerResult:
    lazyllm.globals._init_sid(sid=task_id)
    lazyllm.locals._init_sid(sid=task_id)
    lazyllm.set_trace_context({
        'trace_id': task_id,
        'session_id': task_id,
        'user_id': user_id,
        'sampled': True,
        'request_tags': ['preference_organizer'],
        'trace_metadata': {'task_id': task_id, 'max_passes': max_passes},
    })
    inject_model_config(llm_config)
    lazyllm.globals['agentic_config'] = {
        'user_id': user_id,
        'task_id': task_id,
        'session_id': task_id,
        'memory_operation_ledger': [],
    }
    budget = ChangeBudget(maximum=max_changes)
    pass_results: list[PreferenceOrganizerPassResult] = []

    try:
        initial = load_preference_state()
    except Exception as exc:
        return _failure_result(task_id, 'storage_read_failed', str(exc), retryable=True)

    if target_reached(
        initial.data,
        target_prompt_percent=target_prompt_percent,
    ):
        return _success_result(
            task_id,
            outcome='organized',
            pass_results=[],
            total_changes=0,
            current_pass=0,
            target_reached_value=True,
            stop_reason='target_reached',
            reason='Preference already fits the compact Chat projection target.',
        )
    if initial.data.stored_items <= hard_min_items:
        return _success_result(
            task_id,
            outcome='no_safe_changes',
            pass_results=[],
            total_changes=0,
            current_pass=0,
            target_reached_value=False,
            stop_reason='hard_min_reached',
            reason='Preference is already at or below the hard item minimum.',
        )

    terminal_outcome = ''
    terminal_error = ''
    stop_reason = ''
    reached = False
    attempted = 0
    for pass_number in range(1, min(2, max_passes) + 1):
        attempted = pass_number
        try:
            before = load_preference_state()
        except Exception as exc:
            return _failure_result(
                task_id, 'storage_read_failed', str(exc), retryable=True,
                pass_results=pass_results,
                total_changes=budget.used,
                current_pass=pass_number,
            )
        pass_target_items = target_item_count(
            before,
            preferred_target_items=target_items,
            hard_min_items=hard_min_items,
            target_prompt_percent=target_prompt_percent,
        )
        gate = PreferenceOrganizerGate(
            pass_number=pass_number,
            budget=budget,
            hard_min_items=hard_min_items,
        )
        budget_before = budget.used
        run_error = _run_organizer_pass(
            gate=gate,
            before=before,
            llm_config=llm_config,
            target_items=pass_target_items,
            preferred_min_items=min_items,
            hard_min_items=hard_min_items,
            max_rounds_per_pass=max_rounds_per_pass,
        )
        try:
            after = load_preference_state()
        except Exception as exc:
            if gate.operations or gate.terminal_outcome == 'partial':
                terminal_outcome = 'partial'
                terminal_error = (
                    gate.terminal_error
                    or f'Organizer changed data but validation read failed: {exc}'
                )
                break
            return _failure_result(
                task_id, 'storage_read_failed', str(exc), retryable=True,
                pass_results=pass_results,
                total_changes=budget.used,
                current_pass=pass_number,
            )
        pass_outcome = gate.terminal_outcome
        if run_error and not pass_outcome:
            pass_outcome = 'failed'
            gate.terminal_error = run_error
        if not gate.plan_hash and not pass_outcome:
            pass_outcome = 'failed'
            gate.terminal_error = 'Organizer ended without submit_preference_plan.'
        if (
            not pass_outcome
            and gate.next_operation_index < len(gate.authorized_operations)
        ):
            pass_outcome = 'failed'
            gate.terminal_error = 'Organizer ended before all gated operations were applied.'
        if not pass_outcome:
            if target_reached(
                after.data,
                target_prompt_percent=target_prompt_percent,
            ):
                pass_outcome = 'organized'
            elif budget.used >= budget.maximum:
                pass_outcome = 'budget_exhausted'
            elif budget.used > budget_before:
                pass_outcome = 'changes_applied'
            else:
                pass_outcome = 'no_safe_changes'
        pass_results.append(PreferenceOrganizerPassResult(
            pass_number=pass_number,
            plan_hash=gate.plan_hash,
            before=before.data,
            after=after.data,
            changes=budget.used - budget_before,
            operation_count=len(gate.operations),
            outcome=pass_outcome,
        ))
        if pass_outcome == 'organized':
            terminal_outcome = 'organized'
            stop_reason = 'target_reached'
            reached = True
            break
        if pass_outcome in {'stale_state', 'partial', 'failed', 'budget_exhausted'}:
            terminal_outcome = pass_outcome
            terminal_error = gate.terminal_error
            stop_reason = pass_outcome
            break
        if pass_outcome == 'no_safe_changes':
            terminal_outcome = (
                'no_safe_changes' if budget.used == 0
                else 'organized_with_remaining'
            )
            terminal_error = gate.terminal_error
            stop_reason = 'no_further_safe_changes'
            break
        if pass_number == min(2, max_passes):
            terminal_outcome = 'organized_with_remaining'
            terminal_error = gate.terminal_error
            stop_reason = 'max_passes_reached'
            break

    if not terminal_outcome:
        terminal_outcome = 'failed'
        terminal_error = 'Organizer pass loop ended unexpectedly.'
        stop_reason = 'unexpected_loop_end'
    if terminal_outcome in {
        'organized', 'organized_with_remaining', 'no_safe_changes',
        'budget_exhausted',
    }:
        return _success_result(
            task_id,
            outcome=terminal_outcome,
            pass_results=pass_results,
            total_changes=budget.used,
            current_pass=attempted,
            target_reached_value=reached,
            stop_reason=stop_reason,
            reason=terminal_error,
        )
    return PreferenceOrganizerResult(
        status='failed',
        task_id=task_id,
        outcome=terminal_outcome if terminal_outcome in {'stale_state', 'partial'} else 'failed',
        retryable=_retryable(terminal_error) and terminal_outcome not in {'stale_state', 'partial'},
        result=PreferenceOrganizerResultData(
            current_pass=attempted,
            passes_attempted=attempted,
            passes=pass_results,
            total_changes=budget.used,
            outcome=terminal_outcome or 'failed',
            reason=terminal_error,
            target_reached=False,
            stop_reason=stop_reason or terminal_outcome or 'failed',
        ),
        error=PreferenceOrganizerError(
            code=terminal_outcome or 'failed',
            message=terminal_error or 'Preference Organizer failed.',
        ),
    )


def _run_organizer_pass(
    *,
    gate: PreferenceOrganizerGate,
    before,
    llm_config: Optional[dict[str, Any]],
    target_items: int,
    preferred_min_items: int,
    hard_min_items: int,
    max_rounds_per_pass: int,
) -> str:
    prompt = build_preference_organizer_prompt(
        before,
        pass_number=gate.pass_number,
        preferred_min_items=preferred_min_items,
        hard_min_items=hard_min_items,
        target_items=target_items,
        changes_remaining=gate.budget.maximum - gate.budget.used,
    )
    try:
        llm = AutoModel(model='llm')
        agent = lazyllm.tools.agent.ReactAgent(
            llm=llm,
            tools=[
                PreferenceOrganizerAnalyzeTools(gate),
                PreferenceOrganizerApplyTools(gate),
            ],
            max_retries=max(1, max_rounds_per_pass - 1),
            return_trace=False,
            prompt=' ',
            keep_full_turns=3,
            history_compactor=make_preference_organizer_compactor(
                gate, llm_config=llm_config, llm=llm,
            ),
            fs=FS,
            enable_builtin_tools=False,
            force_summarize=True,
        )
        lazyllm.locals['_lazyllm_agent'] = {}
        result = agent(prompt)
        LOG.info(
            f'[PreferenceOrganizer] pass={gate.pass_number} '
            f'plan_hash={gate.plan_hash} operations={len(gate.operations)} '
            f'result={str(result)[:1000]!r}'
        )
        return ''
    except Exception as exc:
        LOG.exception(f'[PreferenceOrganizer] pass={gate.pass_number} failed: {exc}')
        return str(exc)


def _retryable(message: str) -> bool:
    normalized = str(message or '').casefold()
    return any(marker in normalized for marker in (
        'timeout', 'timed out', 'connection', 'temporarily unavailable', 'rate limit',
    ))


def _failure_result(
    task_id: str,
    code: str,
    message: str,
    *,
    retryable: bool,
    pass_results: Optional[list[PreferenceOrganizerPassResult]] = None,
    total_changes: int = 0,
    current_pass: int = 0,
) -> PreferenceOrganizerResult:
    return PreferenceOrganizerResult(
        status='failed', task_id=task_id, outcome='failed', retryable=retryable,
        result=PreferenceOrganizerResultData(
            current_pass=current_pass,
            passes_attempted=len(pass_results or []),
            passes=pass_results or [],
            total_changes=total_changes,
            outcome='failed',
            reason=message,
            target_reached=False,
            stop_reason=code,
        ),
        error=PreferenceOrganizerError(code=code, message=message),
    )


def _success_result(
    task_id: str,
    *,
    outcome: str,
    pass_results: list[PreferenceOrganizerPassResult],
    total_changes: int,
    current_pass: int,
    target_reached_value: bool,
    stop_reason: str,
    reason: str,
) -> PreferenceOrganizerResult:
    return PreferenceOrganizerResult(
        status='success',
        task_id=task_id,
        outcome=outcome,
        result=PreferenceOrganizerResultData(
            current_pass=current_pass,
            passes_attempted=len(pass_results),
            passes=pass_results,
            total_changes=total_changes,
            outcome=outcome,
            reason=reason,
            target_reached=target_reached_value,
            stop_reason=stop_reason,
        ),
    )
