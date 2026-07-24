from __future__ import annotations

from collections.abc import Mapping
from statistics import fmean
from typing import Any

from evo.operations.public_contracts import AGGREGATES, AbtestComparison, EvalSummary, dump_contract


COMPARE_METRICS = (
    'overall_score',
    'answer_quality_score',
    'retrieval_quality_score',
    'answer_correctness',
    'groundedness',
    'correct_rate',
)
GOODCASE_MAX_OVERALL_DROP = 0.05


def compare_abtest(
    run_id: str,
    baseline: Mapping[str, Any],
    candidate: Mapping[str, Any],
    service: Mapping[str, Any],
) -> dict[str, Any]:
    baseline_summary = EvalSummary.model_validate(_with_scored_count(baseline)).model_dump(mode='json')
    candidate_summary = EvalSummary.model_validate(_with_scored_count(candidate)).model_dump(mode='json')
    origin = _eval_body(baseline_summary)
    after = _eval_body(candidate_summary)
    delta = {key: round(float(after[key]) - float(origin[key]), 4) for key in AGGREGATES}
    failures = []
    if service.get('status') != 'ready':
        failures.append('candidate service is not ready')
    if not origin['cases']:
        failures.append('abtest has no cases')
    if origin['scored_case_num'] < 0:
        failures.append('baseline summary lacks scored case count')
    elif origin['scored_case_num'] != len(origin['cases']):
        failures.append('baseline evaluation has unscored cases')
    if after['scored_case_num'] < 0:
        failures.append('candidate summary lacks scored case count')
    elif after['scored_case_num'] != len(after['cases']):
        failures.append('candidate evaluation has unscored cases')
    if {row['case_id'] for row in origin['cases']} != {row['case_id'] for row in after['cases']}:
        failures.append('baseline and candidate case sets differ')
    reasons = list(failures)
    if delta['avg_overall'] < 0:
        reasons.append('candidate avg_overall regressed')
    if delta['correct_rate'] < 0:
        reasons.append('candidate correct_rate regressed')
    verdict = 'reject' if reasons else 'accept'
    return dump_contract(AbtestComparison, {
        'run_id': str(run_id),
        'algo_id': str(baseline_summary.get('algo_id') or ''),
        'candidate_algo_id': str(service.get('algorithm_id') or ''),
        'status': 'failed' if failures else 'completed',
        'verdict': verdict,
        'reasons': reasons,
        'origin': origin,
        'candidate': after,
        'delta': delta,
    })


def compare_eval_detail_for_repair(
    baseline: Mapping[str, Any],
    candidate: Mapping[str, Any],
) -> dict[str, Any]:
    baseline_rows = _rows_by_case(baseline.get('rows'))
    candidate_rows = _rows_by_case(candidate.get('rows'))
    case_ids = list(dict.fromkeys([
        *baseline_rows,
        *candidate_rows,
        *baseline.get('case_ids', ()),
        *candidate.get('case_ids', ()),
    ]))
    before, after = _summary_metrics(baseline), _summary_metrics(candidate)
    delta = {key: round(after[key] - before[key], 4) for key in before}
    case_deltas = [
        _case_delta(case_id, baseline_rows.get(case_id), candidate_rows.get(case_id))
        for case_id in case_ids
    ]
    failures = list(candidate.get('execution_failures') or [])
    failed = bool(failures) or not (candidate.get('checks') or {}).get('ready')
    reasons = ['candidate evaluation produced execution failures'] if failed else []
    verdict = 'candidate_eval_failed' if failed else 'review_candidate'
    guard = _goodcase_guard(case_deltas)
    return {
        'id': 'abtest.comparison',
        'status': 'failed' if failed else 'completed',
        'verdict': verdict,
        'case_ids': case_ids,
        'case_count': len(case_ids),
        'metrics': {'baseline': before, 'candidate': after, 'delta': delta},
        'case_deltas': case_deltas,
        'goodcase_guard': guard,
        'policy': {'primary_metric': 'overall_score', 'guard_metrics': ['overall_score']},
        'decision': {'status': verdict, 'primary_metric': 'overall_score', 'reasons': reasons},
        'reasons': reasons,
        'missing_metrics': [
            {'case_id': row['case_id'], 'outcome': row['outcome']}
            for row in case_deltas
            if row['outcome'].startswith('missing_')
        ],
        'data_warnings': [],
        'baseline': {'total': baseline.get('total', 0), 'quality_counts': dict(baseline.get('quality_counts') or {})},
        'candidate': {'total': candidate.get('total', 0), 'quality_counts': dict(candidate.get('quality_counts') or {})},
        'summary': {
            'metrics': {'baseline': before, 'candidate': after, 'delta': delta},
            'case_deltas': case_deltas,
            'goodcase_guard': guard,
            'decision': verdict,
            'case_count': len(case_ids),
            'reasons': reasons,
        },
    }


