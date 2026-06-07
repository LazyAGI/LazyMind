from __future__ import annotations

from ..checkpoints import CheckpointRef, CheckpointManager
from ..operations import OperationGraph, OperationSpec
from ..store import EvoStore
from .capabilities import CapabilityRegistry
from .models import IntentPlan, OperationProposal

FIXED_WRITES = {
    'CorpusLoadReport': 'corpus_load_report',
    'CorpusSnapshot': 'corpus_snapshot',
    'EvalDataset': 'eval_dataset',
    'ClassificationReport': 'classification_report',
    'ABTestComparison': 'abtest_comparison',
    'RepairLoopPlan': 'repair_loop_plan',
}


class IntentOperationFactory:
    def __init__(self, *, store: EvoStore, operation_graph: OperationGraph, capability_registry: CapabilityRegistry,
                 checkpoint_manager: CheckpointManager | None = None,):
        self.store = store
        self.operation_graph = operation_graph
        self.capability_registry = capability_registry
        self.checkpoint_manager = checkpoint_manager

    def create_operation(self, run_id: str, checkpoint_ref: CheckpointRef, plan: IntentPlan) -> OperationProposal:
        capability = self.capability_registry.validate(self.store, run_id, checkpoint_ref, plan)
        operation_type = capability.creates_operation_type
        if plan.params.get('operation_type') and plan.params['operation_type'] != operation_type:
            raise PermissionError('intent plan cannot override capability operation_type')
        writes_artifact_id = _writes_artifact_id(capability, plan)
        template = _operation_template(capability)
        ref = self.operation_graph.create_run(
            OperationSpec(
                operation_id=plan.operation_id,
                operation_type=operation_type,
                category=str(template.get('category') or 'intent'),
                flow_tag=str(template.get('flow_tag') or 'intent'),
                stage_tag=str(template.get('stage_tag') or capability.capability_id),
                depends_on=[str(item) for item in template.get('depends_on', [])],
                required_artifact_ids=[str(item) for item in template.get('required_artifact_ids', plan.required_artifact_ids)],
                tags={**_tags(capability.capability_id, writes_artifact_id), **dict(template.get('tags') or {})},
                params={**plan.params, 'capability_id': capability.capability_id},
            ),
            inputs=list(plan.input_refs),
            depends_on=list(plan.depends_on),
            parent=plan.parent,
            source_message_id=plan.source_message_id,
        )
        if capability.confirmation_policy == 'required':
            confirmation = self._create_confirmation_checkpoint(run_id, str(ref), capability.capability_id)
            return OperationProposal(ref, requires_confirmation=True, confirmation_checkpoint_id=confirmation.checkpoint_id)
        return OperationProposal(ref)

    def _create_confirmation_checkpoint(
        self,
        run_id: str,
        operation_run_id: str,
        capability_id: str,
    ) -> CheckpointRef:
        if self.checkpoint_manager is None:
            raise RuntimeError('confirmation policy requires CheckpointManager')
        return self.checkpoint_manager.create_checkpoint(
            run_id,
            None,
            f'confirm {operation_run_id}',
            allowed_capabilities=[capability_id],
        )


def _writes_artifact_id(capability, plan: IntentPlan) -> str:
    writable_schema = capability.writable_artifact_schema
    if not writable_schema or writable_schema in {'IntentAnswer', 'JudgeResult'}:
        return ''
    if plan.params.get('output_id'):
        return str(plan.params['output_id'])
    template = _operation_template(capability)
    if template.get('tags', {}).get('writes_artifact_id'):
        return str(template['tags']['writes_artifact_id'])
    if writable_schema == 'CasePreparation' and plan.params.get('output_case_id'):
        return f"case_preparation_{plan.params['output_case_id']}"
    if writable_schema == 'DatasetCase':
        case_id = _dataset_case_id(plan)
        if case_id:
            return case_id
    if writable_schema in FIXED_WRITES:
        return FIXED_WRITES[writable_schema]
    if plan.params.get('case_id'):
        return str(plan.params['case_id'])
    if plan.input_refs:
        return plan.input_refs[0].artifact_id
    return str(plan.params.get('case_id') or '')


def _dataset_case_id(plan: IntentPlan) -> str:
    if plan.params.get('case_id'):
        return str(plan.params['case_id'])
    ref = str(plan.params.get('case_preparation_ref') or '')
    artifact_id = ref.split('@', 1)[0]
    if artifact_id.startswith('case_preparation_'):
        return artifact_id.removeprefix('case_preparation_')
    return ''


def _tags(capability_id: str, writes_artifact_id: str) -> dict[str, str]:
    tags = {'capability_id': capability_id}
    if writes_artifact_id:
        tags['writes_artifact_id'] = writes_artifact_id
    return tags


def _operation_template(capability) -> dict:
    for example in capability.examples:
        template = example.get('operation_spec') if isinstance(example, dict) else None
        if isinstance(template, dict):
            return template
    return {}
