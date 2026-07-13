from __future__ import annotations

import math
import re
from collections.abc import Mapping
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field

from evo.operations.public_contracts import algo_id, case_source_label

Status = Literal['ok', 'failed']
Quality = Literal['good', 'partial', 'bad', 'infra_failure']
RetrievalFailure = Literal['none', 'retrieval_miss', 'retrieval_partial', 'retrieval_noise', 'not_applicable']
Failure = Literal[
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
CASE_FIELDS = (
    'answer', 'difficulty', 'difficulty_rationale', 'grading_guidance', 'id', 'question', 'question_type',
    'reasoning_steps', 'reference_chunk_ids', 'reference_context', 'reference_doc', 'reference_doc_ids',
    'source_message_id', 'source_preparation', 'type_rationale',
)
NONEMPTY_CASE_FIELDS = CASE_FIELDS[:12]
SCORES = (
    'answer_quality_score', 'retrieval_quality_score', 'overall_score', 'answer_correctness', 'answer_relevance',
    'completeness', 'groundedness', 'format_compliance', 'context_recall', 'context_precision', 'chunk_recall',
    'chunk_precision', 'doc_recall', 'doc_precision',
)
TRACE_FIELDS = (
    'case_id', 'trace_id', 'trace_source', 'route_signature', 'tree_text', 'stage_sequence',
    'diagnostic_stage_sequence', 'edges', 'critical_path', 'bottleneck_stage', 'stages', 'stage_counts',
    'latency_by_stage', 'error_stages', 'retrieval_steps', 'retrieved_doc_ids', 'retrieved_chunk_ids',
    'final_context_doc_ids', 'final_context_chunk_ids', 'semantic_metric_keys', 'features',
)
OLD_ALIASES = {
    'coarse_category', 'fine_category', 'repairable', 'recommended_action', 'repairable_cases',
    'category_counts', 'fine_category_counts', 'llm_analysis_queue', 'answer_score', 'retrieval_score',
    'trace_missing', 'trace_available',
}
CHAT_CONTRACT_ERROR_TYPES = {
    'chat_invalid_answer',
    'chat_invalid_answer_retry_exhausted',
    'chat_no_answer',
    'chat_no_answer_retry_exhausted',
}
UUID_RE = re.compile(r'[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}')


class CaseModel(BaseModel):
    model_config = ConfigDict(extra='allow')
    answer: Any
    difficulty: Any
    difficulty_rationale: Any
    grading_guidance: Any
    id: str
    question: Any
    question_type: Any
    reasoning_steps: Any
    reference_chunk_ids: Any
    reference_context: Any
    reference_doc: Any
    reference_doc_ids: Any
    source_message_id: Any
    source_preparation: Any
    type_rationale: Any


class AnswerModel(BaseModel):
    model_config = ConfigDict(extra='allow')
    case_id: str = ''
    answer: Any = ''
    status: Status
    trace_id: str


class JudgeModel(BaseModel):
    model_config = ConfigDict(extra='allow')
    case_id: str
    retrieval_failure_type: RetrievalFailure
    failure_type: Failure
    quality_label: Quality
    reason: str = ''
    answer_quality_score: float = Field(ge=0.0, le=1.0)
    retrieval_quality_score: float = Field(ge=0.0, le=1.0)
    overall_score: float = Field(ge=0.0, le=1.0)
    answer_correctness: float = Field(ge=0.0, le=1.0)
    answer_relevance: float = Field(ge=0.0, le=1.0)
    completeness: float = Field(ge=0.0, le=1.0)
    groundedness: float = Field(ge=0.0, le=1.0)
    format_compliance: float = Field(ge=0.0, le=1.0)
    context_recall: float = Field(ge=0.0, le=1.0)
    context_precision: float = Field(ge=0.0, le=1.0)
    chunk_recall: float = Field(ge=0.0, le=1.0)
    chunk_precision: float = Field(ge=0.0, le=1.0)
    doc_recall: float = Field(ge=0.0, le=1.0)
    doc_precision: float = Field(ge=0.0, le=1.0)


def classify_case(
    case: Mapping[str, Any],
    answer: Mapping[str, Any],
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
) -> dict[str, Any]:
    _validate(case, answer, judge, trace)
    case_id = _text(case.get('id') or answer.get('case_id') or judge.get('case_id'))
    decision = _decision(case, answer, judge, trace)
    decision['actionable'] = _actionable(decision)
    return {
        'case_id': case_id,
        'trace_id': _text(trace.get('trace_id')),
        'source': case_source_label(case),
        'algo_id': algo_id({'rag_answer': answer, 'target': judge.get('target') or {}}),
        'question_type': _text(case.get('question_type')),
        **decision,
        'judge_reason': _text(judge.get('reason')),
        'root_cause_reason': _reason(decision),
        'diagnosis_features': decision.pop('features'),
        'secondary_signals': decision.pop('secondary_signals'),
        'answer_evidence': decision.pop('answer_evidence'),
        'judge_evidence': decision.pop('judge_evidence'),
        'trace_evidence': decision.pop('trace_evidence'),
        'investigation_note': decision.pop('investigation_note'),
        'case': _scrub(case),
        'rag_answer': _scrub(answer),
        'judge': _scrub(judge),
        'trace_summary': _scrub(trace),
    }


def _decision(
    case: Mapping[str, Any],
    answer: Mapping[str, Any],
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
) -> dict[str, Any]:
    inconsistent = _judge_inconsistency(judge)
    if judge.get('failure_type') == 'judge_contract_error':
        return _row('contract', 'judge_contract_error', 'eval_contract', 'judge_contract_error', 'high',
                    False, [f'failure_type={judge["failure_type"]}'], judge)
    if inconsistent:
        return _row('contract', 'judge_contract_inconsistent', 'eval_contract', 'judge_contract_inconsistent',
                    'high', False, inconsistent, judge)
    if judge.get('failure_type') == 'dataset_contract_error':
        return _row('contract', 'dataset_contract_error', 'eval_contract', 'dataset_contract_error', 'high',
                    False, ['failure_type=dataset_contract_error'], judge)
    chat_contract = _chat_contract_failure(answer, judge, trace, case)
    if chat_contract:
        return chat_contract
    if answer.get('status') != 'ok' or answer.get('chat_error'):
        source = _infra_source(answer)
        return _row('runtime_infra', 'rag_or_judge_infra_failure', 'runtime_infra',
                    'rag_or_judge_infra_failure', 'high', False, [source], judge, answer=answer, trace=trace)
    if _trace_unavailable(trace):
        return _row('tracing', 'trace_unavailable', 'tracing_observability', 'trace_unavailable', 'high', True,
                    ['lazyllm_trace_unavailable'], judge, answer=answer, trace=trace, case=case)
    if judge.get('failure_type') == 'infra_failure':
        if _answer_has_evidence(answer):
            return _row('contract', 'judge_contract_inconsistent', 'eval_contract', 'judge_contract_inconsistent',
                        'high', False, ['failure_type=infra_failure conflicts with usable rag_answer'], judge,
                        answer=answer, trace=trace)
        error = _stage_error(trace)
        if error:
            block, mode = error
            return _row('execution', 'stage_error', block, mode, 'high', False, [f'error_stage={mode}'], judge,
                        answer=answer, trace=trace)
        return _row('runtime_infra', 'rag_or_judge_infra_failure', 'runtime_infra',
                    'rag_or_judge_infra_failure', 'high', False, ['failure_type=infra_failure'], judge, trace=trace)
    if _correct(case, judge, trace):
        return _row('ok', 'correct', 'not_applicable', 'correct', 'high', False, ['quality_label=good'], judge,
                    trace=trace)
    orchestration = _tool_orchestration(case, answer, judge, trace)
    if orchestration:
        return _with_tracing_signal(orchestration, case, answer, judge, trace)
    retrieval = _retrieval(case, answer, judge, trace)
    if retrieval:
        return _with_tracing_signal(retrieval, case, answer, judge, trace)
    generation = _generation(case, answer, judge, trace)
    if generation:
        return _with_tracing_signal(generation, case, answer, judge, trace)
    tracing = _tracing_defect(case, answer, judge, trace)
    if tracing:
        return _row('tracing', tracing, 'tracing_observability', tracing, 'medium', True, [tracing], judge,
                    trace=trace)
    return _row('undetermined', 'insufficient_evidence', 'undetermined', 'insufficient_evidence',
                'low', True, ['no deterministic rule reached threshold'], judge, answer=answer,
                trace=trace)


def _retrieval(
    case: Mapping[str, Any],
    answer: Mapping[str, Any],
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
) -> dict[str, Any] | None:
    if judge.get('retrieval_failure_type') in {'none', 'not_applicable'}:
        return None
    retrieved_docs = _semantic_ids(trace, answer, 'retrieved_doc_ids', 'doc_ids')
    retrieved_chunks = _semantic_ids(trace, answer, 'retrieved_chunk_ids', 'chunk_ids')
    final_docs = _semantic_ids(trace, answer, 'final_context_doc_ids', 'doc_ids')
    final_chunks = _semantic_ids(trace, answer, 'final_context_chunk_ids', 'chunk_ids')
    ref_docs, ref_chunks = _ids(case.get('reference_doc_ids')), _ids(case.get('reference_chunk_ids'))
    doc_hit = _id_overlap(ref_docs, retrieved_docs)
    chunk_hit = _id_overlap(ref_chunks, retrieved_chunks)
    final_hit = bool(_id_overlap(ref_docs, final_docs) or _id_overlap(ref_chunks, final_chunks))
    answer_doc_hit = _id_overlap(ref_docs, _ids(answer.get('doc_ids')))
    answer_chunk_hit = _id_overlap(ref_chunks, _ids(answer.get('chunk_ids')))
    answer_source_complete = (
        (not ref_docs or bool(answer_doc_hit))
        and (not ref_chunks or bool(answer_chunk_hit))
    )
    features = [f'retrieval_failure_type={judge["retrieval_failure_type"]}',
                f'doc_recall={judge["doc_recall"]}', f'chunk_recall={judge["chunk_recall"]}',
                f'doc_precision={judge["doc_precision"]}', f'chunk_precision={judge["chunk_precision"]}']
    if final_hit and not answer_source_complete:
        return _row('generation', 'source_evidence_serialization_missing', 'postprocess_serialization',
                    'source_evidence_serialization_missing', 'high', False,
                    features + ['trace_final_context_has_reference', 'rag_answer_missing_reference_source_ids'],
                    judge, answer=answer, trace=trace, case=case)
    if ref_docs and not doc_hit and not chunk_hit:
        return _row('retrieval', 'reference_document_missing', 'retrieval', 'reference_document_missing', 'high',
                    False, features, judge, answer=answer, trace=trace, case=case)
    if doc_hit and ref_chunks and not chunk_hit:
        return _row('retrieval', 'reference_chunk_missing', 'retrieval', 'reference_chunk_missing', 'high', False,
                    features, judge, answer=answer, trace=trace, case=case)
    if (doc_hit or chunk_hit) and ref_chunks and not _id_overlap(ref_chunks, final_chunks):
        return _row('retrieval', 'context_assembly_failure', 'context_assembly', 'context_reference_chunk_dropped',
                    'high', False, features + ['final_context_missing_reference'], judge,
                    answer=answer, trace=trace, case=case)
    partial_seen = (
        (ref_docs and not _all_refs_seen(ref_docs, retrieved_docs))
        or (ref_chunks and (
            not _all_refs_seen(ref_chunks, retrieved_chunks)
            or not _all_refs_seen(ref_chunks, final_chunks)
        ))
    )
    if (judge.get('retrieval_failure_type') == 'retrieval_partial' or _partial_recall(judge)) and partial_seen:
        return _row('retrieval', 'partial_reference_recall', 'retrieval', 'partial_reference_recall', 'medium',
                    False, features, judge, answer=answer, trace=trace, case=case)
    extra_context = _unmatched_ids(retrieved_docs, ref_docs) or _unmatched_ids(retrieved_chunks, ref_chunks)
    if (judge.get('retrieval_failure_type') == 'retrieval_noise' or _precision_low(judge)) and extra_context:
        block = 'rerank' if 'rerank' in trace.get('diagnostic_stage_sequence', []) else 'retrieval'
        mode = 'rerank_noise_promoted' if block == 'rerank' else 'retrieval_noise'
        issue = 'rerank_failure' if block == 'rerank' else 'retrieval_noise'
        return _row('retrieval', issue, block, mode, 'medium', False, features, judge,
                    answer=answer, trace=trace, case=case)
    if final_hit:
        return None
    return _row('undetermined', 'insufficient_trace_evidence', 'undetermined', 'insufficient_trace_evidence', 'low',
                True, features + ['trace_retrieval_evidence_does_not_confirm_judge_signal'], judge,
                answer=answer, trace=trace, case=case)


def _tool_orchestration(
    case: Mapping[str, Any],
    answer: Mapping[str, Any],
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
) -> dict[str, Any] | None:
    if judge.get('retrieval_failure_type') in {'none', 'not_applicable'}:
        return None
    if not _kb_expected(case, answer):
        return None
    if _retrieval_observed(trace, answer):
        return None
    failure = _text(judge.get('failure_type'))
    if failure not in {'question_not_answered', 'partial_answer', 'wrong_answer', 'format_error'}:
        return None
    features = [
        f'retrieval_failure_type={judge["retrieval_failure_type"]}',
        f'failure_type={failure}',
        'kb_expected',
        'retrieval_not_executed',
    ]
    if _asks_for_external_context(answer):
        features.append('answer_asks_user_for_indexed_material')
    return _row('execution', 'kb_tool_not_used', 'tool_orchestration', 'kb_tool_not_used', 'high', False,
                features, judge, answer=answer, trace=trace, case=case)


def _generation(case: Mapping[str, Any], answer: Mapping[str, Any], judge: Mapping[str, Any],
                trace: Mapping[str, Any]) -> dict[str, Any] | None:
    failure = _text(judge.get('failure_type'))
    if failure not in {'format_error', 'question_not_answered', 'partial_answer', 'wrong_answer', 'hallucination'}:
        return None
    healthy = _retrieval_healthy(case, judge, trace, answer)
    refs_absent = judge.get('retrieval_failure_type') == 'not_applicable'
    context_present = bool(
        _semantic_ids(trace, answer, 'final_context_doc_ids', 'doc_ids')
        or _semantic_ids(trace, answer, 'final_context_chunk_ids', 'chunk_ids')
    )
    pending = False
    llm_completed = _stage_completed(trace, 'llm_generate')
    if not (healthy or refs_absent) and failure != 'format_error':
        return None
    if failure != 'format_error' and not llm_completed:
        return _row('undetermined', 'insufficient_evidence', 'undetermined', 'insufficient_evidence', 'low', True,
                    [f'failure_type={failure}', 'llm_generate_completion_unobserved'], judge, answer=answer,
                    trace=trace, case=case)
    mapping = {
        'format_error': ('answer_format_error', 'postprocess_serialization', 'answer_format_error'),
        'question_not_answered': ('question_not_answered', 'llm_generation', 'question_not_answered'),
        'partial_answer': ('generation_incomplete_answer', 'llm_generation', 'generation_incomplete_answer'),
        'wrong_answer': ('generation_wrong_answer', 'llm_generation', 'generation_wrong_answer'),
        'hallucination': ('generation_hallucination', 'llm_generation', 'generation_hallucination'),
    }
    issue, block, mode = mapping[failure]
    confidence = 'medium' if pending or failure in {'question_not_answered', 'partial_answer'} else 'high'
    features = [f'failure_type={failure}', f'answer_quality_score={judge.get("answer_quality_score")}']
    if refs_absent:
        features.append('retrieval_not_applicable')
    if context_present:
        features.append('trace_context_present')
    return _row('generation', issue, block, mode, confidence, pending, features, judge, answer=answer, trace=trace,
                case=case)


def _row(category: str, issue: str, block: str, mode: str, confidence: str, pending: bool, features: list[str],
         judge: Mapping[str, Any], *, answer: Mapping[str, Any] | None = None,
         trace: Mapping[str, Any] | None = None, case: Mapping[str, Any] | None = None,
         secondary_signals: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    return {
        'issue_category': category,
        'issue_type': issue,
        'affected_block': block,
        'failure_mode': mode,
        'pending_analysis': pending,
        'confidence': confidence,
        'features': _unique(features),
        'secondary_signals': list(secondary_signals or ()),
        'answer_evidence': _answer_evidence(answer or {}),
        'judge_evidence': _judge_evidence(judge),
        'trace_evidence': _trace_evidence(trace or {}, case or {}, answer or {}),
        'investigation_note': _note(category, issue, block, case or {}),
    }


def _with_tracing_signal(
    row: dict[str, Any],
    case: Mapping[str, Any],
    answer: Mapping[str, Any],
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
) -> dict[str, Any]:
    tracing = _tracing_defect(case, answer, judge, trace)
    if not tracing:
        return row
    row['secondary_signals'] = [
        *list(row.get('secondary_signals') or ()),
        {
            'issue_category': 'tracing',
            'issue_type': tracing,
            'affected_block': 'tracing_observability',
            'failure_mode': tracing,
            'pending_analysis': True,
        },
    ]
    return row


def _validate(case: Mapping[str, Any], answer: Mapping[str, Any], judge: Mapping[str, Any],
              trace: Mapping[str, Any]) -> None:
    missing = [field for field in CASE_FIELDS if field not in case]
    if missing:
        raise ValueError('eval.case missing fields: ' + ', '.join(missing))
    empty = [field for field in NONEMPTY_CASE_FIELDS if _empty(case.get(field))]
    if empty:
        raise ValueError('eval.case empty required fields: ' + ', '.join(empty))
    CaseModel.model_validate(case)
    AnswerModel.model_validate(answer)
    JudgeModel.model_validate(judge)
    trace_missing = _trace_unavailable(trace)
    if not answer.get('trace_id') and not trace_missing:
        raise ValueError('eval.rag_answer trace_id is required')
    for field in TRACE_FIELDS:
        if field not in trace:
            raise ValueError(f'analysis.trace_summary missing field: {field}')
    if not trace.get('trace_id') and not trace_missing:
        raise ValueError('analysis.trace_summary trace_id is required')
    if trace.get('trace_id') and answer.get('trace_id') and trace.get('trace_id') != answer.get('trace_id'):
        raise ValueError('analysis.trace_summary trace_id must match eval.rag_answer trace_id')
    if trace.get('trace_source') != 'lazyllm.get_single_trace':
        raise ValueError('analysis.trace_summary trace_source must be lazyllm.get_single_trace')
    for field in SCORES:
        if not math.isfinite(float(judge[field])):
            raise ValueError(f'judge score must be finite: {field}')


def _judge_inconsistency(judge: Mapping[str, Any]) -> list[str]:
    issues = []
    if judge.get('quality_label') == 'good' and (
        judge.get('failure_type') != 'none'
        or _score(judge, 'overall_score') < 0.75
        or _score(judge, 'answer_quality_score') < 0.75
        or (judge.get('retrieval_failure_type') != 'not_applicable'
            and _score(judge, 'retrieval_quality_score') < 0.75)
    ):
        issues.append('quality_label=good conflicts with failure/score')
    if judge.get('failure_type') == 'none' and judge.get('retrieval_failure_type') not in {'none', 'not_applicable'}:
        issues.append('failure_type=none conflicts with retrieval failure')
    if judge.get('failure_type') == 'none' and _score(judge, 'answer_quality_score') < 0.75:
        issues.append('failure_type=none conflicts with answer_quality_score')
    return issues


def _stage_error(trace: Mapping[str, Any]) -> tuple[str, str] | None:
    mapping = {
        'tool_call': ('tool_orchestration', 'tool_error'),
        'retrieve': ('retrieval', 'retrieval_stage_error'),
        'rerank': ('rerank', 'rerank_stage_error'),
        'context_assembly': ('context_assembly', 'context_assembly_stage_error'),
        'prompt_build': ('prompt_build', 'prompt_build_stage_error'),
        'llm_generate': ('llm_generation', 'llm_generation_stage_error'),
        'postprocess': ('postprocess_serialization', 'postprocess_stage_error'),
        'stream': ('postprocess_serialization', 'stream_truncation'),
        'unknown': ('tracing_observability', 'trace_stage_unknown'),
    }
    priority = {stage: index for index, stage in enumerate(mapping)}
    errors = [
        (priority[str(item['stage'])], mapping[str(item['stage'])])
        for item in trace.get('error_stages') or []
        if isinstance(item, Mapping) and item.get('stage') in mapping
    ]
    if errors:
        return min(errors, key=lambda item: item[0])[1]
    return None


def _chat_contract_failure(
    answer: Mapping[str, Any],
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
    case: Mapping[str, Any],
) -> dict[str, Any] | None:
    error = answer.get('chat_error') if isinstance(answer.get('chat_error'), Mapping) else {}
    error_type = _text(error.get('type'))
    if error_type not in CHAT_CONTRACT_ERROR_TYPES:
        return None
    if error_type.startswith('chat_no_answer'):
        issue = mode = 'missing_final_answer'
    else:
        issue = mode = 'answer_contract_error'
    return _row(
        'generation',
        issue,
        'llm_generation',
        mode,
        'high',
        False,
        [f'chat_error={error_type}', f'failure_type={_text(judge.get("failure_type"))}'],
        judge,
        answer=answer,
        trace=trace,
        case=case,
    )


def _tracing_defect(
    case: Mapping[str, Any],
    answer: Mapping[str, Any],
    judge: Mapping[str, Any],
    trace: Mapping[str, Any],
) -> str:
    if _trace_unavailable(trace):
        return 'trace_unavailable'
    unknown_value = trace.get('unknown_stage_count')
    if unknown_value is None:
        unknown_value = (trace.get('stage_counts') or {}).get('unknown') or 0
    unknown = int(unknown_value)
    if unknown and ('unknown' in trace.get('critical_path', []) or judge.get('quality_label') != 'good'):
        return 'trace_stage_unknown'
    needs_ids = (
        judge.get('retrieval_failure_type') != 'not_applicable'
        and (_ids(case.get('reference_doc_ids')) or _ids(case.get('reference_chunk_ids')))
    )
    refs_exist = bool(_ids(case.get('reference_doc_ids')) or _ids(case.get('reference_chunk_ids')))
    answer_failed = judge.get('failure_type') not in {'none', 'infra_failure'}
    retrieved_trace_ids = bool(
        _trace_semantic_ids(trace, 'retrieved_doc_ids')
        or _trace_semantic_ids(trace, 'retrieved_chunk_ids')
    )
    final_trace_ids = bool(
        _trace_semantic_ids(trace, 'final_context_doc_ids')
        or _trace_semantic_ids(trace, 'final_context_chunk_ids')
    )
    retrieved_ids = bool(
        retrieved_trace_ids
        or _ids(answer.get('doc_ids'))
        or _ids(answer.get('chunk_ids'))
    )
    final_ids = bool(
        final_trace_ids
        or _ids(answer.get('doc_ids'))
        or _ids(answer.get('chunk_ids'))
    )
    if refs_exist and judge.get('retrieval_failure_type') == 'not_applicable':
        return 'trace_metrics_missing'
    if needs_ids and not trace.get('retrieval_steps'):
        return 'trace_metrics_missing'
    if needs_ids and trace.get('semantic_metric_keys') and not retrieved_trace_ids:
        return 'trace_metrics_missing'
    if answer_failed and refs_exist and trace.get('semantic_metric_keys') and not final_trace_ids:
        return 'trace_metrics_missing'
    if needs_ids and not trace.get('semantic_metric_keys') and not retrieved_ids:
        return 'trace_metrics_missing'
    if answer_failed and refs_exist and not trace.get('semantic_metric_keys') and not final_ids:
        return 'trace_metrics_missing'
    return ''


def _trace_unavailable(trace: Mapping[str, Any]) -> bool:
    features = trace.get('features') if isinstance(trace.get('features'), Mapping) else {}
    return (
        trace.get('route_signature') == 'trace_unavailable'
        or trace.get('trace_status') == 'unavailable'
        or bool(features.get('trace_unavailable'))
    )


def _correct(case: Mapping[str, Any], judge: Mapping[str, Any], trace: Mapping[str, Any]) -> bool:
    refs_exist = bool(_ids(case.get('reference_doc_ids')) or _ids(case.get('reference_chunk_ids')))
    retrieval_ok = (
        judge.get('retrieval_failure_type') == 'none'
        or (judge.get('retrieval_failure_type') == 'not_applicable' and not refs_exist)
    )
    return (
        judge.get('quality_label') == 'good'
        and judge.get('failure_type') == 'none'
        and retrieval_ok
    )


def _retrieval_healthy(case: Mapping[str, Any], judge: Mapping[str, Any], trace: Mapping[str, Any],
                       answer: Mapping[str, Any] | None = None) -> bool:
    ref_docs, ref_chunks = _ids(case.get('reference_doc_ids')), _ids(case.get('reference_chunk_ids'))
    answer = answer or {}
    final_docs = _semantic_ids(trace, answer, 'final_context_doc_ids', 'doc_ids')
    final_chunks = _semantic_ids(trace, answer, 'final_context_chunk_ids', 'chunk_ids')
    overlap_ok = not (ref_docs or ref_chunks) or bool(
        _id_overlap(ref_docs, final_docs) or _id_overlap(ref_chunks, final_chunks)
    )
    return (
        judge.get('retrieval_failure_type') == 'none'
        and _score(judge, 'retrieval_quality_score') >= 0.75
        and _score(judge, 'context_recall') >= 0.75
        and (not ref_docs or _score(judge, 'doc_recall') >= 0.75)
        and (not ref_chunks or _score(judge, 'chunk_recall') >= 0.75)
        and overlap_ok
    )


def _actionable(row: Mapping[str, Any]) -> bool:
    return (
        row['issue_category'] in {'retrieval', 'generation', 'execution'}
        and row['affected_block'] != 'undetermined'
        and row['failure_mode'] != 'insufficient_evidence'
        and row['confidence'] in {'high', 'medium'}
        and not row['pending_analysis']
    )


def _answer_evidence(answer: Mapping[str, Any]) -> list[dict[str, Any]]:
    items = []
    if not _text(answer.get('answer')):
        items.append(_evidence('empty_answer', 'rag_answer.answer', ''))
    if answer.get('chat_error'):
        items.append(_evidence('chat_error', 'rag_answer.chat_error', answer.get('chat_error')))
    return items


def _judge_evidence(judge: Mapping[str, Any]) -> list[dict[str, Any]]:
    return [
        _evidence('judge_enum_signal', 'eval.judge_result.failure_type', judge.get('failure_type')),
        _evidence('judge_score_snapshot', 'eval.judge_result.overall_score', judge.get('overall_score')),
        _evidence('judge_reason', 'eval.judge_result.reason', _text(judge.get('reason'))[:300]),
    ]


def _trace_evidence(trace: Mapping[str, Any], case: Mapping[str, Any],
                    answer: Mapping[str, Any] | None = None) -> list[dict[str, Any]]:
    ref_docs, ref_chunks = _ids(case.get('reference_doc_ids')), _ids(case.get('reference_chunk_ids'))
    answer = answer or {}
    retrieved_docs = _semantic_ids(trace, answer, 'retrieved_doc_ids', 'doc_ids')
    retrieved_chunks = _semantic_ids(trace, answer, 'retrieved_chunk_ids', 'chunk_ids')
    final_docs = _semantic_ids(trace, answer, 'final_context_doc_ids', 'doc_ids')
    final_chunks = _semantic_ids(trace, answer, 'final_context_chunk_ids', 'chunk_ids')
    source = _semantic_id_source(trace, answer)
    return [
        _evidence('route_signature', 'analysis.trace_summary.route_signature', trace.get('route_signature')),
        _evidence('stage_sequence', 'analysis.trace_summary.diagnostic_stage_sequence',
                  trace.get('diagnostic_stage_sequence') or []),
        _evidence('unknown_stage_count', 'analysis.trace_summary.unknown_stage_count',
                  trace.get('unknown_stage_count') or 0),
        _evidence('error_stage', 'analysis.trace_summary.error_stages', trace.get('error_stages') or []),
        _evidence('semantic_id_source', 'analysis.trace_summary.semantic_metric_keys', source),
        _evidence('retrieved_doc_overlap', 'analysis.trace_summary.retrieved_doc_ids',
                  sorted(_id_overlap(ref_docs, retrieved_docs))),
        _evidence('retrieved_chunk_overlap', 'analysis.trace_summary.retrieved_chunk_ids',
                  sorted(_id_overlap(ref_chunks, retrieved_chunks))),
        _evidence('final_context_reference_overlap', 'analysis.trace_summary.final_context_ids',
                  {'doc_ids': sorted(_id_overlap(ref_docs, final_docs)),
                   'chunk_ids': sorted(_id_overlap(ref_chunks, final_chunks))}),
    ]


def _evidence(kind: str, field: str, value: Any) -> dict[str, Any]:
    return {'type': kind, 'source_field': field, 'observed_value': value}


def _reason(row: Mapping[str, Any]) -> str:
    return f"{row['issue_category']}/{row['issue_type']} at {row['affected_block']}: " + '; '.join(row['features'][:4])


def _note(category: str, issue: str, block: str, case: Mapping[str, Any]) -> str:
    qtype = _text(case.get('question_type'))
    suffix = f' for {qtype}' if qtype else ''
    return f'inspect {block} evidence for {category}/{issue}{suffix}'


def _infra_source(answer: Mapping[str, Any]) -> str:
    error = answer.get('chat_error')
    if isinstance(error, Mapping):
        return 'chat_error=' + _text(error.get('type') or error.get('code') or 'unknown')
    return 'rag_answer.status=' + _text(answer.get('status'))


def _answer_has_evidence(answer: Mapping[str, Any]) -> bool:
    return bool(
        _text(answer.get('answer'))
        and (_ids(answer.get('contexts')) or _ids(answer.get('doc_ids')) or _ids(answer.get('chunk_ids')))
    )


def _kb_expected(case: Mapping[str, Any], answer: Mapping[str, Any]) -> bool:
    target = answer.get('target') if isinstance(answer.get('target'), Mapping) else {}
    metadata = case.get('case_metadata') if isinstance(case.get('case_metadata'), Mapping) else {}
    preparation = case.get('source_preparation') if isinstance(case.get('source_preparation'), Mapping) else {}
    case_source = preparation.get('case_source') if isinstance(preparation.get('case_source'), Mapping) else {}
    return bool(
        _text(target.get('kb_id'))
        or _text(metadata.get('kb_id'))
        or _text(case_source.get('kb_id'))
        or _ids(case.get('reference_doc_ids'))
        or _ids(case.get('reference_chunk_ids'))
    )


def _retrieval_observed(trace: Mapping[str, Any], answer: Mapping[str, Any]) -> bool:
    return bool(
        trace.get('retrieval_steps')
        or _ids(answer.get('doc_ids'))
        or _ids(answer.get('chunk_ids'))
        or _trace_semantic_ids(trace, 'retrieved_doc_ids')
        or _trace_semantic_ids(trace, 'retrieved_chunk_ids')
        or _trace_semantic_ids(trace, 'final_context_doc_ids')
        or _trace_semantic_ids(trace, 'final_context_chunk_ids')
    )


def _asks_for_external_context(answer: Mapping[str, Any]) -> bool:
    text = _text(answer.get('answer')).lower()
    markers = (
        '请提供', '请上传', '提供简历', '上传简历', '简历内容', '相关文件', '相关内容',
        'provide the resume', 'provide resume', 'upload the resume', 'uploaded documents',
        'please share', 'please provide', 'i don’t have access', "i don't have access",
    )
    return any(marker in text for marker in markers)


def _semantic_ids(
    trace: Mapping[str, Any],
    answer: Mapping[str, Any],
    trace_key: str,
    answer_key: str,
) -> set[str]:
    trace_ids = _trace_semantic_ids(trace, trace_key)
    return trace_ids or _ids(answer.get(answer_key))


def _trace_semantic_ids(trace: Mapping[str, Any], trace_key: str) -> set[str]:
    return _ids(trace.get(trace_key))


def _semantic_id_source(trace: Mapping[str, Any], answer: Mapping[str, Any]) -> str:
    trace_ids = (
        _trace_semantic_ids(trace, 'retrieved_doc_ids')
        or _trace_semantic_ids(trace, 'retrieved_chunk_ids')
        or _trace_semantic_ids(trace, 'final_context_doc_ids')
        or _trace_semantic_ids(trace, 'final_context_chunk_ids')
    )
    if trace_ids:
        return 'trace'
    if _ids(answer.get('doc_ids')) or _ids(answer.get('chunk_ids')):
        return 'rag_answer'
    if trace.get('semantic_metric_keys'):
        return 'trace_missing_ids'
    return 'trace'


def _ids(value: Any) -> set[str]:
    if isinstance(value, str):
        items = [value]
    else:
        items = list(value or [])
    return {str(item).strip() for item in items if str(item or '').strip()}


def _id_overlap(expected: set[str], observed: set[str]) -> set[str]:
    observed_tokens = _id_token_set(observed)
    return {item for item in expected if _id_tokens(item) & observed_tokens}


def _all_refs_seen(expected: set[str], observed: set[str]) -> bool:
    if not expected:
        return True
    return len(_id_overlap(expected, observed)) == len(expected)


def _unmatched_ids(observed: set[str], expected: set[str]) -> set[str]:
    expected_tokens = _id_token_set(expected)
    return {item for item in observed if not (_id_tokens(item) & expected_tokens)}


def _id_token_set(values: set[str]) -> set[str]:
    tokens: set[str] = set()
    for value in values:
        tokens.update(_id_tokens(value))
    return tokens


def _id_tokens(value: str) -> set[str]:
    text = str(value or '').strip()
    if not text:
        return set()
    return {text, text.lower(), *(match.group(0).lower() for match in UUID_RE.finditer(text))}


def _score(judge: Mapping[str, Any], key: str) -> float:
    return float(judge.get(key) or 0.0)


def _precision_low(judge: Mapping[str, Any]) -> bool:
    return _score(judge, 'doc_precision') < 0.40 or _score(judge, 'chunk_precision') < 0.40


def _partial_recall(judge: Mapping[str, Any]) -> bool:
    return 0 < _score(judge, 'doc_recall') < 0.75 or 0 < _score(judge, 'chunk_recall') < 0.75


def _stage_completed(trace: Mapping[str, Any], stage: str) -> bool:
    return any(item.get('stage') == stage and item.get('status') in {'ok', 'success', 'done', 'completed', 'finished'}
               for item in trace.get('stages') or [] if isinstance(item, Mapping))


def _empty(value: Any) -> bool:
    if value is None:
        return True
    if isinstance(value, str):
        return not value.strip()
    if isinstance(value, (list, tuple, set, dict)):
        return not value
    return False


def _scrub(value: Any) -> Any:
    if isinstance(value, Mapping):
        return {str(key): _scrub(raw) for key, raw in value.items() if str(key) not in OLD_ALIASES}
    if isinstance(value, (list, tuple)):
        return [_scrub(item) for item in value]
    return value


def _unique(items: list[str]) -> list[str]:
    return [item for item in dict.fromkeys(str(value) for value in items if str(value or '').strip())]


def _text(value: Any) -> str:
    return str(value or '').strip()
