from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True)
class PipelineStageView:
    flow: str
    stage: str
    total: int
    ended: int
    running: int
    checkpointed: int
    pending: int


@dataclass(frozen=True)
class PipelineView:
    run_id: str
    stages: list[PipelineStageView]


@dataclass(frozen=True)
class OperationView:
    run_id: str
    active_operations: list[dict[str, Any]]
    operations: list[dict[str, Any]]
    history: list[dict[str, Any]]


@dataclass(frozen=True)
class ImpactView:
    artifact_ref: str
    impacted_artifacts: list[str]
    affected_operations: list[str]


@dataclass(frozen=True)
class CheckpointView:
    run_id: str
    checkpoint_id: str
    summary: str = ''
    artifact_refs: dict[str, str] = field(default_factory=dict)
    allowed_capabilities: list[str] = field(default_factory=list)
    next_operations: list[str] = field(default_factory=list)


@dataclass(frozen=True)
class CallView:
    run_id: str
    operation_run_id: str
    calls: list[dict[str, Any]]


@dataclass(frozen=True)
class OperationDetailView:
    run_id: str
    operation: dict[str, Any]
    calls: list[dict[str, Any]]
    input_artifacts: list[dict[str, str]]
    output_artifacts: list[dict[str, str]]


@dataclass(frozen=True)
class IntentTraceView:
    run_id: str
    message_id: str
    trace: dict[str, Any]
    operations: list[dict[str, Any]]
    trace_ref: str = ''
