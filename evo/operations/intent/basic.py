from __future__ import annotations

import json
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactGraph, ArtifactRef, validate_artifact_payload
from ...internal_ids import stage_group
from ...projections import rebuild_frontend_state
from ...runtime import AdapterCall, OperationContext, OperationOutput
from ...store import EvoStore
from ..dataset.utils import validate_case_id

_PATCH_STR_FIELDS = {'question', 'answer', 'question_type', 'difficulty', 'grading_guidance'}
_PATCH_LIST_FIELDS = {'reference_context', 'reference_doc', 'reference_doc_ids', 'reference_chunk_ids'}
_JUDGE_PATCH_FIELDS = {
    'answer_correctness', 'faithfulness', 'doc_recall', 'context_recall', 'is_correct', 'quality_label',
    'failure_type', 'reason', 'defect',
}
_CLASSIFICATION_PATCH_FIELDS = {
    'coarse_category', 'fine_category', 'confidence', 'reason', 'classification_method', 'missing_evidence',
    'next_step',
}


class PatchArtifactOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        ref = _single(ctx)
        schema = ctx.artifact_graph.schema_name(ref)
        if schema != 'DatasetCase':
            raise ValueError(f'PatchArtifactOperation only supports DatasetCase typed patches: {schema}')
        payload = {**ctx.artifact_graph.get(ref), **_dataset_case_patch(ctx.params), 'source_case_ref': str(ref)}
        validate_artifact_payload('DatasetCase', payload)
        return _out(ctx, ref.artifact_id, schema, payload, [ref])


class PatchJudgeResultOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        ref = _single(ctx)
        if ctx.artifact_graph.schema_name(ref) != 'JudgeResult':
            raise ValueError(f'PatchJudgeResultOperation only supports JudgeResult: {ref}')
        payload = {**ctx.artifact_graph.get(ref), **_patch(ctx.params, _JUDGE_PATCH_FIELDS),
                   'source_judge_result_ref': str(ref)}
        _validate_score_fields(payload)
        validate_artifact_payload('JudgeResult', payload)
        return _out(ctx, ref.artifact_id, 'JudgeResult', payload, [ref])


class PatchClassificationOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        ref = _single(ctx)
        schema = ctx.artifact_graph.schema_name(ref)
        if schema not in {'CaseCoarseClassification', 'CaseFineClassification'}:
            raise ValueError(f'PatchClassificationOperation only supports classification artifacts: {schema}')
        payload = {**ctx.artifact_graph.get(ref), **_patch(ctx.params, _CLASSIFICATION_PATCH_FIELDS),
                   'source_classification_ref': str(ref)}
        validate_artifact_payload(schema, payload)
        return _out(ctx, ref.artifact_id, schema, payload, [ref])


class RegenerateDatasetCaseOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        ref = _single(ctx)
        if ctx.artifact_graph.schema_name(ref) != 'DatasetCase':
            raise ValueError(f'artifact is not DatasetCase: {ref}')
        case_id = validate_case_id(str(ctx.params['case_id']))
        base = ctx.artifact_graph.get(ref)
        if str(base.get('id') or '') != case_id:
            raise ValueError(f'{ref} payload id mismatch: {base.get("id")} != {case_id}')
        payload = {
            **base, 'id': case_id, 'question': ctx.params['question'], 'answer': ctx.params['answer'],
            'question_type': ctx.params.get('question_type') or base.get('question_type', ''),
            'source_message_id': ctx.params.get('source_message_id', ''), 'source_case_ref': str(ref),
        }
        validate_artifact_payload('DatasetCase', payload)
        return _out(ctx, case_id, 'DatasetCase', payload, [ref])


class RejudgeCaseOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        raise ValueError(
            'rejudge_case cannot create a valid JudgeResult without a bound RagAnswer; use judge_answer_case')


class RedirectResearchOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        rid = ctx.params['researcher_id']
        return _out(ctx, f'research_redirect_{rid}', 'ResearchRedirect', {
            'researcher_id': rid, 'instructions': ctx.params['instructions'],
            'source_message_id': ctx.params.get('source_message_id', ''),
        }, list(ctx.input_refs))


class ReadArtifactQueryOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        if not ctx.input_refs and ctx.params.get('artifact_ref'):
            ref = str(ctx.params['artifact_ref'])
            return _answer(ctx, [ref], {'status': 'missing', 'artifact_ref': ref, 'message': 'artifact not found'}, [])
        payloads = [ctx.artifact_graph.get(ref) for ref in ctx.input_refs]
        return _answer(ctx, [str(ref) for ref in ctx.input_refs], payloads[0] if len(payloads) == 1 else payloads,
                       list(ctx.input_refs))


