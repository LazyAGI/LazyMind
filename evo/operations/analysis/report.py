from __future__ import annotations

from collections import Counter, defaultdict
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import validate_case_id
from ...ids import validate_id
from ...runtime import OperationContext, OperationOutput
from .utils import score

SCORES = ('answer_correctness', 'faithfulness', 'doc_recall', 'context_recall')
CONFIDENCE = {'high': 0, 'medium': 1, 'low': 2}


class AssembleClassificationReportOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        report_ref = ArtifactRef.parse(str(ctx.params.get('eval_report_ref') or ''))
        output_id = validate_id(str(ctx.params.get('output_id') or 'classification_report'), 'output_id')
        fine_refs = [ArtifactRef.parse(str(item)) for item in ctx.params.get('fine_classification_refs') or []]
        if not fine_refs or len(set(fine_refs)) != len(fine_refs):
            raise ValueError('fine_classification_refs must be non-empty and unique')
        report = _typed(ctx, report_ref, 'EvalReport')
        bad = {validate_case_id(str(row.get('case_id') or '')): row for row in report.get('bad_cases') or []}
        rows = []
        for index, ref in enumerate(fine_refs, 1):
            ctx.check_interrupt()
            rows.append(_row(ctx, report_ref, str(report.get('eval_dataset_ref') or ''), ref, bad))
            ctx.report_progress(phase='assemble_classification_report', status='running',
                                message=f'validated {index}/{len(fine_refs)} fine classifications',
                                current_item=str(ref), done=index, total=len(fine_refs))
        _complete(bad, rows)
        priorities = _priorities(rows)
        payload = _payload(output_id, report_ref, report, rows, priorities, str(
            ctx.params.get('source_message_id') or ''
        ))
        ctx.report_progress(phase='assemble_classification_report', status='success',
                            message=f'assembled classification report with {len(rows)} cases',
                            done=len(rows), total=len(rows), detail=payload['summary'])
        return OperationOutput([ArtifactDraft(output_id, 'ClassificationReport', payload, ctx.operation_run_id,
                                              input_refs=[report_ref, *fine_refs])])


def _row(ctx, report_ref: ArtifactRef, dataset_ref: str, fine_ref: ArtifactRef, bad: dict[str, dict[str, Any]]):
    fine = _typed(ctx, fine_ref, 'CaseFineClassification')
    case_id = validate_case_id(str(fine.get('case_id') or ''))
    if case_id not in bad or str(fine.get('eval_report_ref') or '') != str(report_ref):
        raise ValueError(f'{fine_ref} does not match EvalReport bad_cases')
    coarse_ref = ArtifactRef.parse(str(fine.get('coarse_classification_ref') or ''))
    coarse = _typed(ctx, coarse_ref, 'CaseCoarseClassification')
    if str(coarse.get('case_id') or '') != case_id or str(coarse.get('eval_report_ref') or '') != str(report_ref):
        raise ValueError(f'{coarse_ref} does not match {case_id}/{report_ref}')
    if (
        str(fine.get('eval_dataset_ref') or '') != dataset_ref
        or str(coarse.get('eval_dataset_ref') or '') != dataset_ref
    ):
        raise ValueError(f'{fine_ref} eval_dataset_ref mismatch')
    for key in ('case_ref', 'rag_answer_ref', 'judge_result_ref'):
        if str(fine.get(key) or '') != str(coarse.get(key) or ''):
            raise ValueError(f'{fine_ref} {key} mismatch with coarse')
    if str(bad[case_id].get('judge_result_ref') or '') != str(fine.get('judge_result_ref') or ''):
        raise ValueError(f'{fine_ref} judge_result_ref mismatch with EvalReport')
    judge = _typed(ctx, ArtifactRef.parse(str(fine.get('judge_result_ref') or '')), 'JudgeResult')
    for key in ('case_id', 'case_ref', 'rag_answer_ref'):
        if str(judge.get(key) or '') != str(fine.get(key) or ''):
            raise ValueError(f'JudgeResult {key} mismatch for {case_id}')
    allowed = set((coarse.get('next_step') or {}).get('allowed_subcategories') or [])
    if fine.get('classification_method') != 'insufficient_evidence' and fine.get('fine_category') not in allowed:
        raise ValueError(f'{fine_ref} fine_category outside allowed taxonomy')
    quality = {key: _score(judge.get(key)) for key in SCORES}
    return {'case_id': case_id, 'fine_ref': fine_ref, 'fine': fine, 'judge': judge, 'quality': quality,
            'loss_score': round(sum(1 - quality[key] for key in SCORES), 4)}


