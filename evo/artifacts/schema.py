from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any

from .. import validate_case_id
from .models import ArtifactRef


@dataclass(frozen=True)
class ArtifactSchema:
    required: tuple[str, ...] = ()
    types: dict[str, type | tuple[type, ...]] = field(default_factory=dict)
    nonempty: tuple[str, ...] = ()
    validate: Callable[[dict[str, Any]], list[str]] | None = None


def _same_length(left: str, right: str) -> Callable[[dict[str, Any]], list[str]]:
    return lambda p: [] if len(p.get(left) or []) == len(p.get(right) or []) else [f'{left}/{right} length mismatch']


def _blank(value: Any) -> bool:
    if isinstance(value, str): return not value.strip()
    if isinstance(value, (list, tuple, dict, set)): return not value
    return value is None


def _case_id_errors(payload: dict[str, Any], key: str) -> list[str]:
    value = payload.get(key)
    if not isinstance(value, str): return [f'{key} must be str']
    try:
        validate_case_id(value)
    except ValueError as exc:
        return [f'{key} invalid: {exc}']
    return []


def _artifact_ref_errors(payload: dict[str, Any], key: str) -> list[str]:
    value = payload.get(key)
    if not isinstance(value, str): return [f'{key} must be str']
    try:
        ArtifactRef.parse(value)
    except (TypeError, ValueError):
        return [f'{key} must be artifact ref']
    return []


def _judge_result_errors(payload: dict[str, Any]) -> list[str]:
    errors = _case_id_errors(payload, 'case_id')
    for key in ('eval_dataset_ref', 'case_ref', 'rag_answer_ref'):
        errors.extend(_artifact_ref_errors(payload, key))
    for key in ('answer_correctness', 'faithfulness', 'doc_recall', 'context_recall'):
        value = payload.get(key)
        if isinstance(value, bool) or not isinstance(value, (int, float)): errors.append(f'{key} must be number')
        elif not 0 <= float(value) <= 1: errors.append(f'{key} out of range')
    if str(payload.get('quality_label') or '') not in {'good', 'bad', 'partial', 'failed'}:
        errors.append('quality_label invalid')
    if not str(payload.get('failure_type') or '').strip(): errors.append('failure_type must be non-empty')
    return errors


def _dataset_case_errors(payload: dict[str, Any]) -> list[str]:
    errors = _case_id_errors(payload, 'id')
    for key in ('reference_context', 'reference_doc', 'reference_doc_ids', 'reference_chunk_ids'):
        values = payload.get(key)
        if isinstance(values, list) and not all(isinstance(item, str) for item in values):
            errors.append(f'{key} must contain only str')
    return errors


def _checkpoint_resume_errors(payload: dict[str, Any]) -> list[str]:
    allowed = {'id', 'checkpoint_id', 'input_policy', 'next_operations', 'rebound_input_refs', 'resume_context'}
    errors = [f'unsupported top-level field: {key}' for key in sorted(set(payload) - allowed)]
    if payload.get('input_policy') not in {'resume_from_snapshot', 'resume_with_interventions'}:
        errors.append('input_policy invalid')
    if not all(isinstance(item, str) and item.strip() for item in payload.get('next_operations') or []):
        errors.append('next_operations must contain only non-empty str')
    context = payload.get('resume_context')
    if context is not None:
        errors.extend(_checkpoint_resume_context_errors(context))
    rebound = payload.get('rebound_input_refs') or {}
    if not isinstance(rebound, dict): return errors
    for operation_id, changes in rebound.items():
        if not isinstance(operation_id, str) or not operation_id.strip():
            errors.append('rebound operation id must be non-empty str')
        if not isinstance(changes, list):
            errors.append(f'rebound changes for {operation_id} must be list')
            continue
        for change in changes:
            if not isinstance(change, dict):
                errors.append(f'rebound change for {operation_id} must be object')
                continue
            for key in ('artifact_id', 'old_ref', 'new_ref'):
                if not isinstance(change.get(key), str) or not change[key].strip():
                    errors.append(f'rebound change {key} must be non-empty str')
            for key in ('old_ref', 'new_ref'):
                if isinstance(change.get(key), str):
                    errors.extend(_artifact_ref_errors(change, key))
            old_ref = str(change.get('old_ref') or '')
            new_ref = str(change.get('new_ref') or '')
            artifact_id = str(change.get('artifact_id') or '')
            if old_ref and new_ref and old_ref == new_ref: errors.append('rebound old_ref and new_ref must differ')
            if artifact_id and old_ref and old_ref.split('@v', 1)[0] != artifact_id:
                errors.append('rebound old_ref artifact_id mismatch')
            if artifact_id and new_ref and new_ref.split('@v', 1)[0] != artifact_id:
                errors.append('rebound new_ref artifact_id mismatch')
    return errors


