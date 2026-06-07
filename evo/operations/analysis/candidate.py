from __future__ import annotations

from collections import Counter
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ...runtime import OperationContext
from . import coarse as coarse_rules
from . import fine as fine_rules
from .trace import build_trace_access, list_trace_steps, load_trace_payload
from .utils import METRICS, typed_payload


def classify_candidate_case(
    ctx: OperationContext,
    row: dict[str, Any],
    plan: dict[str, Any],
    attempt: int,
    llm: Any,
) -> tuple[dict[str, Any], list[ArtifactDraft]]:
    case_id = row['case_id']
    rag_row, judge_row = row.get('candidate_rag_answer') or {}, row.get('candidate_judge_result') or {}
    dataset_ref = ArtifactRef.parse(str(plan['eval_dataset_ref']))
    report_ref = ArtifactRef.parse(str(plan['eval_report_ref']))
    case_ref = ArtifactRef.parse(str(row['case_ref']))
    case = typed_payload(ctx, case_ref, 'DatasetCase')
    ids = {
        name: f'candidate_{name}_{case_id}_attempt_{attempt}'
        for name in ('rag_answer', 'judge_result', 'coarse_classification', 'fine_classification')
    }
    refs = {name: _next_ref(ctx, artifact_id) for name, artifact_id in ids.items()}
    rag = {'case_id': case_id, 'case_ref': str(case_ref), 'eval_dataset_ref': str(dataset_ref), **rag_row}
    judge = {
        'case_id': case_id,
        'case_ref': str(case_ref),
        'eval_dataset_ref': str(dataset_ref),
        'rag_answer_ref': str(refs['rag_answer']),
        **judge_row,
    }
    llm_refs, trace_plan, trace_reads, trace_error = [], {}, [], ''
    try:
        trace_id = str(rag.get('trace_id') or '')
        rag['trace'] = load_trace_payload(ctx, trace_id, rag)
        coarse = coarse_rules.classify_payload(
            report_ref, dataset_ref, case_ref, refs['rag_answer'], refs['judge_result'],
            case, rag, judge, trace_id, rag['trace'],
        )
        fine = fine_rules.classify_payload(
            ctx,
            refs['coarse_classification'],
            coarse,
            case,
            rag,
            judge,
            fine_rules.CaseFineClassificationOperation(llm),
        )
        llm_refs = list(fine.get('llm_call_refs') or [])
        trace_plan, trace_reads = fine.get('trace_plan') or {}, fine.get('trace_reads') or []
    except Exception as exc:
        trace_error = str(exc)[:200]
        coarse = _coarse_error(
            case_id, report_ref, dataset_ref, case_ref, refs['rag_answer'], refs['judge_result'], judge, trace_error
        )
        fine = {
            'case_id': case_id,
            'coarse_classification_ref': str(refs['coarse_classification']),
            'eval_report_ref': str(report_ref),
            'eval_dataset_ref': str(dataset_ref),
            'case_ref': str(case_ref),
            'rag_answer_ref': str(refs['rag_answer']),
            'judge_result_ref': str(refs['judge_result']),
            'coarse_category': 'insufficient_evidence',
            **fine_rules._insufficient([trace_error]),
            'llm_used': False,
            'llm_call_refs': [],
            'llm_call_reasons': [],
            'trace_used': True,
            'trace_plan': {},
            'trace_reads': [],
        }
    drafts = [
        ArtifactDraft(
            ids['rag_answer'],
            'RagAnswer',
            {key: value for key, value in rag.items() if key != 'trace'},
            ctx.operation_run_id,
            input_refs=[case_ref],
        ),
        ArtifactDraft(
            ids['judge_result'],
            'JudgeResult',
            judge,
            ctx.operation_run_id,
            input_refs=[case_ref, refs['rag_answer']],
        ),
        ArtifactDraft(
            ids['coarse_classification'],
            'CaseCoarseClassification',
            coarse,
            ctx.operation_run_id,
            input_refs=[report_ref, dataset_ref, case_ref, refs['rag_answer'], refs['judge_result']],
        ),
        ArtifactDraft(
            ids['fine_classification'],
            'CaseFineClassification',
            fine,
            ctx.operation_run_id,
            input_refs=[
                refs['coarse_classification'], report_ref, dataset_ref, case_ref,
                refs['rag_answer'], refs['judge_result'],
            ],
        ),
    ]
    return _candidate_row(row, refs, coarse, fine, judge, rag, llm_refs, trace_plan, trace_reads, trace_error), drafts


def candidate_row_refs(row: dict[str, Any]) -> list[str]:
    keys = (
        'candidate_rag_answer_ref',
        'candidate_judge_result_ref',
        'candidate_coarse_classification_ref',
        'candidate_fine_classification_ref',
    )
    return [str(row.get(key) or '') for key in keys if row.get(key)]


def candidate_failure_categories(candidate_report: dict[str, Any]) -> list[dict[str, str]]:
    return [
        {
            'case_id': row.get('case_id'),
            'coarse': row.get('candidate_coarse_category', ''),
            'fine': row.get('candidate_fine_category', ''),
            'outcome': row.get('outcome', ''),
        }
        for row in candidate_report.get('cases', [])
    ]


