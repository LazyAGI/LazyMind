from __future__ import annotations

from typing import Any

from .schemas import validate_repair_artifact


def build_patch_critique(attempt: int, diagnosis: dict[str, Any], instruction: dict[str, Any],
                         worker_report: dict[str, Any], patch: dict[str, Any], evaluation: dict[str, Any],
                         candidate_report: dict[str, Any], correctness: dict[str, Any],
                         policy: dict[str, Any]) -> dict[str, Any]:
    metric = classify_metric_progress(evaluation, policy)
    trace = classify_trace_progress(evaluation)
    diff_hit = touched_suspected_location(patch, diagnosis, instruction)
    if instruction.get('mode') == 'no_patch':
        matched = worker_report.get('protocol_status') == 'not_run'
    else:
        matched = bool(diff_hit and worker_report.get('protocol_status') == 'valid')
    hypothesis = _hypothesis_status(metric, trace, diff_hit, correctness)
    reason = (f'metric_progress={metric}; trace_progress={trace}; '
              f"diff_hit={diff_hit}; correctness={correctness.get('verdict', '')}")
    primary_id = ((diagnosis.get('root_cause_hypotheses') or [{}])[0]).get('id', '')
    first_row = next((row for row in candidate_report.get('cases') or [] if isinstance(row, dict)), {})
    avoid = [] if diff_hit else ['patch did not touch suspected location']
    avoid += [str(risk.get('kind')) for risk in correctness.get('overfitting_risks') or [] if risk.get('kind')]
    scope_failure = (patch.get('scope_check') or {}).get('failure')
    if scope_failure: avoid.append(str(scope_failure))
    focus_cases = [str(row.get('case_id') or '') for row in (candidate_report.get('cases') or [])
                   if isinstance(row, dict) and row.get('case_id')]
    focus_cases += [str(row.get('case_id') or '')
                    for row in ((evaluation.get('badcase_eval') or {}).get('case_outcomes') or [])
                    if row.get('outcome') in {'unchanged', 'failed', 'regressed'}]
    payload = {
        'id': f'patch_critique_attempt_{attempt}',
        'attempt': attempt,
        'targeted_hypotheses': [str(item.get('id') or '') for item in diagnosis.get('root_cause_hypotheses') or []
                                if item.get('id')][:1],
        'diff_assessment': {
            'matched_instruction': matched, 'touched_suspected_location': diff_hit,
            'patch_shape': correctness.get('patch_shape') or {},
            'risk_flags': [risk.get('kind') for risk in correctness.get('overfitting_risks') or [] if risk.get('kind')],
        },
        'effect_assessment': {
            'metric_progress': metric, 'trace_progress': trace,
            'candidate_failure_shift': {'before_fine': str(first_row.get('baseline_fine_category') or ''),
                                        'after_fine': str(first_row.get('candidate_fine_category') or '')},
            'goodcase_impact': _goodcase_impact(evaluation),
        },
        'hypothesis_assessment': [{'hypothesis_id': primary_id, 'status': hypothesis, 'reason': reason}],
        'next_focus': {
            'cases': list(dict.fromkeys(item for item in focus_cases if item))[:5],
            'suspected_locations': [f"{item.get('path')}:{item.get('symbol')}"
                                    for item in diagnosis.get('suspected_code_locations') or []
                                    if item.get('path') and item.get('symbol')][:3],
            'hypotheses_to_continue': [primary_id] if hypothesis in {'supported', 'needs_more_patch'} else [],
            'hypotheses_to_drop': [primary_id] if hypothesis == 'rejected' else [],
        },
        'memory_update': {
            'lessons': [reason],
            'avoid_repeating': list(dict.fromkeys(avoid)),
            'promoted_knowledge': [] if metric not in {'strong', 'partial'} else ['metric progress observed'],
        },
    }
    validate_repair_artifact('PatchCritique', payload)
    return payload