def _checkpoint_resume_context_errors(context: dict[str, Any]) -> list[str]:
    kind = str(context.get('kind') or '')
    expected = {'intent_confirmation': {'kind', 'message_id'},
                'stage': {'kind', 'stage', 'next_stage', 'source', 'recovered'}}.get(kind)
    if expected is None: return ['resume_context kind invalid']
    errors = [f'resume_context unsupported field: {key}' for key in sorted(set(context) - expected)]
    if kind == 'intent_confirmation':
        if not isinstance(context.get('message_id'), str) or not context['message_id'].strip():
            errors.append('resume_context message_id must be non-empty str')
    if kind == 'stage':
        for key in ('stage', 'next_stage', 'source'):
            if not isinstance(context.get(key), str): errors.append(f'resume_context {key} must be str')
        if not str(context.get('next_stage') or '').strip():
            errors.append('resume_context next_stage must be non-empty')
        if not isinstance(context.get('recovered'), bool): errors.append('resume_context recovered must be bool')
    return errors


SCHEMAS: dict[str, ArtifactSchema] = {
    'DatasetCase': ArtifactSchema(
        required=('id', 'question', 'answer', 'question_type', 'difficulty', 'grading_guidance'),
        nonempty=('id', 'question', 'answer', 'question_type', 'difficulty', 'grading_guidance'),
        types={'id': str, 'question': str, 'answer': str, 'question_type': str, 'difficulty': str,
               'grading_guidance': str, 'reference_context': list, 'reference_doc': list,
               'reference_doc_ids': list, 'reference_chunk_ids': list},
        validate=_dataset_case_errors,
    ),
    'EvalDataset': ArtifactSchema(required=('case_ids', 'case_refs'), nonempty=('case_ids', 'case_refs'),
                                  types={'case_ids': list, 'case_refs': list},
                                  validate=_same_length('case_ids', 'case_refs')),
    'RagAnswer': ArtifactSchema(required=('case_id', 'eval_dataset_ref', 'case_ref', 'answer', 'status'),
                                nonempty=('case_id', 'eval_dataset_ref', 'case_ref', 'status')),
    'JudgeResult': ArtifactSchema(
        required=('case_id', 'eval_dataset_ref', 'case_ref', 'rag_answer_ref', 'answer_correctness', 'faithfulness',
                  'doc_recall', 'context_recall', 'is_correct', 'quality_label', 'failure_type'),
        nonempty=('case_id', 'eval_dataset_ref', 'case_ref', 'rag_answer_ref', 'quality_label', 'failure_type'),
        types={'is_correct': bool},
        validate=_judge_result_errors,
    ),
    'EvalReport': ArtifactSchema(types={'judge_result_refs': list}),
    'CaseCoarseClassification': ArtifactSchema(
        required=('case_id', 'eval_report_ref', 'eval_dataset_ref', 'case_ref', 'rag_answer_ref',
                  'judge_result_ref', 'coarse_category', 'next_step'),
        types={'next_step': dict},
    ),
    'CaseFineClassification': ArtifactSchema(
        required=('case_id', 'coarse_classification_ref', 'eval_report_ref', 'eval_dataset_ref',
                  'case_ref', 'rag_answer_ref', 'judge_result_ref', 'coarse_category', 'fine_category'),
    ),
    'ClassificationReport': ArtifactSchema(
        required=('eval_report_ref', 'eval_dataset_ref', 'fine_classification_refs', 'cases'),
        types={'fine_classification_refs': list, 'cases': list},
    ),
    'ABTestComparison': ArtifactSchema(types={'case_ids': list, 'metrics': dict, 'case_deltas': list,
                                              'decision': dict}),
    'ABTestReport': ArtifactSchema(required=('abtest_comparison_id', 'markdown'),
                                   nonempty=('abtest_comparison_id', 'markdown'), types={'markdown': str}),
    'CallPayload': ArtifactSchema(required=('call_id', 'kind', 'payload', 'audit'), nonempty=('call_id', 'kind'),
                                  types={'call_id': str, 'kind': str, 'audit': dict}),
    'CallRecord': ArtifactSchema(required=('operation_run_id', 'adapter_type', 'call_id', 'status'),
                                 nonempty=('operation_run_id', 'adapter_type', 'call_id', 'status')),
    'CandidateAlgorithmCutover': ArtifactSchema(
        required=('id', 'abtest_comparison_ref', 'candidate_workspace_ref', 'router_admin_url'),
        nonempty=('id', 'abtest_comparison_ref', 'candidate_workspace_ref', 'router_admin_url'),
    ),
    'CandidateClassificationReport': ArtifactSchema(required=('id', 'cases'), nonempty=('id',),
                                                    types={'id': str, 'cases': list}),
    'CandidateServiceRun': ArtifactSchema(required=('id',), nonempty=('id',), types={'id': str}),
    'CandidateServiceStop': ArtifactSchema(
        required=('id', 'candidate_service_ref', 'pid', 'stopped'), nonempty=('id', 'candidate_service_ref'),
        types={'id': str, 'candidate_service_ref': str, 'stopped': bool},
    ),
    'CandidateWorkspace': ArtifactSchema(required=('id', 'workspace_ref'), nonempty=('id', 'workspace_ref'),
                                         types={'id': str, 'workspace_ref': str}),
    'CheckpointResume': ArtifactSchema(
        required=('id', 'checkpoint_id', 'input_policy', 'next_operations', 'rebound_input_refs'),
        nonempty=('id', 'checkpoint_id', 'input_policy'),
        types={'id': str, 'checkpoint_id': str, 'input_policy': str, 'next_operations': list,
               'rebound_input_refs': dict, 'resume_context': dict},
        validate=_checkpoint_resume_errors,
    ),
    'CasePreparation': ArtifactSchema(
        required=('case_id', 'question_type', 'difficulty', 'source_snapshot_ref'),
        nonempty=('case_id', 'question_type', 'difficulty', 'source_snapshot_ref'),
        validate=lambda payload: _case_id_errors(payload, 'case_id'),
    ),
    'ConditionalIntent': ArtifactSchema(
        required=('conditional_intent_id', 'source_message_id', 'checkpoint_id', 'status', 'predicate'),
        nonempty=('conditional_intent_id', 'source_message_id', 'checkpoint_id', 'status', 'predicate'),
        types={'predicate': dict, 'then_intents': list, 'else_intents': list},
    ),
    'CorpusDocumentPage': ArtifactSchema(required=('source_id', 'page_index', 'documents'),
                                         nonempty=('source_id', 'documents'), types={'documents': list}),
    'CorpusLoadReport': ArtifactSchema(
        required=('sources', 'document_page_refs', 'stats', 'skipped', 'errors'),
        types={'sources': list, 'document_page_refs': list, 'stats': dict, 'skipped': list, 'errors': list},
    ),
    'CorpusSnapshot': ArtifactSchema(
        required=('snapshot_id', 'source_report_ref', 'source_unit_page_refs', 'stats'),
        nonempty=('snapshot_id', 'source_report_ref', 'source_unit_page_refs'),
        types={'snapshot_id': str, 'source_unit_page_refs': list, 'stats': dict},
    ),
    'CorpusSourceUnitPage': ArtifactSchema(required=('snapshot_id', 'page_index', 'source_units'),
                                           nonempty=('snapshot_id', 'source_units'),
                                           types={'snapshot_id': str, 'source_units': list}),
    'ErrorArtifact': ArtifactSchema(required=('operation_run_id', 'error_type', 'message', 'traceback'),
                                    nonempty=('operation_run_id', 'error_type', 'message')),
    'IntentAnswer': ArtifactSchema(required=('query_intent_id', 'target_refs', 'answer'),
                                   nonempty=('query_intent_id',), types={'target_refs': list}),
    'IntentParse': ArtifactSchema(required=('message_id', 'message', 'checkpoint_id', 'raw_response'),
                                  nonempty=('message_id', 'message', 'checkpoint_id')),
    'IntentTrace': ArtifactSchema(required=('message_id', 'checkpoint_id', 'result_action', 'intents'),
                                  nonempty=('message_id', 'checkpoint_id', 'result_action', 'intents')),
    'ResearchRedirect': ArtifactSchema(required=('researcher_id', 'instructions'),
                                       nonempty=('researcher_id', 'instructions')),
    'Trace': ArtifactSchema(required=('trace_id', 'execution_tree'), nonempty=('trace_id',),
                            types={'execution_tree': dict}),
    'UserMessage': ArtifactSchema(required=('message_id', 'message'), nonempty=('message_id', 'message')),
}


