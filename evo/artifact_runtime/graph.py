from __future__ import annotations

import hashlib
from collections.abc import Iterable, Mapping
from types import MappingProxyType

import networkx as nx

from .artifact import ArtifactInput, ArtifactKey, ArtifactOutput, ArtifactRef, ArtifactVersionResolver
from .errors import (
    CycleError,
    DAGGraphError,
    DuplicateArtifactWriterError,
    DuplicateOpError,
    MissingArtifactVersionError,
    UnknownTargetError,
)
from .ops import FixedOp
from .partition import ArtifactPartitionSpec, is_unpartitioned, partition_keys
from .plan import ExecutionPlan, MaterializerNode, PlanInput, PlanOp
from .utils import unique_ordered, validate_nonempty


class DAGGraph:
    """Declarative artifact-centric DAG graph.

    The graph owns artifact lineage and plan compilation. It does not own
    artifact versions; plan compilation binds versions through a resolver.
    """

    def __init__(self) -> None:
        self._nodes: dict[str, MaterializerNode] = {}
        self._order: list[str] = []
        self._graph_revision = 0

    def register(self, op_cls: type[FixedOp]) -> None:
        if not isinstance(op_cls, type) or not issubclass(op_cls, FixedOp):
            raise TypeError('op_cls must be a FixedOp subclass')
        op_id = str(getattr(op_cls, 'op_id', '') or '')
        validate_nonempty(op_id, 'op_id')
        if op_id in self._nodes:
            raise DuplicateOpError(f'duplicate op_id: {op_id}')
        self._validate_op_metadata(op_cls)
        self._nodes[op_id] = MaterializerNode(
            op_id=op_id,
            op_cls=op_cls,
            inputs=dict(op_cls.inputs),
            outputs=dict(op_cls.outputs),
            producer_dependencies=(),
            flow=str(op_cls.flow or ''),
            stage=str(op_cls.stage or ''),
            tags=MappingProxyType(dict(op_cls.tags or {})),
        )
        self._order.append(op_id)
        self._graph_revision += 1

    def validate(self) -> None:
        self._instance_graph(self._materialize_nodes())

    def artifact_keys(self) -> set[ArtifactKey]:
        return {
            key
            for output_key_by_name in self._all_declared_output_key_maps()
            for key in output_key_by_name.values()
        }

    def artifacts(self) -> set[ArtifactKey]:
        return self.artifact_keys()

    def artifact_ids(self) -> set[str]:
        nodes = self._materialize_nodes()
        return {
            output.artifact_id
            for node in nodes.values()
            for output in node.outputs.values()
        }

    def declares_artifact_key(self, key: ArtifactKey) -> bool:
        nodes = self._materialize_nodes()
        return any(
            item.artifact_id == key.artifact_id and _spec_declares_key(item.partition_spec, key)
            for node in nodes.values()
            for item in (*node.inputs.values(), *node.outputs.values())
        )

    def materializer_for_plan_op(self, plan_op: PlanOp) -> type[FixedOp]:
        if plan_op.graph_revision != self._graph_revision:
            raise DAGGraphError(
                f'plan graph revision {plan_op.graph_revision} does not match graph revision {self._graph_revision}'
            )
        nodes = self._materialize_nodes()
        op_id = _base_op_id(plan_op.op_id)
        self._require_materializer(op_id, nodes)
        return nodes[op_id].op_cls

    def root_artifacts(self) -> set[ArtifactKey]:
        instance_graph, _, outputs_by_instance = self._instance_graph(self._materialize_nodes())
        return _output_keys_for_instances(
            (node for node in instance_graph.nodes if instance_graph.in_degree(node) == 0),
            outputs_by_instance,
        )

    def sink_artifacts(self) -> set[ArtifactKey]:
        instance_graph, _, outputs_by_instance = self._instance_graph(self._materialize_nodes())
        return _output_keys_for_instances(
            (node for node in instance_graph.nodes if instance_graph.out_degree(node) == 0),
            outputs_by_instance,
        )

    def consumer_artifacts_of(self, key: ArtifactKey) -> set[ArtifactKey]:
        instance_graph, _, outputs_by_instance = self._instance_graph(self._materialize_nodes())
        consumers = {
            instance_id
            for instance_id in instance_graph.nodes
            if key in instance_graph.nodes[instance_id].get('input_keys', ())
        }
        return _output_keys_for_instances(consumers, outputs_by_instance)

    def affected_artifacts_of(self, key: ArtifactKey) -> set[ArtifactKey]:
        return self.affected_keys_of(key)

    def affected_keys_of(self, key: ArtifactKey) -> set[ArtifactKey]:
        instance_graph, writer_by_key, outputs_by_instance = self._instance_graph(self._materialize_nodes())
        producer = writer_by_key.get(key)
        if producer is not None:
            affected_instances = nx.descendants(instance_graph, producer)
        else:
            affected_instances = {
                instance_id
                for instance_id in instance_graph.nodes
                if key in instance_graph.nodes[instance_id].get('input_keys', ())
            }
            for instance_id in tuple(affected_instances):
                affected_instances.update(nx.descendants(instance_graph, instance_id))
        return {
            output_key
            for instance_id in affected_instances
            for output_key in outputs_by_instance[instance_id].values()
        }

    def upstream_artifacts_of(self, key: ArtifactKey) -> set[ArtifactKey]:
        instance_graph, writer_by_key, _ = self._instance_graph(self._materialize_nodes())
        producer = writer_by_key.get(key)
        if producer is None:
            raise UnknownTargetError(f'no producer for artifact key: {key}')
        return {
            input_key
            for instance_id in (*nx.ancestors(instance_graph, producer), producer)
            for input_key in instance_graph.nodes[instance_id].get('input_keys', ())
        }

    def downstream_artifacts_of(self, key: ArtifactKey) -> set[ArtifactKey]:
        return self.affected_artifacts_of(key)

    def select_artifact_keys(
        self,
        *,
        flow: str | None = None,
        stage: str | None = None,
        tags: dict[str, str] | None = None,
    ) -> set[ArtifactKey]:
        return {
            key
            for output_key_by_name in self._selected_output_key_maps(flow=flow, stage=stage, tags=tags)
            for key in output_key_by_name.values()
        }

    def select_artifacts(
        self,
        *,
        flow: str | None = None,
        stage: str | None = None,
        tags: dict[str, str] | None = None,
    ) -> set[ArtifactKey]:
        return self.select_artifact_keys(flow=flow, stage=stage, tags=tags)

    def select_artifact_ids(
        self,
        *,
        flow: str | None = None,
        stage: str | None = None,
        tags: dict[str, str] | None = None,
    ) -> set[str]:
        nodes = self._materialize_nodes()
        return {
            output.artifact_id
            for node in nodes.values()
            for output in node.outputs.values()
            if _matches_selection(node, flow=flow, stage=stage, tags=tags or {})
        }

    def _all_declared_output_key_maps(self) -> tuple[Mapping[str, ArtifactKey], ...]:
        return tuple(
            output_key_by_name
            for node in self._materialize_nodes().values()
            for output_key_by_name in _output_key_groups(node).values()
        )

    def _selected_output_key_maps(
        self,
        *,
        flow: str | None = None,
        stage: str | None = None,
        tags: dict[str, str] | None = None,
    ) -> tuple[Mapping[str, ArtifactKey], ...]:
        return tuple(
            output_key_by_name
            for node in self._materialize_nodes().values()
            if _matches_selection(node, flow=flow, stage=stage, tags=tags or {})
            for output_key_by_name in _output_key_groups(node).values()
        )

    def artifact_topological_sort(self) -> list[ArtifactKey]:
        instance_graph, _, outputs_by_instance = self._instance_graph(self._materialize_nodes())
        order_index = {op_id: index for index, op_id in enumerate(self._order)}
        return [
            key
            for generation in nx.topological_generations(instance_graph)
            for instance_id in sorted(
                generation,
                key=lambda item: _instance_sort_key(item, order_index),
            )
            for key in outputs_by_instance[instance_id].values()
        ]

    def artifact_execution_layers(self) -> list[list[ArtifactKey]]:
        instance_graph, _, outputs_by_instance = self._instance_graph(self._materialize_nodes())
        position = {op_id: index for index, op_id in enumerate(self._order)}
        return [
            [
                key
                for instance_id in sorted(layer, key=lambda item: _instance_sort_key(item, position))
                for key in outputs_by_instance[instance_id].values()
            ]
            for layer in nx.topological_generations(instance_graph)
        ]

    def build_plan_for_keys(
        self,
        resolver: ArtifactVersionResolver,
        targets: set[ArtifactKey],
    ) -> ExecutionPlan:
        if not targets:
            raise UnknownTargetError('target keys must not be empty')
        nodes = self._materialize_nodes()
        instance_graph, writer_by_key, outputs_by_instance = self._instance_graph(nodes)
        unknown = sorted(target for target in targets if target not in writer_by_key)
        if unknown:
            raise UnknownTargetError(f"no producer for artifact key: {', '.join(str(item) for item in unknown)}")
        selected: set[str] = set()
        for target in targets:
            producer = writer_by_key[target]
            selected.add(producer)
            selected.update(nx.ancestors(instance_graph, producer))
        return self._build_plan_for_selected_instances(
            nodes,
            instance_graph,
            outputs_by_instance,
            selected,
            resolver,
        )

    def build_plan_for_selected_artifacts(
        self,
        resolver: ArtifactVersionResolver,
        *,
        flow: str | None = None,
        stage: str | None = None,
        tags: dict[str, str] | None = None,
    ) -> ExecutionPlan:
        keys = self.select_artifacts(flow=flow, stage=stage, tags=tags)
        if not keys:
            raise UnknownTargetError('selection matched no artifacts')
        nodes = self._materialize_nodes()
        instance_graph, writer_by_key, outputs_by_instance = self._instance_graph(nodes)
        selected = {writer_by_key[key] for key in keys if key in writer_by_key}
        if not selected:
            raise UnknownTargetError('selection matched no materializers')
        return self._build_plan_for_selected_instances(
            nodes,
            instance_graph,
            outputs_by_instance,
            selected,
            resolver,
        )

    def build_recompute_plan(
        self,
        resolver: ArtifactVersionResolver,
        *,
        materialize_keys: set[ArtifactKey] | None = None,
        changed_keys: set[ArtifactKey] | None = None,
        include_downstream: bool = True,
    ) -> ExecutionPlan:
        return self.build_recompute_plan_for_keys(
            resolver,
            materialize_keys=materialize_keys or set(),
            changed_keys=changed_keys or set(),
            include_downstream=include_downstream,
        )

    def build_recompute_plan_for_keys(
        self,
        resolver: ArtifactVersionResolver,
        *,
        materialize_keys: set[ArtifactKey] | None = None,
        changed_keys: set[ArtifactKey] | None = None,
        include_downstream: bool = True,
    ) -> ExecutionPlan:
        materialize_keys = materialize_keys or set()
        changed_keys = changed_keys or set()
        nodes = self._materialize_nodes()
        instance_graph, writer_by_key, outputs_by_instance = self._instance_graph(nodes)
        selected = {writer_by_key[key] for key in materialize_keys if key in writer_by_key}
        unknown_materialize = sorted(key for key in materialize_keys if key not in writer_by_key)
        if unknown_materialize:
            raise UnknownTargetError(
                f"no producer for artifact key: {', '.join(str(item) for item in unknown_materialize)}")
        for key in changed_keys:
            direct = {
                instance_id
                for instance_id in instance_graph.nodes
                if key in instance_graph.nodes[instance_id].get('input_keys', ())
            }
            selected.update(direct)
        if include_downstream:
            for instance_id in tuple(selected):
                selected.update(nx.descendants(instance_graph, instance_id))
        if not selected:
            raise UnknownTargetError('recompute selection matched no artifacts')
        return self._build_plan_for_selected_instances(
            nodes,
            instance_graph,
            outputs_by_instance,
            selected,
            resolver,
        )

    def _build_plan_for_selected_instances(
        self,
        nodes: dict[str, MaterializerNode],
        instance_graph: nx.DiGraph,
        outputs_by_instance: dict[str, Mapping[str, ArtifactKey]],
        selected: set[str],
        resolver: ArtifactVersionResolver,
    ) -> ExecutionPlan:
        writer_by_key = {
            key: instance_id
            for instance_id, output_key_by_name in outputs_by_instance.items()
            for key in output_key_by_name.values()
        }
        position = {op_id: index for index, op_id in enumerate(self._order)}
        plan_layers = tuple(
            tuple(
                self._plan_op_for_instance(
                    nodes[_base_op_id(instance_id)],
                    instance_id,
                    instance_graph,
                    outputs_by_instance[instance_id],
                    selected,
                    resolver,
                    writer_by_key,
                )
                for instance_id in sorted(layer, key=lambda item: _instance_sort_key(item, position))
            )
            for layer in self._layers_for_instances(instance_graph, selected, position)
        )
        return ExecutionPlan(
            plan_id=self._plan_id(self._graph_revision, plan_layers),
            graph_revision=self._graph_revision,
            layers=plan_layers,
        )

    def _plan_op_for_instance(
        self,
        node: MaterializerNode,
        instance_id: str,
        instance_graph: nx.DiGraph,
        output_key_by_name: Mapping[str, ArtifactKey],
        selected: set[str],
        resolver: ArtifactVersionResolver,
        writer_by_key: dict[ArtifactKey, str],
    ) -> PlanOp:
        input_bindings: list[PlanInput] = []
        output_keys = tuple(output_key_by_name.values())
        output_ref_key = output_keys[0]
        output_spec = _output_spec_for_key(node, output_ref_key)
        for input_name, input_item in node.inputs.items():
            input_keys = tuple(
                input_item.partition_mapping.upstream_keys(
                    output_ref_key,
                    ArtifactPartitionSpec(input_item.artifact_id, input_item.partition_spec),
                    output_spec,
                )
            )
            input_kind = (
                'partition_collection'
                if input_item.partition_mapping.kind == 'all_to_unpartitioned'
                else 'single'
            )
            collection_name = input_name if input_kind == 'partition_collection' else ''
            for input_key in input_keys:
                if input_item.version is not None:
                    if input_kind != 'single':
                        raise DAGGraphError('pinned partition collection inputs are not supported')
                    input_bindings.append(
                        PlanInput(
                            name=input_name,
                            key=input_key,
                            version=ArtifactRef(input_key, input_item.version),
                            required=input_item.required,
                        )
                    )
                    continue
                writer = writer_by_key.get(input_key)
                if writer is not None and writer in selected and writer != instance_id:
                    input_bindings.append(
                        PlanInput(
                            name=input_name,
                            key=input_key,
                            planned=True,
                            required=input_item.required,
                            input_kind=input_kind,
                            collection_name=collection_name,
                        )
                    )
                    continue
                try:
                    ref = resolver.latest(input_key)
                except LookupError:
                    if input_item.required:
                        raise MissingArtifactVersionError(f'missing latest version for required artifact: {input_key}')
                    continue
                if ref.key != input_key:
                    raise DAGGraphError(f'resolver returned {ref} for artifact {input_key}')
                input_bindings.append(
                    PlanInput(
                        name=input_name,
                        key=input_key,
                        version=ref,
                        required=input_item.required,
                        input_kind=input_kind,
                        collection_name=collection_name,
                    )
                )
        return PlanOp(
            op_id=instance_id,
            input_bindings=tuple(input_bindings),
            output_key_by_name=output_key_by_name,
            depends_on=tuple(
                sorted(
                    (dep for dep in instance_graph.predecessors(instance_id) if dep in selected),
                    key=lambda item: _instance_sort_key(
                        item, {op_id: index for index, op_id in enumerate(self._order)}),
                )
            ),
            graph_revision=self._graph_revision,
            flow=node.flow,
            stage=node.stage,
            tags=MappingProxyType(dict(node.tags)),
        )

    def _instance_graph(
        self,
        nodes: dict[str, MaterializerNode],
    ) -> tuple[nx.DiGraph, dict[ArtifactKey, str], dict[str, Mapping[str, ArtifactKey]]]:
        graph = nx.DiGraph()
        outputs_by_instance: dict[str, Mapping[str, ArtifactKey]] = {}
        writer_by_key: dict[ArtifactKey, str] = {}

        for op_id in self._order:
            node = nodes[op_id]
            for partition, output_key_by_name in _output_key_groups(node).items():
                instance_id = _instance_id(op_id, partition)
                graph.add_node(instance_id)
                outputs_by_instance[instance_id] = output_key_by_name
                for key in output_key_by_name.values():
                    existing = writer_by_key.get(key)
                    if existing is not None and existing != instance_id:
                        raise DuplicateArtifactWriterError(
                            f'artifact {key} has multiple writers: {existing}, {instance_id}')
                    writer_by_key[key] = instance_id

        for instance_id, output_key_by_name in outputs_by_instance.items():
            node = nodes[_base_op_id(instance_id)]
            output_ref_key = next(iter(output_key_by_name.values()))
            output_spec = _output_spec_for_key(node, output_ref_key)
            input_keys: list[ArtifactKey] = []
            for input_item in node.inputs.values():
                if input_item.version is not None:
                    continue
                for input_key in input_item.partition_mapping.upstream_keys(
                    output_ref_key,
                    ArtifactPartitionSpec(input_item.artifact_id, input_item.partition_spec),
                    output_spec,
                ):
                    input_keys.append(input_key)
                    writer = writer_by_key.get(input_key)
                    if writer is not None and writer != instance_id:
                        graph.add_edge(writer, instance_id)
            graph.nodes[instance_id]['input_keys'] = tuple(input_keys)

        if not nx.is_directed_acyclic_graph(graph):
            raise CycleError('operation graph must be a DAG')
        return graph, writer_by_key, outputs_by_instance

    @staticmethod
    def _layers_for_instances(
        instance_graph: nx.DiGraph,
        selected: set[str],
        position: dict[str, int],
    ) -> list[list[str]]:
        graph = instance_graph.subgraph(selected).copy()
        if not nx.is_directed_acyclic_graph(graph):
            raise CycleError('operation graph must be a DAG')
        return [sorted(layer, key=lambda item: _instance_sort_key(item, position))
                for layer in nx.topological_generations(graph)]

    def _materialize_nodes(self) -> dict[str, MaterializerNode]:
        writer_by_artifact = self._writer_by_artifact_from_registered_nodes()
        nodes: dict[str, MaterializerNode] = {}
        for op_id in self._order:
            registered = self._nodes[op_id]
            inputs = dict(registered.inputs)
            outputs = dict(registered.outputs)
            producer_dependencies = []
            for input_item in inputs.values():
                if input_item.version is not None:
                    continue
                writer = writer_by_artifact.get(input_item.artifact_id)
                if writer and writer != op_id:
                    producer_dependencies.append(writer)
            nodes[op_id] = MaterializerNode(
                op_id=op_id,
                op_cls=registered.op_cls,
                inputs=inputs,
                outputs=outputs,
                producer_dependencies=unique_ordered(producer_dependencies),
                flow=registered.flow,
                stage=registered.stage,
                tags=registered.tags,
            )
        return nodes

    def _writer_by_artifact_from_registered_nodes(self) -> dict[str, str]:
        writer_by_artifact: dict[str, str] = {}
        for op_id in self._order:
            for output in self._nodes[op_id].outputs.values():
                existing = writer_by_artifact.get(output.artifact_id)
                if existing and existing != op_id:
                    raise DuplicateArtifactWriterError(
                        f'artifact {output.artifact_id} has multiple writers: {existing}, {op_id}'
                    )
                writer_by_artifact[output.artifact_id] = op_id
        return writer_by_artifact

    @staticmethod
    def _require_materializer(op_id: str, nodes: dict[str, MaterializerNode]) -> None:
        if op_id not in nodes:
            raise UnknownTargetError(f'unknown materializer: {op_id}')

    @staticmethod
    def _validate_op_metadata(op_cls: type[FixedOp]) -> None:
        if 'depends_on' in op_cls.__dict__:
            raise TypeError(
                f'{op_cls.__name__}.depends_on is not supported; declare artifact dependencies as inputs'
            )
        if not isinstance(op_cls.inputs, Mapping):
            raise TypeError(f'{op_cls.__name__}.inputs must be a mapping')
        if not isinstance(op_cls.outputs, Mapping):
            raise TypeError(f'{op_cls.__name__}.outputs must be a mapping')
        if not isinstance(op_cls.flow, str):
            raise TypeError(f'{op_cls.__name__}.flow must be a str')
        if not isinstance(op_cls.stage, str):
            raise TypeError(f'{op_cls.__name__}.stage must be a str')
        if not isinstance(op_cls.tags, Mapping):
            raise TypeError(f'{op_cls.__name__}.tags must be a mapping')
        for key, value in op_cls.tags.items():
            validate_nonempty(str(key), 'tag key')
            if not isinstance(value, str):
                raise TypeError(f'{op_cls.__name__}.tags[{key!r}] must be str')
        for name, input_item in op_cls.inputs.items():
            validate_nonempty(str(name), 'input name')
            if not isinstance(input_item, ArtifactInput):
                raise TypeError(f'{op_cls.__name__}.inputs[{name!r}] must be ArtifactInput')
        declared_output_ids: set[str] = set()
        for name, output in op_cls.outputs.items():
            validate_nonempty(str(name), 'output name')
            if not isinstance(output, ArtifactOutput):
                raise TypeError(f'{op_cls.__name__}.outputs[{name!r}] must be ArtifactOutput')
            if output.artifact_id in declared_output_ids:
                raise DuplicateArtifactWriterError(
                    f'{op_cls.__name__} declares artifact {output.artifact_id} more than once'
                )
            declared_output_ids.add(output.artifact_id)

    @staticmethod
    def _plan_id(graph_revision: int, layers: tuple[tuple[PlanOp, ...], ...]) -> str:
        digest = hashlib.sha256()
        digest.update(str(graph_revision).encode('utf-8'))
        for layer in layers:
            digest.update(b'|')
            for plan_op in layer:
                digest.update(plan_op.op_id.encode('utf-8'))
                for dep in plan_op.depends_on:
                    digest.update(f'dep:{dep};'.encode('utf-8'))
                for key in plan_op.planned_input_keys:
                    digest.update(f'planned:{key};'.encode('utf-8'))
                for key, ref in sorted(plan_op.input_key_versions.items()):
                    digest.update(f'{key}:{ref};'.encode('utf-8'))
                for key in plan_op.output_keys:
                    digest.update(f'out:{key};'.encode('utf-8'))
        return f'plan_{digest.hexdigest()[:16]}'


