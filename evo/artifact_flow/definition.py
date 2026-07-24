from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from types import MappingProxyType

from evo.artifact_runtime import ArtifactKey, Operation, OperationSpec


@dataclass(frozen=True)
class FlowStage:
    name: str
    result_key: ArtifactKey
    approval_key: ArtifactKey | None = None

    def __post_init__(self) -> None:
        if not isinstance(self.name, str) or not self.name.strip():
            raise ValueError('stage name must be non-empty')
        if not isinstance(self.result_key, ArtifactKey):
            raise TypeError('stage result_key must be ArtifactKey')
        if self.result_key.partition_key:
            raise ValueError('stage result_key must identify a scalar artifact')
        if self.approval_key is not None:
            if not isinstance(self.approval_key, ArtifactKey):
                raise TypeError('stage approval_key must be ArtifactKey or None')
            if self.approval_key.partition_key:
                raise ValueError('stage approval_key must identify a scalar artifact')
            if self.approval_key == self.result_key:
                raise ValueError('stage approval_key must differ from result_key')


@dataclass(frozen=True)
class FlowDefinition:
    operations: tuple[Operation, ...]
    stages: tuple[FlowStage, ...]
    _stage_by_artifact_id: Mapping[str, int] = field(
        init=False,
        repr=False,
        compare=False,
    )
    _stage_by_operation_id: Mapping[str, int] = field(
        init=False,
        repr=False,
        compare=False,
    )

    def __post_init__(self) -> None:
        operations = tuple(self.operations)
        stages = tuple(self.stages)
        if not operations:
            raise ValueError('flow definition requires at least one operation')
        if not stages:
            raise ValueError('flow definition requires at least one stage')
        if not all(
            callable(operation)
            and isinstance(getattr(operation, 'spec', None), OperationSpec)
            for operation in operations
        ):
            raise TypeError('flow operations must contain declared Operation callables')
        if not all(isinstance(stage, FlowStage) for stage in stages):
            raise TypeError('flow stages must contain FlowStage values')
        if len({stage.name for stage in stages}) != len(stages):
            raise ValueError('flow stage names must be unique')
        if len({stage.result_key for stage in stages}) != len(stages):
            raise ValueError('flow stage result keys must be unique')

        approvals = tuple(
            stage.approval_key
            for stage in stages
            if stage.approval_key is not None
        )
        if len(set(approvals)) != len(approvals):
            raise ValueError('flow stage approval keys must be unique')
        if any(stage.approval_key is None for stage in stages[:-1]):
            raise ValueError('every non-final stage requires an approval key')
        if stages[-1].approval_key is not None:
            raise ValueError('final stage must not require approval')

        scalar_input_ids = {
            binding.artifact_id
            for operation in operations
            for binding in operation.spec.inputs.values()
            if binding.mode == 'one'
        }
        output_modes = {
            output.artifact_id: output.mode
            for operation in operations
            for output in operation.spec.outputs.values()
        }
        for stage in stages:
            if output_modes.get(stage.result_key.artifact_id) != 'scalar':
                raise ValueError(
                    f'stage result must be a scalar operation output: {stage.result_key.artifact_id}'
                )
            if (
                stage.approval_key is not None
                and stage.approval_key.artifact_id not in scalar_input_ids
            ):
                raise ValueError(
                    f'stage approval must be a scalar operation input: '
                    f'{stage.approval_key.artifact_id}'
                )
            if (
                stage.approval_key is not None
                and stage.approval_key.artifact_id in output_modes
            ):
                raise ValueError(
                    f'stage approval must be supplied externally: '
                    f'{stage.approval_key.artifact_id}'
                )

        object.__setattr__(self, 'operations', operations)
        object.__setattr__(self, 'stages', stages)
        artifact_stages, operation_stages = _stage_ownership(operations, stages)
        unowned = sorted(
            operation.spec.op_id
            for operation in operations
            if operation.spec.op_id not in operation_stages
        )
        if unowned:
            raise ValueError(
                f'flow operations must contribute to a stage result: {", ".join(unowned)}'
            )

        object.__setattr__(self, '_stage_by_artifact_id', MappingProxyType(artifact_stages))
        object.__setattr__(self, '_stage_by_operation_id', MappingProxyType(operation_stages))

    def stage_index_for_artifact(self, artifact_id: str) -> int | None:
        return self._stage_by_artifact_id.get(artifact_id)

    def stage_index_for_operation(self, operation_id: str) -> int | None:
        return self._stage_by_operation_id.get(operation_id)


