from __future__ import annotations

import json
import re
from collections import Counter
from collections.abc import Mapping
from typing import Any

import numpy as np

from evo.operations.common import METRICS, as_list, avg, json_safe, text

ANSWER_METRICS = ('answer_correctness', 'answer_relevance', 'completeness', 'format_compliance')
RETRIEVAL_METRICS = ('chunk_recall', 'chunk_precision', 'doc_recall', 'doc_precision')
SUMMARY_METRICS = (
    *ANSWER_METRICS,
    'answer_score',
    *RETRIEVAL_METRICS,
    'retrieval_score',
    *METRICS,
)

ANSWER_WEIGHTS = np.array([0.45, 0.20, 0.20, 0.15], dtype=float)
RETRIEVAL_WEIGHTS = np.array([0.50, 0.10, 0.30, 0.10], dtype=float)

FAILURE_TYPES = {'none', 'wrong_answer', 'partial_answer', 'question_not_answered', 'format_error'}


def judge_answer(answer: Mapping[str, Any], policy: Mapping[str, Any], services: Any) -> dict[str, Any]:
    case = answer.get('case') if isinstance(answer.get('case'), Mapping) else {}
    if answer.get('status') == 'failed' or answer.get('chat_error'):
        err = answer.get('chat_error') if isinstance(answer.get('chat_error'), Mapping) else {}
        reason = f"{text(err.get('type') or 'ChatError')}: {text(err.get('message') or 'RAG call failed')}"
        return unscored_judge_result(
            answer,
            policy,
            quality_label='infra_failure',
            failure_type='infra_failure',
            reason=reason,
        )
    try:
        judged = _llm_judge(case, answer, services)
    except Exception as error:  # noqa: BLE001 - worker boundary records judge infra failures.
        return unscored_judge_result(
            answer,
            policy,
            quality_label='infra_failure',
            failure_type='infra_failure',
            reason=f'JudgeError: {type(error).__name__}: {error}',
        )

    scores = {metric: _score(judged.get(metric)) for metric in ANSWER_METRICS}
    answer_score = answer_score_from_metrics(scores)
    quality = _quality_label(answer_score, scores['answer_correctness'], policy)
    failure = _failure_type(judged.get('failure_type'), quality, scores)
    reason = text(judged.get('reason')) or 'LLM answer quality judge completed'
    return _judge_result(answer, policy, scores, answer_score, quality, failure, reason)


def unscored_judge_result(answer: Mapping[str, Any], policy: Mapping[str, Any], *,
                          quality_label: str, failure_type: str, reason: str
                          ) -> dict[str, Any]:
    return _judge_result(
        answer,
        policy,
        {metric: 0.0 for metric in ANSWER_METRICS},
        0.0,
        quality_label,
        failure_type,
        reason,
    )


def _judge_result(answer: Mapping[str, Any], policy: Mapping[str, Any], scores: Mapping[str, float],
                  answer_score: float, quality: str, failure: str, reason: str) -> dict[str, Any]:
    case = answer.get('case') if isinstance(answer.get('case'), Mapping) else {}
    case_id = text(answer.get('case_id') or case.get('id'))
    retrieval = retrieval_metrics(case, answer)
    retrieval_failure = _retrieval_failure_type(retrieval)
    return {
        'case_id': case_id,
        'case': case,
        'rag_answer': answer,
        **scores,
        'answer_score': answer_score,
        **retrieval,
        'retrieval_score': _weighted_score(retrieval, RETRIEVAL_METRICS, RETRIEVAL_WEIGHTS),
        'retrieval_failure_type': retrieval_failure,
        'quality_label': quality,
        'failure_type': failure,
        'is_correct': quality == 'good',
        'reason': reason[:500],
        'defect': '' if failure == 'none' else failure,
        'trace_id': text(answer.get('trace_id')),
        'target': dict(answer.get('target') or {}) if isinstance(answer.get('target'), Mapping) else {},
        'eval_policy': dict(policy),
        'tool_errors': list(answer.get('tool_errors') or []),
    }


