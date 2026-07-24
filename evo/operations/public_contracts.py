from __future__ import annotations

from collections import Counter
from collections.abc import Mapping, Sequence
from statistics import fmean
from typing import Any

from pydantic import BaseModel, ConfigDict, Field, FiniteFloat, StrictInt, StrictStr

METRIC_SOURCES = {
    'correctness': 'answer_correctness',
    'relevance': 'answer_relevance',
    'completeness': 'completeness',
    'groundedness': 'groundedness',
    'format_compliance': 'format_compliance',
    'answer_quality': 'answer_quality_score',
    'retrieval_quality': 'retrieval_quality_score',
    'overall': 'overall_score',
}
METRIC_NAMES = tuple(METRIC_SOURCES)
AGGREGATES = (*METRIC_NAMES, 'correct_rate', 'good_rate')
UNSCORED_FAILURES = {
    'infra_failure',
    'judge_contract_error',
    'dataset_contract_error',
}
KNOWN_QUESTION_TYPE_ORDER = {
    'single_hop': 0,
    'single_doc_multi_hop': 1,
    'multi_doc_multi_hop': 2,
}


class Contract(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)


class EvalMetrics(Contract):
    correctness: FiniteFloat = Field(ge=0.0, le=1.0)
    relevance: FiniteFloat = Field(ge=0.0, le=1.0)
    completeness: FiniteFloat = Field(ge=0.0, le=1.0)
    groundedness: FiniteFloat = Field(ge=0.0, le=1.0)
    format_compliance: FiniteFloat = Field(ge=0.0, le=1.0)
    answer_quality: FiniteFloat = Field(ge=0.0, le=1.0)
    retrieval_quality: FiniteFloat = Field(ge=0.0, le=1.0)
    overall: FiniteFloat = Field(ge=0.0, le=1.0)


class EvalCase(Contract):
    case_id: StrictStr
    question: StrictStr
    question_type: StrictStr
    ground_truth: Any
    rag_answer: Any
    quality_label: StrictStr
    failure_type: StrictStr
    retrieval_failure_type: StrictStr
    defect: StrictStr
    reason: StrictStr
    trace_id: StrictStr
    metrics: EvalMetrics


class TraceCoverage(Contract):
    covered_case_num: StrictInt = Field(ge=0)
    total_case_num: StrictInt = Field(ge=0)
    rate: FiniteFloat = Field(ge=0.0, le=1.0)


class QuestionTypeSummary(Contract):
    question_type: StrictStr
    case_num: StrictInt = Field(ge=0)
    scored_case_num: StrictInt = Field(ge=0)
    correct_rate: FiniteFloat = Field(ge=0.0, le=1.0)
    good_rate: FiniteFloat = Field(ge=0.0, le=1.0)
    metrics: EvalMetrics
    quality_counts: dict[StrictStr, StrictInt]
    failure_type_counts: dict[StrictStr, StrictInt]


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
    algo_id: StrictStr
    case_num: StrictInt = Field(ge=0)
    scored_case_num: StrictInt = Field(ge=0)
    correct_rate: FiniteFloat = Field(ge=0.0, le=1.0)
    good_rate: FiniteFloat = Field(ge=0.0, le=1.0)
    trace_coverage: TraceCoverage
    metrics: EvalMetrics
    quality_counts: dict[StrictStr, StrictInt]
    failure_type_counts: dict[StrictStr, StrictInt]
    question_type_summaries: list[QuestionTypeSummary]
    cases: list[EvalCase]


class EvalBody(Contract):
    scored_case_num: StrictInt = Field(ge=0)
    correct_rate: FiniteFloat = Field(ge=0.0, le=1.0)
    good_rate: FiniteFloat = Field(ge=0.0, le=1.0)
    metrics: EvalMetrics
    cases: list[EvalCase]


