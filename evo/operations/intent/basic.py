from __future__ import annotations

from ...artifacts import ArtifactDraft, ArtifactRef
from ...runtime import AdapterCall, OperationContext, OperationOutput
from ...store import EvoStore


class PatchArtifactOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        ref = _single(ctx)
        return _out(ctx, ref.artifact_id, ctx.artifact_graph.schema_name(ref), ctx.params['payload'], [ref])


class RegenerateDatasetCaseOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        case_id = ctx.params['case_id']
        payload = {'id': case_id, 'question': ctx.params['question'], 'answer': ctx.params['answer'],
                   'question_type': ctx.params.get('question_type', ''),
                   'source_message_id': ctx.params.get('source_message_id', '')}
        return _out(ctx, case_id, 'DatasetCase', payload, list(ctx.input_refs))


class RejudgeCaseOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        ref = _single(ctx)
        return _out(ctx, f'judge_{ref.artifact_id}', 'JudgeResult', {
            'case_ref': str(ref), 'score': ctx.params['score'], 'rationale': ctx.params.get('rationale', ''),
        }, [ref])


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
            answer = {'status': 'missing', 'artifact_ref': ref, 'message': 'artifact not found'}
            return _answer(ctx, [ref], answer, [])
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
        return _answer(ctx, [f'run:{run_id}'], self.store.read_json(self.store.run_dir(run_id) / 'run.json'))


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
    if len(ctx.input_refs) != 1:
        raise ValueError('operation requires exactly one input artifact')
    return ctx.input_refs[0]


def _out(ctx: OperationContext, artifact_id: str, schema: str, payload, refs) -> OperationOutput:
    return OperationOutput([ArtifactDraft(artifact_id, schema, payload, ctx.operation_run_id, input_refs=refs)])


def _answer(ctx: OperationContext, refs: list[str], answer, input_refs: list[ArtifactRef] | None = None):
    payload = {'source_message_id': ctx.params.get('source_message_id', ''),
               'query_intent_id': ctx.params['query_intent_id'], 'target_refs': refs, 'answer': answer}
    return _out(ctx, f"intent_answer_{ctx.params['query_intent_id']}", 'IntentAnswer', payload, input_refs or [])
