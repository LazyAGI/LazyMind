from __future__ import annotations

from collections import Counter
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import validate_case_id
from ...ids import validate_id
from ...runtime import OperationOutput

METRICS = ('answer_correctness', 'faithfulness', 'doc_recall', 'context_recall')


class EvalAggregateOperation:
    def execute(self, ctx) -> OperationOutput:
        dataset_ref = ArtifactRef.parse(str(ctx.params.get('eval_dataset_ref') or ''))
        report_id = validate_id(str(ctx.params.get('report_id') or 'eval_report'), 'report_id')
        if ctx.artifact_graph.schema_name(dataset_ref) != 'EvalDataset':
            raise ValueError(f'artifact is not EvalDataset: {dataset_ref}')
        case_ids, case_refs = _dataset_cases(ctx.artifact_graph.get(dataset_ref))
        rows = []
        for index, (case_id, case_ref) in enumerate(zip(case_ids, case_refs), 1):
            ctx.check_interrupt()
            rows.append(_row(ctx, dataset_ref, case_id, case_ref))
            ctx.report_progress(phase='eval_aggregate', status='running',
                                message=f'aggregated {index}/{len(case_ids)} judge results',
                                current_item=case_id, done=index, total=len(case_ids))
        payload = _report(report_id, dataset_ref, rows, str(ctx.params.get('source_message_id') or ''))
        ctx.report_progress(phase='eval_aggregate', status='success',
                            message=f'aggregated eval report with {len(rows)} cases',
                            current_item=report_id, done=len(rows), total=len(rows), detail=payload['metrics'])
        refs = [dataset_ref, *[row['judge_ref'] for row in rows]]
        return OperationOutput([ArtifactDraft(report_id, 'EvalReport', payload, ctx.operation_run_id, input_refs=refs)])


def _dataset_cases(dataset: dict[str, Any]) -> tuple[list[str], list[ArtifactRef]]:
    case_ids = [validate_case_id(str(item)) for item in dataset.get('case_ids') or []]
    case_refs = [ArtifactRef.parse(str(item)) for item in dataset.get('case_refs') or []]
    if not case_ids or len(case_ids) != len(case_refs):
        raise ValueError('EvalDataset case_ids/case_refs length mismatch')
    return case_ids, case_refs


def _row(ctx, dataset_ref: ArtifactRef, case_id: str, case_ref: ArtifactRef) -> dict[str, Any]:
    judge_ref = next((ref for ref in ctx.input_refs if ref.artifact_id == f'judge_result_{case_id}'), None)
    judge_ref = judge_ref or ctx.artifact_graph.latest_ref(f'judge_result_{case_id}')
    if ctx.artifact_graph.schema_name(judge_ref) != 'JudgeResult':
        raise ValueError(f'artifact is not JudgeResult: {judge_ref}')
    if ctx.artifact_graph.schema_name(case_ref) != 'DatasetCase':
        raise ValueError(f'artifact is not DatasetCase: {case_ref}')
    judge, case = ctx.artifact_graph.get(judge_ref), ctx.artifact_graph.get(case_ref)
    for key, value in {'eval_dataset_ref': str(dataset_ref), 'case_id': case_id, 'case_ref': str(case_ref)}.items():
        if str(judge.get(key) or '') != value:
            raise ValueError(f'JudgeResult {key} mismatch: {judge.get(key)!r} != {value!r}')
    scores = {key: _score(judge, key) for key in METRICS}
    return {'case_id': case_id, 'case_ref': case_ref, 'judge_ref': judge_ref, 'judge': judge, 'case': case, **scores}


def _report(report_id: str, dataset_ref: ArtifactRef, rows: list[dict[str, Any]],
            source_message_id: str) -> dict[str, Any]:
    quality_counts, failure_counts = Counter(map(_quality, rows)), Counter(map(_failure, rows))
    return {
        'id': report_id,
        'eval_dataset_ref': str(dataset_ref),
        'total': len(rows),
        'judge_result_refs': [str(row['judge_ref']) for row in rows],
        'metrics': {
            'correct_count': sum(row['judge'].get('is_correct') is True for row in rows),
            'correct_rate': _avg([1.0 if row['judge'].get('is_correct') is True else 0.0 for row in rows]),
            **{f'{key}_avg': _avg([row[key] for row in rows]) for key in METRICS},
        },
        'quality_counts': dict(quality_counts),
        'failure_type_counts': dict(failure_counts),
        'by_question_type': _group(rows, 'question_type'),
        'by_difficulty': _group(rows, 'difficulty'),
        'bad_cases': [_bad_case(row) for row in rows if _quality(row) != 'good'],
        'checks': _checks(rows),
        'source_message_id': source_message_id,
    }


def _group(rows: list[dict[str, Any]], key: str) -> dict[str, Any]:
    out = {}
    for name in sorted({str(row['case'].get(key) or '') for row in rows}):
        group = [row for row in rows if str(row['case'].get(key) or '') == name]
        out[name] = {'total': len(group), 'correct_rate': _avg([
            1.0 if row['judge'].get('is_correct') is True else 0.0 for row in group
        ]), 'quality_counts': dict(Counter(map(_quality, group)))}
    return out


def _bad_case(row: dict[str, Any]) -> dict[str, Any]:
    keys = ('case_id', 'quality_label', 'failure_type', 'answer_correctness', 'faithfulness', 'reason', 'defect',
            'trace_id')
    return {key: row['judge'].get(key) for key in keys} | {'judge_result_ref': str(row['judge_ref'])}


def _checks(rows: list[dict[str, Any]]) -> dict[str, Any]:
    warnings = []
    for row in rows:
        case_id, judge = row['case_id'], row['judge']
        warnings += [{'code': 'bad_case', 'case_id': case_id, 'message': 'case quality_label is bad'}] \
            if _quality(row) == 'bad' else []
        warnings += [{'code': 'failure_type', 'case_id': case_id, 'message': f'failure_type={_failure(row)}'}] \
            if _failure(row) != 'none' else []
        warnings += [{'code': 'missing_trace_id', 'case_id': case_id, 'message': 'judge result has empty trace_id'}] \
            if not str(judge.get('trace_id') or '').strip() else []
        warnings += [{'code': 'low_recall', 'case_id': case_id, 'message': 'doc_recall or context_recall is zero'}] \
            if row['doc_recall'] == 0 or row['context_recall'] == 0 else []
    return {'ready': True, 'errors': [], 'warnings': warnings}


def _score(judge: dict[str, Any], key: str) -> float:
    value = round(float(judge.get(key)), 4)
    if value < 0 or value > 1:
        raise ValueError(f'{key} out of range: {judge.get(key)!r}')
    return value


def _avg(values: list[float]) -> float:
    return round(sum(values) / len(values), 4) if values else 0.0


def _quality(row: dict[str, Any]) -> str:
    return str(row['judge'].get('quality_label') or 'bad')


def _failure(row: dict[str, Any]) -> str:
    return str(row['judge'].get('failure_type') or 'unknown')
