from __future__ import annotations

import math
from collections.abc import Mapping
from pathlib import Path
from statistics import fmean
from typing import Any

from evo.operations.abtest.materializers import (
    GOODCASE_MAX_OVERALL_DROP,
    candidate_rag_answer,
    candidate_service,
    compare_eval_detail_for_repair,
)
from evo.operations.analysis.summary import build_analysis_from_answers
from evo.operations.eval.judge import judge_case
from evo.operations.eval.materializers import build_eval_detail_summary
from evo.operations.router_manager import RouterManager, RouterManagerError

from .errors import EXTERNAL_CHAT_FAILURE_TYPES
from .trace import safe_emit

PUBLIC_SERVICE_KEYS = {
    'status',
    'service_kind',
    'algorithm_id',
    'router_chat_url',
    'router_admin_url',
    'code_path',
}
EPSILON = 0.0001
BADCASE_MIN_OVERALL_GAIN = 0.10


def validate_candidate_patch(
    root: Path,
    diff: str,
    plan: Mapping[str, Any],
    cases: Mapping[str, Mapping[str, Any]],
    baseline_judges: Mapping[str, Mapping[str, Any]],
    eval_policy: Mapping[str, Any],
    candidate_config: Mapping[str, Any],
    ctx: Any,
    trace: Any | None = None,
    attempt: int | None = None,
) -> dict[str, Any]:
    objective = plan.get('objective') if isinstance(plan.get('objective'), Mapping) else {}
    required = [_text(item) for item in objective.get('validation_case_ids') or [] if _text(item)]
    selected = {case_id: cases[case_id] for case_id in required if case_id in cases}
    missing_cases = [case_id for case_id in required if case_id not in cases]
    missing_baseline = [case_id for case_id in required if case_id in cases and case_id not in baseline_judges]
    if missing_cases or missing_baseline:
        return {
            'status': 'rejected',
            'accepted': False,
            'reason': 'validation_case_coverage_missing',
            'missing_cases': missing_cases,
            'missing_baseline_judges': missing_baseline,
            'case_ids': list(selected),
        }
    if not selected:
        return {'status': 'rejected', 'accepted': False, 'reason': 'no_validation_cases'}
    patch = {'status': 'verified', 'workspace_ref': str(root), 'diff': diff}
    safe_emit(
        trace, 'candidate.service_started', status='started', attempt=attempt,
        payload={'case_count': len(selected)},
    )
    service: Mapping[str, Any] | None = None
    cleanup_service = True
    try:
        service = candidate_service(candidate_config, patch, ctx)
    except Exception as exc:
        safe_emit(
            trace, 'candidate.service_failed', status='failed', attempt=attempt,
            payload={'error_type': type(exc).__name__},
        )
        raise
    try:
        public_service = {key: value for key, value in service.items()
                          if key in PUBLIC_SERVICE_KEYS and key != 'healthcheck'}
        health = service.get('healthcheck') if isinstance(service.get('healthcheck'), Mapping) else {}
        public_service['healthcheck'] = {
            key: health.get(key)
            for key in ('status', 'type', 'algorithm_status', 'healthy_instances')
            if key in health
        }
        if service.get('status') != 'ready':
            safe_emit(
                trace, 'candidate.service_failed', status='failed', attempt=attempt,
                payload={'reason': 'candidate_service_failed', 'service': public_service},
            )
            return {'status': 'candidate_service_failed', 'accepted': False, 'reason': 'candidate_service_failed',
                    'service': public_service, 'case_ids': list(selected)}
        safe_emit(trace, 'candidate.service_ready', status='completed', attempt=attempt,
                  payload={'service': public_service})
        answers, judges, early_stop_reason = {}, {}, ''
        for case_id, case in selected.items():
            safe_emit(trace, 'candidate.case_started', status='started', attempt=attempt, payload={'case_id': case_id})
            try:
                answer = candidate_rag_answer(case, service)
                answers[case_id] = answer
                judges[case_id] = judge_case(case, answer, eval_policy)
            except Exception as exc:
                safe_emit(
                    trace, 'candidate.case_completed', status='failed', attempt=attempt,
                    payload={'case_id': case_id, 'error_type': type(exc).__name__},
                )
                raise
            safe_emit(trace, 'candidate.case_completed', status='completed', attempt=attempt, payload={
                'case_id': case_id,
                'answer_status': answer.get('status'),
                'trace_id': answer.get('trace_id'),
                'quality_label': judges[case_id].get('quality_label'),
                'answer_correctness': judges[case_id].get('answer_correctness'),
                'overall_score': judges[case_id].get('overall_score'),
            })
            chat_error = answer.get('chat_error') if isinstance(answer.get('chat_error'), Mapping) else {}
            if (
                judges[case_id].get('failure_type') == 'infra_failure'
                and chat_error.get('type') in EXTERNAL_CHAT_FAILURE_TYPES
            ):
                early_stop_reason = str(chat_error.get('type') or 'candidate_external_failure')
                break
        answer_refs = {
            case_id: {'status': answer.get('status'), 'trace_id': answer.get('trace_id')}
            for case_id, answer in answers.items()
        }
        judge_refs = {
            case_id: {
                'quality_label': judge.get('quality_label'),
                'failure_type': judge.get('failure_type'),
                'retrieval_failure_type': judge.get('retrieval_failure_type'),
                'overall_score': judge.get('overall_score'),
                'answer_correctness': judge.get('answer_correctness'),
            }
            for case_id, judge in judges.items()
        }
        baseline_summary = build_eval_detail_summary(tuple(
            baseline_judges[case_id] for case_id in judges if case_id in baseline_judges
        ))
        candidate_summary = build_eval_detail_summary(tuple(judges[case_id] for case_id in judges))
        candidate_summary = candidate_summary | {'id': 'repair.candidate_eval_summary'}
        comparison = compare_eval_detail_for_repair(baseline_summary, candidate_summary)
        safe_emit(trace, 'candidate.eval_summary_completed', status='completed', attempt=attempt, payload={
            'case_count': len(selected),
            'evaluated_case_count': len(judges),
            'early_stop_reason': early_stop_reason,
            'execution_failure_count': len(candidate_summary.get('execution_failures') or []),
            'metrics': candidate_summary.get('metrics') if isinstance(candidate_summary.get('metrics'), Mapping) else {},
            'comparison_status': comparison.get('status'),
            'comparison_verdict': comparison.get('verdict'),
        })
        evidence = {
            'case_ids': list(selected),
            'service': public_service,
            'candidate_answer_refs': answer_refs,
            'candidate_judge_refs': judge_refs,
            'candidate_eval_summary': candidate_summary,
            'comparison': comparison,
        }
        if early_stop_reason:
            return {
                'status': 'rejected',
                'accepted': False,
                'reason': f'candidate_eval_stopped:{early_stop_reason}',
                **evidence,
                'evaluated_case_ids': list(judges),
                'early_stop_reason': early_stop_reason,
            }
        try:
            safe_emit(
                trace, 'analysis.candidate_started', status='started', attempt=attempt,
                payload={'case_count': len(selected)},
            )
            analysis = build_analysis_from_answers(selected, answers, judges) | {'id': 'repair.candidate_analysis'}
        except Exception as exc:
            safe_emit(
                trace, 'analysis.candidate_completed', status='failed', attempt=attempt,
                payload={'error_type': type(exc).__name__},
            )
            return {
                'status': 'rejected',
                'accepted': False,
                'reason': f'candidate_analysis_failed:{type(exc).__name__}',
                **evidence,
                'candidate_analysis_error': str(exc),
            }
        delta = _analysis_delta_from(plan, comparison, analysis, candidate_summary)
        safe_emit(
            trace, 'analysis.candidate_completed', status='completed', attempt=attempt,
            payload={'row_count': len(analysis.get('rows') or [])},
        )
        safe_emit(trace, 'analysis.delta_completed', status='completed', attempt=attempt, payload={
            key: delta.get(key)
            for key in ('target_group_status', 'target_remaining_badcase_count',
                        'target_remaining_delta', 'target_badcase_count', 'new_group_count',
                        'goodcase_guard_status', 'recommended_action')
        })
        accepted, reason = _candidate_gate(comparison, candidate_summary, delta)
        cleanup_service = not accepted
        return {
            'status': 'accepted' if accepted else 'rejected',
            'accepted': accepted,
            'reason': reason,
            **evidence,
            'candidate_analysis': analysis,
            'analysis_delta': delta,
        }
    finally:
        if cleanup_service:
            _cleanup_candidate_service(service, trace=trace, attempt=attempt)


