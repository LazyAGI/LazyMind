from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from types import MappingProxyType
from typing import Any
from typing import Protocol

from .partition import PartitionMapping, PartitionSpec, Unpartitioned, same_partition
from .utils import validate_nonempty


@dataclass(frozen=True, order=True)
class ArtifactKey:
    artifact_id: str
    partition: str = ''

    def __post_init__(self) -> None:
        validate_nonempty(self.artifact_id, 'artifact_id')

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
        if self.version < 1:
            raise ValueError('artifact version must be >= 1')

    @property
    def artifact_id(self) -> str:
        return self.key.artifact_id

    @property
    def partition(self) -> str:
        return self.key.partition

    def __str__(self) -> str:
        if self.key.partition:
            return f'{self.key.artifact_id}[{self.key.partition}]@v{self.version}'
        return f'{self.key.artifact_id}@v{self.version}'


@dataclass(frozen=True)
class ArtifactPayload:
    schema: str
    payload: Any
    metadata: Mapping[str, Any] = field(default_factory=lambda: MappingProxyType({}))
    fragments: tuple[Any, ...] = ()
    role: str = ''

    def __post_init__(self) -> None:
        validate_nonempty(self.schema, 'schema')
        object.__setattr__(self, 'metadata', MappingProxyType(dict(self.metadata)))
        object.__setattr__(self, 'fragments', tuple(self.fragments))

    def __reduce__(self) -> tuple[Any, tuple[Any, ...]]:
        return (type(self), (self.schema, self.payload, dict(self.metadata), tuple(self.fragments), self.role))

    @classmethod
    def from_value(
        cls,
        value: Any,
        *,
        schema: str = 'RawPayload',
        metadata: Mapping[str, Any] | None = None,
        role: str = '',
    ) -> 'ArtifactPayload':
        if isinstance(value, ArtifactPayload):
            return value
        return cls(schema, value, MappingProxyType(dict(metadata or {})), role=role)


@dataclass(frozen=True)
class ArtifactInput:
    artifact_id: str
    required: bool = True
    version: int | None = None
    partition_spec: PartitionSpec = Unpartitioned()
    partition_mapping: PartitionMapping = same_partition()

    def __post_init__(self) -> None:
        validate_nonempty(self.artifact_id, 'artifact_id')
        if self.version is not None and self.version < 1:
            raise ValueError('artifact input version must be >= 1')


@dataclass(frozen=True)
class ArtifactOutput:
    artifact_id: str
    partition_spec: PartitionSpec = Unpartitioned()

    def __post_init__(self) -> None:
        validate_nonempty(self.artifact_id, 'artifact_id')


class ArtifactVersionResolver(Protocol):
    def latest(self, key: ArtifactKey) -> ArtifactRef:
        ...
