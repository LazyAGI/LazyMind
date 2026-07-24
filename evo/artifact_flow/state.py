from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from types import MappingProxyType
from typing import Literal, TypeAlias

from evo.artifact_runtime.kernel.artifact import ArtifactKey, ArtifactRef


FlowStatus: TypeAlias = Literal['idle', 'paused', 'cancelled', 'failed']
FLOW_STATUSES = ('idle', 'paused', 'cancelled', 'failed')


@dataclass(frozen=True)
class Checkpoint:
    step: str
    root: ArtifactKey
    ref: ArtifactRef

    def __post_init__(self) -> None:
        _require_text(self.step, 'step')
        if not isinstance(self.root, ArtifactKey):
            raise TypeError('root must be ArtifactKey')
        if not isinstance(self.ref, ArtifactRef):
            raise TypeError('ref must be ArtifactRef')
        if self.ref.key != self.root:
            raise ValueError('checkpoint ref key must match root')


@dataclass(frozen=True)
class CheckpointPolicy:
    pause_after_steps: tuple[str, ...] = ('dataset', 'eval', 'analysis', 'repair')

    def __post_init__(self) -> None:
        steps = self.pause_after_steps
        if not isinstance(steps, tuple):
            raise TypeError('pause_after_steps must be tuple')
        for step in steps:
            _require_text(step, 'step')
        if len(set(steps)) != len(steps):
            raise ValueError('pause_after_steps must be unique')


@dataclass(frozen=True)
class FlowRunState:
    run_id: str
    status: FlowStatus = 'idle'
    status_version: int = 0
    pending_checkpoint: Checkpoint | None = None
    released_checkpoints: Mapping[str, ArtifactRef] = field(default_factory=dict)
    last_error: str = ''

    def __post_init__(self) -> None:
        _require_text(self.run_id, 'run_id')
        if self.status not in FLOW_STATUSES:
            raise ValueError('status must be a FlowStatus')
        if not isinstance(self.status_version, int) or isinstance(self.status_version, bool):
            raise TypeError('status_version must be int')
        if self.status_version < 0:
            raise ValueError('status_version must be >= 0')
        if self.pending_checkpoint is not None and not isinstance(self.pending_checkpoint, Checkpoint):
            raise TypeError('pending_checkpoint must be Checkpoint or None')
        if not isinstance(self.released_checkpoints, Mapping):
            raise TypeError('released_checkpoints must be mapping')
        released = dict(self.released_checkpoints)
        for step, ref in released.items():
            _require_text(step, 'step')
            if not isinstance(ref, ArtifactRef):
                raise TypeError('released checkpoint values must be ArtifactRef')
        if not isinstance(self.last_error, str):
            raise TypeError('last_error must be str')
        object.__setattr__(self, 'released_checkpoints', MappingProxyType(released))


def _require_text(value: str, name: str) -> None:
    if not isinstance(value, str):
        raise TypeError(f'{name} must be str')
    if not value.strip():
        raise ValueError(f'{name} must be non-empty')


__all__ = [
    'Checkpoint',
    'CheckpointPolicy',
    'FLOW_STATUSES',
    'FlowRunState',
    'FlowStatus',
]
