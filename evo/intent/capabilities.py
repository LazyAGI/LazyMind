from __future__ import annotations

from ..checkpoints import CheckpointRef
from ..store import EvoStore
from .models import CapabilitySpec, IntentPlan


class CapabilityRegistry:
    def __init__(self, specs: list[CapabilitySpec]):
        self._specs = {spec.capability_id: spec for spec in specs}

    def get(self, capability_id: str) -> CapabilitySpec:
        try:
            return self._specs[capability_id]
        except KeyError as exc:
            raise ValueError(f'unknown capability: {capability_id}') from exc

    def capability_ids(self) -> list[str]:
        return sorted(self._specs)

    def allowed_for_checkpoint(
        self,
        store: EvoStore,
        run_id: str,
        checkpoint_ref: CheckpointRef,
    ) -> list[CapabilitySpec]:
        data = store.read_json(store.run_dir(run_id) / 'checkpoints' / f'{checkpoint_ref.checkpoint_id}.json')
        return [self.get(capability_id) for capability_id in data.get('allowed_capabilities', [])]

    def planning_context(self, store: EvoStore, run_id: str, checkpoint_ref: CheckpointRef) -> list[dict]:
        return [
            _planning_view(spec, include_system_contract=False)
            for spec in self.allowed_for_checkpoint(store, run_id, checkpoint_ref)
        ]

    def execution_context(self, store: EvoStore, run_id: str, checkpoint_ref: CheckpointRef) -> list[dict]:
        return [
            _planning_view(spec, include_system_contract=True)
            for spec in self.allowed_for_checkpoint(store, run_id, checkpoint_ref)
        ]

    def validate(self, store: EvoStore, run_id: str, checkpoint_ref: CheckpointRef, plan: IntentPlan) -> CapabilitySpec:
        allowed_ids = {spec.capability_id for spec in self.allowed_for_checkpoint(store, run_id, checkpoint_ref)}
        if plan.capability_id not in allowed_ids:
            raise PermissionError(f'capability not allowed at checkpoint {checkpoint_ref}: {plan.capability_id}')
        spec = self.get(plan.capability_id)
        _validate_schema_permissions(store, run_id, spec, plan)
        return spec


def _validate_schema_permissions(store: EvoStore, run_id: str, spec: CapabilitySpec, plan: IntentPlan) -> None:
    if not spec.target_artifact_schemas:
        return
    artifact_graph = store.artifact_graph(run_id)
    for ref in plan.input_refs:
        schema_name = artifact_graph.schema_name(ref)
        if schema_name not in spec.target_artifact_schemas:
            raise PermissionError(f'capability {spec.capability_id} cannot target schema: {schema_name}')
    for artifact_id in plan.required_artifact_ids:
        schema_name = artifact_graph.schema_name(artifact_graph.latest_ref(artifact_id))
        if schema_name not in spec.target_artifact_schemas:
            raise PermissionError(f'capability {spec.capability_id} cannot target schema: {schema_name}')


def _planning_view(spec: CapabilitySpec, *, include_system_contract: bool) -> dict:
    view = {
        'capability_id': spec.capability_id,
        'creates_operation_type': spec.creates_operation_type,
        'title': spec.title,
        'description': spec.description,
        'use_when': list(spec.use_when),
        'avoid_when': list(spec.avoid_when),
        'intent_use_when': list(spec.use_when),
        'intent_avoid_when': list(spec.avoid_when),
        'task_type': spec.task_type,
        'semantic_schema': dict(spec.semantic_schema),
        'effects': list(spec.effects),
        'target_artifact_schemas': list(spec.target_artifact_schemas),
        'writable_artifact_schema': spec.writable_artifact_schema,
        'params_schema': dict(spec.params_schema),
        'examples': list(spec.examples),
        'risk_level': spec.risk_level,
        'confirmation_policy': spec.confirmation_policy,
        'batch_policy': spec.batch_policy,
        'cross_stage_policy': spec.cross_stage_policy,
    }
    if include_system_contract:
        view['system_param_contract'] = dict(spec.system_param_contract)
    return view
