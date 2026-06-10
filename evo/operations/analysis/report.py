from __future__ import annotations

from collections import Counter, defaultdict
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import validate_case_id
from ... import validate_id
from ...runtime import OperationContext, OperationOutput
from .coarse import NON_REPAIRABLE_CATEGORIES
from .utils import bound_input_ref, score, typed_payload

SCORES = ('answer_correctness', 'faithfulness', 'doc_recall', 'context_recall')
CONFIDENCE = {'high': 0, 'medium': 1, 'low': 2}


class AssembleClassificationReportOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        report_ref = bound_input_ref(ctx, ctx.params.get('eval_report_ref'), 'EvalReport')
        output_id = validate_id(str(ctx.params.get('output_id') or 'classification_report'), 'output_id')
        fine_refs = [bound_input_ref(ctx, item, 'CaseFineClassification')
                     for item in ctx.params.get('fine_classification_refs') or []]
        if not fine_refs or len(set(fine_refs)) != len(fine_refs):
            raise ValueError('fine_classification_refs must be non-empty and unique')
        report = typed_payload(ctx, report_ref, 'EvalReport')
        bad = {validate_case_id(str(row.get('case_id') or '')): row for row in report.get('bad_cases') or []}
        rows = []
        for index, ref in enumerate(fine_refs, 1):
            ctx.check_interrupt()
            rows.append(_row(ctx, report_ref, str(report.get('eval_dataset_ref') or ''), ref, bad))
            ctx.report_progress(phase='assemble_classification_report', status='running',
                                message=f'validated {index}/{len(fine_refs)} fine classifications',
                                current_item=str(ref), done=index, total=len(fine_refs))
        cases = [row['case_id'] for row in rows]
        dupes = sorted(case for case, count in Counter(cases).items() if count > 1)
        missing, extra = sorted(set(bad) - set(cases)), sorted(set(cases) - set(bad))
        if dupes or missing or extra:
            raise ValueError(f'fine classifications must match bad_cases exactly: dupes={dupes}, '
                             f'missing={missing}, extra={extra}')
        priorities = _priorities(rows)
        calibration = _calibration(ctx, ctx.params.get('calibration_classification_refs') or [])
        rows = sorted(rows, key=lambda row: row['case_id'])
        fines = [row['fine'] for row in rows]
        counts = ('coarse_category', 'fine_category', 'classification_method', 'confidence')
        payload = {'id': output_id, 'eval_report_ref': str(report_ref),
                   'eval_dataset_ref': str(report.get('eval_dataset_ref') or ''), 'bad_case_count': len(rows),
                   'classified_case_count': len(rows),
                   'fine_classification_refs': [str(r['fine_ref']) for r in rows],
                   'summary': {f'{k}_counts': dict(Counter(str(f.get(k) or '') for f in fines)) for k in counts},
                   'priorities': priorities, 'cases': [_case(row) for row in rows], 'calibration': calibration,
                   'quality_gate': _quality_gate(rows, calibration),
                   'handoff': {'representative_fine_refs': [ref for item in priorities[:3]
                                                            for ref in item['representative_case_refs'][:1]]},
                   'source_message_id': str(ctx.params.get('source_message_id') or '')}
        ctx.report_progress(phase='assemble_classification_report', status='success',
                            message=f'assembled classification report with {len(rows)} cases',
                            done=len(rows), total=len(rows), detail=payload['summary'])
        return OperationOutput([ArtifactDraft(output_id, 'ClassificationReport', payload, ctx.operation_run_id,
                                              input_refs=[report_ref, *fine_refs])])