def build_branch_decision(attempt: int, critique: dict[str, Any], evaluation: dict[str, Any], patch: dict[str, Any],
                          service: dict[str, Any], correctness: dict[str, Any], worker_report: dict[str, Any],
                          policy: dict[str, Any], *, budget_exhausted: bool = False,
                          terminal_failure: bool = False) -> dict[str, Any]:
    decision = decide_branch(critique, evaluation, patch, service, correctness, worker_report,
                             budget_exhausted or terminal_failure)
    carry_patch = decision in {'accept_verified', 'promote_to_best', 'continue_current_branch', 'fix_current_patch'}
    patch_ref = f"{patch.get('id')}@v1" if carry_patch and patch.get('id') else ''
    effect = critique.get('effect_assessment') or {}
    focus = critique.get('next_focus') if isinstance(critique.get('next_focus'), dict) else {}
    payload = {
        'id': f'branch_decision_attempt_{attempt}',
        'attempt': attempt,
        'decision': decision,
        'reason': (f"{decision}: metric={effect.get('metric_progress')}; "
                   f"trace={effect.get('trace_progress')}; eval={evaluation.get('status')}; "
                   f"scope={(patch.get('scope_check') or {}).get('status')}; "
                   f"correctness={correctness.get('verdict')}; worker={worker_report.get('protocol_status')}"),
        'next_base': {
            'workspace_ref': str(patch.get('workspace_ref') or service.get('workspace_ref') or ''),
            'branch_id': 'branch_active', 'base_patch_ref': patch_ref,
            'patch_lineage': [patch_ref] if patch_ref else [],
        },
        'next_instruction_seed': {
            'focus_hypothesis_ids': [str(i) for i in focus.get('hypotheses_to_continue') or [] if i],
            'abandoned_hypothesis_ids': [str(i) for i in focus.get('hypotheses_to_drop') or [] if i]
            if decision == 'abandon_direction' else [],
            'focus_case_ids': focus.get('cases') or [],
            'avoid': (critique.get('memory_update') or {}).get('avoid_repeating') or [],
            'required_shift': _required_shift(decision),
        },
        'decision_inputs': {
            'metric_progress': str(effect.get('metric_progress') or ''),
            'trace_progress': str(effect.get('trace_progress') or ''),
            'diff_hit': bool((critique.get('diff_assessment') or {}).get('touched_suspected_location')),
            'goodcase_impact': str(effect.get('goodcase_impact') or ''),
            'correctness_verdict': correctness.get('verdict', ''),
            'worker_protocol_status': worker_report.get('protocol_status', ''),
        },
    }
    validate_repair_artifact('BranchDecision', payload)
    return payload


def classify_metric_progress(evaluation: dict[str, Any], policy: dict[str, Any]) -> str:
    if evaluation.get('status') == 'incomplete': return 'execution_failed'
    overall = (evaluation.get('overall_eval') or {}).get('summary') or {}
    bad = (evaluation.get('badcase_eval') or {}).get('summary') or {}
    delta = float(overall.get('delta_mean') or bad.get('delta_mean') or 0.0)
    target = float(policy.get('target_mean_delta') or overall.get('required_delta_mean') or 0.0)
    improved, regressed = int(bad.get('improved_case_count') or 0), int(bad.get('regressed_case_count') or 0)
    if evaluation.get('status') == 'passed' and delta >= target and regressed == 0: return 'strong'
    if delta < -0.001 or regressed > improved: return 'regressed'
    if delta > 0.001 or improved > regressed: return 'partial'
    return 'none'


def classify_trace_progress(evaluation: dict[str, Any]) -> str:
    deltas = evaluation.get('trace_delta_by_case') if isinstance(evaluation.get('trace_delta_by_case'), list) else []
    kinds = {str(item.get('delta') or '') for item in deltas if isinstance(item, dict)}
    if 'fixed_transition_failure' in kinds: return 'fixed_transition'
    if 'new_failure' in kinds: return 'new_failure'
    if 'partial' in kinds or 'moved_later' in kinds: return 'partial'
    if 'unknown' in kinds: return 'unknown'
    return 'unknown' if not kinds else 'none'


def touched_suspected_location(patch: dict[str, Any], diagnosis: dict[str, Any], instruction: dict[str, Any]) -> bool:
    files = {str(path) for path in patch.get('files_changed') or []}
    primary = str(instruction.get('primary_source_observation_ref') or '')
    start = next((item for item in instruction.get('start_points') or []
                  if isinstance(item, dict) and item.get('source_observation_ref') == primary), {})
    if start:
        path = str(start.get('path') or '')
        if path not in files: return False
        hunks = patch.get('changed_hunks') if isinstance(patch.get('changed_hunks'), list) else []
        if hunks: return _hunks_touch_start(hunks, start)
        changed = _changed_code_lines(str(patch.get('diff') or ''), path)
        line_start, line_end = int(start.get('line_start') or 0), int(start.get('line_end') or 0)
        if line_start and line_end: return any(line_start <= line <= line_end for line in changed)
        return bool(changed)
    locations = diagnosis.get('suspected_code_locations') or []
    return any(str(item.get('path') or '') in files for item in locations if isinstance(item, dict))


