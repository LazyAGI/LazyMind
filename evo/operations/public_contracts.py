from __future__ import annotations

from collections import Counter
from collections.abc import Mapping
from statistics import fmean
from typing import Any

from pydantic import BaseModel, ConfigDict, Field, FiniteFloat, StrictInt, StrictStr

METRICS = {
    'answer_correctness': ('correctness', 'avg_correctness'),
    'answer_relevance': ('relevance', 'avg_relevance'),
    'completeness': ('completeness', 'avg_completeness'),
    'groundedness': ('groundedness', 'avg_groundedness'),
    'format_compliance': ('format_compliance', 'avg_format_compliance'),
    'answer_quality_score': ('answer_quality', 'avg_answer_quality'),
    'retrieval_quality_score': ('retrieval_quality', 'avg_retrieval_quality'),
    'overall_score': ('overall', 'avg_overall'),
}
AGGREGATES = tuple(value[1] for value in METRICS.values()) + ('correct_rate',)


class Contract(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class EvalCase(Contract):
    case_id: StrictStr
    trace_id: StrictStr
    correctness: FiniteFloat = Field(ge=0.0, le=1.0)
    relevance: FiniteFloat = Field(ge=0.0, le=1.0)
    completeness: FiniteFloat = Field(ge=0.0, le=1.0)
    groundedness: FiniteFloat = Field(ge=0.0, le=1.0)
    format_compliance: FiniteFloat = Field(ge=0.0, le=1.0)
    answer_quality: FiniteFloat = Field(ge=0.0, le=1.0)
    retrieval_quality: FiniteFloat = Field(ge=0.0, le=1.0)
    overall: FiniteFloat = Field(ge=0.0, le=1.0)
    reason: StrictStr


class DatasetCase(Contract):
    case_id: StrictStr
    source: StrictStr
    answer: Any
    difficulty: StrictStr
    difficulty_rationale: StrictStr
    grading_guidance: StrictStr
    original_id: StrictStr
    question: StrictStr
    question_type: StrictStr
    reasoning_steps: list[StrictStr]
    reference_chunk_ids: list[StrictStr]
    reference_context: list[StrictStr]
    reference_doc: list[StrictStr]
    reference_doc_ids: list[StrictStr]
    source_message_id: StrictStr
    source_preparation: dict[str, Any]
    type_rationale: StrictStr


class DatasetRoot(Contract):
    run_id: StrictStr
    case_num: StrictInt
    cases: list[DatasetCase]


class EvalSummary(Contract):
    run_id: StrictStr
    case_num: StrictInt
    algo_id: StrictStr
    avg_correctness: FiniteFloat
    avg_relevance: FiniteFloat
    avg_completeness: FiniteFloat
    avg_groundedness: FiniteFloat
    avg_format_compliance: FiniteFloat
    avg_answer_quality: FiniteFloat
    avg_retrieval_quality: FiniteFloat
    avg_overall: FiniteFloat
    correct_rate: FiniteFloat
    cases: list[EvalCase]


class EvalBody(Contract):
    avg_correctness: FiniteFloat
    avg_relevance: FiniteFloat
    avg_completeness: FiniteFloat
    avg_groundedness: FiniteFloat
    avg_format_compliance: FiniteFloat
    avg_answer_quality: FiniteFloat
    avg_retrieval_quality: FiniteFloat
    avg_overall: FiniteFloat
    correct_rate: FiniteFloat
    cases: list[EvalCase]


class AnalysisCase(Contract):
    case_id: StrictStr
    trace_id: StrictStr
    source: StrictStr
    failure_type: StrictStr
    reason: StrictStr


class AnalysisSummary(Contract):
    run_id: StrictStr
    case_num: StrictInt
    algo_id: StrictStr
    type_count: dict[StrictStr, StrictInt]
    cases: list[AnalysisCase]


class RepairPatch(Contract):
    run_id: StrictStr
    algo_id: StrictStr
    candidate_algo_id: StrictStr
    status: StrictStr
    diff: dict[StrictStr, StrictStr]


class AbtestComparison(Contract):
    run_id: StrictStr
    algo_id: StrictStr
    candidate_algo_id: StrictStr
    status: StrictStr
    verdict: StrictStr
    reasons: list[StrictStr]
    origin: EvalBody
    candidate: EvalBody
    delta: dict[StrictStr, FiniteFloat]


def dump_contract(model: type[BaseModel], value: Mapping[str, Any]) -> dict[str, Any]:
    return model.model_validate(value).model_dump(mode='json')


def algo_id(value: Mapping[str, Any]) -> str:
    answer = value.get('rag_answer') if isinstance(value.get('rag_answer'), Mapping) else {}
    for source in (answer.get('target') if isinstance(answer.get('target'), Mapping) else {},
                   value.get('target') if isinstance(value.get('target'), Mapping) else {}):
        for key in ('routed_algorithm_id', 'algorithm_id'):
            text = str(source.get(key) or '').strip()
            if text:
                return text
    return ''


def case_source_label(case: Mapping[str, Any], *, csv_first: bool = False) -> str:
    prep = case.get('source_preparation') if isinstance(case.get('source_preparation'), Mapping) else {}
    source = prep.get('case_source') if isinstance(prep.get('case_source'), Mapping) else {}
    metadata = case.get('case_metadata') if isinstance(case.get('case_metadata'), Mapping) else {}
    if csv_first and source.get('source') == 'imported_csv':
        values = (source.get('csv_path'), source.get('kb_id'), metadata.get('kb_id'), source.get('source'))
    else:
        values = (source.get('kb_id'), metadata.get('kb_id'), source.get('csv_path'), source.get('source'))
    for value in values:
        text = str(value or '').strip()
        if text:
            return text
    return ''


def build_eval_summary_root(
    run_id: str,
    judges: tuple[Mapping[str, Any], ...] | list[Mapping[str, Any]],
) -> dict[str, Any]:
    cases = []
    for judge in judges:
        case = judge.get('case') if isinstance(judge.get('case'), Mapping) else {}
        cases.append({
            'case_id': str(judge.get('case_id') or case.get('id') or ''),
            'trace_id': str(judge.get('trace_id') or ''),
            **{public: _score(judge.get(raw)) for raw, (public, _) in METRICS.items()},
            'reason': str(judge.get('reason') or ''),
        })
    scored = [judge for judge in judges if str(judge.get('quality_label') or '') != 'infra_failure'
              and str(judge.get('failure_type') or '') not in {'infra_failure', 'judge_contract_error',
                                                               'dataset_contract_error'}]
    payload = {
        'run_id': str(run_id),
        'case_num': len(cases),
        'algo_id': next((text for judge in judges for text in (algo_id(judge),) if text), ''),
        **{aggregate: _avg(scored, raw) for raw, (_, aggregate) in METRICS.items()},
        'correct_rate': round(sum(1 for row in scored if row.get('quality_label') == 'good') / len(scored), 4)
        if scored else 0.0,
        'cases': cases,
    }
    return dump_contract(EvalSummary, payload)


def build_analysis_summary_root(run_id: str, classifications: tuple[Mapping[str, Any], ...]) -> dict[str, Any]:
    rows = sorted(classifications, key=lambda row: str(row.get('case_id') or ''))
    cases = [{
        'case_id': str(row.get('case_id') or ''),
        'trace_id': str(row.get('trace_id') or ''),
        'source': str(row.get('source') or ''),
        'failure_type': str(row.get('issue_type') or ''),
        'reason': str(row.get('root_cause_reason') or row.get('reason') or ''),
    } for row in rows]
    payload = {
        'run_id': str(run_id),
        'case_num': len(rows),
        'algo_id': next((str(row.get('algo_id') or '') for row in rows if row.get('algo_id')), ''),
        'type_count': dict(Counter(case['failure_type'] for case in cases)),
        'cases': cases,
    }
    return dump_contract(AnalysisSummary, payload)


def build_abtest_comparison_root(
    run_id: str,
    baseline: Mapping[str, Any],
    candidate: Mapping[str, Any],
    service: Mapping[str, Any],
) -> dict[str, Any]:
    baseline = EvalSummary.model_validate(baseline).model_dump(mode='json')
    candidate = EvalSummary.model_validate(candidate).model_dump(mode='json')
    origin = _eval_body(baseline)
    after = _eval_body(candidate)
    delta = {key: round(float(after.get(key) or 0.0) - float(origin.get(key) or 0.0), 4) for key in AGGREGATES}
    verdict = 'review_candidate'
    status = 'completed'
    reasons: list[str] = []
    if service.get('status') == 'skipped':
        verdict = 'skipped'
        status = 'skipped'
        reasons.append('candidate evaluation skipped because repair patch is not verified')
    elif service.get('status') != 'ready':
        verdict = 'candidate_service_unavailable'
        status = 'failed'
        reasons.append('candidate service is not ready')
    payload = {
        'run_id': str(run_id),
        'algo_id': str(baseline.get('algo_id') or ''),
        'candidate_algo_id': str(service.get('algorithm_id') or ''),
        'status': status,
        'verdict': verdict,
        'reasons': reasons,
        'origin': origin,
        'candidate': after,
        'delta': delta,
    }
    return dump_contract(AbtestComparison, payload)


def _eval_body(summary: Mapping[str, Any]) -> dict[str, Any]:
    return {key: summary[key] for key in (*AGGREGATES, 'cases') if key in summary}


def _score(value: object) -> float:
    if value is None:
        raise ValueError('metric value is missing')
    return round(float(value), 4)


def _avg(rows: list[Mapping[str, Any]], key: str) -> float:
    return round(fmean(_score(row.get(key)) for row in rows), 4) if rows else 0.0
