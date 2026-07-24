from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import TypeAlias

import networkx as nx

from .artifact import ArtifactInput, ArtifactKey, ArtifactOutput, ArtifactRef
from .errors import CycleError, DAGGraphError, DuplicateArtifactWriterError, DuplicateOpError
from .ops import FixedOp
from .partition import ArtifactPartitionSpec, is_unpartitioned, partition_keys

InstanceId: TypeAlias = tuple[str, str]


@dataclass(frozen=True)
class NextOp:
    op_id: str
    base_op_id: str
    partition: str
    sort_key: tuple[int, int]
    materializer_id: str
    input_refs: Mapping[str, ArtifactRef | tuple[ArtifactRef, ...]]
    output_key_by_name: Mapping[str, ArtifactKey]


class DAGGraph:
    def __init__(self) -> None:
        self._ops: dict[str, type[FixedOp]] = {}
        self._compiled: nx.DiGraph | None = None

    def register(self, op_cls: type[FixedOp]) -> None:
        if not isinstance(op_cls, type) or not issubclass(op_cls, FixedOp):
            raise TypeError('op_cls must be a FixedOp subclass')
        op_id = str(getattr(op_cls, 'op_id', '') or '')
        _require_text(op_id, 'op_id')
        if op_id in self._ops:
            raise DuplicateOpError(f'duplicate op_id: {op_id}')
        _validate_op_metadata(op_cls)
        self._ops[op_id] = op_cls
        self._compiled = None

    def validate(self) -> None:
        self._compiled_graph()

    def next_ops(self, effective_artifacts: Mapping[ArtifactKey, ArtifactRef]) -> tuple[NextOp, ...]:
        effective = _validate_effective_artifacts(effective_artifacts)
        effective_keys = frozenset(effective)
        graph = self._compiled_graph()
        dirty_nodes = {
            node
            for node, data in graph.nodes(data=True)
            if not data['output_keys'] <= effective_keys
        }
        blocked_nodes = _blocked_by_dirty_required_inputs(graph, dirty_nodes)
        ordered_nodes = nx.lexicographical_topological_sort(graph, key=lambda node: graph.nodes[node]['sort_key'])
        return tuple(
            _next_op(instance_id, data, effective)
            for instance_id in ordered_nodes
            for data in (graph.nodes[instance_id],)
            if (
                instance_id in dirty_nodes
                and data['required_input_keys'] <= effective_keys
                and instance_id not in blocked_nodes
            )
        )

    def _compiled_graph(self) -> nx.DiGraph:
        if self._compiled is None:
            self._compiled = self._compile()
        return self._compiled

    def _compile(self) -> nx.DiGraph:
        graph = nx.DiGraph()
        writer_by_key: dict[ArtifactKey, InstanceId] = {}

        for op_order, (op_id, op_cls) in enumerate(self._ops.items()):
            for partition_order, (partition, outputs) in enumerate(_output_key_groups(op_cls).items()):
                instance_id = (op_id, partition)
                output_keys = frozenset(outputs.values())
                graph.add_node(
                    instance_id,
                    op_cls=op_cls,
                    materializer_id=op_id,
                    output_key_by_name=outputs,
                    output_keys=output_keys,
                    sort_key=(op_order, partition_order),
                )
                for key in output_keys:
                    if key in writer_by_key:
                        raise DuplicateArtifactWriterError(
                            f'artifact {key} has multiple writers: {writer_by_key[key]}, {instance_id}'
                        )
                    writer_by_key[key] = instance_id

        for instance_id, data in graph.nodes(data=True):
            op_cls = data['op_cls']
            input_keys_by_name = _input_key_groups(op_cls, data['output_key_by_name'])
            collection_input_names = frozenset(
                name
                for name, input_item in op_cls.inputs.items()
                if input_item.partition_mapping.kind == 'all_to_unpartitioned'
            )
            required_input_keys = frozenset(
                key
                for input_name, keys in input_keys_by_name.items()
                if op_cls.inputs[input_name].required
                for key in keys
            )
            data.update(
                input_keys_by_name=input_keys_by_name,
                collection_input_names=collection_input_names,
                required_input_keys=required_input_keys,
            )
            for input_name, keys in input_keys_by_name.items():
                for key in keys:
                    if key in writer_by_key:
                        _add_dependency_edge(graph, writer_by_key[key], instance_id, op_cls.inputs[input_name].required)

        if not nx.is_directed_acyclic_graph(graph):
            raise CycleError('operation graph must be a DAG')
        return graph


def _add_dependency_edge(graph: nx.DiGraph, source: InstanceId, target: InstanceId, required: bool) -> None:
    if graph.has_edge(source, target):
        graph[source][target]['required'] = graph[source][target]['required'] or required
    else:
        graph.add_edge(source, target, required=required)


def _blocked_by_dirty_required_inputs(graph: nx.DiGraph, dirty_nodes: set[InstanceId]) -> set[InstanceId]:
    required_graph = nx.DiGraph(
        (source, target)
        for source, target, data in graph.edges(data=True)
        if data['required']
    )
    required_graph.add_nodes_from(graph.nodes)
    blocked: set[InstanceId] = set()
    for node in dirty_nodes:
        blocked.update(nx.descendants(required_graph, node))
    return blocked


