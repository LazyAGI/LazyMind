from __future__ import annotations

from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from types import MappingProxyType
from typing import TypeAlias

import networkx as nx

from .artifact import (
    ArtifactCommit,
    ArtifactKey,
    ArtifactRecord,
    ArtifactRef,
    ArtifactSnapshot,
    PartitionGuard,
    PartitionSet,
    merge_refs,
)
from .errors import DefinitionError, PlanningError
from .operation import BoundAggregate, BoundInput, Operation, OperationInvocation, OperationSpec
from .state import ArtifactRetryRequest


@dataclass(frozen=True)
class PlanReady:
    view: ArtifactSnapshot
    invocations: tuple[OperationInvocation, ...]

    def __post_init__(self) -> None:
        invocations = tuple(self.invocations)
        if not invocations:
            raise DefinitionError('ready plan requires at least one invocation')
        if not all(isinstance(invocation, OperationInvocation) for invocation in invocations):
            raise TypeError('ready plan must contain OperationInvocation values')
        object.__setattr__(self, 'invocations', invocations)


@dataclass(frozen=True)
class PlanAwaiting:
    view: ArtifactSnapshot
    artifact_keys: tuple[ArtifactKey, ...]

    def __post_init__(self) -> None:
        keys = tuple(self.artifact_keys)
        if not keys:
            raise DefinitionError('awaiting plan requires at least one artifact key')
        if not all(isinstance(key, ArtifactKey) for key in keys):
            raise TypeError('awaiting plan must contain ArtifactKey values')
        if len(set(keys)) != len(keys):
            raise DefinitionError('awaiting artifact keys must be unique')
        object.__setattr__(self, 'artifact_keys', keys)


@dataclass(frozen=True)
class PlanComplete:
    view: ArtifactSnapshot


PlanningResult: TypeAlias = PlanReady | PlanAwaiting | PlanComplete


@dataclass(frozen=True)
class RuntimeDefinition:
    operations: tuple[Operation, ...]
    artifact_modes: Mapping[str, str]
    partition_set_by_artifact: Mapping[str, str]
    producer_by_artifact: Mapping[str, Operation]
    terminal_artifact_ids: tuple[str, ...]

    def __post_init__(self) -> None:
        operations = tuple(self.operations)
        terminals = tuple(self.terminal_artifact_ids)
        if not operations:
            raise DefinitionError('runtime definition requires at least one operation')
        if not terminals:
            raise DefinitionError('runtime definition requires at least one terminal artifact')
        object.__setattr__(self, 'operations', operations)
        object.__setattr__(self, 'artifact_modes', MappingProxyType(dict(self.artifact_modes)))
        object.__setattr__(
            self,
            'partition_set_by_artifact',
            MappingProxyType(dict(self.partition_set_by_artifact)),
        )
        object.__setattr__(
            self,
            'producer_by_artifact',
            MappingProxyType(dict(self.producer_by_artifact)),
        )
        object.__setattr__(self, 'terminal_artifact_ids', terminals)

    @property
    def partition_set_ids(self) -> frozenset[str]:
        return frozenset(self.partition_set_by_artifact.values())

    def validate_commit(self, commit: ArtifactCommit) -> None:
        if not isinstance(commit, ArtifactCommit):
            raise TypeError('commit must be ArtifactCommit')

        partition_sets = {
            write.key: write.value
            for write in commit.writes
            if isinstance(write.value, PartitionSet)
        }
        guards = set(commit.partition_guards)
        for guard in guards:
            if guard.partition_set_key.artifact_id not in self.partition_set_ids:
                raise DefinitionError(
                    f'unknown partition set: {guard.partition_set_key.artifact_id}'
                )

        for write in commit.writes:
            artifact_id = write.key.artifact_id
            mode = self.artifact_modes.get(artifact_id)
            if mode is None:
                raise DefinitionError(f'unknown artifact: {artifact_id}')
            if (mode == 'partitioned') != bool(write.key.partition_key):
                raise DefinitionError(f'{artifact_id} requires a {mode} artifact key')

            is_partition_set = artifact_id in self.partition_set_ids
            if is_partition_set != isinstance(write.value, PartitionSet):
                expected = 'PartitionSet' if is_partition_set else 'ordinary artifact value'
                raise DefinitionError(f'{artifact_id} requires {expected}')
            if mode != 'partitioned':
                continue

            set_key = ArtifactKey.scalar(self.partition_set_by_artifact[artifact_id])
            current_commit_set = partition_sets.get(set_key)
            if current_commit_set is not None:
                if write.key.partition_key not in current_commit_set:
                    raise DefinitionError(
                        f'{write.key} is not present in the committed PartitionSet'
                    )
                continue
            guard = PartitionGuard(set_key, write.key.partition_key)
            if guard not in guards:
                raise DefinitionError(f'{write.key} requires partition membership protection')