class RepairPatch(Contract):
    run_id: StrictStr
    algo_id: StrictStr
    candidate_algo_id: StrictStr
    status: StrictStr
    workspace_ref: StrictStr
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
    cases = [_eval_case(judge) for judge in judges]
    scored = [judge for judge in judges if _is_scored(judge)]
    return dump_contract(EvalSummary, {
        'run_id': str(run_id),
        'algo_id': next((text for judge in judges for text in (algo_id(judge),) if text), ''),
        **_summary(judges, scored),
        'trace_coverage': _trace_coverage(cases),
        'question_type_summaries': _question_type_summaries(judges),
        'cases': cases,
    })


def normalize_eval_summary(value: Mapping[str, Any]) -> dict[str, Any]:
    """Convert stored pre-v2 summaries once, at their consumption boundary."""
    if isinstance(value.get('metrics'), Mapping):
        return dump_contract(EvalSummary, value)
    raw_cases = [item for item in value.get('cases', ()) if isinstance(item, Mapping)]
    cases = [_legacy_case(item) for item in raw_cases]
    scored = [item for item in cases if item['failure_type'] not in UNSCORED_FAILURES]
    grouped = _case_question_type_summaries(cases)
    metrics = {
        name: _number(value.get(f'avg_{name}'))
        for name in METRIC_NAMES
    }
    return dump_contract(EvalSummary, {
        'run_id': str(value.get('run_id') or ''),
        'algo_id': str(value.get('algo_id') or ''),
        'case_num': len(cases),
        'scored_case_num': int(value.get('scored_case_num', len(scored))),
        'correct_rate': _rate(sum(case['metrics']['correctness'] >= 0.6 for case in scored), len(scored)),
        'good_rate': _number(value.get('correct_rate')),
        'trace_coverage': _trace_coverage(cases),
        'metrics': metrics,
        'quality_counts': _counts(case['quality_label'] for case in cases),
        'failure_type_counts': _counts(case['failure_type'] for case in cases),
        'question_type_summaries': grouped,
        'cases': cases,
    })


def _eval_case(judge: Mapping[str, Any]) -> dict[str, Any]:
    case = judge.get('case') if isinstance(judge.get('case'), Mapping) else {}
    answer = judge.get('rag_answer') if isinstance(judge.get('rag_answer'), Mapping) else {}
    return {
        'case_id': str(judge.get('case_id') or case.get('id') or ''),
        'question': str(case.get('question') or ''),
        'question_type': _question_type(case.get('question_type')),
        'ground_truth': case.get('answer'),
        'rag_answer': answer.get('answer'),
        'quality_label': str(judge.get('quality_label') or 'infra_failure'),
        'failure_type': str(judge.get('failure_type') or 'infra_failure'),
        'retrieval_failure_type': str(judge.get('retrieval_failure_type') or 'not_applicable'),
        'defect': str(judge.get('defect') or ''),
        'reason': str(judge.get('reason') or ''),
        'trace_id': str(judge.get('trace_id') or '').strip(),
        'metrics': _metrics((judge,)),
    }


def _legacy_case(item: Mapping[str, Any]) -> dict[str, Any]:
    return {
        'case_id': str(item.get('case_id') or ''),
        'question': str(item.get('question') or ''),
        'question_type': _question_type(item.get('question_type')),
        'ground_truth': item.get('ground_truth'),
        'rag_answer': item.get('rag_answer'),
        'quality_label': str(item.get('quality_label') or ''),
        'failure_type': str(item.get('failure_type') or ''),
        'retrieval_failure_type': str(item.get('retrieval_failure_type') or ''),
        'defect': str(item.get('defect') or ''),
        'reason': str(item.get('reason') or ''),
        'trace_id': str(item.get('trace_id') or '').strip(),
        'metrics': {name: _number(item.get(name)) for name in METRIC_NAMES},
    }