def _typed(ctx: OperationContext, ref: ArtifactRef, schema: str) -> dict[str, Any]:
    if ctx.artifact_graph.schema_name(ref) != schema:
        raise ValueError(f'artifact is not {schema}: {ref}')
    return ctx.artifact_graph.get(ref)


def _complete(bad: dict[str, dict[str, Any]], rows: list[dict[str, Any]]) -> None:
    cases = [row['case_id'] for row in rows]
    dupes = sorted(case for case, count in Counter(cases).items() if count > 1)
    missing, extra = sorted(set(bad) - set(cases)), sorted(set(cases) - set(bad))
    if dupes or missing or extra:
        raise ValueError(f'fine classifications must match bad_cases exactly: dupes={dupes}, '
                         f'missing={missing}, extra={extra}')


def _payload(output_id, report_ref, report, rows, priorities, source_message_id) -> dict[str, Any]:
    rows = sorted(rows, key=lambda row: row['case_id'])
    summary = _summary(rows)
    return {'id': output_id, 'eval_report_ref': str(report_ref),
            'eval_dataset_ref': str(report.get('eval_dataset_ref') or ''), 'bad_case_count': len(rows),
            'classified_case_count': len(rows), 'fine_classification_refs': [str(r['fine_ref']) for r in rows],
            'summary': summary, 'priorities': priorities, 'cases': [_case(row) for row in rows],
            'handoff': _handoff(rows, priorities), 'source_message_id': source_message_id}


def _summary(rows) -> dict[str, Any]:
    fines = [row['fine'] for row in rows]
    return {'coarse_category_counts': _counts(fines, 'coarse_category'),
            'fine_category_counts': _counts(fines, 'fine_category'),
            'classification_method_counts': _counts(fines, 'classification_method'),
            'confidence_counts': _counts(fines, 'confidence')}


def _priorities(rows) -> list[dict[str, Any]]:
    groups: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        groups[str(row['fine'].get('fine_category') or '')].append(row)
    ranked = sorted(
        (-(len(group) + sum(row['loss_score'] for row in group)), category, group)
        for category, group in groups.items()
    )
    out = []
    for rank, (_, category, group) in enumerate(ranked, 1):
        loss = round(sum(row['loss_score'] for row in group), 4)
        reps = sorted(group, key=lambda row: (
            -row['loss_score'], CONFIDENCE.get(str(row['fine'].get('confidence') or ''), 3), row['case_id'],
        ))[:3]
        out.append({'rank': rank, 'fine_category': category,
                    'coarse_categories': sorted({str(row['fine'].get('coarse_category') or '') for row in group}),
                    'case_count': len(group), 'loss_score': loss, 'priority_score': round(len(group) + loss, 4),
                    'case_ids': [row['case_id'] for row in sorted(group, key=lambda item: item['case_id'])],
                    'representative_case_refs': [str(row['fine_ref']) for row in reps]})
    return out


def _case(row) -> dict[str, Any]:
    fine = row['fine']
    return {'case_id': row['case_id'], 'fine_classification_ref': str(row['fine_ref']),
            'coarse_category': fine.get('coarse_category'), 'fine_category': fine.get('fine_category'),
            'confidence': fine.get('confidence'), 'classification_method': fine.get('classification_method'),
            'llm_used': fine.get('llm_used') is True, 'quality': row['quality'],
            'loss_score': row['loss_score'], 'missing_evidence': list(fine.get('missing_evidence') or []),
            'judge_result_ref': str(fine.get('judge_result_ref') or '')}


def _handoff(rows, priorities) -> dict[str, Any]:
    return {'representative_fine_refs': [
        ref for item in priorities[:3] for ref in item['representative_case_refs'][:1]
    ]}


def _counts(items, key) -> dict[str, int]:
    return dict(Counter(str(item.get(key) or '') for item in items))


def _score(value: Any) -> float:
    number = score(value)
    if not 0 <= number <= 1:
        raise ValueError(f'score out of range: {value!r}')
    return round(number, 4)