def _eval_body(summary: Mapping[str, Any]) -> dict[str, Any]:
    return {key: summary[key] for key in ('scored_case_num', *AGGREGATES, 'cases')}


def _with_scored_count(summary: Mapping[str, Any]) -> dict[str, Any]:
    return dict(summary) | {'scored_case_num': int(summary.get('scored_case_num', -1))}


def _summary_metrics(summary: Mapping[str, Any]) -> dict[str, float]:
    metrics = summary.get('metrics') if isinstance(summary.get('metrics'), Mapping) else {}
    return {key: _float(metrics.get(key if key == 'correct_rate' else f'{key}_avg')) for key in COMPARE_METRICS}


def _rows_by_case(rows: object) -> dict[str, Mapping[str, Any]]:
    items = rows if isinstance(rows, (list, tuple)) else ()
    return {
        _text(row.get('case_id')): row
        for row in items
        if isinstance(row, Mapping) and _text(row.get('case_id'))
    }


def _case_delta(
    case_id: str,
    baseline: Mapping[str, Any] | None,
    candidate: Mapping[str, Any] | None,
) -> dict[str, Any]:
    before, after = _row_metrics(baseline or {}), _row_metrics(candidate or {})
    delta = {key: round(after[key] - before[key], 4) for key in before}
    if not baseline:
        outcome = 'missing_baseline'
    elif not candidate:
        outcome = 'missing_candidate'
    elif candidate.get('failure_type') == 'infra_failure' or delta['overall_score'] < -0.0001:
        outcome = 'regressed'
    elif delta['overall_score'] > 0.0001:
        outcome = 'improved'
    else:
        outcome = 'unchanged'
    return {
        'case_id': case_id,
        'outcome': outcome,
        'before': before,
        'after': after,
        'delta': delta,
        'baseline_quality': _text((baseline or {}).get('quality_label')),
        'candidate_quality': _text((candidate or {}).get('quality_label')),
    }


def _row_metrics(row: Mapping[str, Any]) -> dict[str, float]:
    return {key: _float(row.get(key)) for key in COMPARE_METRICS if key != 'correct_rate'}


def _goodcase_guard(case_deltas: list[dict[str, Any]]) -> dict[str, Any]:
    pairs = [
        (row['before']['overall_score'], row['after']['overall_score'])
        for row in case_deltas
        if row.get('baseline_quality') == 'good'
    ]
    if not pairs:
        return {'status': 'not_applicable', 'count': 0}
    before = fmean(pair[0] for pair in pairs)
    after = fmean(pair[1] for pair in pairs)
    drop = round(before - after, 4)
    return {
        'status': 'failed' if drop > GOODCASE_MAX_OVERALL_DROP else 'passed',
        'count': len(pairs),
        'baseline_overall_avg': round(before, 4),
        'candidate_overall_avg': round(after, 4),
        'overall_delta': round(after - before, 4),
        'allowed_drop': GOODCASE_MAX_OVERALL_DROP,
    }


def _float(value: object) -> float:
    try:
        return round(float(value or 0.0), 4)
    except (TypeError, ValueError):
        return 0.0


def _text(value: object) -> str:
    return str(value or '').strip()
