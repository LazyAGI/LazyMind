from __future__ import annotations

from typing import Any, Optional

from lazymind.common.maintenance import initialize_context, check_cancelled

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
    MAX_PASSES,
    MAX_ROUNDS_PER_PASS,
)
from lazymind.review.preference_organizer.tools import (
    PreferenceOrganizerAnalyzeTools,
    PreferenceOrganizerApplyTools,
    PreferenceOrganizerGate,
)


def organize_preferences(
    *,
    task_id: str,
    run_id: str,
    user_id: str,
    llm_config: Optional[dict[str, Any]] = None,
    force_analysis: bool = False,
) -> PreferenceOrganizerResult:
    initialize_context(task_id, run_id, user_id)
    lazyllm.set_trace_context(
        {
            'trace_id': task_id,
            'user_id': user_id,
            'sampled': True,
            'request_tags': ['preference_organizer'],
            'trace_metadata': {'task_id': task_id, 'run_id': run_id},
        }
    )
    inject_model_config(llm_config)
    lazyllm.globals['agentic_config'] = {
        **lazyllm.globals['agentic_config'],
        'user_id': user_id,
        'task_id': task_id,
        'memory_operation_ledger': [],
    }
    pass_results: list[PreferenceOrganizerPassResult] = []

    try:
        initial = load_preference_state()
    except ValueError as exc:
        return _failure_result(task_id, 'invalid_preference_index', str(exc), retryable=False)
    except Exception as exc:
        return _failure_result(task_id, 'storage_read_failed', str(exc), retryable=True)

    if initial.data.stored_items == 0 or (not force_analysis and target_reached(initial.data)):
        return _result(
            task_id,
            outcome='no_safe_changes',
            pass_results=[],
            passes_attempted=0,
            target_reached_value=target_reached(initial.data),
            stop_reason='empty_index' if initial.data.stored_items == 0 else 'target_reached',
            reason='No adjustment needed.',
        )

    terminal_outcome = ''
    terminal_error = ''
    stop_reason = ''
    reached = False
    attempted = 0
    for pass_number in range(1, MAX_PASSES + 1):
        attempted = pass_number
        try:
            before = load_preference_state()
        except Exception as exc:
            return _failure_result(
                task_id,
                'storage_read_failed',
                str(exc),
                retryable=True,
                pass_results=pass_results,
                passes_attempted=attempted,
            )
        check_cancelled()
        gate = PreferenceOrganizerGate(pass_number=pass_number)
        run_error = _run_organizer_pass(
            gate=gate,
            before=before,
            llm_config=llm_config,
            max_rounds_per_pass=MAX_ROUNDS_PER_PASS,
        )
        pass_changes = sum(receipt['changes'] for receipt in gate.operations)
        try:
            after = load_preference_state()
        except Exception as exc:
            pass_results.append(
                PreferenceOrganizerPassResult(
                    pass_number=pass_number,
                    plan_hash=gate.plan_hash,
                    before=before.data,
                    after=None,
                    changes=pass_changes,
                    operation_count=len(gate.operations),
                    receipts=gate.operations,
                    outcome='partial' if pass_changes else 'failed',
                )
            )
            terminal_outcome = 'partial' if pass_changes else 'failed'
            terminal_error = gate.terminal_error or f'Final validation read failed: {exc}'
            stop_reason = 'validation_read_failed'
            break
        reached = target_reached(after.data)
        pass_outcome = 'failed' if gate.terminal_outcome == 'cancelled' else gate.terminal_outcome
        if run_error and not pass_outcome:
            pass_outcome = 'failed'
            gate.terminal_error = run_error
        if not gate.plan_hash and not pass_outcome:
            pass_outcome = 'failed'
            gate.terminal_error = 'Organizer ended without submit_preference_plan.'
        if not pass_outcome and gate.next_operation_index < len(gate.authorized_operations):
            pass_outcome = 'failed'
            gate.terminal_error = 'Organizer ended before all gated operations were applied.'
        if not pass_outcome:
            if not pass_changes:
                pass_outcome = 'no_safe_changes'
            elif reached:
                pass_outcome = 'organized'
            else:
                pass_outcome = 'changes_applied'
        pass_results.append(
            PreferenceOrganizerPassResult(
                pass_number=pass_number,
                plan_hash=gate.plan_hash,
                before=before.data,
                after=after.data,
                changes=pass_changes,
                operation_count=len(gate.operations),
                receipts=gate.operations,
                outcome=pass_outcome,
            )
        )
        if pass_outcome == 'organized':
            terminal_outcome = 'organized'
            stop_reason = 'target_reached'
            break
        if pass_outcome in {'stale_state', 'partial', 'failed'}:
            terminal_outcome = pass_outcome
            terminal_error = gate.terminal_error
            stop_reason = pass_outcome
            break
        if pass_outcome == 'no_safe_changes':
            terminal_outcome = 'organized_with_remaining' if any(p.changes for p in pass_results) else 'no_safe_changes'
            terminal_error = gate.terminal_error
            stop_reason = 'no_further_safe_changes'
            break
        if pass_number == MAX_PASSES:
            terminal_outcome = 'organized_with_remaining'
            terminal_error = gate.terminal_error
            stop_reason = 'max_passes_reached'
            break

    if not terminal_outcome:
        terminal_outcome = 'failed'
        terminal_error = 'Organizer pass loop ended unexpectedly.'
        stop_reason = 'unexpected_loop_end'
    return _result(
        task_id,
        outcome=terminal_outcome,
        pass_results=pass_results,
        passes_attempted=attempted,
        target_reached_value=reached,
        stop_reason=stop_reason,
        reason=terminal_error,
        retryable=_retryable(terminal_error) and terminal_outcome not in {'stale_state', 'partial'},
    )


