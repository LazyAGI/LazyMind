from __future__ import annotations

from dataclasses import dataclass, field
from typing import Literal, Protocol

from ..artifacts.models import ArtifactRef
from .. import validate_id

OperationRunStatus = Literal['pending', 'running', 'checkpointed', 'ended']
WritePolicy = Literal['single', 'versioned']
OperationRunChangeKind = Literal['created', 'started', 'checkpointed', 'ended', 'reset', 'superseded',
                                 'dependencies_updated', 'inputs_bound']


@dataclass(frozen=True, order=True)
class OperationRunRef:
    operation_run_id: str

    def __post_init__(self) -> None:
        validate_id(self.operation_run_id, 'operation_run_id')

    def __str__(self) -> str:
        return self.operation_run_id


@dataclass(frozen=True)
class ArtifactSetRequirement:
    name: str
    producer_tag: str
    producer_value: str
    min_count: int = 1


@dataclass(frozen=True)
class OperationSpec:
    operation_id: str
    operation_type: str
    category: str = 'pipeline'
    flow_tag: str = ''
    stage_tag: str = ''
    depends_on: list[str] = field(default_factory=list)
    required_artifact_refs: list[ArtifactRef] = field(default_factory=list)
    required_artifact_ids: list[str] = field(default_factory=list)
    required_artifact_sets: list[ArtifactSetRequirement] = field(default_factory=list)
    write_policy: WritePolicy = 'single'
    tags: dict[str, str] = field(default_factory=dict)
    params: dict = field(default_factory=dict)

    def __post_init__(self) -> None:
        validate_id(self.operation_id, 'operation_id')
        if self.write_policy not in {'single', 'versioned'}:
            raise ValueError(f'invalid write_policy: {self.write_policy!r}')
        for artifact_ref in self.required_artifact_refs:
            if not isinstance(artifact_ref, ArtifactRef):
                raise TypeError('required_artifact_refs must contain ArtifactRef values')
        for artifact_id in self.required_artifact_ids:
            validate_id(artifact_id, 'artifact_id')


@dataclass
class OperationRun:
    ref: OperationRunRef
    spec: OperationSpec
    status: OperationRunStatus = 'pending'
    attempt: int = 1
    parent: OperationRunRef | None = None
    source_message_id: str | None = None
    input_refs: list[ArtifactRef] = field(default_factory=list)
    output_refs: list[ArtifactRef] = field(default_factory=list)
    depends_on: list[OperationRunRef] = field(default_factory=list)
    superseded_by: OperationRunRef | None = None
    supersede_reason: str = ''
    outcome: str = ''


@dataclass(frozen=True)
class ScheduleBlocker:
    operation_run_id: str
    reason: str
    depends_on: list[str] = field(default_factory=list)
    missing_artifact_ids: list[str] = field(default_factory=list)
    missing_artifact_sets: list[str] = field(default_factory=list)


@dataclass(frozen=True)
class ScheduleState:
    ready: list[OperationRunRef]
    running: list[OperationRunRef]
    checkpointed: list[OperationRunRef]
    blockers: list[ScheduleBlocker]
    complete: bool


@dataclass(frozen=True)
class OperationRunSnapshot:
    operation_run_id: str
    operation_id: str
    operation_type: str
    status: OperationRunStatus
    attempt: int
    category: str
    flow_tag: str
    stage_tag: str
    input_refs: list[str] = field(default_factory=list)
    output_refs: list[str] = field(default_factory=list)
    depends_on: list[str] = field(default_factory=list)
    parent: str = ''
    source_message_id: str = ''
    superseded_by: str = ''
    supersede_reason: str = ''
    outcome: str = ''
    tags: dict[str, str] = field(default_factory=dict)
    params: dict = field(default_factory=dict)
    required_artifact_refs: list[str] = field(default_factory=list)
    required_artifact_ids: list[str] = field(default_factory=list)
    required_artifact_sets: list[dict] = field(default_factory=list)
    write_policy: WritePolicy = 'single'


@dataclass(frozen=True)
class OperationRunChange:
    kind: OperationRunChangeKind
    before: OperationRunSnapshot | None
    after: OperationRunSnapshot
    reason: str = ''


class OperationRunObserver(Protocol):
    def on_operation_run_change(self, change: OperationRunChange) -> None:
        ...