def _cleanup_candidate_service(
    service: Mapping[str, Any] | None,
    *,
    trace: Any | None = None,
    attempt: int | None = None,
) -> dict[str, Any]:
    if not service:
        return {'status': 'not_applicable', 'reason': 'missing_service'}
    if service.get('status') != 'ready':
        return {'status': 'not_applicable', 'reason': 'service_not_ready'}
    if service.get('cleanup_allowed') is not True:
        return {'status': 'not_applicable', 'reason': 'cleanup_not_owned'}
    registered = service.get('register_response') if isinstance(service.get('register_response'), Mapping) else {}
    if registered.get('reused') is True:
        return {'status': 'not_applicable', 'reason': 'reused_service'}
    algorithm_id = _text(service.get('algorithm_id'))
    admin_url = _text(service.get('router_admin_url'))
    if not algorithm_id or not admin_url:
        return {'status': 'not_applicable', 'reason': 'missing_router_target'}
    if not algorithm_id.startswith('evo_'):
        return {'status': 'not_applicable', 'reason': 'non_evo_algorithm', 'algorithm_id': algorithm_id}

    payload = {'algorithm_id': algorithm_id}
    try:
        RouterManager(admin_url, _text(service.get('router_chat_url'))).stop_algorithm(algorithm_id)
    except RouterManagerError as exc:
        safe_emit(trace, 'candidate.service_stopped', status='failed', attempt=attempt,
                  payload=payload | {'error_type': exc.kind})
        return {'status': 'failed', 'algorithm_id': algorithm_id, 'error_type': exc.kind, 'message': str(exc)}
    safe_emit(trace, 'candidate.service_stopped', status='completed', attempt=attempt, payload=payload)
    return {'status': 'completed', 'algorithm_id': algorithm_id}