def _next_op(instance_id: InstanceId, data: Mapping[str, object],
             effective: Mapping[ArtifactKey, ArtifactRef]) -> NextOp:
    input_refs: dict[str, ArtifactRef | tuple[ArtifactRef, ...]] = {}
    for name, keys in data['input_keys_by_name'].items():
        refs = tuple(effective[key] for key in keys if key in effective)
        if len(refs) == len(keys):
            input_refs[name] = refs if name in data['collection_input_names'] else refs[0]
    return NextOp(
        op_id=_instance_op_id(instance_id),
        base_op_id=instance_id[0],
        partition=instance_id[1],
        sort_key=data['sort_key'],
        materializer_id=data['materializer_id'],
        input_refs=input_refs,
        output_key_by_name=data['output_key_by_name'],
    )


def _input_key_groups(op_cls: type[FixedOp], output_key_by_name: Mapping[str, ArtifactKey]) -> dict[str, tuple[ArtifactKey, ...]]:  # noqa: E501
    output_ref_key = next(iter(output_key_by_name.values()))
    output_spec = _output_spec_for_key(op_cls, output_ref_key)
    return {
        name: tuple(
            input_item.partition_mapping.upstream_keys(
                output_ref_key,
                ArtifactPartitionSpec(input_item.artifact_id, input_item.partition_spec),
                output_spec,
            )
        )
        for name, input_item in op_cls.inputs.items()
    }


def _output_key_groups(op_cls: type[FixedOp]) -> dict[str, Mapping[str, ArtifactKey]]:
    static_partitions = {
        output.partition_spec.keys
        for output in op_cls.outputs.values()
        if not is_unpartitioned(output.partition_spec)
    }
    if not static_partitions:
        return {'': {name: ArtifactKey.of(output.artifact_id) for name, output in op_cls.outputs.items()}}
    if len(static_partitions) != 1:
        raise DAGGraphError(f'{op_cls.op_id} outputs use incompatible partition specs')
    if any(is_unpartitioned(output.partition_spec) for output in op_cls.outputs.values()):
        raise DAGGraphError(f'{op_cls.op_id} cannot mix partitioned and unpartitioned outputs')
    partitions = next(iter(static_partitions))
    return {
        partition: {name: ArtifactKey(output.artifact_id, partition) for name, output in op_cls.outputs.items()}
        for partition in partitions
    }


def _output_spec_for_key(op_cls: type[FixedOp], key: ArtifactKey) -> ArtifactPartitionSpec:
    for output in op_cls.outputs.values():
        if output.artifact_id != key.artifact_id:
            continue
        if key.partition and key.partition not in partition_keys(output.partition_spec):
            raise DAGGraphError(f'output key {key} is not declared by {op_cls.op_id}')
        return ArtifactPartitionSpec(output.artifact_id, output.partition_spec)
    raise DAGGraphError(f'output key {key} is not declared by {op_cls.op_id}')


def _validate_op_metadata(op_cls: type[FixedOp]) -> None:
    if 'depends_on' in op_cls.__dict__:
        raise TypeError(f'{op_cls.__name__}.depends_on is not supported')
    if not isinstance(op_cls.inputs, Mapping):
        raise TypeError(f'{op_cls.__name__}.inputs must be a mapping')
    if not isinstance(op_cls.outputs, Mapping):
        raise TypeError(f'{op_cls.__name__}.outputs must be a mapping')
    if not op_cls.outputs:
        raise ValueError(f'{op_cls.__name__}.outputs must not be empty')
    for name, input_item in op_cls.inputs.items():
        if not isinstance(name, str):
            raise TypeError(f'{op_cls.__name__}.inputs keys must be str')
        _require_text(name, 'input name')
        if not isinstance(input_item, ArtifactInput):
            raise TypeError(f'{op_cls.__name__}.inputs[{name!r}] must be ArtifactInput')

    artifact_ids: set[str] = set()
    for name, output in op_cls.outputs.items():
        if not isinstance(name, str):
            raise TypeError(f'{op_cls.__name__}.outputs keys must be str')
        _require_text(name, 'output name')
        if not isinstance(output, ArtifactOutput):
            raise TypeError(f'{op_cls.__name__}.outputs[{name!r}] must be ArtifactOutput')
        if output.artifact_id in artifact_ids:
            raise DuplicateArtifactWriterError(f'{op_cls.__name__} declares duplicate output {output.artifact_id}')
        artifact_ids.add(output.artifact_id)


def _validate_effective_artifacts(values: Mapping[ArtifactKey, ArtifactRef]) -> Mapping[ArtifactKey, ArtifactRef]:
    for key, ref in values.items():
        if not isinstance(key, ArtifactKey):
            raise TypeError('effective artifact keys must be ArtifactKey values')
        if not isinstance(ref, ArtifactRef):
            raise TypeError('effective artifact values must be ArtifactRef values')
        if ref.key != key:
            raise DAGGraphError(f'effective artifact ref {ref} does not match key {key}')
    return values


def _instance_op_id(instance_id: InstanceId) -> str:
    op_id, partition = instance_id
    return op_id if not partition else f'{op_id}[{partition}]'


def _require_text(value: str, name: str) -> None:
    if not value or not value.strip():
        raise ValueError(f'{name} must be non-empty')