def candidate_trace_summary(ctx: OperationContext, rag: dict[str, Any]) -> dict[str, Any]:
    trace_id = str(rag.get('trace_id') or '')
    try:
        trace = rag.get('trace') if isinstance(rag.get('trace'), dict) else load_trace_payload(ctx, trace_id, rag)
        access = build_trace_access(trace, trace_id)
        steps = list_trace_steps(access)
        return {
            'trace_id': trace_id,
            'trace_available': True,
            'step_count': len(steps),
            'role_counts': dict(Counter(step.get('role') for step in steps)),
            'error_steps': [
                _brief(step)
                for step in steps
                if str(step.get('status') or '').lower() not in {'', 'ok', 'success', 'succeeded'}
            ][:5],
        }
    except Exception as exc:
        return {'trace_id': trace_id, 'trace_available': False, 'error': str(exc)[:200]}


def _candidate_row(
    row: dict[str, Any],
    refs: dict[str, ArtifactRef],
    coarse: dict[str, Any],
    fine: dict[str, Any],
    judge: dict[str, Any],
    rag: dict[str, Any],
    llm_refs: list[str],
    trace_plan: dict[str, Any],
    trace_reads: list[dict[str, Any]],
    trace_error: str,
) -> dict[str, Any]:
    return {
        'case_id': row['case_id'],
        'case_ref': row.get('case_ref'),
        'baseline_judge_ref': row.get('baseline_judge_ref'),
        'candidate_rag_answer_ref': str(refs['rag_answer']),
        'candidate_judge_result_ref': str(refs['judge_result']),
        'candidate_coarse_classification_ref': str(refs['coarse_classification']),
        'candidate_fine_classification_ref': str(refs['fine_classification']),
        'outcome': row.get('outcome'),
        'before': row.get('before'),
        'after': row.get('after'),
        'delta': row.get('delta'),
        'classification_source': 'case_coarse_fine_operation_logic',
        'candidate_coarse_category': coarse['coarse_category'],
        'candidate_coarse_evidence': {
            'reason': coarse.get('coarse_reason'),
            'confidence': coarse.get('confidence'),
            'rule_hits': (coarse.get('evidence') or {}).get('rule_hits', [])[:3],
            'missing_evidence': coarse.get('missing_evidence', []),
        },
        'candidate_fine_category': fine['fine_category'],
        'candidate_fine_evidence_summary': {
            'classification_method': fine.get('classification_method'),
            'reason': fine.get('reason'),
            'fine_rule_hits': (fine.get('evidence') or {}).get('fine_rule_hits', [])[:3],
            'llm_evidence_refs': (fine.get('evidence') or {}).get('llm_evidence_refs', []),
            'llm_call_refs': llm_refs,
            'trace_plan': trace_plan,
            'trace_reads': trace_reads,
            'missing_evidence': fine.get('missing_evidence', []),
            'quality_label': judge.get('quality_label'),
            'failure_type': judge.get('failure_type'),
            'doc_ids': rag.get('doc_ids', []),
            'chunk_ids': rag.get('chunk_ids', []),
        },
        'trace_read_summary': row.get('candidate_trace_summary') or {},
        'classification_error': trace_error,
        'regression_reason': judge.get('reason') if row.get('outcome') == 'regressed' else '',
    }


def _coarse_error(
    case_id: str,
    report_ref: ArtifactRef,
    dataset_ref: ArtifactRef,
    case_ref: ArtifactRef,
    rag_ref: ArtifactRef,
    judge_ref: ArtifactRef,
    judge: dict[str, Any],
    error: str,
) -> dict[str, Any]:
    return {
        'case_id': case_id,
        'eval_report_ref': str(report_ref),
        'eval_dataset_ref': str(dataset_ref),
        'case_ref': str(case_ref),
        'rag_answer_ref': str(rag_ref),
        'judge_result_ref': str(judge_ref),
        'coarse_category': 'insufficient_evidence',
        'coarse_reason': 'candidate classification lacks readable trace or artifact evidence',
        'confidence': 'low',
        'trace_used': True,
        'evidence': {
            'summary': {key: judge.get(key) for key in ('quality_label', 'failure_type', *METRICS)},
            'rule_hits': [],
        },
        'missing_evidence': [error],
        'next_step': {
            'operation_type': 'CaseFineClassificationOperation',
            'allowed_subcategories': [],
            'llm_allowed': False,
        },
    }


def _next_ref(ctx: OperationContext, artifact_id: str) -> ArtifactRef:
    try:
        return ArtifactRef(artifact_id, ctx.artifact_graph.latest_ref(artifact_id).version + 1)
    except KeyError:
        return ArtifactRef(artifact_id, 1)


def _brief(step: dict[str, Any]) -> dict[str, Any]:
    return {
        key: step.get(key)
        for key in ('index', 'step_id', 'name', 'node_type', 'role', 'status', 'parent_step_id')
    }