def _summary(rows: Sequence[Mapping[str, Any]], scored: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    return {
        'case_num': len(rows),
        'scored_case_num': len(scored),
        'correct_rate': _rate(sum(_is_correct(row) for row in scored), len(scored)),
        'good_rate': _rate(sum(row.get('quality_label') == 'good' for row in scored), len(scored)),
        'metrics': _metrics(scored),
        'quality_counts': _counts(str(row.get('quality_label') or 'unknown') for row in rows),
        'failure_type_counts': _counts(str(row.get('failure_type') or 'unknown') for row in rows),
    }


def _question_type_summaries(judges: Sequence[Mapping[str, Any]]) -> list[dict[str, Any]]:
    groups: dict[str, list[Mapping[str, Any]]] = {}
    for judge in judges:
        case = judge.get('case') if isinstance(judge.get('case'), Mapping) else {}
        groups.setdefault(_question_type(case.get('question_type')), []).append(judge)
    return [
        {'question_type': question_type, **_summary(rows, [row for row in rows if _is_scored(row)])}
        for question_type, rows in sorted(groups.items(), key=lambda item: _question_type_sort(item[0]))
    ]


def _case_question_type_summaries(cases: Sequence[Mapping[str, Any]]) -> list[dict[str, Any]]:
    groups: dict[str, list[Mapping[str, Any]]] = {}
    for case in cases:
        groups.setdefault(_question_type(case.get('question_type')), []).append(case)
    result = []
    for question_type, rows in sorted(groups.items(), key=lambda item: _question_type_sort(item[0])):
        scored = [row for row in rows if row.get('failure_type') not in UNSCORED_FAILURES]
        result.append({
            'question_type': question_type,
            'case_num': len(rows),
            'scored_case_num': len(scored),
            'correct_rate': _rate(sum(_number(row['metrics'].get('correctness')) >= 0.6 for row in scored), len(scored)),
            'good_rate': _rate(sum(row.get('quality_label') == 'good' for row in scored), len(scored)),
            'metrics': {
                name: round(fmean(_number(row['metrics'].get(name)) for row in scored), 4) if scored else 0.0
                for name in METRIC_NAMES
            },
            'quality_counts': _counts(str(row.get('quality_label') or 'unknown') for row in rows),
            'failure_type_counts': _counts(str(row.get('failure_type') or 'unknown') for row in rows),
        })
    return result


def _metrics(rows: Sequence[Mapping[str, Any]]) -> dict[str, float]:
    return {
        public: round(fmean(_score(row.get(raw)) for row in rows), 4) if rows else 0.0
        for public, raw in METRIC_SOURCES.items()
    }


def _trace_coverage(cases: Sequence[Mapping[str, Any]]) -> dict[str, Any]:
    covered = sum(bool(str(case.get('trace_id') or '').strip()) for case in cases)
    return {
        'covered_case_num': covered,
        'total_case_num': len(cases),
        'rate': _rate(covered, len(cases)),
    }


def _is_scored(judge: Mapping[str, Any]) -> bool:
    return (
        str(judge.get('quality_label') or '') != 'infra_failure'
        and str(judge.get('failure_type') or '') not in UNSCORED_FAILURES
    )


def _is_correct(judge: Mapping[str, Any]) -> bool:
    policy = judge.get('eval_policy') if isinstance(judge.get('eval_policy'), Mapping) else {}
    floor = _number(policy.get('answer_correctness_floor'), default=0.6)
    return _score(judge.get('answer_correctness')) >= floor


def _question_type(value: object) -> str:
    return str(value or '').strip() or 'unknown'


def _question_type_sort(question_type: str) -> tuple[int, str]:
    if question_type == 'unknown':
        return 2, question_type
    if question_type in KNOWN_QUESTION_TYPE_ORDER:
        return 0, f'{KNOWN_QUESTION_TYPE_ORDER[question_type]:02d}'
    return 1, question_type


def _counts(values: Sequence[str] | Any) -> dict[str, int]:
    return dict(sorted(Counter(values).items()))


def _score(value: object) -> float:
    if value is None:
        raise ValueError('metric value is missing')
    return round(float(value), 4)


def _number(value: object, *, default: float = 0.0) -> float:
    try:
        return round(float(value), 4)
    except (TypeError, ValueError):
        return default


def _rate(numerator: int, denominator: int) -> float:
    return round(numerator / denominator, 4) if denominator else 0.0
