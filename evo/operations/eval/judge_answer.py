from collections import Counter
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ..analysis.utils import METRICS, clean_contexts
from ..dataset.utils import json_object, progress, validate_case_id
from ... import validate_id
from ...runtime import AdapterCall, OperationOutput, evo_llm
from .policy import failure_type as policy_failure_type, quality_label as policy_quality_label
from .policy import validate_evaluation_policy
from .rag_answer import _case_ref

PROMPT = ('你是严格的 RAG 评测裁判。只输出 JSON，不要 markdown，不要解释。\n\n评分规则：\n'
          '- answer_correctness: 1.0=正确；0.7=基本正确；0.4=部分正确；0.0=错误、拒答或矛盾。\n'
          '- faithfulness: 1.0=主要事实均有上下文支持；0.7=大部分支持；0.4=部分支持；0.0=主要结论无支持。\n'
          '- reason 解释评分依据，100字以内；defect 给轻量诊断，80字以内。\n\n问题：{question}\n标准答案：{answer}\n'
          '判分指导：{guidance}\nRAG 回答：{rag_answer}\n清洗后的召回上下文：\n{contexts}\n\n输出格式：\n'
          '{{"answer_correctness":0.0,"faithfulness":0.0,"is_correct":false,"reason":"...","defect":"..."}}\n')
MAX_FIELD, MAX_ANSWER, MAX_CONTEXT, MAX_CONTEXT_ITEM, MAX_CONTEXT_ITEMS = 4000, 12000, 20000, 4000, 8


