from __future__ import annotations

from dataclasses import dataclass

from .partition import PartitionMapping, PartitionSpec, Unpartitioned, same_partition


def _require_text(value: str, name: str) -> None:
    if not isinstance(value, str):
        raise TypeError(f'{name} must be str')
    if not value or not value.strip():
        raise ValueError(f'{name} must be non-empty')


@dataclass(frozen=True, order=True)
class ArtifactKey:
    artifact_id: str
    partition: str = ''

    def __post_init__(self) -> None:
        _require_text(self.artifact_id, 'artifact_id')
        if not isinstance(self.partition, str):
            raise TypeError('partition must be str')
        if self.partition and not self.partition.strip():
            raise ValueError('partition must be non-empty when set')

    @classmethod
    def of(cls, artifact_id: str) -> 'ArtifactKey':
        return cls(artifact_id)


@dataclass(frozen=True, order=True)
class ArtifactRef:
    key: ArtifactKey
    version: int

    def __post_init__(self) -> None:
        if not isinstance(self.key, ArtifactKey):
            raise TypeError('artifact ref key must be an ArtifactKey')
        if not isinstance(self.version, int) or isinstance(self.version, bool):
            raise TypeError('artifact version must be int')
        if self.version < 1:
            raise ValueError('artifact version must be >= 1')


@dataclass(frozen=True)
class ArtifactInput:
    artifact_id: str
    required: bool = True
    partition_spec: PartitionSpec = Unpartitioned()
    partition_mapping: PartitionMapping = same_partition()

    def __post_init__(self) -> None:
        _require_text(self.artifact_id, 'artifact_id')


@dataclass(frozen=True)
class ArtifactOutput:
    artifact_id: str
    partition_spec: PartitionSpec = Unpartitioned()

    def __post_init__(self) -> None:
        _require_text(self.artifact_id, 'artifact_id')
