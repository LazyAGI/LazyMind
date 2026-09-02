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
    max_items: int = 40,
    target_prompt_percent: int = 70,
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

    if initial.data.stored_items < min_items:
        return PreferenceOrganizerResult(
            status='success', task_id=task_id, outcome='no_safe_changes',
            result=PreferenceOrganizerResultData(
                current_pass=0,
                passes_attempted=0,
                passes=[],
                total_changes=0,
                outcome='no_safe_changes',
                reason='Preference is already below the configured safe minimum.',
            ),
        )
    if target_reached(
        initial.data,
        min_items=min_items,
        max_items=max_items,
        target_prompt_percent=target_prompt_percent,
    ):
        return PreferenceOrganizerResult(
            status='success', task_id=task_id, outcome='organized',
            result=PreferenceOrganizerResultData(
                current_pass=0,
                passes_attempted=0,
                passes=[],
                total_changes=0,
                outcome='organized',
                reason='Preference already fits the resident target.',
            ),
        )

    terminal_outcome = ''
    terminal_error = ''
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
        gate = PreferenceOrganizerGate(
            pass_number=pass_number,
            budget=budget,
            min_items=min_items,
        )
        budget_before = budget.used
        run_error = _run_organizer_pass(
            gate=gate,
            before=before,
            llm_config=llm_config,
            target_items=target_items,
            min_items=min_items,
            max_items=max_items,
            target_prompt_percent=target_prompt_percent,
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
                min_items=min_items,
                max_items=max_items,
                target_prompt_percent=target_prompt_percent,
            ):
                pass_outcome = 'organized'
            elif budget.used >= budget.maximum:
                pass_outcome = 'budget_exhausted'
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
            break
        if pass_outcome in {'stale_state', 'partial', 'failed', 'budget_exhausted'}:
            terminal_outcome = pass_outcome
            terminal_error = gate.terminal_error
            break
        terminal_outcome = pass_outcome
        terminal_error = gate.terminal_error

    if terminal_outcome == 'no_safe_changes' and attempted < min(2, max_passes):
        terminal_outcome = 'failed'
        terminal_error = 'Organizer pass loop ended unexpectedly.'
    if terminal_outcome in {'organized', 'no_safe_changes', 'budget_exhausted'}:
        return PreferenceOrganizerResult(
            status='success', task_id=task_id, outcome=terminal_outcome,
            result=PreferenceOrganizerResultData(
                current_pass=attempted,
                passes_attempted=attempted,
                passes=pass_results,
                total_changes=budget.used,
                outcome=terminal_outcome,
                reason=terminal_error,
            ),
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
    min_items: int,
    max_items: int,
    target_prompt_percent: int,
    max_rounds_per_pass: int,
) -> str:
    prompt = build_preference_organizer_prompt(
        before,
        pass_number=gate.pass_number,
        min_items=min_items,
        max_items=max_items,
        target_items=target_items,
        target_prompt_percent=target_prompt_percent,
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
        ),
        error=PreferenceOrganizerError(code=code, message=message),
    )
