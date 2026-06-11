from __future__ import annotations

from ...artifacts import ArtifactDraft, ArtifactGraph, ArtifactRef, validate_artifact_payload
from ...internal_ids import stage_group
from ...projections import rebuild_frontend_state
from ...runtime import AdapterCall, OperationContext, OperationOutput
from ...store import EvoStore
from ..dataset.utils import validate_case_id

_PATCH_STR_FIELDS = {'question', 'answer', 'question_type', 'difficulty', 'grading_guidance'}
_PATCH_LIST_FIELDS = {'reference_context', 'reference_doc', 'reference_doc_ids', 'reference_chunk_ids'}


class PatchArtifactOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        ref = _single(ctx)
        schema = ctx.artifact_graph.schema_name(ref)
        if schema != 'DatasetCase':
            raise ValueError(f'PatchArtifactOperation only supports DatasetCase typed patches: {schema}')
        payload = {**ctx.artifact_graph.get(ref), **_dataset_case_patch(ctx.params), 'source_case_ref': str(ref)}
        validate_artifact_payload('DatasetCase', payload)
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
    return {**(projection.get('run') or {}), 'projection': projection}


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


def _out(ctx: OperationContext, artifact_id: str, schema: str, payload, refs) -> OperationOutput:
    return OperationOutput([ArtifactDraft(artifact_id, schema, payload, ctx.operation_run_id, input_refs=refs)])


def _answer(ctx: OperationContext, refs: list[str], answer, input_refs: list[ArtifactRef] | None = None):
    payload = {'source_message_id': ctx.params.get('source_message_id', ''),
               'query_intent_id': ctx.params['query_intent_id'], 'target_refs': refs, 'answer': answer}
    return _out(ctx, f"intent_answer_{ctx.params['query_intent_id']}", 'IntentAnswer', payload, input_refs or [])


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
