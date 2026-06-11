from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Literal

from .. import validate_id

ArtifactStatus = Literal['active', 'stale', 'archived']
ArtifactRole = Literal['operation_output', 'audit', 'external_input']


@dataclass(frozen=True, order=True)
class ArtifactRef:
    artifact_id: str
    version: int

    def __post_init__(self) -> None:
        validate_id(self.artifact_id, 'artifact_id')
        if self.version < 1: raise ValueError(f'invalid artifact version: {self.version}')

    def __str__(self) -> str:
        return f'{self.artifact_id}@v{self.version}'

    @classmethod
    def parse(cls, value: str) -> 'ArtifactRef':
        artifact_id, raw_version = value.rsplit('@v', 1)
        return cls(artifact_id=artifact_id, version=int(raw_version))


@dataclass(frozen=True)
class SnapshotRef:
    snapshot_id: str


@dataclass(frozen=True)
class ArtifactFragment:
    fragment_id: str
    artifact_id: str
    version: int
    json_pointer: str
    kind: str
    label: str = ''


@dataclass(frozen=True)
class ArtifactDraft:
    artifact_id: str
    schema_name: str
    payload: Any
    producer_operation_run_id: str
    input_refs: list[ArtifactRef] = field(default_factory=list)
    fragments: list[ArtifactFragment] = field(default_factory=list)
    role: ArtifactRole = 'operation_output'

    def __post_init__(self) -> None:
        validate_id(self.artifact_id, 'artifact_id')
        if self.producer_operation_run_id: validate_id(self.producer_operation_run_id, 'producer_operation_run_id')
        if self.role not in {'operation_output', 'audit', 'external_input'}:
            raise ValueError(f'invalid artifact role: {self.role!r}')


@dataclass(frozen=True)
class DiffEntry:
    op: Literal['add', 'remove', 'replace']
    path: str
    old: Any = None
    new: Any = None


@dataclass(frozen=True)
class ArtifactDiff:
    old_ref: ArtifactRef
    new_ref: ArtifactRef
    entries: list[DiffEntry]


@dataclass(frozen=True)
class ImpactReport:
    changed: set[ArtifactRef]
    impacted: set[ArtifactRef]


@dataclass(frozen=True)
class ArtifactValidationReport:
    invalid_artifacts: list[dict[str, Any]]
    orphan_blobs: list[str]
    orphan_fragments: list[str]