for _schema_name in ('ChildPayload', 'ConcurrentPayload', 'PatchablePayload', 'SharedPayload'):
    SCHEMAS[_schema_name] = ArtifactSchema()


for _schema_name in (
    'BranchDecision', 'CodePatchCandidate', 'DiagnosticProbePlan', 'DiagnosticProbeResult',
    'FaultLocalizationReport', 'OpenCodeInstruction', 'OpenCodeRunTrace', 'OpenCodeWorkerReport',
    'PatchCorrectnessAssessment', 'PatchCritique', 'RepairBranchState', 'RepairDiagnosis', 'RepairEvaluation',
    'RepairEvidencePacket', 'RepairHypothesis', 'RepairLoopDecision', 'RepairLoopMemory', 'RepairLoopPlan',
    'RepairLoopState', 'RepairPlan', 'RepairStateTransition', 'VerifiedRepair',
):
    SCHEMAS[_schema_name] = ArtifactSchema(required=('id',), nonempty=('id',), types={'id': str})


def validate_artifact_payload(schema_name: str, payload: Any) -> None:
    schema = SCHEMAS.get(schema_name)
    if schema is None: raise ValueError(f'unregistered artifact schema: {schema_name}')
    if not isinstance(payload, dict): raise ValueError(f'{schema_name} payload must be object')
    missing = [key for key in schema.required if key not in payload or payload[key] is None]
    if missing: raise ValueError(f'{schema_name} missing required fields: {", ".join(missing)}')
    errors = [f'{key} must be non-empty' for key in schema.nonempty if key in payload and _blank(payload.get(key))]
    errors += [f'{key} must be {_type_name(expected)}' for key, expected in schema.types.items()
               if key in payload and not isinstance(payload[key], expected)]
    errors += schema.validate(payload) if schema.validate else []
    if errors: raise ValueError(f'{schema_name} schema invalid: {"; ".join(errors)}')


def _type_name(expected: type | tuple[type, ...]) -> str:
    return ' or '.join(item.__name__ for item in expected) if isinstance(expected, tuple) else expected.__name__
