from __future__ import annotations

from dataclasses import dataclass
from typing import TYPE_CHECKING, Literal, TypeAlias

from .utils import validate_nonempty

if TYPE_CHECKING:
    from .artifact import ArtifactKey


@dataclass(frozen=True)
class Unpartitioned:
    pass


@dataclass(frozen=True)
class StaticPartitions:
    keys: tuple[str, ...]

    def __post_init__(self) -> None:
        keys = tuple(sorted(set(self.keys)))
        if not keys:
            raise ValueError('static partitions must not be empty')
        if any(not key or not key.strip() for key in keys):
            raise ValueError('partition keys must be non-empty')
        object.__setattr__(self, 'keys', keys)


PartitionSpec: TypeAlias = Unpartitioned | StaticPartitions
PartitionMappingKind: TypeAlias = Literal['same_partition', 'all_to_unpartitioned', 'unpartitioned_to_all']


@dataclass(frozen=True)
class ArtifactPartitionSpec:
    artifact_id: str
    partition_spec: PartitionSpec = Unpartitioned()

    def __post_init__(self) -> None:
        validate_nonempty(self.artifact_id, 'artifact_id')


@dataclass(frozen=True)
class PartitionMapping:
    kind: PartitionMappingKind = 'same_partition'

    def downstream_keys(
        self,
        upstream_key: ArtifactKey,
        upstream_spec: ArtifactPartitionSpec,
        downstream_spec: ArtifactPartitionSpec,
    ) -> tuple[ArtifactKey, ...]:
        from .artifact import ArtifactKey

        if self.kind == 'same_partition':
            _require_partition_member(upstream_key.partition, upstream_spec.partition_spec)
            _require_partition_member(upstream_key.partition, downstream_spec.partition_spec)
            return (ArtifactKey(downstream_spec.artifact_id, upstream_key.partition),)
        if self.kind == 'all_to_unpartitioned':
            _require_static(upstream_spec.partition_spec)
            if upstream_key.partition not in upstream_spec.partition_spec.keys:
                raise ValueError(f'unknown upstream partition: {upstream_key.partition}')
            _require_unpartitioned(downstream_spec.partition_spec)
            return (ArtifactKey.of(downstream_spec.artifact_id),)
        if self.kind == 'unpartitioned_to_all':
            _require_unpartitioned(upstream_spec.partition_spec)
            if upstream_key.partition:
                raise ValueError('upstream key must be unpartitioned')
            downstream_partitions = _require_static(downstream_spec.partition_spec)
            return tuple(ArtifactKey(downstream_spec.artifact_id, partition)
                         for partition in downstream_partitions.keys)
        raise ValueError(f'unknown partition mapping: {self.kind}')

    def upstream_keys(
        self,
        downstream_key: ArtifactKey,
        upstream_spec: ArtifactPartitionSpec,
        downstream_spec: ArtifactPartitionSpec,
    ) -> tuple[ArtifactKey, ...]:
        from .artifact import ArtifactKey

        if self.kind == 'same_partition':
            _require_partition_member(downstream_key.partition, downstream_spec.partition_spec)
            _require_partition_member(downstream_key.partition, upstream_spec.partition_spec)
            return (ArtifactKey(upstream_spec.artifact_id, downstream_key.partition),)
        if self.kind == 'all_to_unpartitioned':
            _require_unpartitioned(downstream_spec.partition_spec)
            if downstream_key.partition:
                raise ValueError('downstream key must be unpartitioned')
            upstream_partitions = _require_static(upstream_spec.partition_spec)
            return tuple(ArtifactKey(upstream_spec.artifact_id, partition) for partition in upstream_partitions.keys)
        if self.kind == 'unpartitioned_to_all':
            _require_static(downstream_spec.partition_spec)
            _require_partition_member(downstream_key.partition, downstream_spec.partition_spec)
            _require_unpartitioned(upstream_spec.partition_spec)
            return (ArtifactKey.of(upstream_spec.artifact_id),)
        raise ValueError(f'unknown partition mapping: {self.kind}')


def same_partition() -> PartitionMapping:
    return PartitionMapping('same_partition')


def all_to_unpartitioned() -> PartitionMapping:
    return PartitionMapping('all_to_unpartitioned')


def unpartitioned_to_all() -> PartitionMapping:
    return PartitionMapping('unpartitioned_to_all')


def is_unpartitioned(spec: PartitionSpec) -> bool:
    return isinstance(spec, Unpartitioned)


def partition_keys(spec: PartitionSpec) -> tuple[str, ...]:
    if isinstance(spec, Unpartitioned):
        return ('',)
    return spec.keys


def _require_unpartitioned(spec: PartitionSpec) -> None:
    if not isinstance(spec, Unpartitioned):
        raise ValueError('expected unpartitioned spec')


def _require_static(spec: PartitionSpec) -> StaticPartitions:
    if not isinstance(spec, StaticPartitions):
        raise ValueError('expected static partitions')
    return spec


def _require_partition_member(partition: str, spec: PartitionSpec) -> None:
    if isinstance(spec, Unpartitioned):
        if partition:
            raise ValueError('expected unpartitioned key')
        return
    if partition not in spec.keys:
        raise ValueError(f'unknown partition: {partition}')