class JudgeAnswerOperation:
    def __init__(self, llm: Any | None = None):
        self.llm = llm

    def execute(self, ctx) -> OperationOutput:
        graph = ctx.artifact_graph
        dataset_ref = ArtifactRef.parse(str(ctx.params.get('eval_dataset_ref') or ''))
        case_id = validate_case_id(str(ctx.params.get('case_id') or ''))
        raw = str(ctx.params.get('rag_answer_ref') or '').strip()
        if not raw: raise ValueError('rag_answer_ref is required')
        rag_ref = ArtifactRef.parse(raw) if '@v' in raw else next((i for i in ctx.input_refs if i.artifact_id == raw),
                                                                  None)
        if rag_ref is None: raise ValueError(f'rag_answer_ref is not bound in operation inputs: {raw}')
        if graph.schema_name(dataset_ref) != 'EvalDataset' or graph.schema_name(rag_ref) != 'RagAnswer':
            raise ValueError('eval_dataset_ref must be EvalDataset and rag_answer_ref must be RagAnswer')
        policy = validate_evaluation_policy(str(ctx.params.get('evaluation_policy') or ''))
        case_ref = _case_ref(graph.get(dataset_ref), case_id)
        ctx.check_interrupt()
        case, rag = graph.get(case_ref), graph.get(rag_ref)
        output_id = validate_id(str(ctx.params.get('output_id') or f'judge_result_{case_id}'), 'output_id')
        if rag.get('status') == 'failed' or rag.get('chat_error'):
            error = rag.get('chat_error') if isinstance(rag.get('chat_error'), dict) else {}
            reason = f"{error.get('type') or 'ChatError'}: {error.get('message') or 'RAG call failed'}"[:100]
            scores = {'answer_correctness': 0.0, 'faithfulness': 0.0, 'is_correct': False, 'reason': reason,
                      'defect': 'chat call failed; no quality scoring performed'[:80]}
            payload = self._result(ctx, policy, dataset_ref, case_ref, rag_ref, case_id,
                                   str(rag.get('trace_id') or ''), scores, 0.0, 0.0, 'failed', 'infra_failure', [])
            progress(ctx, 'judge_answer', 'success', 'RAG call failed; judge recorded infra failure without scoring',
                     current_item=case_id)
            return OperationOutput([ArtifactDraft(output_id, 'JudgeResult', payload, ctx.operation_run_id,
                                                  input_refs=[dataset_ref, case_ref, rag_ref])])
        if str(case.get('id') or '') != case_id: raise ValueError(f'{case_ref} payload id mismatch')
        for k, v in (('case_id', case_id), ('eval_dataset_ref', str(dataset_ref)), ('case_ref', str(case_ref))):
            if str(rag.get(k) or '') != v: raise ValueError(f'RagAnswer {k} mismatch: {rag.get(k)!r} != {v!r}')
        if any(not str(case.get(key) or '').strip() for key in ('question', 'answer', 'grading_guidance')):
            raise ValueError(f'{case_ref} missing question, answer or grading_guidance')
        prompt, contexts = build_judge_prompt(case, rag)
        progress(ctx, 'judge_answer', 'running', 'judging RAG answer', current_item=case_id)
        for attempt in range(1, 4):
            result = AdapterCall('llm.judge_answer', lambda p: self._llm()(p['prompt'], stream=False)).run(
                ctx, {'case_id': case_id, 'prompt': prompt, 'attempt': attempt}, phase='judge_answer', item_ref=case_id)
            try:
                scores = _scores(json_object(result.response))
                doc_recall = _recall(*_hits(case.get('reference_doc_ids'), rag.get('doc_ids')))
                context_recall = _recall(*_hits(case.get('reference_chunk_ids'), rag.get('chunk_ids')))
                args = (scores['answer_correctness'], scores['faithfulness'], doc_recall, context_recall)
                quality = policy_quality_label(policy, *args)
                payload = self._result(ctx, policy, dataset_ref, case_ref, rag_ref, case_id,
                                       str(rag.get('trace_id') or ''), scores, doc_recall, context_recall, quality,
                                       policy_failure_type(policy, quality, *args), contexts)
                break
            except ValueError:
                if attempt == 3: raise
                progress(ctx, 'judge_answer', 'retrying', 'retrying judge JSON parse')
        progress(ctx, 'judge_answer', 'success', 'judge result generated', current_item=case_id,
                 detail={'call_id': result.record.call_id, 'quality_label': payload['quality_label']})
        return OperationOutput([ArtifactDraft(output_id, 'JudgeResult', payload, ctx.operation_run_id,
                                              input_refs=[dataset_ref, case_ref, rag_ref])])

    def _llm(self):
        if self.llm is None: self.llm = evo_llm()
        return self.llm

    def _result(self, ctx, policy, dataset_ref, case_ref, rag_ref, case_id, trace_id, scores, doc_recall,
                context_recall, quality, failure, contexts) -> dict[str, Any]:
        return {'case_id': case_id, 'eval_dataset_ref': str(dataset_ref), 'case_ref': str(case_ref),
                'rag_answer_ref': str(rag_ref), 'trace_id': trace_id, **scores,
                'context_recall': context_recall, 'doc_recall': doc_recall, 'quality_label': quality,
                'failure_type': failure, 'evaluation_policy': policy, 'judge_contexts': contexts,
                'source_message_id': str(ctx.params.get('source_message_id') or '')}


