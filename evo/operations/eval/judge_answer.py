from __future__ import annotations

from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import json_object, validate_case_id
from ...runtime import AdapterCall, OperationOutput, evo_llm

PROMPT = """你是严格的 RAG 评测裁判。只输出 JSON，不要 markdown，不要解释。

评分规则：
- answer_correctness: 1.0=正确；0.7=基本正确；0.4=部分正确；0.0=错误、拒答或矛盾。
- faithfulness: 1.0=主要事实均有上下文支持；0.7=大部分支持；0.4=部分支持；0.0=主要结论无支持。
- reason 解释评分依据，100字以内；defect 给轻量诊断，80字以内。

问题：{question}
标准答案：{answer}
判分指导：{guidance}
RAG 回答：{rag_answer}
清洗后的召回上下文：
{contexts}

输出格式：
{{"answer_correctness":0.0,"faithfulness":0.0,"is_correct":false,"reason":"...","defect":"..."}}
"""
TEXT_KEYS = ('text', 'content', 'context', 'page_content', 'chunk_text')
LOC_KEYS = ('doc_id', 'document_id', 'chunk_id', 'segment_id', 'filename')


class JudgeAnswerOperation:
    def __init__(self, llm: Any | None = None):
        self.llm = llm

    def execute(self, ctx) -> OperationOutput:
        dataset_ref = ArtifactRef.parse(str(ctx.params.get('eval_dataset_ref') or ''))
        case_id = validate_case_id(str(ctx.params.get('case_id') or ''))
        rag_ref = _ref(ctx, str(ctx.params.get('rag_answer_ref') or ''))
        if ctx.artifact_graph.schema_name(dataset_ref) != 'EvalDataset' or ctx.artifact_graph.schema_name(
            rag_ref
        ) != 'RagAnswer':
            raise ValueError('eval_dataset_ref must be EvalDataset and rag_answer_ref must be RagAnswer')
        dataset = ctx.artifact_graph.get(dataset_ref)
        case_ref = _case_ref(dataset, case_id)
        ctx.check_interrupt()
        case, rag = ctx.artifact_graph.get(case_ref), ctx.artifact_graph.get(rag_ref)
        _validate_binding(case, rag, case_id, dataset_ref, case_ref)
        contexts = _judge_contexts(rag.get('contexts'))
        prompt = PROMPT.format(
            question=case['question'], answer=case['answer'], guidance=case['grading_guidance'],
            rag_answer=rag['answer'], contexts='\n\n'.join(contexts),
        )
        ctx.report_progress(phase='judge_answer', status='running', message='judging RAG answer',
                            current_item=case_id)
        for attempt in range(1, 4):
            request = {'case_id': case_id, 'prompt': prompt, 'attempt': attempt}
            result = AdapterCall('llm.judge_answer', lambda payload: self._llm()(payload['prompt'], stream=False)).run(
                ctx, request, phase='judge_answer', item_ref=case_id
            )
            try:
                payload = _payload(ctx, dataset_ref, case_ref, rag_ref, case, rag, json_object(result.response),
                                   contexts)
                break
            except ValueError:
                if attempt == 3:
                    raise
                ctx.report_progress(phase='judge_answer', status='retrying', message='retrying judge JSON parse')
        ctx.report_progress(
            phase='judge_answer', status='success', message='judge result generated', current_item=case_id,
            detail={'call_id': result.record.call_id, 'quality_label': payload['quality_label']},
        )
        return OperationOutput([ArtifactDraft(
            f'judge_result_{case_id}', 'JudgeResult', payload, ctx.operation_run_id,
            input_refs=[dataset_ref, case_ref, rag_ref],
        )])

    def _llm(self):
        if self.llm is None:
            self.llm = evo_llm()
        return self.llm


def _payload(ctx, dataset_ref, case_ref, rag_ref, case, rag, raw_scores, contexts) -> dict[str, Any]:
    scores = _scores(raw_scores)
    doc_hits, doc_misses = _hits(case.get('reference_doc_ids'), rag.get('doc_ids'))
    chunk_hits, chunk_misses = _hits(case.get('reference_chunk_ids'), rag.get('chunk_ids'))
    doc_recall, context_recall = _recall(doc_hits, doc_misses), _recall(chunk_hits, chunk_misses)
    quality = _quality(scores['answer_correctness'], scores['faithfulness'], doc_recall, context_recall)
    return {
        'case_id': validate_case_id(str(case.get('id') or '')),
        'eval_dataset_ref': str(dataset_ref),
        'case_ref': str(case_ref),
        'rag_answer_ref': str(rag_ref),
        'trace_id': str(rag.get('trace_id') or ''),
        **scores,
        'context_recall': context_recall,
        'doc_recall': doc_recall,
        'quality_label': quality,
        'failure_type': _failure_type(
            quality, scores['answer_correctness'], scores['faithfulness'], doc_recall, context_recall
        ),
        'judge_contexts': contexts,
        'source_message_id': str(ctx.params.get('source_message_id') or ''),
    }