def eval_summary(judges: Mapping[str, Any]) -> dict[str, Any]:
    rows = [_judge_row(case_id, item) for case_id, item in sorted(judges.items())]
    scored = [row for row in rows if row['failure_type'] != 'infra_failure']
    metrics = {
        'scored_count': len(scored),
        'correct_count': sum(row['is_correct'] for row in scored),
        'correct_rate': avg(1.0 if row['is_correct'] else 0.0 for row in scored),
        **{f'{key}_avg': avg(row[key] for row in scored) for key in SUMMARY_METRICS},
    }
    return {
        'id': 'eval.summary',
        'total': len(rows),
        'case_ids': [row['case_id'] for row in rows],
        'metrics': metrics,
        'quality_counts': dict(Counter(row['quality_label'] for row in rows)),
        'failure_type_counts': dict(Counter(row['failure_type'] for row in rows)),
        'retrieval_failure_type_counts': dict(Counter(row['retrieval_failure_type'] for row in rows)),
        'bad_cases': [
            {key: row[key] for key in ('case_id', 'quality_label', 'failure_type', 'reason', 'trace_id')}
            for row in rows if row['quality_label'] != 'good'
        ],
        'execution_failures': [
            {'case_id': row['case_id'], 'reason': row['reason']}
            for row in rows if row['failure_type'] == 'infra_failure'
        ],
        'checks': {
            'ready': not any(row['failure_type'] == 'infra_failure' for row in rows),
            'errors': [],
            'warnings': [],
        },
        'rows': rows,
    }


def retrieval_metrics(case: Mapping[str, Any], answer: Mapping[str, Any]) -> dict[str, float]:
    chunk_recall, chunk_precision = _overlap_scores(case.get('reference_chunk_ids'), answer.get('chunk_ids'))
    doc_recall, doc_precision = _overlap_scores(case.get('reference_doc_ids'), answer.get('doc_ids'))
    return {
        'chunk_recall': chunk_recall,
        'chunk_precision': chunk_precision,
        'doc_recall': doc_recall,
        'doc_precision': doc_precision,
    }


def answer_score_from_metrics(scores: Mapping[str, Any]) -> float:
    return _weighted_score(
        {metric: _score(scores.get(metric)) for metric in ANSWER_METRICS},
        ANSWER_METRICS,
        ANSWER_WEIGHTS,
    )


def _llm_judge(case: Mapping[str, Any], answer: Mapping[str, Any], services: Any) -> dict[str, Any]:
    raw = services.llm_complete(_judge_prompt(case, answer))
    data = _json_object(raw)
    if not data:
        raise ValueError('judge LLM did not return a JSON object')
    return data


def _judge_prompt(case: Mapping[str, Any], answer: Mapping[str, Any]) -> str:
    payload = {
        'question': text(case.get('question')),
        'question_type': text(case.get('question_type')),
        'reference_answer': text(case.get('answer')),
        'rag_answer': text(answer.get('answer')),
        'grading_guidance': text(case.get('grading_guidance')),
    }
    return (
        'You are an evaluation judge. Score only the answer quality. Use only input_json.\n'
        'Return one JSON object with fields: answer_correctness, answer_relevance, completeness, '
        'format_compliance, failure_type, reason.\n'
        'Scores must be numbers from 0.0 to 1.0.\n'
        'failure_type must be one of: none, wrong_answer, partial_answer, question_not_answered, format_error.\n'
        'Use grading_guidance as the rubric. If the answer is essentially correct, failure_type must be none.\n\n'
        f'input_json: {json.dumps(json_safe(payload), ensure_ascii=False, sort_keys=True)}'
    )