def compile_operations(operations: Sequence[Operation]) -> RuntimeDefinition:
    declared = tuple(operations)
    if not declared:
        raise DefinitionError('at least one operation is required')

    by_id: dict[str, Operation] = {}
    artifact_modes: dict[str, str] = {}
    producer_by_artifact: dict[str, Operation] = {}
    partition_set_by_artifact: dict[str, str] = {}

    def declare_mode(artifact_id: str, mode: str) -> None:
        previous = artifact_modes.setdefault(artifact_id, mode)
        if previous != mode:
            raise DefinitionError(f'artifact {artifact_id} is used as both {previous} and {mode}')

    def assign_partitions(artifact_id: str, partition_set_id: str) -> None:
        previous = partition_set_by_artifact.setdefault(artifact_id, partition_set_id)
        if previous != partition_set_id:
            raise DefinitionError(
                f'partitioned artifact {artifact_id} uses both {previous} and {partition_set_id}'
            )

    for operation in declared:
        spec = getattr(operation, 'spec', None)
        if not callable(operation) or not isinstance(spec, OperationSpec):
            raise TypeError('operations must contain declared Operation callables')
        if spec.op_id in by_id:
            raise DefinitionError(f'duplicate operation id: {spec.op_id}')
        by_id[spec.op_id] = operation

        for binding in spec.inputs.values():
            mode = 'scalar' if binding.mode == 'one' else 'partitioned'
            declare_mode(binding.artifact_id, mode)
            if binding.mode in {'each', 'all'}:
                declare_mode(binding.partition_set_id, 'scalar')
                assign_partitions(binding.artifact_id, binding.partition_set_id)

        if spec.driver_input is not None:
            for binding in spec.inputs.values():
                if binding.mode == 'keyed':
                    assign_partitions(binding.artifact_id, spec.partition_set_id)

        for output in spec.outputs.values():
            declare_mode(output.artifact_id, output.mode)
            previous = producer_by_artifact.get(output.artifact_id)
            if previous is not None:
                raise DefinitionError(
                    f'artifact {output.artifact_id} has multiple writers: '
                    f'{previous.spec.op_id}, {spec.op_id}'
                )
            producer_by_artifact[output.artifact_id] = operation
            if output.mode == 'partitioned':
                partition_set_id = output.partition_set_id or spec.partition_set_id
                declare_mode(partition_set_id, 'scalar')
                assign_partitions(output.artifact_id, partition_set_id)

    graph = nx.DiGraph()
    graph.add_nodes_from(by_id)
    for operation in declared:
        dependencies = {binding.artifact_id for binding in operation.spec.inputs.values()}
        dependencies.update(
            binding.partition_set_id
            for binding in operation.spec.inputs.values()
            if binding.mode in {'each', 'all'}
        )
        for artifact_id in dependencies:
            producer = producer_by_artifact.get(artifact_id)
            if producer is not None:
                graph.add_edge(producer.spec.op_id, operation.spec.op_id)

    try:
        order = tuple(nx.lexicographical_topological_sort(graph, key=str))
    except nx.NetworkXUnfeasible as exc:
        edges = nx.find_cycle(graph)
        cycle = ' -> '.join((edges[0][0], *(target for _, target in edges)))
        raise DefinitionError(f'operation dependencies must be acyclic: {cycle}') from exc

    ordered = tuple(by_id[op_id] for op_id in order)
    consumed = {
        binding.artifact_id
        for operation in ordered
        for binding in operation.spec.inputs.values()
    }
    consumed.update(
        binding.partition_set_id
        for operation in ordered
        for binding in operation.spec.inputs.values()
        if binding.mode in {'each', 'all'}
    )
    structural_sets = frozenset(partition_set_by_artifact.values())
    terminal_set = set(producer_by_artifact) - consumed - structural_sets
    terminals = tuple(
        output.artifact_id
        for operation in ordered
        for output in operation.spec.outputs.values()
        if output.artifact_id in terminal_set
    )
    return RuntimeDefinition(
        ordered,
        artifact_modes,
        partition_set_by_artifact,
        producer_by_artifact,
        terminals,
    )