class EvalAggregateOperation:
    def execute(self, ctx) -> OperationOutput:
        dataset_ref = ArtifactRef.parse(str(ctx.params.get('eval_dataset_ref') or ''))
        report_id = validate_id(str(ctx.params.get('report_id') or 'eval_report'), 'report_id')
        if ctx.artifact_graph.schema_name(dataset_ref) != 'EvalDataset':
            raise ValueError(f'artifact is not EvalDataset: {dataset_ref}')
        dataset = ctx.artifact_graph.get(dataset_ref)
        case_ids = [validate_case_id(str(item)) for item in dataset.get('case_ids') or []]
        case_refs = [ArtifactRef.parse(str(item)) for item in dataset.get('case_refs') or []]
        if not case_ids or len(case_ids) != len(case_refs):
            raise ValueError('EvalDataset case_ids/case_refs length mismatch')
        raw = ctx.params.get('judge_result_ids') or {}
        if raw and not isinstance(raw, dict): raise ValueError('judge_result_ids must be a mapping')
        ids = {c: validate_id(str(raw.get(c) or f'judge_result_{c}'), 'judge_result_id') for c in case_ids}
        extra = sorted(set(raw) - set(case_ids)) if isinstance(raw, dict) else []
        if extra: raise ValueError(f'judge_result_ids contains unknown cases: {extra}')
        rows = []
        for index, (case_id, case_ref) in enumerate(zip(case_ids, case_refs), 1):
            ctx.check_interrupt()
            rows.append(self._row(ctx, dataset_ref, case_id, case_ref, ids[case_id]))
            progress(ctx, 'eval_aggregate', 'running', f'aggregated {index}/{len(case_ids)} judge results',
                     current_item=case_id, done=index, total=len(case_ids))
        payload = self._report(report_id, dataset_ref, rows, str(ctx.params.get('source_message_id') or ''))
        progress(ctx, 'eval_aggregate', 'success', f'aggregated eval report with {len(rows)} cases',
                 current_item=report_id, done=len(rows), total=len(rows), detail=payload['metrics'])
        refs = [dataset_ref, *[row['judge_ref'] for row in rows]]
        return OperationOutput([ArtifactDraft(report_id, 'EvalReport', payload, ctx.operation_run_id,
                                              input_refs=refs)])

    def _row(self, ctx, dataset_ref, case_id, case_ref, judge_result_id) -> dict[str, Any]:
        judge_ref = next((ref for ref in ctx.input_refs if ref.artifact_id == judge_result_id), None)
        if judge_ref is None: raise ValueError(f'JudgeResult is not bound in operation inputs: {judge_result_id}')
        for ref, schema in ((judge_ref, 'JudgeResult'), (case_ref, 'DatasetCase')):
            if ctx.artifact_graph.schema_name(ref) != schema: raise ValueError(f'artifact is not {schema}: {ref}')
        judge, case = ctx.artifact_graph.get(judge_ref), ctx.artifact_graph.get(case_ref)
        for k, v in (('eval_dataset_ref', str(dataset_ref)), ('case_id', case_id), ('case_ref', str(case_ref))):
            if str(judge.get(k) or '') != v: raise ValueError(f'JudgeResult {k} mismatch: {judge.get(k)!r} != {v!r}')
        scores = {key: round(float(judge.get(key)), 4) for key in METRICS}
        bad = [key for key in METRICS if not 0 <= scores[key] <= 1]
        if bad: raise ValueError(f'{bad[0]} out of range: {judge.get(bad[0])!r}')
        return {'case_id': case_id, 'case_ref': case_ref, 'judge_ref': judge_ref, 'judge': judge, 'case': case,
                **scores}

    def _report(self, report_id, dataset_ref, rows, source_message_id) -> dict[str, Any]:
        # Infra failures (chat call failed) are execution problems, not quality data points:
        # they are excluded from quality metrics and block the report via the quality gate.
        scored = [row for row in rows if self._failure(row) != 'infra_failure']
        failed = [row for row in rows if self._failure(row) == 'infra_failure']
        metrics = {'scored_count': len(scored),
                   'correct_count': sum(row['judge'].get('is_correct') is True for row in scored),
                   'correct_rate': self._avg([1.0 if row['judge'].get('is_correct') is True else 0.0
                                              for row in scored]),
                   **{f'{key}_avg': self._avg([row[key] for row in scored]) for key in METRICS}}
        bad_keys = ('case_id', 'quality_label', 'failure_type', 'answer_correctness', 'faithfulness', 'reason',
                    'defect', 'trace_id')
        return {'id': report_id, 'eval_dataset_ref': str(dataset_ref), 'total': len(rows),
                'judge_result_refs': [str(row['judge_ref']) for row in rows], 'metrics': metrics,
                'quality_counts': dict(Counter(map(self._quality, rows))),
                'failure_type_counts': dict(Counter(map(self._failure, rows))),
                'by_question_type': self._group(scored, 'question_type'),
                'by_difficulty': self._group(scored, 'difficulty'),
                'bad_cases': [{key: row['judge'].get(key) for key in bad_keys}
                              | {'judge_result_ref': str(row['judge_ref'])}
                              for row in scored if self._quality(row) != 'good'],
                'execution_failures': [{'case_id': row['case_id'], 'judge_result_ref': str(row['judge_ref']),
                                        'reason': str(row['judge'].get('reason') or '')} for row in failed],
                'checks': self._checks(scored, failed), 'source_message_id': source_message_id}

    def _group(self, rows, key) -> dict[str, Any]:
        out = {}
        for name in sorted({str(row['case'].get(key) or '') for row in rows}):
            group = [row for row in rows if str(row['case'].get(key) or '') == name]
            out[name] = {'total': len(group), 'correct_rate': self._avg([
                1.0 if row['judge'].get('is_correct') is True else 0.0 for row in group
            ]), 'quality_counts': dict(Counter(map(self._quality, group)))}
        return out

    def _checks(self, scored, failed) -> dict[str, Any]:
        errors = [{'code': 'infra_failure', 'case_id': row['case_id'],
                   'message': str(row['judge'].get('reason') or 'RAG call failed')} for row in failed]
        if scored and all(row['doc_recall'] == 0 and row['context_recall'] == 0 for row in scored):
            errors.append({'code': 'systemic_zero_recall', 'case_id': '', 'message':
                           'every scored case has zero doc and context recall; trace/citation pipeline broken'})
        warnings = []
        for row in scored:
            case_id, judge = row['case_id'], row['judge']
            for code, message, hit in (
                ('bad_case', 'case quality_label is bad', self._quality(row) == 'bad'),
                ('failure_type', f'failure_type={self._failure(row)}', self._failure(row) != 'none'),
                ('missing_trace_id', 'judge result has empty trace_id', not str(judge.get('trace_id') or '').strip()),
                ('low_recall', 'doc_recall or context_recall is zero',
                 row['doc_recall'] == 0 or row['context_recall'] == 0),
            ):
                if hit: warnings.append({'code': code, 'case_id': case_id, 'message': message})
        return {'ready': not errors, 'errors': errors, 'warnings': warnings}

    def _avg(self, values) -> float:
        return round(sum(values) / len(values), 4) if values else 0.0

    def _quality(self, row) -> str:
        return str(row['judge'].get('quality_label') or 'bad')

    def _failure(self, row) -> str:
        return str(row['judge'].get('failure_type') or 'unknown')


