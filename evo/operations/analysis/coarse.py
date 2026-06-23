from __future__ import annotations

import math
from collections.abc import Mapping
from typing import Any

from evo.operations.common import as_list, text

REQUIRED_JUDGE_FIELDS = (
    'answer_score',
    'retrieval_score',
    'chunk_recall',
    'chunk_precision',
    'doc_recall',
    'doc_precision',
    'retrieval_failure_type',
    'failure_type',
    'quality_label',
)
RETRIEVAL_FAILURE_TYPES = {'none', 'retrieval_miss', 'retrieval_partial', 'retrieval_noise'}
FAILURE_TYPES = {'none', 'wrong_answer', 'partial_answer', 'question_not_answered', 'format_error', 'infra_failure'}
QUALITY_LABELS = {'good', 'partial', 'bad', 'infra_failure'}
SCORE_FIELDS = ('answer_score', 'retrieval_score', 'chunk_recall', 'chunk_precision', 'doc_recall', 'doc_precision')


def classify_case(
    case: Mapping[str, Any],
    answer: Mapping[str, Any],
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
) -> dict[str, Any]:
    case_id = text(case.get('id') or judge.get('case_id') or answer.get('case_id'))
    category, fine, repairable, confidence, features, action = _diagnosis(case, answer, judge, trace)
    llm_required = _needs_llm_analysis(judge, trace, category, fine)
    return {
        'case_id': case_id,
        'question_type': text(case.get('question_type')),
        'coarse_category': category,
        'fine_category': fine,
        'repairable': repairable and not llm_required,
        'pending_analysis': llm_required,
        'confidence': confidence,
        'judge_reason': text(judge.get('reason')),
        'root_cause_reason': _root_cause_reason(fine, features),
        'reason': _root_cause_reason(fine, features),
        'diagnosis_features': features,
        'recommended_action': action,
        'llm_analysis_required': llm_required,
        'llm_analysis_reason': _llm_reason(judge, trace, fine) if llm_required else '',
        'case': case,
        'rag_answer': answer,
        'judge': judge,
        'trace_summary': trace,
    }


def _diagnosis(
    case: Mapping[str, Any],
    answer: Mapping[str, Any],
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
) -> tuple[str, str, bool, str, list[str], str]:
    contract_error = _contract_error(judge)
    if contract_error:
        return (
            'infra_failure',
            'judge_contract_error',
            False,
            'high',
            _features(contract_error),
            'fix eval judge artifact contract before RCA',
        )

    failure = text(judge['failure_type'])
    quality = text(judge['quality_label'])
    tool_errors = [text(item) for item in as_list(answer.get('tool_errors') or judge.get('tool_errors')) if text(item)]
    trace_errors = as_list(trace.get('error_stages'))
    if tool_errors or trace_errors:
        return (
            'execution_issue',
            'tool_or_trace_error',
            True,
            'high',
            _features('tool_errors_present' if tool_errors else '',
                      'trace_error_stages_present' if trace_errors else '', *tool_errors),
            'inspect failed tool/span and stabilize execution path',
        )
    if failure == 'infra_failure':
        return (
            'infra_failure',
            'rag_or_judge_infra_failure',
            False,
            'high',
            _features('failure_type=infra_failure'),
            'fix service/runtime failure before retrieval or generation tuning',
        )
    if quality == 'good' or failure == 'none':
        return (
            'none',
            'correct',
            False,
            'high',
            _features('quality_label=good'),
            'no repair needed',
        )

    retrieval_failure = text(judge['retrieval_failure_type'])
    if retrieval_failure != 'none':
        return _retrieval_diagnosis(judge, trace, retrieval_failure)
    return _generation_diagnosis(case, judge, trace, failure)


def _retrieval_diagnosis(
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
    retrieval_failure: str,
) -> tuple[str, str, bool, str, list[str], str]:
    chunk_recall = _number(judge['chunk_recall'])
    doc_recall = _number(judge['doc_recall'])
    chunk_precision = _number(judge['chunk_precision'])
    doc_precision = _number(judge['doc_precision'])
    base = _features(
        f'retrieval_failure_type={retrieval_failure}',
        f'chunk_recall={chunk_recall}',
        f'doc_recall={doc_recall}',
        f'chunk_precision={chunk_precision}',
        f'doc_precision={doc_precision}',
        _route(trace),
    )
    if doc_recall == 0.0 and chunk_recall == 0.0:
        fine = 'reference_document_missing'
        action = 'inspect document-level recall, kb filters, and dataset routing'
    elif chunk_recall == 0.0:
        fine = 'reference_chunk_missing'
        action = 'increase reference chunk recall: inspect retriever filters, top_k, chunk ids, and query rewrite'
    elif retrieval_failure == 'retrieval_partial':
        fine = 'partial_reference_recall'
        action = 'tune retriever/reranker coverage for required evidence'
    elif retrieval_failure == 'retrieval_noise':
        fine = 'retrieval_noise'
        action = 'reduce irrelevant retrieval noise and reranker false positives'
    else:
        fine = 'low_retrieval_quality'
        action = 'inspect retrieval metrics and trace route'
    return ('retrieval_issue', fine, True, 'high', base, action)