def _analysis_delta_from(
    plan: Mapping[str, Any],
    comparison: Mapping[str, Any],
    analysis: Mapping[str, Any],
    candidate_summary: Mapping[str, Any],
) -> dict[str, Any]:
    selected = plan.get('selected_group') if isinstance(plan.get('selected_group'), Mapping) else {}
    target = set((plan.get('objective') or {}).get('target_cases') or [])
    baseline_target_count = len(set(selected.get('case_ids') or []) & target) or len(target)
    rows = [row for row in analysis.get('rows') or [] if isinstance(row, Mapping) and row.get('case_id') in target]
    target_badcases = [row for row in rows if _text(row.get('issue_type')) != 'correct']
    remaining = [
        row for row in rows
        if _text(row.get('affected_block')) == _text(selected.get('function_block_id'))
        and _text(row.get('failure_mode')) == _text(selected.get('failure_mode'))
        and _text(row.get('issue_type')) != 'correct'
    ]
    old_groups = {
        (
            _text(group.get('function_block_id')),
            _text(group.get('issue_type')),
            _text(group.get('failure_mode')),
            _text(group.get('trace_signature')),
        )
        for group in plan.get('repair_group_queue') or []
        if isinstance(group, Mapping)
    }
    new_groups = [
        group for group in analysis.get('repair_group_queue') or []
        if isinstance(group, Mapping)
        and (
            _text(group.get('function_block_id')),
            _text(group.get('issue_type')),
            _text(group.get('failure_mode')),
            _text(group.get('trace_signature')),
        ) not in old_groups
    ]
    metrics = comparison.get('metrics') if isinstance(comparison.get('metrics'), Mapping) else {}
    delta = metrics.get('delta') if isinstance(metrics.get('delta'), Mapping) else {}
    target_metric_delta = _target_metric_delta(selected, rows)
    target_remaining_delta = baseline_target_count - len(remaining)
    resolved = not remaining and not target_badcases
    improved = target_remaining_delta > 0
    status = 'resolved' if resolved else 'improved' if improved else 'unchanged'
    score_gate = _overall_score_gate(comparison)
    if score_gate['goodcase_status'] == 'failed':
        status = 'regressed'
    score_ready = (
        score_gate['overall_status'] == 'passed'
        and score_gate['badcase_status'] == 'passed'
        and score_gate['goodcase_status'] != 'failed'
    )
    recommended_action = 'accept_patch' if score_ready else 'rollback_to_baseline'
    return {
        'status': 'completed',
        'target_group_status': status,
        'target_remaining_badcase_count': len(remaining),
        'target_remaining_delta': target_remaining_delta,
        'target_badcase_count': len(target_badcases),
        'target_total': len(target),
        'new_group_count': len(new_groups),
        'new_groups': new_groups[:5],
        'goodcase_guard_status': score_gate['goodcase_status'],
        'metric_delta': delta,
        'target_metric_delta': target_metric_delta,
        'overall_score_gate': score_gate,
        'primary_metrics': list(selected.get('primary_metrics') or ()),
        'execution_failures': candidate_summary.get('execution_failures') or [],
        'recommended_action': recommended_action,
    }