def _stage_ownership(operations: tuple[Operation, ...],
                     stages: tuple[FlowStage, ...]
                     ) -> tuple[dict[str, int], dict[str, int]]:
    producers = {
        output.artifact_id: operation
        for operation in operations
        for output in operation.spec.outputs.values()
    }
    result_producers: dict[str, int] = {}
    artifact_stages: dict[str, int] = {}
    operation_stages: dict[str, int] = {}
    for stage_index, stage in enumerate(stages):
        result_producer = producers[stage.result_key.artifact_id]
        previous_stage = result_producers.setdefault(
            result_producer.spec.op_id,
            stage_index,
        )
        if previous_stage != stage_index:
            raise ValueError(
                f'one operation cannot produce results for multiple stages: '
                f'{result_producer.spec.op_id}'
            )

        pending = [stage.result_key.artifact_id]
        visited: set[str] = set()
        while pending:
            artifact_id = pending.pop()
            if artifact_id in visited:
                continue
            visited.add(artifact_id)
            artifact_stages.setdefault(artifact_id, stage_index)
            producer = producers.get(artifact_id)
            if producer is None:
                continue
            operation_stages.setdefault(producer.spec.op_id, stage_index)
            for output in producer.spec.outputs.values():
                artifact_stages.setdefault(output.artifact_id, stage_index)
            for binding in producer.spec.inputs.values():
                pending.append(binding.artifact_id)
                if binding.partition_set_id:
                    pending.append(binding.partition_set_id)
    _validate_stage_results(stages, producers, operation_stages)
    _validate_approval_gates(operations, stages, producers, operation_stages)
    return artifact_stages, operation_stages


def _validate_stage_results(stages: tuple[FlowStage, ...],
                            producers: Mapping[str, Operation],
                            operation_stages: Mapping[str, int]
                            ) -> None:
    for stage_index, stage in enumerate(stages):
        producer = producers[stage.result_key.artifact_id]
        if operation_stages.get(producer.spec.op_id) != stage_index:
            raise ValueError(
                f'flow stage results must follow stage order: {stage.name}'
            )
        if (
            stage.approval_key is not None
            and _depends_on(producer, stage.approval_key.artifact_id, producers)
        ):
            raise ValueError(
                f'flow stage result cannot depend on its own approval: {stage.name}'
            )


def _validate_approval_gates(operations: tuple[Operation, ...],
                             stages: tuple[FlowStage, ...],
                             producers: Mapping[str, Operation],
                             operation_stages: Mapping[str, int]
                             ) -> None:
    for operation in operations:
        stage_index = operation_stages.get(operation.spec.op_id)
        if stage_index is None or stage_index == 0:
            continue
        approval_key = stages[stage_index - 1].approval_key
        if approval_key is None:
            raise ValueError('non-initial stage requires the previous approval key')
        if not _depends_on(operation, approval_key.artifact_id, producers):
            raise ValueError(
                f'flow operation must depend on the previous stage approval: '
                f'{operation.spec.op_id}'
            )


def _depends_on(operation: Operation, artifact_id: str,
                producers: Mapping[str, Operation]
                ) -> bool:
    pending = [
        dependency
        for binding in operation.spec.inputs.values()
        for dependency in (binding.artifact_id, binding.partition_set_id)
        if dependency
    ]
    visited: set[str] = set()
    while pending:
        dependency = pending.pop()
        if dependency == artifact_id:
            return True
        if dependency in visited:
            continue
        visited.add(dependency)
        producer = producers.get(dependency)
        if producer is None:
            continue
        for binding in producer.spec.inputs.values():
            pending.append(binding.artifact_id)
            if binding.partition_set_id:
                pending.append(binding.partition_set_id)
    return False


__all__ = ['FlowDefinition', 'FlowStage']