def plan_next(definition: RuntimeDefinition, artifacts: ArtifactSnapshot,
              retries: Iterable[ArtifactRetryRequest] = ()
              ) -> PlanningResult:
    if not isinstance(definition, RuntimeDefinition):
        raise TypeError('definition must be RuntimeDefinition')
    if not isinstance(artifacts, ArtifactSnapshot):
        raise TypeError('artifacts must be ArtifactSnapshot')
    pending = tuple(retries)
    if not all(isinstance(request, ArtifactRetryRequest) for request in pending):
        raise TypeError('retries must contain ArtifactRetryRequest values')
    if any(request.status != 'pending' for request in pending):
        raise DefinitionError('planner retries must be pending')

    effective = _operation_effective_records(definition, artifacts)
    partition_sets = _effective_partition_sets(artifacts, effective)
    view = ArtifactSnapshot(effective, partition_sets)
    planner = _DemandPlanner(definition, artifacts, view)

    satisfied = True
    if pending:
        retry_invocations: set[tuple[str, str]] = set()
        for request in pending:
            operation = definition.producer_by_artifact.get(request.artifact_key.artifact_id)
            if operation is None:
                raise PlanningError(f'retry target has no producer: {request.artifact_key}')
            identity = (
                operation.spec.op_id,
                request.artifact_key.partition_key if operation.spec.driver_input else '',
            )
            if identity in retry_invocations:
                raise PlanningError('one invocation cannot satisfy multiple retry requests')
            retry_invocations.add(identity)
            satisfied &= planner.require_retry(request)
    else:
        for artifact_id in definition.terminal_artifact_ids:
            satisfied &= planner.require_family(artifact_id)

    invocations = planner.ready_invocations()
    if invocations:
        return PlanReady(view, invocations)
    if planner.awaiting:
        return PlanAwaiting(view, tuple(sorted(planner.awaiting)))
    if satisfied:
        return PlanComplete(view)
    raise PlanningError('terminal artifact demand cannot be resolved')


def obsolete_retries(definition: RuntimeDefinition, artifacts: ArtifactSnapshot,
                     retries: Iterable[ArtifactRetryRequest]
                     ) -> tuple[ArtifactRetryRequest, ...]:
    requests = tuple(retries)
    view = plan_next(definition, artifacts).view
    return tuple(
        request
        for request in requests
        if (
            view.records.get(request.artifact_key) is None
            or view.records[request.artifact_key].ref != request.base_ref
        )
    )


class _DemandPlanner:
    def __init__(self, definition: RuntimeDefinition, artifacts: ArtifactSnapshot,
                 view: ArtifactSnapshot
                 ) -> None:
        self.definition = definition
        self.artifacts = artifacts
        self.view = view
        self.awaiting: set[ArtifactKey] = set()
        self._visited: set[tuple[str, str]] = set()
        self._ready: dict[tuple[str, str], OperationInvocation] = {}
        self._operation_order = {
            operation.spec.op_id: index
            for index, operation in enumerate(definition.operations)
        }

    def require_retry(self, request: ArtifactRetryRequest) -> bool:
        current = self.view.records.get(request.artifact_key)
        if current is None or current.ref != request.base_ref:
            raise PlanningError(
                f'retry base is no longer effective: {request.artifact_key}'
            )
        operation = self.definition.producer_by_artifact.get(request.artifact_key.artifact_id)
        if operation is None:
            raise PlanningError(f'retry target has no producer: {request.artifact_key}')
        partition_key = request.artifact_key.partition_key if operation.spec.driver_input else ''
        self._require_invocation(operation, partition_key, request.request_id)
        return False

    def require_family(self, artifact_id: str) -> bool:
        if self.definition.artifact_modes[artifact_id] == 'scalar':
            return self.require_key(ArtifactKey.scalar(artifact_id))

        set_key = ArtifactKey.scalar(self.definition.partition_set_by_artifact[artifact_id])
        if not self.require_key(set_key):
            return False
        partitions = self.view.partition_sets.get(set_key)
        if partitions is None:
            return False
        satisfied = True
        for partition_key in partitions.keys:
            satisfied &= self.require_key(ArtifactKey.partition(artifact_id, partition_key))
        return satisfied

    def require_key(self, key: ArtifactKey) -> bool:
        if key in self.view.records:
            return True
        operation = self.definition.producer_by_artifact.get(key.artifact_id)
        if operation is None:
            self.awaiting.add(key)
            return False
        partition_key = key.partition_key if operation.spec.driver_input else ''
        self._require_invocation(operation, partition_key)
        return False

    def _require_invocation(self, operation: Operation, partition_key: str,
                            retry_request_id: str = ''
                            ) -> None:
        identity = (operation.spec.op_id, partition_key)
        previous = self._ready.get(identity)
        if previous is not None:
            if retry_request_id and previous.retry_request_id != retry_request_id:
                raise PlanningError('one invocation cannot satisfy multiple retry requests')
            return
        if identity in self._visited:
            return
        self._visited.add(identity)

        if operation.spec.driver_input is not None:
            set_key = ArtifactKey.scalar(operation.spec.partition_set_id)
            if not self.require_key(set_key):
                return
            partitions = self.view.partition_sets.get(set_key)
            if partitions is None or partition_key not in partitions:
                return

        input_keys = _input_keys(
            operation,
            self.view.partition_sets,
            None if not partition_key else partition_key,
        )
        if input_keys is None:
            return
        for keys in input_keys.values():
            for key in keys:
                self.require_key(key)
        inputs = _bind_inputs(
            operation,
            self.view.records,
            self.view.partition_sets,
            None if not partition_key else partition_key,
        )
        if inputs is None:
            return
        self._ready[identity] = OperationInvocation(
            operation,
            inputs,
            _expected_heads(operation, partition_key, self.artifacts),
            partition_key,
            retry_request_id,
        )

    def ready_invocations(self) -> tuple[OperationInvocation, ...]:
        def order(invocation: OperationInvocation) -> tuple[int, int, str]:
            operation_index = self._operation_order[invocation.operation.spec.op_id]
            partition_index = 0
            if invocation.partition_key:
                set_key = ArtifactKey.scalar(invocation.operation.spec.partition_set_id)
                partitions = self.view.partition_sets[set_key]
                partition_index = partitions.keys.index(invocation.partition_key)
            return operation_index, partition_index, invocation.partition_key

        return tuple(sorted(self._ready.values(), key=order))