class ReadOperationQueryOperation:
    def __init__(self, store: EvoStore):
        self.store = store

    def execute(self, ctx: OperationContext) -> OperationOutput:
        oid = ctx.params['operation_run_id']
        return _answer(ctx, [f'operation:{oid}'], read_operation_query_answer(self.store, ctx.run_id, oid))


class ReadRunStatusQueryOperation:
    def __init__(self, store: EvoStore):
        self.store = store

    def execute(self, ctx: OperationContext) -> OperationOutput:
        run_id = ctx.params.get('run_id') or ctx.run_id
        return _answer(ctx, [f'run:{run_id}'], read_run_status_query_answer(self.store, run_id, write=True))


class ExplainRunFailureQueryOperation:
    def __init__(self, store: EvoStore):
        self.store = store

    def execute(self, ctx: OperationContext) -> OperationOutput:
        run_id = ctx.params.get('run_id') or ctx.run_id
        stage = str(ctx.params.get('stage') or '').strip()
        evidence = explain_run_failure_query_answer(self.store, ctx.artifact_graph, run_id, stage)
        failed_ops = evidence['failed_operations']
        return _answer(ctx, [f'run:{run_id}', *(str(op.get('operation_run_id')) for op in failed_ops[-10:])],
                       evidence)


def read_operation_query_answer(store: EvoStore, run_id: str, operation_run_id: str) -> dict:
    return store.read_operation(run_id, operation_run_id)


def read_run_status_query_answer(store: EvoStore, run_id: str, *, write: bool = False) -> dict:
    projection = rebuild_frontend_state(store, run_id, write=write)
    return _compact_run_status(projection)


def _compact_run_status(projection: dict) -> dict:
    run = projection.get('run') if isinstance(projection.get('run'), dict) else {}
    operations = projection.get('operations') if isinstance(projection.get('operations'), list) else []
    operations = _effective_operations(operations)
    progress = projection.get('progress') if isinstance(projection.get('progress'), dict) else {}
    stages: dict[str, dict[str, Any]] = {}
    active_operations, failed_operations = [], []
    for operation in operations:
        if not isinstance(operation, dict):
            continue
        if _is_intent_helper_operation(operation):
            continue
        stage = str(operation.get('stage_tag') or operation.get('stage') or operation.get('flow_tag') or 'default')
        status = _operation_status(operation)
        row = stages.setdefault(stage, {'stage': stage, 'total': 0, 'statuses': {}})
        row['total'] += 1
        row['statuses'][status] = row['statuses'].get(status, 0) + 1
        summary = _operation_summary(operation, progress)
        if status in {'running', 'checkpointed'}:
            active_operations.append(summary)
        if status == 'failed':
            failed_operations.append(summary)
    latest = projection.get('latest_artifacts') if isinstance(projection.get('latest_artifacts'), dict) else {}
    return {
        'run': {key: run.get(key) for key in sorted(run) if key not in {'parent_checkpoint'}},
        'stages': list(stages.values()),
        'active_operations': active_operations[-20:],
        'failed_operations': failed_operations[-20:],
        'blockers': projection.get('blockers') or [],
        'latest_artifact_count': len(latest),
        'latest_result_artifacts': {
            key: latest[key] for key in sorted(latest)
            if key in {'eval_dataset', 'eval_report', 'classification_report', 'repair_loop_plan',
                       'candidate_eval_report', 'abtest_comparison'}
        },
        'built_at': projection.get('built_at'),
        'source_event_count': projection.get('source_event_count'),
    }


def _operation_status(operation: dict) -> str:
    if operation.get('status') == 'ended':
        return str(operation.get('outcome') or 'ended')
    return str(operation.get('status') or 'pending')


def _effective_operations(operations: list) -> list[dict]:
    grouped: dict[str, list[dict]] = {}
    for operation in operations:
        if isinstance(operation, dict):
            grouped.setdefault(_logical_operation_id(operation), []).append(operation)
    return [_effective_operation(items) for items in grouped.values()]


