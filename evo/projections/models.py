from __future__ import annotations

from dataclasses import dataclass
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
class CallView:
    run_id: str
    operation_run_id: str
    calls: list[dict[str, Any]]
