from __future__ import annotations

from typing import Any

from ..analysis.utils import METRICS, typed_payload
from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import validate_case_id
from ...ids import validate_id
from ...runtime import OperationContext, OperationOutput


class CompareABTestOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        base_ref, cand_ref = _ref(ctx, 'baseline_eval_report_ref'), _ref(ctx, 'candidate_eval_report_ref')
        base, cand = typed_payload(ctx, base_ref, 'EvalReport'), typed_payload(ctx, cand_ref, 'EvalReport')
        dataset_ref, case_ids = _dataset(base, cand), _case_ids(base, cand)
        if ctx.params.get('case_ids') is not None:
            raise ValueError('case_ids is not supported; compare uses full EvalReport case set')
        policy = _policy(ctx.params)
        before, after = _rows(ctx, base), _rows(ctx, cand)
        missing = sorted(set(case_ids) - set(before)) + sorted(set(case_ids) - set(after))
        if missing:
            raise ValueError(f'EvalReport missing JudgeResult cases: {missing}')
        rows = [_delta(case_id, before[case_id], after[case_id], policy) for case_id in case_ids]
        metrics, guard = _metrics(case_ids, before, after), _guard(rows, policy)
        decision = _decision(metrics, guard, policy)
        output_id = validate_id(str(ctx.params.get('output_id') or 'abtest_comparison'), 'output_id')
        payload = {'id': output_id, 'baseline_eval_report_ref': str(base_ref),
                   'candidate_eval_report_ref': str(cand_ref), 'eval_dataset_ref': str(dataset_ref),
                   'case_ids': case_ids, 'metrics': metrics, 'case_deltas': rows,
                   'goodcase_guard': guard, 'decision': decision,
                   'source_message_id': str(ctx.params.get('source_message_id') or '')}
        ctx.report_progress(phase='abtest_compare', status='success',
                            message=f"abtest decision: {decision['status']}", current_item=output_id,
                            detail={'primary_delta': decision['primary_delta'], 'guard_passed': guard['passed']})
        return OperationOutput([ArtifactDraft(output_id, 'ABTestComparison', payload, ctx.operation_run_id,
                                              [base_ref, cand_ref])])


def _ref(ctx, name) -> ArtifactRef:
    ref = ArtifactRef.parse(str(ctx.params.get(name) or ''))
    if ctx.artifact_graph.schema_name(ref) != 'EvalReport':
        raise ValueError(f'{name} must be EvalReport: {ref}')
    return ref


def _dataset(base, cand) -> ArtifactRef:
    ref = str(base.get('eval_dataset_ref') or '')
    if not ref or ref != str(cand.get('eval_dataset_ref') or ''):
        raise ValueError('baseline and candidate EvalReport must use the same eval_dataset_ref')
    return ArtifactRef.parse(ref)


def _case_ids(base, cand) -> list[str]:
    base_ids, cand_ids = (_ids(report) for report in (base, cand))
    if base_ids != cand_ids:
        raise ValueError(f'EvalReport case sets mismatch: {sorted(base_ids ^ cand_ids)}')
    if not base_ids:
        raise ValueError('ABTest comparison case_ids cannot be empty')
    return sorted(base_ids)


def _ids(report) -> set[str]:
    return {case_id for ref in report.get('judge_result_refs') or [] for case_id in [_case_id(ref)] if case_id}


def _rows(ctx, report) -> dict[str, dict[str, Any]]:
    rows = {}
    for raw_ref in report.get('judge_result_refs') or []:
        ref = ArtifactRef.parse(str(raw_ref))
        judge = typed_payload(ctx, ref, 'JudgeResult')
        rows[validate_case_id(str(judge.get('case_id') or _case_id(raw_ref)))] = judge | {'judge_ref': str(ref)}
    return rows


