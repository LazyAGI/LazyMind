from __future__ import annotations

from dataclasses import dataclass
from typing import TYPE_CHECKING, Literal, Union

if TYPE_CHECKING:
    from .artifact import ArtifactKey


@dataclass(frozen=True)
class Unpartitioned:
    pass


@dataclass(frozen=True)
class StaticPartitions:
    keys: tuple[str, ...]

    def __post_init__(self) -> None:
        keys = tuple(sorted(dict.fromkeys(self.keys)))
        if not keys:
            raise ValueError('static partitions must not be empty')
        for key in keys:
            _require_partition_key(key)
        object.__setattr__(self, 'keys', keys)


PartitionSpec = Union[Unpartitioned, StaticPartitions]
PartitionMappingKind = Literal['same_partition', 'all_to_unpartitioned', 'unpartitioned_to_all']


@dataclass(frozen=True)
class ArtifactPartitionSpec:
    artifact_id: str
    partition_spec: PartitionSpec = Unpartitioned()

    def __post_init__(self) -> None:
        if not isinstance(self.artifact_id, str):
            raise TypeError('artifact_id must be str')
        if not self.artifact_id or not self.artifact_id.strip():
            raise ValueError('artifact_id must be non-empty')


@dataclass(frozen=True)
class PartitionMapping:
    kind: PartitionMappingKind = 'same_partition'

    def upstream_keys(self, downstream_key: ArtifactKey, upstream_spec: ArtifactPartitionSpec,
                      downstream_spec: ArtifactPartitionSpec) -> tuple[ArtifactKey, ...]:
        from .artifact import ArtifactKey

        if self.kind == 'same_partition':
            _require_same_partition_specs(upstream_spec.partition_spec, downstream_spec.partition_spec)
            _require_partition(downstream_key.partition, downstream_spec.partition_spec)
            _require_partition(downstream_key.partition, upstream_spec.partition_spec)
            return (ArtifactKey(upstream_spec.artifact_id, downstream_key.partition),)
        if self.kind == 'all_to_unpartitioned':
            _require_unpartitioned(downstream_spec.partition_spec)
            if downstream_key.partition:
                raise ValueError('downstream key must be unpartitioned')
            return tuple(
                ArtifactKey(upstream_spec.artifact_id, partition)
                for partition in _require_static(upstream_spec.partition_spec).keys
            )
        if self.kind == 'unpartitioned_to_all':
            _require_static(downstream_spec.partition_spec)
            _require_partition(downstream_key.partition, downstream_spec.partition_spec)
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
    return ('',) if is_unpartitioned(spec) else spec.keys


def _require_unpartitioned(spec: PartitionSpec) -> None:
    if not isinstance(spec, Unpartitioned):
        raise ValueError('expected unpartitioned spec')


def _require_static(spec: PartitionSpec) -> StaticPartitions:
    if not isinstance(spec, StaticPartitions):
        raise ValueError('expected static partitions')
    return spec


def _require_partition(partition: str, spec: PartitionSpec) -> None:
    if isinstance(spec, Unpartitioned):
        if partition:
            raise ValueError('expected unpartitioned key')
        return
    if partition not in spec.keys:
        raise ValueError(f'unknown partition: {partition}')


def _require_partition_key(key: object) -> None:
    if not isinstance(key, str):
        raise TypeError('partition keys must be str')
    if not key or not key.strip():
        raise ValueError('partition keys must be non-empty')


def _require_same_partition_specs(upstream: PartitionSpec, downstream: PartitionSpec) -> None:
    if (
        isinstance(upstream, StaticPartitions)
        and isinstance(downstream, StaticPartitions)
        and upstream.keys != downstream.keys
    ):
        raise ValueError('same_partition requires identical static partition sets')