def _operation_effective_records(definition: RuntimeDefinition, artifacts: ArtifactSnapshot
                                 ) -> dict[ArtifactKey, ArtifactRecord]:
    effective = dict(artifacts.effective_records())
    changed = True
    while changed:
        changed = _remove_inactive_partitions(definition, artifacts, effective)
        partition_sets = _effective_partition_sets(artifacts, effective)
        for operation in definition.operations:
            if operation.spec.driver_input is None:
                changed |= _validate_batch_outputs(operation, effective, partition_sets)
            else:
                changed |= _validate_partitioned_outputs(operation, effective, partition_sets)
    return effective


def _remove_inactive_partitions(definition: RuntimeDefinition, artifacts: ArtifactSnapshot,
                                effective: dict[ArtifactKey, ArtifactRecord]
                                ) -> bool:
    changed = False
    partition_sets = _effective_partition_sets(artifacts, effective)
    for key in tuple(effective):
        if not key.partition_key:
            continue
        partition_set_id = definition.partition_set_by_artifact.get(key.artifact_id)
        if partition_set_id is None:
            continue
        partitions = partition_sets.get(ArtifactKey.scalar(partition_set_id))
        if partitions is None or key.partition_key not in partitions:
            del effective[key]
            changed = True
    return changed


def _validate_batch_outputs(operation: Operation, effective: dict[ArtifactKey, ArtifactRecord],
                            partition_sets: Mapping[ArtifactKey, PartitionSet]
                            ) -> bool:
    inputs = _bind_inputs(operation, effective, partition_sets, None)
    expected_inputs = None if inputs is None else _lineage_refs(inputs)
    changed = False
    for output in operation.spec.outputs.values():
        records = (
            ((ArtifactKey.scalar(output.artifact_id), effective.get(ArtifactKey.scalar(output.artifact_id))),)
            if output.mode == 'scalar'
            else tuple(
                (key, record)
                for key, record in effective.items()
                if key.artifact_id == output.artifact_id
            )
        )
        for key, record in records:
            if record is None or not record.producer.startswith('operation:'):
                continue
            if (
                expected_inputs is None
                or record.producer != f'operation:{operation.spec.op_id}'
                or record.input_refs != expected_inputs
            ):
                del effective[key]
                changed = True
    return changed