def _policy(params) -> dict[str, Any]:
    primary = str(params.get('primary_metric') or 'answer_correctness')
    if primary not in METRICS:
        raise ValueError(f'unsupported primary_metric: {primary}')
    return {'primary_metric': primary,
            'target_mean_delta': _ratio(params, 'target_mean_delta', 0.02),
            'goodcase_regression_ratio_limit': _ratio(params, 'goodcase_regression_ratio_limit', 0.34),
            'regression_epsilon': _ratio(params, 'regression_epsilon', 0.02)}


def _delta(case_id, before, after, policy) -> dict[str, Any]:
    b, a = _scores(before), _scores(after)
    delta = {metric: round(a[metric] - b[metric], 4) for metric in METRICS}
    return {'case_id': case_id, 'baseline_judge_ref': before['judge_ref'],
            'candidate_judge_ref': after['judge_ref'],
            'before': b | {'quality_label': before.get('quality_label', 'bad')},
            'after': a | {'quality_label': after.get('quality_label', 'bad')},
            'delta': delta, 'outcome': _outcome(delta[policy['primary_metric']], policy['regression_epsilon'])}


def _scores(row) -> dict[str, float]:
    out = {metric: _number(row.get(metric), metric) for metric in METRICS}
    bad = [metric for metric, value in out.items() if not 0 <= value <= 1]
    if bad:
        raise ValueError(f'{bad[0]} out of range for {row.get("judge_ref")}: {row.get(bad[0])!r}')
    return out


def _metrics(case_ids, before_rows, after_rows) -> dict[str, dict[str, float]]:
    before, after = (_summary([rows[case_id] for case_id in case_ids]) for rows in (before_rows, after_rows))
    return {'baseline': before, 'candidate': after,
            'delta': {key: round(after[key] - before[key], 4) for key in before}}


def _summary(rows) -> dict[str, float]:
    if any(not isinstance(row.get('is_correct'), bool) for row in rows):
        raise ValueError('is_correct missing from ABTest JudgeResult')
    scores = [_scores(row) for row in rows]
    return {f'{m}_avg': _avg([item[m] for item in scores]) for m in METRICS} | {
        'correct_rate': _avg([1.0 if row.get('is_correct') is True else 0.0 for row in rows]),
    }


def _guard(rows, policy) -> dict[str, Any]:
    good = [row for row in rows if row['before']['quality_label'] == 'good']
    regressed = [row for row in good if row['outcome'] == 'regressed']
    ratio = round(len(regressed) / len(good), 4) if good else 0.0
    return {'baseline_goodcase_count': len(good), 'regressed_count': len(regressed),
            'regression_ratio': ratio, 'limit': policy['goodcase_regression_ratio_limit'],
            'passed': ratio <= policy['goodcase_regression_ratio_limit']}


def _decision(metrics, guard, policy) -> dict[str, Any]:
    key = f"{policy['primary_metric']}_avg"
    delta, target = metrics['delta'][key], policy['target_mean_delta']
    passed = delta >= target and guard['passed']
    return {'status': 'accept' if passed else 'reject', 'primary_metric': key, 'primary_delta': delta,
            'target_mean_delta': target,
            'reasons': [f"primary metric delta {delta} {'>=' if delta >= target else '<'} target {target}",
                        f"goodcase regression ratio {guard['regression_ratio']} "
                        f"{'<=' if guard['passed'] else '>'} limit {guard['limit']}"]}


def _ratio(params, name, default) -> float:
    value = _number(params.get(name, default), name)
    value = value / 100 if value > 1 else value
    if not 0 <= value <= 1:
        raise ValueError(f'{name} out of range: {value}')
    return value


def _number(value, name) -> float:
    try:
        return round(float(value), 4)
    except (TypeError, ValueError) as exc:
        raise ValueError(f'{name} must be number: {value!r}') from exc


def _case_id(value) -> str:
    text = str(value)
    return text.split('judge_result_', 1)[1].split('@', 1)[0] if 'judge_result_' in text else ''


def _outcome(delta, epsilon) -> str:
    return 'improved' if delta > epsilon else 'regressed' if delta < -epsilon else 'unchanged'


def _avg(values) -> float:
    return round(sum(values) / len(values), 4) if values else 0.0