def _matches_selection(
    node: MaterializerNode,
    *,
    flow: str | None,
    stage: str | None,
    tags: dict[str, str],
) -> bool:
    if flow is not None and node.flow != flow:
        return False
    if stage is not None and node.stage != stage:
        return False
    node_tags = node.tags or {}
    return all(node_tags.get(key) == value for key, value in tags.items())


def _output_keys_for_instances(
    instance_ids: Iterable[str],
    outputs_by_instance: Mapping[str, Mapping[str, ArtifactKey]],
) -> set[ArtifactKey]:
    return {
        key
        for instance_id in instance_ids
        for key in outputs_by_instance[instance_id].values()
    }


def _output_key_groups(node: MaterializerNode) -> dict[str, Mapping[str, ArtifactKey]]:
    static_partitions = {
        output.partition_spec.keys
        for output in node.outputs.values()
        if not is_unpartitioned(output.partition_spec)
    }
    if not static_partitions:
        return {'': {name: ArtifactKey.of(output.artifact_id) for name, output in node.outputs.items()}}
    if len(static_partitions) != 1:
        raise DAGGraphError(f'{node.op_id} outputs use incompatible partition specs')
    if any(is_unpartitioned(output.partition_spec) for output in node.outputs.values()):
        raise DAGGraphError(f'{node.op_id} cannot mix partitioned and unpartitioned outputs')
    partitions = next(iter(static_partitions))
    return {
        partition: {name: ArtifactKey(output.artifact_id, partition) for name, output in node.outputs.items()}
        for partition in partitions
    }


def _output_spec_for_key(node: MaterializerNode, key: ArtifactKey) -> ArtifactPartitionSpec:
    for output in node.outputs.values():
        if output.artifact_id == key.artifact_id:
            if key.partition and key.partition not in partition_keys(output.partition_spec):
                raise DAGGraphError(f'output key {key} is not declared by {node.op_id}')
            return ArtifactPartitionSpec(output.artifact_id, output.partition_spec)
    raise DAGGraphError(f'output key {key} is not declared by {node.op_id}')


def _spec_declares_key(spec: object, key: ArtifactKey) -> bool:
    if is_unpartitioned(spec):
        return key.partition == ''
    return key.partition in partition_keys(spec)  # type: ignore[arg-type]


def _instance_id(op_id: str, partition: str) -> str:
    return op_id if not partition else f'{op_id}[{partition}]'


def _base_op_id(instance_id: str) -> str:
    return instance_id.split('[', 1)[0]


def _instance_sort_key(instance_id: str, position: dict[str, int]) -> tuple[int, str]:
    if '[' not in instance_id:
        return (position.get(instance_id, len(position)), '')
    op_id, partition = instance_id.split('[', 1)
    return (position.get(op_id, len(position)), partition.rstrip(']'))