def _effective_operation(items: list[dict]) -> dict:
    active = [item for item in items if _operation_status(item) in {'running', 'checkpointed'}]
    if active:
        return sorted(active, key=_operation_order)[-1]
    success = [item for item in items if _operation_status(item) == 'success']
    if success:
        return sorted(success, key=_operation_order)[-1]
    terminal = [item for item in items if item.get('status') == 'ended']
    if terminal:
        return sorted(terminal, key=_operation_order)[-1]
    return sorted(items, key=_operation_order)[-1]


def _logical_operation_id(operation: dict) -> str:
    return str(operation.get('operation_id') or operation.get('operation_run_id') or '')


def _operation_order(operation: dict) -> tuple[int, str]:
    return int(operation.get('attempt') or 0), str(operation.get('created_at') or operation.get('ended_at') or '')


def _is_intent_helper_operation(operation: dict) -> bool:
    operation_type = str(operation.get('operation_type') or '')
    operation_id = str(operation.get('operation_run_id') or operation.get('operation_id') or '')
    return operation_type in {
        'IntentParseOperation', 'ReadRunStatusQueryOperation', 'ExplainRunFailureQueryOperation',
        'ReadOperationQueryOperation', 'ReadArtifactQueryOperation', 'RespondToUserOperation',
        'RenderIntentAnswerOperation',
    } or operation_id.startswith('intent.')


def _operation_summary(operation: dict, progress: dict) -> dict:
    operation_id = str(operation.get('operation_run_id') or '')
    item_progress = progress.get(operation_id) if isinstance(progress.get(operation_id), dict) else {}
    return {
        'operation_run_id': operation_id,
        'operation_type': operation.get('operation_type'),
        'flow_tag': operation.get('flow_tag'),
        'stage_tag': operation.get('stage_tag'),
        'status': _operation_status(operation),
        'progress': {
            key: item_progress.get(key)
            for key in ('phase', 'message', 'current_item', 'done', 'total', 'status')
            if item_progress.get(key) not in (None, '')
        },
    }


def explain_run_failure_query_answer(store: EvoStore, artifact_graph: ArtifactGraph, run_id: str,
                                     stage: str = '') -> dict:
    run_dir = store.run_dir(run_id)
    operations = store.list_operations(run_id)
    events = [event.__dict__ for event in store.read_events(run_id)[-80:]]
    failed_ops = [row for row in operations if _failed_operation(row, stage)]
    return {
        'run': store.read_json(run_dir / 'run.json') if (run_dir / 'run.json').exists() else {},
        'stage': stage,
        'failed_operations': failed_ops[-30:],
        'recent_failure_events': [event for event in events if _failure_event(event, stage)][-30:],
        'eval_report': _latest_payload(artifact_graph, 'eval_report'),
        'recent_calls': [_call_row(record) for record in store.read_calls(run_id)[-50:]],
    }


class RespondToUserOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        return _answer(ctx, [], ctx.params['answer'])


class RenderIntentAnswerOperation:
    def __init__(self, llm):
        self.llm = llm

    def execute(self, ctx: OperationContext) -> OperationOutput:
        answers = [ctx.artifact_graph.get(ref) for ref in ctx.input_refs]
        evidence = {
            'message': ctx.params.get('message', ''),
            'query_action': ctx.params.get('query_action', ''),
            'task_results': ctx.params.get('task_results') or [],
            'answers': answers,
        }
        result = AdapterCall('llm.render_intent_answer', lambda payload: self.llm(payload['prompt'], stream=False)).run(
            ctx, {'message': evidence['message'], 'query_action': evidence['query_action'],
                  'answer_count': len(answers), 'prompt': _render_prompt(evidence)},
            phase='render_intent_answer', item_ref=str(ctx.params.get('source_message_id') or ''),
        )
        return _answer(ctx, _target_refs(answers), str(result.response).strip(), list(ctx.input_refs))


class IntentParseOperation:
    def __init__(self, llm):
        self.llm = llm

    def execute(self, ctx: OperationContext) -> OperationOutput:
        request = {key: ctx.params[key] for key in ('message_id', 'message', 'checkpoint_id', 'capabilities')}
        result = AdapterCall('llm.intent_parser', lambda payload: self.llm(payload['prompt'], stream=False)).run(
            ctx, request | {'prompt': ctx.params['prompt']}, phase='parse_intent', item_ref=request['message_id']
        )
        payload = request | {'raw_response': result.response, 'call_id': result.record.call_id}
        return _out(ctx, f"intent_parse_{request['message_id']}", 'IntentParse', payload, list(ctx.input_refs))


def _single(ctx: OperationContext) -> ArtifactRef:
    if len(ctx.input_refs) != 1: raise ValueError('operation requires exactly one input artifact')
    return ctx.input_refs[0]


