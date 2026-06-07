from __future__ import annotations

import json
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ...runtime import AdapterCall, OperationContext, OperationOutput, evo_llm
from .models import DatasetCase, artifact_payload
from .utils import json_object, validate_case_id

PROMPT = (
    '严格基于生成计划生成一条可验证 LazyMind 评测样本，只输出 JSON。\n'
    '问题必须独立完整，不能出现“参考内容/证据片段/上述/本文”等来源指代；答案只能来自证据片段。\n'
    '英文问题涉及论文/文章/框架时必须写出标题、文件名或 arXiv id，不能只写 the paper/this paper。\n'
    'grading_guidance 写给 judge，说明覆盖哪些核心事实即可。\n\n生成计划:\n{prompt}\n\n'
    '输出字段: question, answer, grading_guidance, generate_reason'
)
SOURCE_PREFIXES = ('根据参考内容，', '根据参考内容', '根据证据片段，', '根据证据片段',
                   '在参考内容中，', '在参考内容中', '在证据片段中，', '在证据片段中',
                   '参考内容中，', '证据片段中，')
SOURCE_DEICTICS = ('参考内容', '证据片段', '给定信息', '根据上述', '根据上文', '上述', '上文',
                   '本文', '参考材料', '给定材料', '参考上下文', '给定上下文',
                   'according to the paper', 'according to the passage', 'according to the text', 'the paper',
                   'this paper', 'the passage', 'the text', 'the provided')


class GenerateDatasetCaseOperation:
    def __init__(self, llm: Any | None = None):
        self.llm = llm

    def execute(self, ctx: OperationContext) -> OperationOutput:
        ref = _preparation_ref(ctx)
        if ctx.input_refs and (ref is None or ctx.input_refs[0].artifact_id == ref.artifact_id):
            ref = ctx.input_refs[0]
        if ref is None:
            raise ValueError('case_preparation_ref is required')
        plan = ctx.artifact_graph.get(ref)
        ctx.report_progress(
            phase='generate_case', status='running', message='generating dataset case',
            current_item=str(plan['case_id'])
        )
        prompt, feedback, result = PROMPT.format(prompt=plan['prompt']), '', None
        for attempt in range(2):
            ctx.check_interrupt()
            request = {'case_id': plan['case_id'], 'attempt': attempt + 1, 'prompt': prompt + feedback}
            result = AdapterCall(
                'llm.generate_dataset_case',
                lambda payload: self._llm()(payload['prompt'], stream=False),
            ).run(ctx, request, phase='generate_case', item_ref=str(plan['case_id']))
            try:
                payload = _case_payload(plan, json_object(result.response), str(ref))
                break
            except (ValueError, TypeError, KeyError, json.JSONDecodeError) as exc:
                feedback = f'\n\n上次输出无效：{exc}。请只输出合法 JSON，并包含全部必填字段。'
        else:
            raise ValueError('generated case remained invalid after retry')
        ctx.report_progress(
            phase='generate_case', status='success', message='dataset case generated',
            current_item=payload['id'], detail={'artifact_id': payload['id'], 'call_id': result.record.call_id}
        )
        return OperationOutput([ArtifactDraft(payload['id'], 'DatasetCase', payload, ctx.operation_run_id, [ref])])

    def _llm(self):
        if self.llm is None:
            self.llm = evo_llm()
        return self.llm


def _case_payload(plan: dict[str, Any], data: dict[str, Any], preparation_ref: str) -> dict[str, Any]:
    contexts = list(plan.get('context_reference') or [])
    question = _standalone(str(data.get('question') or '').strip())
    answer = str(data.get('answer') or data.get('ground_truth') or '').strip()
    guidance = str(data.get('grading_guidance') or data.get('judge_guidance')
                   or data.get('grading_judge') or data.get('evaluation_guidance') or '').strip()
    if not question or not answer or not guidance:
        raise ValueError('generated case missing question, answer or grading_guidance')
    return artifact_payload(DatasetCase(
        id=validate_case_id(str(plan['case_id'])),
        question=question,
        answer=answer,
        question_type=str(plan['question_type']),
        difficulty=str(plan['difficulty']),
        grading_guidance=guidance,
        reference_context=[str(item.get('content_preview') or '') for item in contexts],
        reference_doc=[str(item.get('filename') or '') for item in contexts],
        reference_doc_ids=[str(item.get('doc_id') or '') for item in contexts],
        reference_chunk_ids=[str(item.get('chunk_id') or '') for item in contexts],
        generate_reason=str(data.get('generate_reason') or data.get('reason') or '').strip(),
        source_preparation_ref=preparation_ref,
        source_message_id=str(plan.get('source_message_id') or ''),
    ))


def _preparation_ref(ctx: OperationContext) -> ArtifactRef | None:
    value = str(ctx.params.get('case_preparation_ref') or '').strip()
    if not value:
        return None
    return ArtifactRef.parse(value) if '@' in value else ctx.artifact_graph.latest_ref(value)


def _standalone(question: str) -> str:
    for prefix in SOURCE_PREFIXES:
        if question.startswith(prefix):
            question = question[len(prefix):].strip()
    lower = question.lower()
    if any(phrase in question for phrase in SOURCE_DEICTICS[:12]) or any(
        _bad_english(phrase, lower) for phrase in SOURCE_DEICTICS[12:]
    ):
        raise ValueError('generated question must name the referenced paper/source instead of using deictic wording')
    return question


def _bad_english(phrase: str, question: str) -> bool:
    return phrase in question and not (
        "paper '" in question or 'paper "' in question or 'paper titled' in question or 'arxiv' in question
        or '.pdf' in question
    )