def _row(ctx, report_ref: ArtifactRef, dataset_ref: str, fine_ref: ArtifactRef, bad: dict[str, dict[str, Any]]):
    fine = typed_payload(ctx, fine_ref, 'CaseFineClassification')
    case_id = validate_case_id(str(fine.get('case_id') or ''))
    if case_id not in bad or str(fine.get('eval_report_ref') or '') != str(report_ref):
        raise ValueError(f'{fine_ref} does not match EvalReport bad_cases')
    coarse_ref = ArtifactRef.parse(str(fine.get('coarse_classification_ref') or ''))
    coarse = typed_payload(ctx, coarse_ref, 'CaseCoarseClassification')
    if str(coarse.get('case_id') or '') != case_id or str(coarse.get('eval_report_ref') or '') != str(report_ref):
        raise ValueError(f'{coarse_ref} does not match {case_id}/{report_ref}')
    if str(fine.get('eval_dataset_ref') or '') != dataset_ref \
            or str(coarse.get('eval_dataset_ref') or '') != dataset_ref:
        raise ValueError(f'{fine_ref} eval_dataset_ref mismatch')
    for key in ('case_ref', 'rag_answer_ref', 'judge_result_ref'):
        if str(fine.get(key) or '') != str(coarse.get(key) or ''):
            raise ValueError(f'{fine_ref} {key} mismatch with coarse')
    if str(bad[case_id].get('judge_result_ref') or '') != str(fine.get('judge_result_ref') or ''):
        raise ValueError(f'{fine_ref} judge_result_ref mismatch with EvalReport')
    judge = typed_payload(ctx, ArtifactRef.parse(str(fine.get('judge_result_ref') or '')), 'JudgeResult')
    for key in ('case_id', 'case_ref', 'rag_answer_ref'):
        if str(judge.get(key) or '') != str(fine.get(key) or ''):
            raise ValueError(f'JudgeResult {key} mismatch for {case_id}')
    allowed = set((coarse.get('next_step') or {}).get('allowed_subcategories') or [])
    if (fine.get('classification_method') not in {'insufficient_evidence', 'infra_failure'}
            and fine.get('fine_category') not in allowed):
        raise ValueError(f'{fine_ref} fine_category outside allowed taxonomy')
    quality = {}
    for key in SCORES:
        number = score(judge.get(key))
        if not 0 <= number <= 1: raise ValueError(f'score out of range: {judge.get(key)!r}')
        quality[key] = round(number, 4)
    return {'case_id': case_id, 'fine_ref': fine_ref, 'fine': fine, 'judge': judge, 'quality': quality,
            'loss_score': round(sum(1 - quality[key] for key in SCORES), 4)}


def _calibration(ctx, raw_refs) -> dict[str, Any]:
    """Goodcase calibration sample: a high-confidence failure verdict on a good case is a false positive."""
    samples = []
    for item in raw_refs:
        ref = bound_input_ref(ctx, item, 'CaseCoarseClassification')
        coarse = typed_payload(ctx, ref, 'CaseCoarseClassification')
        flagged = (coarse.get('coarse_category') not in {'other', 'infra_failure'}
                   and coarse.get('confidence') == 'high')
        samples.append({'case_id': str(coarse.get('case_id') or ''), 'ref': str(ref),
                        'coarse_category': coarse.get('coarse_category'),
                        'confidence': coarse.get('confidence'), 'false_positive': flagged})
    false_positives = [item for item in samples if item['false_positive']]
    return {'sampled': len(samples), 'false_positive_count': len(false_positives), 'samples': samples,
            'false_positive_ratio': round(len(false_positives) / len(samples), 4) if samples else 0.0}


def _quality_gate(rows, calibration) -> dict[str, Any]:
    """Block repair handoff when classification ran fully rule-driven without any LLM review at scale,
    or when the goodcase calibration sample shows the classifier flags every good case as a failure."""
    fines = [row['fine'] for row in rows]
    reviewed = sum(1 for fine in fines if fine.get('llm_used') or (fine.get('adjudication') or {}).get('sampled'))
    disagreements = sum(1 for fine in fines if (fine.get('adjudication') or {}).get('agreement') is False)
    coverage = round(reviewed / len(fines), 4) if fines else 0.0
    errors = []
    if len(fines) >= 10 and coverage == 0:
        errors.append({'code': 'no_llm_review',
                       'message': 'large rule-only classification requires LLM review coverage'})
    if calibration['sampled'] >= 2 and calibration['false_positive_ratio'] >= 1.0:
        errors.append({'code': 'calibration_failed',
                       'message': 'classifier flagged every goodcase calibration sample as a failure'})
    return {'ready': not errors, 'llm_review_count': reviewed, 'llm_review_coverage': coverage,
            'adjudication_disagreements': disagreements, 'errors': errors}


def _priorities(rows) -> list[dict[str, Any]]:
    groups: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        groups[str(row['fine'].get('fine_category') or '')].append(row)
    ranked = sorted((-(len(group) + sum(row['loss_score'] for row in group)), category, group)
                    for category, group in groups.items())
    out = []
    for rank, (_, category, group) in enumerate(ranked, 1):
        loss = round(sum(row['loss_score'] for row in group), 4)
        reps = sorted(group, key=lambda row: (
            -row['loss_score'], CONFIDENCE.get(str(row['fine'].get('confidence') or ''), 3), row['case_id']))[:3]
        out.append({'rank': rank, 'fine_category': category,
                    'coarse_categories': sorted({str(row['fine'].get('coarse_category') or '') for row in group}),
                    'case_count': len(group), 'loss_score': loss, 'priority_score': round(len(group) + loss, 4),
                    'repairable': category not in NON_REPAIRABLE_CATEGORIES,
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