def _ref(ctx, value: str) -> ArtifactRef:
    value = value.strip()
    if not value:
        raise ValueError('rag_answer_ref is required')
    return ArtifactRef.parse(value) if '@v' in value else ctx.artifact_graph.latest_ref(value)


def _case_ref(dataset: dict[str, Any], case_id: str) -> ArtifactRef:
    case_ids, case_refs = list(dataset.get('case_ids') or []), list(dataset.get('case_refs') or [])
    if len(case_ids) != len(case_refs) or case_id not in case_ids:
        raise ValueError(f'case_id not found in EvalDataset: {case_id}')
    return ArtifactRef.parse(str(case_refs[case_ids.index(case_id)]))


def _validate_binding(case, rag, case_id: str, dataset_ref: ArtifactRef, case_ref: ArtifactRef) -> None:
    if str(case.get('id') or '') != case_id:
        raise ValueError(f'{case_ref} payload id mismatch')
    expected = {'case_id': case_id, 'eval_dataset_ref': str(dataset_ref), 'case_ref': str(case_ref)}
    for key, value in expected.items():
        if str(rag.get(key) or '') != value:
            raise ValueError(f'RagAnswer {key} mismatch: {rag.get(key)!r} != {value!r}')
    if any(not str(case.get(key) or '').strip() for key in ('question', 'answer', 'grading_guidance')):
        raise ValueError(f'{case_ref} missing question, answer or grading_guidance')
    if not str(rag.get('answer') or '').strip():
        raise ValueError('RagAnswer missing answer')


def _judge_contexts(contexts: Any) -> list[str]:
    out = []
    for item in contexts if isinstance(contexts, list) else []:
        if isinstance(item, str):
            text, loc = item.strip(), ''
        elif isinstance(item, dict):
            text = next((str(item[key]).strip() for key in TEXT_KEYS if item.get(key)), '')
            loc = ' '.join(f'{key}={item[key]}' for key in LOC_KEYS if item.get(key))
        else:
            text, loc = '', ''
        if text:
            out.append(f'{loc}\n{text}'.strip())
    return out


def _scores(data: dict[str, Any]) -> dict[str, Any]:
    answer_correctness, faithfulness = _score(data.get('answer_correctness')), _score(data.get('faithfulness'))
    reason = str(data.get('reason') or '').strip()[:100]
    if not reason:
        raise ValueError('judge response missing reason')
    is_correct = data.get('is_correct')
    if is_correct is None:
        is_correct = answer_correctness >= 0.8 and faithfulness >= 0.8
    if not isinstance(is_correct, bool):
        raise ValueError('judge response is_correct must be boolean')
    return {
        'answer_correctness': answer_correctness, 'faithfulness': faithfulness, 'is_correct': is_correct,
        'reason': reason, 'defect': str(data.get('defect') or '').strip()[:80],
    }


def _score(value: Any) -> float:
    score = round(float(value), 4)
    if not 0 <= score <= 1:
        raise ValueError(f'score out of range: {value}')
    return score


def _hits(expected: Any, actual: Any) -> tuple[list[str], list[str]]:
    exp, act = [str(x) for x in expected or [] if str(x)], {str(x) for x in actual or [] if str(x)}
    return [x for x in exp if x in act], [x for x in exp if x not in act]


def _recall(hit: list[str], miss: list[str]) -> float:
    return round(len(hit) / (len(hit) + len(miss)), 4) if hit or miss else 0.0


def _quality(answer_correctness: float, faithfulness: float, doc_recall: float, context_recall: float) -> str:
    if answer_correctness >= 0.8 and faithfulness >= 0.8 and (doc_recall > 0 or context_recall > 0):
        return 'good'
    if answer_correctness < 0.5 or faithfulness < 0.5 or (doc_recall == 0 and context_recall == 0):
        return 'bad'
    return 'partial'


def _failure_type(quality, answer_correctness, faithfulness, doc_recall, context_recall) -> str:
    if quality == 'good':
        return 'none'
    if doc_recall == 0 and context_recall == 0:
        return 'no_evidence'
    if answer_correctness < 0.5 and (doc_recall == 0 or context_recall == 0):
        return 'retrieval_miss'
    if answer_correctness >= 0.8 and faithfulness < 0.8:
        return 'unsupported_correct_answer'
    return 'faithfulness_issue' if faithfulness < 0.8 and answer_correctness < 0.8 else 'generation_gap'
