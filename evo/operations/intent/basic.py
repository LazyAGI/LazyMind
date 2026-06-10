from __future__ import annotations

from ...artifacts import ArtifactDraft, ArtifactRef, validate_artifact_payload
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
        return _answer(ctx, [f'operation:{oid}'], self.store.read_operation(ctx.run_id, oid))


class ReadRunStatusQueryOperation:
    def __init__(self, store: EvoStore):
        self.store = store

    def execute(self, ctx: OperationContext) -> OperationOutput:
        run_id = ctx.params.get('run_id') or ctx.run_id
        projection = rebuild_frontend_state(self.store, run_id, write=True)
        return _answer(ctx, [f'run:{run_id}'], {**(projection.get('run') or {}), 'projection': projection})


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