def _generation_diagnosis(
    case: Mapping[str, Any],
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
    failure: str,
) -> tuple[str, str, bool, str, list[str], str]:
    answer_score = _number(judge['answer_score'])
    if failure == 'format_error':
        fine, confidence, action = 'answer_format_error', 'high', 'tighten answer format instruction and output parser'
    elif failure == 'question_not_answered':
        fine, confidence, action = 'question_not_answered', 'medium', 'inspect prompt routing and final response policy'
    elif failure == 'partial_answer':
        fine = 'generation_incomplete_answer'
        confidence = 'medium'
        action = 'improve synthesis completeness from retrieved evidence'
    elif failure == 'wrong_answer':
        fine = 'generation_wrong_answer' if answer_score < 0.5 else 'partial_or_ambiguous_answer'
        confidence = 'medium' if answer_score < 0.5 else 'low'
        action = 'inspect prompt grounding and final answer synthesis using retrieved evidence'
    else:
        fine = 'partial_or_ambiguous_answer'
        confidence = 'low'
        action = 'queue for deeper analysis before repair planning'
    return (
        'generation_issue',
        fine,
        True,
        confidence,
        _features(f'answer_score={answer_score}', f'failure_type={failure}', _route(trace), _question_type(case)),
        action,
    )


def _needs_llm_analysis(judge: Mapping[str, Any], trace: Mapping[str, Any], category: str, fine: str) -> bool:
    if category != 'generation_issue':
        return False
    answer_score = _number(judge['answer_score']) if 'answer_score' in judge else 0.0
    retrieval_score = _number(judge['retrieval_score']) if 'retrieval_score' in judge else 0.0
    chunk_recall = _number(judge['chunk_recall']) if 'chunk_recall' in judge else 0.0
    unknown_stages = int((trace.get('stage_counts') if isinstance(
        trace.get('stage_counts'), Mapping) else {}).get('unknown') or 0)
    return (
        fine == 'generation_wrong_answer'
        and retrieval_score >= 0.75
        and chunk_recall >= 0.75
        and answer_score < 0.6
        or fine == 'partial_or_ambiguous_answer'
        or unknown_stages > 0
    )


def _llm_reason(judge: Mapping[str, Any], trace: Mapping[str, Any], fine: str) -> str:
    if fine == 'partial_or_ambiguous_answer':
        return 'deterministic signals do not isolate one root cause'
    stage_counts = trace.get('stage_counts') if isinstance(trace.get('stage_counts'), Mapping) else {}
    if int(stage_counts.get('unknown') or 0) > 0:
        return 'trace contains unknown execution stages'
    return 'retrieval looks healthy but answer quality is low'


def _contract_error(judge: Mapping[str, Any]) -> str:
    missing = [field for field in REQUIRED_JUDGE_FIELDS if field not in judge]
    if missing:
        return 'missing_judge_fields=' + ','.join(missing)
    for field in SCORE_FIELDS:
        try:
            value = float(judge[field])
        except (TypeError, ValueError):
            return f'invalid_judge_field={field}'
        if not math.isfinite(value) or value < 0.0 or value > 1.0:
            return f'out_of_range_judge_field={field}'
    retrieval_failure = text(judge['retrieval_failure_type'])
    failure = text(judge['failure_type'])
    quality = text(judge['quality_label'])
    if retrieval_failure not in RETRIEVAL_FAILURE_TYPES:
        return f'invalid_retrieval_failure_type={retrieval_failure}'
    if failure not in FAILURE_TYPES:
        return f'invalid_failure_type={failure}'
    if quality not in QUALITY_LABELS:
        return f'invalid_quality_label={quality}'
    return ''


def _root_cause_reason(fine: str, features: list[str]) -> str:
    return f'{fine}: ' + '; '.join(features[:5])


def _features(*items: str) -> list[str]:
    return [item for item in dict.fromkeys(text(item) for item in items) if item]


def _route(trace: Mapping[str, Any]) -> str:
    route = text(trace.get('route_signature'))
    return f'route={route}' if route else ''


def _question_type(case: Mapping[str, Any]) -> str:
    value = text(case.get('question_type'))
    return f'question_type={value}' if value else ''


def _number(value: Any) -> float:
    return float(value)