def _judge_row(case_id: str, value: Any) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f'JudgeResult payload for {case_id} must be an object')
    row = {
        'case_id': text(value.get('case_id') or case_id),
        'quality_label': text(value.get('quality_label') or 'bad'),
        'failure_type': text(value.get('failure_type') or 'unknown'),
        'retrieval_failure_type': text(value.get('retrieval_failure_type') or 'unknown'),
        'is_correct': bool(value.get('is_correct')),
        'reason': text(value.get('reason')),
        'trace_id': text(value.get('trace_id')),
        'target': dict(value.get('target') or {}) if isinstance(value.get('target'), Mapping) else {},
    }
    row.update({key: round(float(value.get(key) or 0.0), 4) for key in SUMMARY_METRICS})
    case = value.get('case') if isinstance(value.get('case'), Mapping) else {}
    rag = value.get('rag_answer') if isinstance(value.get('rag_answer'), Mapping) else {}
    row.update({
        'question': text(case.get('question')),
        'question_type': text(case.get('question_type')),
        'ground_truth': text(case.get('answer')),
        'rag_answer': text(rag.get('answer')),
        'reference_chunk_ids': list(case.get('reference_chunk_ids') or []),
        'reference_doc_ids': list(case.get('reference_doc_ids') or []),
        'retrieve_chunk_ids': list(rag.get('chunk_ids') or []),
        'retrieve_doc_ids': list(rag.get('doc_ids') or []),
        'retrieve_contexts': list(rag.get('contexts') or []),
    })
    return row


def _overlap_scores(expected: Any, actual: Any) -> tuple[float, float]:
    expected_set = {text(item) for item in as_list(expected) if text(item)}
    actual_set = {text(item) for item in as_list(actual) if text(item)}
    if not expected_set:
        return 0.0, 0.0
    hits = len(expected_set & actual_set)
    recall = hits / len(expected_set)
    precision = hits / len(actual_set) if actual_set else 0.0
    return round(recall, 4), round(precision, 4)


def _weighted_score(scores: Mapping[str, float], metrics: tuple[str, ...], weights: np.ndarray) -> float:
    values = np.array([float(scores.get(metric) or 0.0) for metric in metrics], dtype=float)
    return round(float(np.dot(values, weights)), 4)


def _quality_label(answer_score: float, answer_correctness: float, policy: Mapping[str, Any]) -> str:
    good_threshold = float(policy.get('answer_good_threshold') or 0.8)
    partial_threshold = float(policy.get('answer_partial_threshold') or 0.5)
    correctness_floor = float(policy.get('answer_correctness_floor') or 0.75)
    if answer_score >= good_threshold and answer_correctness >= correctness_floor:
        return 'good'
    return 'partial' if answer_score >= partial_threshold else 'bad'


def _failure_type(value: Any, quality: str, scores: Mapping[str, float]) -> str:
    if quality == 'good':
        return 'none'
    failure = text(value)
    if failure in FAILURE_TYPES and failure != 'none':
        return failure
    if scores['format_compliance'] < 0.5:
        return 'format_error'
    if scores['answer_relevance'] < 0.5:
        return 'question_not_answered'
    return 'wrong_answer' if scores['answer_correctness'] < 0.5 else 'partial_answer'


def _retrieval_failure_type(metrics: Mapping[str, float]) -> str:
    if metrics['chunk_recall'] == 0 and metrics['doc_recall'] == 0:
        return 'retrieval_miss'
    if metrics['chunk_recall'] < 1 or metrics['doc_recall'] < 1:
        return 'retrieval_partial'
    if metrics['chunk_precision'] < 0.5 and metrics['doc_precision'] < 0.5:
        return 'retrieval_noise'
    return 'none'


def _score(value: Any) -> float:
    try:
        score = float(value)
    except (TypeError, ValueError):
        score = 0.0
    return round(min(1.0, max(0.0, score)), 4)


def _json_object(raw: str) -> dict[str, Any]:
    stripped = raw.strip()
    candidates = [stripped]
    fenced = re.search(r'```(?:json)?\s*(\{.*?\})\s*```', stripped, flags=re.S)
    if fenced:
        candidates.insert(0, fenced.group(1))
    start, end = stripped.find('{'), stripped.rfind('}')
    if start >= 0 and end > start:
        candidates.append(stripped[start:end + 1])
    for candidate in candidates:
        try:
            data = json.loads(candidate)
        except json.JSONDecodeError:
            continue
        if isinstance(data, Mapping):
            return dict(data)
    return {}
