from __future__ import annotations

import json
import math
from collections.abc import Mapping
from typing import Any, Literal

from json_repair import repair_json
from pydantic import BaseModel, ConfigDict, Field, ValidationError

QualityLabel = Literal['good', 'partial', 'bad', 'infra_failure']
FailureType = Literal[
    'none',
    'wrong_answer',
    'partial_answer',
    'question_not_answered',
    'format_error',
    'hallucination',
    'infra_failure',
    'judge_contract_error',
    'dataset_contract_error',
]
RetrievalFailureType = Literal['none', 'retrieval_miss', 'retrieval_partial', 'retrieval_noise', 'not_applicable']
SCORE_KEYS = ('answer_correctness', 'answer_relevance', 'completeness', 'groundedness', 'format_compliance')


class JudgeScores(BaseModel):
    model_config = ConfigDict(extra='ignore')

    answer_correctness: float = Field(ge=0.0, le=1.0)
    answer_relevance: float = Field(ge=0.0, le=1.0)
    completeness: float = Field(ge=0.0, le=1.0)
    groundedness: float = Field(ge=0.0, le=1.0)
    format_compliance: float = Field(ge=0.0, le=1.0)
    failure_type: Literal[
        'none',
        'wrong_answer',
        'partial_answer',
        'question_not_answered',
        'format_error',
        'hallucination',
    ]
    reason: str
    defect: str


class JudgeResult(JudgeScores):
    model_config = ConfigDict(extra='allow')

    case_id: str
    answer_quality_score: float = Field(ge=0.0, le=1.0)
    retrieval_quality_score: float = Field(ge=0.0, le=1.0)
    overall_score: float = Field(ge=0.0, le=1.0)
    retrieval_failure_type: RetrievalFailureType
    quality_label: QualityLabel
    failure_type: FailureType
    is_correct: bool


def judge_case(
    case: Mapping[str, Any],
    rag_answer: Mapping[str, Any],
    policy: Mapping[str, Any],
) -> dict[str, Any]:
    try:
        return validate_judge_result(judge_answer(case, rag_answer, policy))
    except Exception as exc:
        return validate_judge_result(judge_contract_error(case, rag_answer, policy, str(exc)))


def judge_answer(case: Mapping[str, Any], rag_answer: Mapping[str, Any], policy: Mapping[str, Any]) -> dict[str, Any]:
    base = {
        'case_id': str(case.get('id') or rag_answer.get('case_id') or ''),
        'case': dict(case),
        'rag_answer': dict(rag_answer),
        'trace_id': str(rag_answer.get('trace_id') or ''),
        'target': dict(rag_answer.get('target') or {}),
        'tool_errors': list(rag_answer.get('tool_errors') or []),
        'eval_policy': {
            key: policy[key]
            for key in (
                'answer_good_threshold',
                'answer_partial_threshold',
                'answer_correctness_floor',
                'groundedness_floor',
                'answer_relevance_floor',
                'judge_schema_version',
                'rubric',
                'judge_model',
            )
            if key in policy
        },
    }
    if rag_answer.get('status') != 'ok':
        error = rag_answer.get('chat_error') if isinstance(rag_answer.get('chat_error'), Mapping) else {}
        failure = 'dataset_contract_error' if error.get('type') == 'dataset_contract_error' else 'infra_failure'
        reason = str(error.get('message') or error.get('type') or 'chat_failed')
        return _failure(base, failure, f'{error.get("type")}: {reason}' if error.get('type') else reason)

    try:
        from evo.llm import LazyLLMClient

        llm_config = policy.get('judge_llm_config') if isinstance(policy.get('judge_llm_config'), Mapping) else {}
        if not isinstance(llm_config.get('evo_llm'), Mapping):
            raise ValueError('eval.policy.judge_llm_config.evo_llm missing; eval must use core model-config injection')
        client = LazyLLMClient(llm_config=llm_config, model='evo_llm')
        raw = str(client(_prompt(case, rag_answer, policy), stream=False))
        repaired = repair_json(raw, return_objects=True)
        if not isinstance(repaired, Mapping):
            raise ValueError('judge did not return a JSON object')
        scores = JudgeScores.model_validate(repaired)
    except Exception as exc:
        return judge_contract_error(case, rag_answer, policy, str(exc))

    ref_chunks = {item.rsplit(':', 1)[-1] for item in _ids(case.get('reference_chunk_ids'))}
    got_chunks = {item.rsplit(':', 1)[-1] for item in _ids(rag_answer.get('chunk_ids'))}
    ref_docs = {item.rsplit(':', 1)[-1] for item in _ids(case.get('reference_doc_ids'))}
    got_docs = {item.rsplit(':', 1)[-1] for item in _ids(rag_answer.get('doc_ids'))}
    chunk_recall, chunk_precision = _overlap(ref_chunks, got_chunks)
    doc_recall, doc_precision = _overlap(ref_docs, got_docs)
    recall = chunk_recall if ref_chunks and got_chunks else doc_recall
    precision = chunk_precision if ref_chunks and got_chunks else doc_precision
    if not ref_chunks and not ref_docs:
        retrieval_failure, recall, precision = 'not_applicable', 1.0, 1.0
    elif recall == 0.0:
        retrieval_failure = 'retrieval_miss'
    elif recall < 1.0:
        retrieval_failure = 'retrieval_partial'
    elif precision < 0.5:
        retrieval_failure = 'retrieval_noise'
    else:
        retrieval_failure = 'none'

    answer_quality = _score(
        0.45 * scores.answer_correctness
        + 0.25 * scores.completeness
        + 0.20 * scores.groundedness
        + 0.10 * scores.answer_relevance
    )
    retrieval_quality = _score(0.70 * recall + 0.30 * precision)
    overall = answer_quality if retrieval_failure == 'not_applicable' else _score(
        0.80 * answer_quality + 0.20 * retrieval_quality
    )
    failure = scores.failure_type
    if failure == 'none':
        failure = (
            'question_not_answered' if scores.answer_relevance < 0.4 else
            'hallucination' if scores.groundedness < 0.4 else
            'wrong_answer' if scores.answer_correctness < 0.5 else
            'partial_answer' if scores.completeness < 0.6 else
            'none'
        )
    if scores.format_compliance < 1.0:
        failure = 'format_error'

    gates_ok = (
        failure == 'none'
        and scores.answer_correctness >= float(policy.get('answer_correctness_floor') or 0.6)
        and scores.groundedness >= float(policy.get('groundedness_floor') or 0.6)
        and scores.answer_relevance >= float(policy.get('answer_relevance_floor') or 0.6)
    )
    if gates_ok and overall >= float(policy.get('answer_good_threshold') or 0.8):
        label, failure = 'good', 'none'
    elif overall >= float(policy.get('answer_partial_threshold') or 0.5):
        label, failure = 'partial', failure if failure != 'none' else 'partial_answer'
    else:
        label, failure = 'bad', failure if failure != 'none' else 'wrong_answer'
    if retrieval_failure not in {'none', 'not_applicable'} and label == 'good':
        label = 'partial'
    if retrieval_failure not in {'none', 'not_applicable'} and failure == 'none':
        failure = 'partial_answer'

    return base | scores.model_dump() | {
        'chunk_recall': chunk_recall,
        'chunk_precision': chunk_precision,
        'doc_recall': doc_recall,
        'doc_precision': doc_precision,
        'context_recall': recall,
        'context_precision': precision,
        'retrieval_quality_score': retrieval_quality,
        'retrieval_failure_type': retrieval_failure,
        'answer_quality_score': answer_quality,
        'overall_score': overall,
        'quality_label': label,
        'failure_type': failure,
        'is_correct': label == 'good',
    }


