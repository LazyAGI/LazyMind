from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from types import MappingProxyType
from typing import Literal

from .artifact import ArtifactInput, ArtifactKey, ArtifactOutput, ArtifactRef
from .ops import FixedOp
from .utils import validate_nonempty


@dataclass(frozen=True)
class MaterializerNode:
    op_id: str
    op_cls: type[FixedOp]
    inputs: Mapping[str, ArtifactInput]
    outputs: Mapping[str, ArtifactOutput]
    producer_dependencies: tuple[str, ...]
    flow: str = ''
    stage: str = ''
    tags: Mapping[str, str] = field(default_factory=lambda: MappingProxyType({}))

    @property
    def depends_on(self) -> tuple[str, ...]:
        return self.producer_dependencies


PlanInputKind = Literal['single', 'partition_collection']


@dataclass(frozen=True, init=False)
class PlanInput:
    name: str
    key: ArtifactKey
    version: ArtifactRef | None = None
    planned: bool = False
    required: bool = True
    input_kind: PlanInputKind = 'single'
    collection_name: str = ''

    def __init__(
        self,
        name: str,
        key: ArtifactKey,
        version: ArtifactRef | None = None,
        planned: bool = False,
        required: bool = True,
        *,
        input_kind: PlanInputKind = 'single',
        collection_name: str = '',
    ) -> None:
        object.__setattr__(self, 'name', name)
        object.__setattr__(self, 'key', key)
        object.__setattr__(self, 'version', version)
        object.__setattr__(self, 'planned', planned)
        object.__setattr__(self, 'required', required)
        object.__setattr__(self, 'input_kind', input_kind)
        object.__setattr__(self, 'collection_name', collection_name)
        self.__post_init__()

    def __post_init__(self) -> None:
        validate_nonempty(self.name, 'input name')
        if not isinstance(self.key, ArtifactKey):
            raise TypeError('plan input key must be an ArtifactKey')
        if self.version is not None and self.version.key != self.key:
            raise ValueError('input version key must match input key')
        if self.version is not None and self.planned:
            raise ValueError('plan input cannot be both version-bound and planned')
        if self.input_kind not in ('single', 'partition_collection'):
            raise ValueError(f'invalid plan input kind: {self.input_kind}')
        if self.input_kind == 'single' and self.collection_name:
            raise ValueError('single plan input cannot have collection_name')
        if self.input_kind == 'partition_collection':
            validate_nonempty(self.collection_name or self.name, 'collection_name')


@dataclass(frozen=True, init=False)
class PlanOp:
    op_id: str
    input_bindings: tuple[PlanInput, ...]
    output_key_by_name: Mapping[str, ArtifactKey]
    depends_on: tuple[str, ...]
    graph_revision: int
    flow: str = ''
    stage: str = ''
    tags: Mapping[str, str] = field(default_factory=lambda: MappingProxyType({}))

    def __init__(
        self,
        op_id: str,
        input_bindings: tuple[PlanInput, ...],
        output_key_by_name: Mapping[str, ArtifactKey],
        depends_on: tuple[str, ...] = (),
        graph_revision: int = 0,
        flow: str = '',
        stage: str = '',
        tags: Mapping[str, str] | None = None,
    ) -> None:
        object.__setattr__(self, 'op_id', op_id)
        object.__setattr__(self, 'input_bindings', tuple(input_bindings))
        object.__setattr__(self, 'output_key_by_name', MappingProxyType(dict(output_key_by_name)))
        object.__setattr__(self, 'depends_on', tuple(depends_on))
        object.__setattr__(self, 'graph_revision', graph_revision)
        object.__setattr__(self, 'flow', flow)
        object.__setattr__(self, 'stage', stage)
        object.__setattr__(self, 'tags', MappingProxyType(dict(tags or {})))
        self.__post_init__()

    def __post_init__(self) -> None:
        validate_nonempty(self.op_id, 'op_id')
        if not self.output_key_by_name:
            raise ValueError('plan op must declare at least one output')
        for name, key in self.output_key_by_name.items():
            validate_nonempty(name, 'output name')
            if not isinstance(key, ArtifactKey):
                raise TypeError('plan op output keys must be ArtifactKey values')
        _validate_plan_input_bindings(self.input_bindings)

    @property
    def input_keys(self) -> tuple[ArtifactKey, ...]:
        return tuple(binding.key for binding in self.input_bindings)

    @property
    def input_key_versions(self) -> dict[ArtifactKey, ArtifactRef]:
        out: dict[ArtifactKey, ArtifactRef] = {}
        for binding in self.input_bindings:
            if binding.version is not None:
                out.setdefault(binding.key, binding.version)
        return out

    @property
    def planned_input_keys(self) -> tuple[ArtifactKey, ...]:
        return tuple(binding.key for binding in self.input_bindings if binding.planned)

    @property
    def output_keys(self) -> tuple[ArtifactKey, ...]:
        return tuple(self.output_key_by_name[name] for name in sorted(self.output_key_by_name))

    @property
    def output_names(self) -> tuple[str, ...]:
        return tuple(sorted(self.output_key_by_name))


@dataclass(frozen=True)
class ExecutionPlan:
    plan_id: str
    graph_revision: int
    layers: tuple[tuple[PlanOp, ...], ...]

    @property
    def op_ids(self) -> tuple[str, ...]:
        return tuple(plan_op.op_id for layer in self.layers for plan_op in layer)


def _validate_plan_input_bindings(bindings: tuple[PlanInput, ...]) -> None:
    input_kind_by_name: dict[str, PlanInputKind] = {}
    collection_partitions: dict[str, set[str]] = {}
    for binding in bindings:
        existing_kind = input_kind_by_name.setdefault(binding.name, binding.input_kind)
        if existing_kind != binding.input_kind:
            raise ValueError('plan input name cannot mix input kinds')
        if binding.input_kind == 'partition_collection':
            collection = binding.collection_name or binding.name
            if collection in input_kind_by_name and input_kind_by_name.get(collection) == 'single':
                raise ValueError('single and partition_collection inputs cannot share a name')
            partitions = collection_partitions.setdefault(collection, set())
            if binding.key.partition in partitions:
                raise ValueError('duplicate partition binding in collection input')
            partitions.add(binding.key.partition)
        elif binding.name in collection_partitions:
            raise ValueError('single and partition_collection inputs cannot share a name')