def _dataset_case_patch(params: dict) -> dict:
    patch = {key: params[key] for key in _PATCH_STR_FIELDS | _PATCH_LIST_FIELDS if key in params}
    if not patch: raise ValueError('DatasetCase patch must include at least one typed field')
    for key in _PATCH_STR_FIELDS & patch.keys():
        if not isinstance(patch[key], str): raise ValueError(f'DatasetCase patch field {key} must be str')
    for key in _PATCH_LIST_FIELDS & patch.keys():
        if not isinstance(patch[key], list) or not all(isinstance(item, str) for item in patch[key]):
            raise ValueError(f'DatasetCase patch field {key} must be list[str]')
    return patch


def _patch(params: dict, allowed: set[str]) -> dict:
    patch = {key: params[key] for key in allowed if key in params}
    if not patch: raise ValueError('patch must include at least one supported field')
    return patch


def _validate_score_fields(payload: dict) -> None:
    for key in ('answer_correctness', 'faithfulness', 'doc_recall', 'context_recall'):
        value = float(payload.get(key))
        if not 0 <= value <= 1:
            raise ValueError(f'{key} out of range: {payload.get(key)!r}')
    if not isinstance(payload.get('is_correct'), bool):
        raise ValueError('is_correct must be bool')


def _out(ctx: OperationContext, artifact_id: str, schema: str, payload, refs) -> OperationOutput:
    return OperationOutput([ArtifactDraft(artifact_id, schema, payload, ctx.operation_run_id, input_refs=refs)])


def _answer(ctx: OperationContext, refs: list[str], answer, input_refs: list[ArtifactRef] | None = None):
    payload = {'source_message_id': ctx.params.get('source_message_id', ''),
               'query_intent_id': ctx.params['query_intent_id'], 'target_refs': refs, 'answer': answer}
    return _out(ctx, f"intent_answer_{ctx.params['query_intent_id']}", 'IntentAnswer', payload, input_refs or [])


def _target_refs(answers: list[dict]) -> list[str]:
    refs: list[str] = []
    for answer in answers:
        refs.extend(str(ref) for ref in answer.get('target_refs') or [])
    return list(dict.fromkeys(refs))


def _render_prompt(evidence: dict) -> str:
    return f"""
你是 LazyMind evo 的 intent 回复生成器。只输出给用户看的自然语言回复，不要 JSON，不要 Markdown 表格。

要求：
- 只基于 task_results 和 IntentAnswer 证据回复，不要编造。
- 复杂消息可能分多步处理，回复中简要说明已经完成/执行到的原子任务。
- task_results 按顺序表示已经执行过的步骤，不要把其中的 action 或 remaining_message 说成“下一步将执行”。
- 只有最后一个 task_results 之后仍有明确未处理内容时，才说明还有待处理请求。
- 用户问进度/状态时，说明当前状态、关键阶段进展、失败或阻塞项，以及建议的下一步。
- 如果 evidence 中有 failed_operations，点名少量关键 operation/case，并说明这意味着流程未完全成功。
- 如果 evidence 显示没有 active task 但 run.status 仍是 running，要指出状态不一致。
- 回复要简洁，中文为主。

evidence:
{json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True, default=str)}
""".strip()


def _failed_operation(row: dict, stage: str) -> bool:
    if str(row.get('outcome') or row.get('status') or '') != 'failed': return False
    if not stage: return True
    tags = row.get('tags') or {}
    return stage in {
        stage_group(str(row.get('flow_tag') or '')),
        stage_group(str(row.get('stage_tag') or '')),
        stage_group(str(tags.get('evo_step') or '')),
    }


def _failure_event(event: dict, stage: str) -> bool:
    text = f"{event.get('event_type', '')} {event.get('payload', {})}"
    if not any(token in text.lower() for token in ('fail', 'error', 'exception', 'timeout', '403')): return False
    return not stage or stage in text


def _latest_payload(artifact_graph: ArtifactGraph, artifact_id: str):
    try:
        return artifact_graph.get(artifact_graph.latest_ref(artifact_id))
    except Exception as exc:
        return {'missing': artifact_id, 'error': str(exc)}


def _call_row(record) -> dict:
    row = record.__dict__.copy()
    for key in ('request', 'response'):
        value = row.get(key)
        if isinstance(value, str) and len(value) > 4000: row[key] = value[:4000] + '...<truncated>'
    return row
