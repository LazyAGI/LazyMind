from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any
from uuid import uuid4


@dataclass(frozen=True)
class Event:
    event_type: str
    run_id: str
    payload: dict[str, Any] = field(default_factory=dict)
    event_id: str = field(default_factory=lambda: uuid4().hex)
    created_at: str = field(default_factory=lambda: datetime.now(timezone.utc).isoformat())
    sequence: int = 0


@dataclass(frozen=True)
class RecoveryReport:
    run_id: str
    active_run_id: str | None
    running_operations: list[str]
    latest_checkpoint_id: str | None
    removed_tmp_files: list[str]
    artifact_indexes_rebuilt: bool
    invalid_artifacts: list[dict[str, Any]] = field(default_factory=list)
    orphan_blobs: list[str] = field(default_factory=list)
    orphan_fragments: list[str] = field(default_factory=list)
    producer_mismatches: list[dict[str, Any]] = field(default_factory=list)