def _validate_partitioned_outputs(operation: Operation,
                                  effective: dict[ArtifactKey, ArtifactRecord],
                                  partition_sets: Mapping[ArtifactKey, PartitionSet]
                                  ) -> bool:
    partition_keys = _partition_keys(operation, partition_sets)
    active_keys = set(() if partition_keys is None else partition_keys)
    changed = False
    for output in operation.spec.outputs.values():
        for key, record in tuple(effective.items()):
            if (
                key.artifact_id == output.artifact_id
                and key.partition_key not in active_keys
                and record.producer == f'operation:{operation.spec.op_id}'
            ):
                del effective[key]
                changed = True

    if partition_keys is None:
        return changed
    for partition_key in partition_keys:
        inputs = _bind_inputs(operation, effective, partition_sets, partition_key)
        expected_inputs = None if inputs is None else _lineage_refs(inputs)
        for output in operation.spec.outputs.values():
            key = ArtifactKey.partition(output.artifact_id, partition_key)
            record = effective.get(key)
            if record is None or not record.producer.startswith('operation:'):
                continue
            if (
                expected_inputs is None
                or record.producer != f'operation:{operation.spec.op_id}'
                or record.input_refs != expected_inputs
            ):
                del effective[key]
                changed = True
    return changed


def _effective_partition_sets(artifacts: ArtifactSnapshot,
                              effective: Mapping[ArtifactKey, ArtifactRecord]
                              ) -> dict[ArtifactKey, PartitionSet]:
    return {
        key: partitions
        for key, partitions in artifacts.partition_sets.items()
        if key in effective
    }


def _expected_heads(operation: Operation, partition_key: str,
                    artifacts: ArtifactSnapshot
                    ) -> dict[ArtifactKey, ArtifactRef | None]:
    expected: dict[ArtifactKey, ArtifactRef | None] = {}
    for output in operation.spec.outputs.values():
        if output.mode == 'partitioned' and not partition_key:
            expected.update(
                (key, record.ref)
                for key, record in artifacts.records.items()
                if key.artifact_id == output.artifact_id
            )
            continue
        key = output.key_for(partition_key)
        record = artifacts.records.get(key)
        expected[key] = None if record is None else record.ref
    return expected


def _partition_keys(operation: Operation,
                    partition_sets: Mapping[ArtifactKey, PartitionSet]
                    ) -> tuple[str, ...] | None:
    if operation.spec.driver_input is None:
        return ()
    partitions = partition_sets.get(ArtifactKey.scalar(operation.spec.partition_set_id))
    return None if partitions is None else partitions.keys


def _bind_inputs(operation: Operation, effective: Mapping[ArtifactKey, ArtifactRecord],
                 partition_sets: Mapping[ArtifactKey, PartitionSet], partition_key: str | None
                 ) -> dict[str, BoundInput] | None:
    keys_by_input = _input_keys(operation, partition_sets, partition_key)
    if keys_by_input is None:
        return None
    inputs: dict[str, BoundInput] = {}
    for name, binding in operation.spec.inputs.items():
        keys = keys_by_input[name]
        records = tuple(effective.get(key) for key in keys)
        if any(record is None for record in records):
            return None
        if binding.mode == 'all':
            inputs[name] = BoundAggregate(records[0].ref, tuple(
                record.ref for record in records[1:]
            ))
        else:
            inputs[name] = records[0].ref
    return inputs


def _input_keys(operation: Operation,
                partition_sets: Mapping[ArtifactKey, PartitionSet],
                partition_key: str | None
                ) -> dict[str, tuple[ArtifactKey, ...]] | None:
    keys: dict[str, tuple[ArtifactKey, ...]] = {}
    for name, binding in operation.spec.inputs.items():
        if binding.mode == 'one':
            keys[name] = (ArtifactKey.scalar(binding.artifact_id),)
        elif binding.mode in {'each', 'keyed'}:
            if partition_key is None:
                return None
            keys[name] = (ArtifactKey.partition(binding.artifact_id, partition_key),)
        else:
            set_key = ArtifactKey.scalar(binding.partition_set_id)
            partitions = partition_sets.get(set_key)
            members = () if partitions is None else tuple(
                ArtifactKey.partition(binding.artifact_id, current)
                for current in partitions.keys
            )
            keys[name] = (set_key, *members)
    return keys


def _lineage_refs(inputs: Mapping[str, BoundInput]) -> tuple[ArtifactRef, ...]:
    refs: list[ArtifactRef] = []
    for value in inputs.values():
        if isinstance(value, ArtifactRef):
            refs.append(value)
        else:
            refs.append(value.partition_set_ref)
            refs.extend(value.member_refs)
    return merge_refs(refs)


__all__ = [
    'PlanAwaiting', 'PlanComplete', 'PlanReady', 'PlanningResult', 'RuntimeDefinition',
    'compile_operations', 'obsolete_retries', 'plan_next',
]