def build_judge_prompt(case: dict[str, Any], rag: dict[str, Any]) -> tuple[str, list[str]]:
    contexts, remaining = [], MAX_CONTEXT
    for context in clean_contexts(rag.get('contexts'))[:MAX_CONTEXT_ITEMS]:
        if remaining <= 0: break
        text = _clip(context, min(MAX_CONTEXT_ITEM, remaining))
        if text:
            contexts.append(text)
            remaining -= len(text) + 2
    return PROMPT.format(question=_clip(case['question'], MAX_FIELD), answer=_clip(case['answer'], MAX_FIELD),
                         guidance=_clip(case['grading_guidance'], MAX_FIELD),
                         rag_answer=_clip(rag.get('answer'), MAX_ANSWER),
                         contexts='\n\n'.join(contexts)), contexts


def _clip(value: Any, limit: int) -> str:
    text, marker = str(value or '').strip(), '\n...[truncated]'
    return text if len(text) <= limit else text[:max(0, limit - len(marker))] + marker


def _scores(data: dict[str, Any]) -> dict[str, Any]:
    answer_correctness, faithfulness = _score(data.get('answer_correctness')), _score(data.get('faithfulness'))
    reason = str(data.get('reason') or '').strip()[:100]
    if not reason: raise ValueError('judge response missing reason')
    is_correct = data.get('is_correct')
    if is_correct is None: is_correct = answer_correctness >= 0.8 and faithfulness >= 0.8
    if not isinstance(is_correct, bool): raise ValueError('judge response is_correct must be boolean')
    return {'answer_correctness': answer_correctness, 'faithfulness': faithfulness, 'is_correct': is_correct,
            'reason': reason, 'defect': str(data.get('defect') or '').strip()[:80]}


def _score(value: Any) -> float:
    score = round(float(value), 4)
    if not 0 <= score <= 1: raise ValueError(f'score out of range: {value}')
    return score


def _hits(expected: Any, actual: Any) -> tuple[list[str], list[str]]:
    exp, act = [str(x) for x in expected or [] if str(x)], {str(x) for x in actual or [] if str(x)}
    return [x for x in exp if x in act], [x for x in exp if x not in act]


def _recall(hit: list[str], miss: list[str]) -> float:
    return round(len(hit) / (len(hit) + len(miss)), 4) if hit or miss else 0.0