def decide_branch(critique: dict[str, Any], evaluation: dict[str, Any], patch: dict[str, Any],
                  service: dict[str, Any], correctness: dict[str, Any], worker_report: dict[str, Any],
                  budget_exhausted: bool) -> str:
    diff = critique.get('diff_assessment') or {}
    effect = critique.get('effect_assessment') or {}
    metric, trace = effect.get('metric_progress'), effect.get('trace_progress')
    if budget_exhausted: return 'stop_failed'
    if correctness.get('verdict') == 'reject': return 'fork_from_best'
    if effect.get('goodcase_impact') == 'over_budget': return 'fork_from_best'
    if _verification_command_failed(evaluation): return 'fork_from_best'
    gates = _quality_gates_pass(critique, patch, service, correctness, worker_report)
    if (evaluation.get('status') == 'passed' and gates
            and (critique.get('effect_assessment') or {}).get('goodcase_impact') != 'over_budget'
            and not _verification_command_failed(evaluation)):
        return 'accept_verified'
    lessons = (critique.get('memory_update') or {}).get('lessons') or []
    # Full cross-attempt counting is added with BranchManager memory. For record mode,
    # only an explicit repeated_rejection lesson triggers abandon_direction.
    if any('rejected_twice' in str(item) for item in lessons): return 'abandon_direction'
    if trace == 'new_failure': return 'fork_from_best'
    if metric == 'execution_failed':
        service_failed = (service.get('healthcheck') or {}).get('status') == 'failed'
        return 'fix_current_patch' if diff.get('touched_suspected_location') and service_failed else 'fork_from_best'
    if (metric in {'strong', 'partial'} and trace in {'fixed_transition', 'partial'} and gates
            and effect.get('goodcase_impact') == 'within_budget'):
        return 'promote_to_best'
    if diff.get('touched_suspected_location') and trace in {'partial', 'unknown'} and metric in {'none', 'partial'}:
        return 'continue_current_branch'
    return 'fork_from_best'


def _quality_gates_pass(critique: dict[str, Any], patch: dict[str, Any], service: dict[str, Any],
                        correctness: dict[str, Any], worker_report: dict[str, Any]) -> bool:
    diff = critique.get('diff_assessment') if isinstance(critique.get('diff_assessment'), dict) else {}
    if (service.get('healthcheck') or {}).get('status') != 'passed': return False
    if (patch.get('scope_check') or {}).get('status') != 'passed': return False
    if correctness.get('verdict') != 'acceptable': return False
    if worker_report.get('protocol_status') != 'valid': return False
    return bool(diff.get('matched_instruction')) and bool(diff.get('touched_suspected_location'))


def _hypothesis_status(metric: str, trace: str, diff_hit: bool, correctness: dict[str, Any]) -> str:
    if correctness.get('verdict') == 'reject' or not diff_hit or metric == 'regressed' or trace == 'new_failure':
        return 'rejected'
    if metric in {'strong', 'partial'} and trace in {'fixed_transition', 'partial', 'unknown'}: return 'supported'
    if diff_hit and metric in {'none', 'partial'} and trace in {'partial', 'unknown'}: return 'needs_more_patch'
    return 'weakened'


def _goodcase_impact(evaluation: dict[str, Any]) -> str:
    guard = evaluation.get('goodcase_impact') or evaluation.get('goodcase_guard') or {}
    if not guard or guard.get('skipped'): return 'within_budget'
    return 'within_budget' if guard.get('passed') else 'over_budget'


def _required_shift(decision: str) -> str:
    return {
        'fork_from_best': 'change hypothesis or source location',
        'continue_current_branch': 'continue adjacent focused patch',
        'fix_current_patch': 'fix execution bug in current patch',
        'promote_to_best': 'build on promoted partial improvement',
        'accept_verified': 'stop; verified repair accepted',
        'stop_failed': 'stop; attempt budget or unrecoverable failure',
        'abandon_direction': 'drop rejected hypothesis direction',
    }.get(decision, '')


def _verification_command_failed(evaluation: dict[str, Any]) -> bool:
    return any(isinstance(item, dict) and (item.get('status') == 'failed' or item.get('exit_code'))
               for item in evaluation.get('verification_command_failures') or [])


def _hunks_touch_start(hunks: list[Any], start: dict[str, Any]) -> bool:
    path = str(start.get('path') or '')
    symbol = str(start.get('symbol_hint') or start.get('symbol') or '')
    line_start, line_end = int(start.get('line_start') or 0), int(start.get('line_end') or 0)
    for hunk in hunks:
        if not isinstance(hunk, dict) or hunk.get('comment_only') is True: continue
        if str(hunk.get('path') or '') != path: continue
        hunk_symbol = str(hunk.get('symbol') or '')
        if symbol and hunk_symbol and hunk_symbol == symbol: return True
        hunk_start, hunk_end = int(hunk.get('line_start') or 0), int(hunk.get('line_end') or 0)
        if line_start and line_end and hunk_start and hunk_end and hunk_start <= line_end and hunk_end >= line_start:
            return True
    return False


def _changed_code_lines(diff: str, path: str) -> list[int]:
    current, new_line, changed = '', 0, []
    for line in diff.splitlines():
        if line.startswith('@@'):
            marker = next((part for part in line.split() if part.startswith('+')), '+0')
            new_line = int(marker[1:].split(',', 1)[0] or 0)
            continue
        if line.startswith('+++ b/'):
            current = line[6:]
            continue
        if current != path: continue
        if line.startswith('+') and not line.startswith('+++'):
            text = line[1:].strip()
            if text and not text.startswith('#'): changed.append(new_line)
            new_line += 1
        elif line.startswith('-') and not line.startswith('---'):
            text = line[1:].strip()
            if text and not text.startswith('#'): changed.append(new_line)
        elif not line.startswith('\\'):
            new_line += 1
    return changed