def _candidate_gate(
    comparison: Mapping[str, Any],
    candidate_summary: Mapping[str, Any],
    delta: Mapping[str, Any],
) -> tuple[bool, str]:
    if comparison.get('status') != 'completed':
        return False, _text(comparison.get('verdict')) or 'comparison_not_completed'
    if candidate_summary.get('execution_failures'):
        return False, 'candidate_execution_failed'
    metric_status = _metric_gate(comparison)
    if metric_status:
        return False, metric_status
    return True, 'overall_score_improved'


def _target_metric_delta(selected: Mapping[str, Any], rows: list[Mapping[str, Any]]) -> dict[str, float]:
    baseline = {
        _text(item.get('case_id')): item.get('metrics')
        for item in selected.get('evidence') or []
        if isinstance(item, Mapping) and isinstance(item.get('metrics'), Mapping)
    }
    result: dict[str, list[float]] = {}
    for row in rows:
        case_id = _text(row.get('case_id'))
        before = baseline.get(case_id)
        after = row.get('judge') if isinstance(row.get('judge'), Mapping) else {}
        if not before:
            continue
        for metric in selected.get('primary_metrics') or ():
            key = _text(metric)
            if key and key in before and key in after:
                result.setdefault(key, []).append(_float(after.get(key)) - _float(before.get(key)))
    return {key: round(sum(values) / len(values), 4) for key, values in result.items() if values}


def _metric_gate(comparison: Mapping[str, Any]) -> str:
    score_gate = _overall_score_gate(comparison)
    if score_gate['overall_status'] == 'failed':
        return 'overall_score_not_improved'
    if score_gate['badcase_status'] != 'passed':
        return 'badcase_overall_not_improved'
    if score_gate['goodcase_status'] == 'failed':
        return 'goodcase_overall_regressed'
    return ''


def _overall_score_gate(comparison: Mapping[str, Any]) -> dict[str, Any]:
    metrics = comparison.get('metrics') if isinstance(comparison.get('metrics'), Mapping) else {}
    delta = metrics.get('delta') if isinstance(metrics.get('delta'), Mapping) else {}
    overall_delta = _float(delta.get('overall_score'))
    result = {
        'overall_delta': round(overall_delta, 4) if math.isfinite(overall_delta) else overall_delta,
        'overall_status': 'passed' if math.isfinite(overall_delta) and overall_delta > EPSILON else 'failed',
        'badcase_status': 'missing',
        'goodcase_status': 'not_applicable',
    }
    badcase, goodcase = [], []
    for row in comparison.get('case_deltas') or ():
        if not isinstance(row, Mapping):
            continue
        before = row.get('before') if isinstance(row.get('before'), Mapping) else {}
        after = row.get('after') if isinstance(row.get('after'), Mapping) else {}
        pair = (_float(before.get('overall_score')), _float(after.get('overall_score')))
        if not all(math.isfinite(value) for value in pair):
            return result | {'badcase_status': 'failed', 'goodcase_status': 'failed'}
        if _text(row.get('baseline_quality')) == 'good':
            goodcase.append(pair)
        else:
            badcase.append(pair)
    if badcase:
        before = fmean(item[0] for item in badcase)
        after = fmean(item[1] for item in badcase)
        gain = after - before
        result |= {
            'badcase_count': len(badcase),
            'badcase_baseline_overall_avg': round(before, 4),
            'badcase_candidate_overall_avg': round(after, 4),
            'badcase_overall_delta': round(gain, 4),
            'badcase_required_delta': BADCASE_MIN_OVERALL_GAIN,
            'badcase_status': 'passed' if gain + EPSILON >= BADCASE_MIN_OVERALL_GAIN else 'failed',
        }
    if goodcase:
        before = fmean(item[0] for item in goodcase)
        after = fmean(item[1] for item in goodcase)
        drop = before - after
        result |= {
            'goodcase_count': len(goodcase),
            'goodcase_baseline_overall_avg': round(before, 4),
            'goodcase_candidate_overall_avg': round(after, 4),
            'goodcase_overall_delta': round(after - before, 4),
            'goodcase_allowed_drop': GOODCASE_MAX_OVERALL_DROP,
            'goodcase_status': 'passed' if drop <= GOODCASE_MAX_OVERALL_DROP + EPSILON else 'failed',
        }
    return result


def _float(value: Any) -> float:
    try:
        return float(value or 0.0)
    except (TypeError, ValueError):
        return 0.0


def _text(value: Any) -> str:
    return str(value or '').strip()