def judge_contract_error(
    case: Mapping[str, Any],
    rag_answer: Mapping[str, Any],
    policy: Mapping[str, Any],
    reason: str,
) -> dict[str, Any]:
    base = {
        'case_id': str(case.get('id') or rag_answer.get('case_id') or ''),
        'case': dict(case),
        'rag_answer': dict(rag_answer),
        'trace_id': str(rag_answer.get('trace_id') or ''),
        'target': dict(rag_answer.get('target') or {}),
        'tool_errors': list(rag_answer.get('tool_errors') or []),
        'eval_policy': {'judge_schema_version': policy.get('judge_schema_version', 'v1')},
    }
    return _failure(base, 'judge_contract_error', reason)


def validate_judge_result(value: Mapping[str, Any]) -> dict[str, Any]:
    try:
        JudgeResult.model_validate(value)
    except ValidationError as exc:
        raise ValueError(str(exc)) from exc
    return dict(value)


def _failure(base: Mapping[str, Any], failure_type: FailureType, reason: str) -> dict[str, Any]:
    return dict(base) | {
        **{key: 0.0 for key in SCORE_KEYS},
        **{key: 0.0 for key in ('chunk_recall', 'chunk_precision', 'doc_recall', 'doc_precision',
                                'context_recall', 'context_precision')},
        'answer_quality_score': 0.0,
        'retrieval_quality_score': 0.0,
        'overall_score': 0.0,
        'retrieval_failure_type': 'not_applicable',
        'quality_label': 'infra_failure',
        'failure_type': failure_type,
        'is_correct': False,
        'reason': reason,
        'defect': failure_type,
    }


def _prompt(case: Mapping[str, Any], rag_answer: Mapping[str, Any], policy: Mapping[str, Any]) -> str:
    payload = {
        'question': case.get('question'),
        'reference_answer': case.get('answer'),
        'grading_guidance': case.get('grading_guidance'),
        'reference_context': case.get('reference_context'),
        'rag_answer': rag_answer.get('answer'),
        'retrieved_contexts': rag_answer.get('contexts'),
    }
    return (
        'Judge one RAG answer. Return one JSON object only, no markdown. '
        f'Scores must be floats from 0 to 1 with keys: {", ".join(SCORE_KEYS)}. '
        'Return failure_type, reason, defect. failure_type must be one of none, wrong_answer, '
        'partial_answer, question_not_answered, format_error, hallucination. '
        'Judge correctness against reference_answer and grading_guidance; judge groundedness against '
        'reference_context and retrieved_contexts.\n'
        f'rubric: {policy.get("rubric") or "Use the provided references and grading guidance."}\n'
        f'case_json: {json.dumps(payload, ensure_ascii=False, sort_keys=True)}'
    )


def _ids(value: Any) -> set[str]:
    items = [value] if isinstance(value, str) else list(value or [])
    return {str(item).strip() for item in items if str(item or '').strip()}


def _overlap(reference: set[str], retrieved: set[str]) -> tuple[float, float]:
    if not reference:
        return 0.0, 0.0
    hit = len(reference & retrieved)
    return _score(hit / len(reference)), _score(hit / len(retrieved)) if retrieved else 0.0


def _score(value: float) -> float:
    if not math.isfinite(float(value)):
        raise ValueError('score must be finite')
    return round(max(0.0, min(1.0, float(value))), 4)