def _run_organizer_pass(
    *,
    gate: PreferenceOrganizerGate,
    before,
    llm_config: Optional[dict[str, Any]],
    max_rounds_per_pass: int,
) -> str:
    prompt = build_preference_organizer_prompt(
        before,
        pass_number=gate.pass_number,
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
                gate,
                llm_config=llm_config,
                llm=llm,
            ),
            fs=FS,
            enable_builtin_tools=False,
            force_summarize=True,
            extra_stop_condition=check_cancelled,
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
    return any(
        marker in normalized
        for marker in (
            'timeout',
            'timed out',
            'connection',
            'temporarily unavailable',
            'rate limit',
        )
    )


def _failure_result(
    task_id: str,
    code: str,
    message: str,
    *,
    retryable: bool,
    pass_results: Optional[list[PreferenceOrganizerPassResult]] = None,
    passes_attempted: int = 0,
) -> PreferenceOrganizerResult:
    return _result(
        task_id,
        outcome='failed',
        retryable=retryable,
        pass_results=pass_results or [],
        passes_attempted=passes_attempted,
        reason=message,
        stop_reason=code,
    )


def _result(
    task_id: str,
    *,
    outcome: str,
    pass_results: list[PreferenceOrganizerPassResult],
    passes_attempted: int,
    stop_reason: str,
    reason: str,
    target_reached_value: bool = False,
    retryable: bool = False,
) -> PreferenceOrganizerResult:
    success = outcome in {'organized', 'organized_with_remaining', 'no_safe_changes'}
    return PreferenceOrganizerResult(
        status='success' if success else 'failed',
        task_id=task_id,
        outcome=outcome,
        retryable=retryable if not success else False,
        error=None if success else PreferenceOrganizerError(
            code=stop_reason, message=reason or 'Preference Organizer failed.'
        ),
        result=PreferenceOrganizerResultData(
            passes_attempted=passes_attempted,
            passes=pass_results,
            total_changes=sum(p.changes for p in pass_results),
            outcome=outcome,
            reason=reason,
            target_reached=target_reached_value if success else False,
            stop_reason=stop_reason,
        ),
    )
