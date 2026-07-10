from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Protocol

from evo.artifact_runtime.evo.actions import EvoQuery, dispatch_evo_query
from evo.artifact_runtime.evo.flow import EvoFlowSpec
from evo.artifact_runtime.evo.progress import StepProgress, progress_view
from evo.artifact_runtime.evo.use_cases import EvoArtifactReader
from evo.artifact_runtime.kernel.artifact import ArtifactRef

from .checkpoints import CheckpointProjection, checkpoint_projection
from .state import Checkpoint, FlowRunState, FlowStatus


class QueryGatePort(Protocol):
    def get(self, run_id: str) -> FlowRunState | None:
        ...


@dataclass(frozen=True)
class FlowSnapshot:
    run_id: str
    status: FlowStatus
    pending_checkpoint: Checkpoint | None
    released_checkpoints: dict[str, ArtifactRef]
    progress: tuple[StepProgress, ...]
    checkpoint: CheckpointProjection


class FlowQueryService:
    def __init__(self, gate: QueryGatePort, adapter_factory: Callable[[], EvoArtifactReader],
                 spec: EvoFlowSpec) -> None:
        if not isinstance(spec, EvoFlowSpec):
            raise TypeError('spec must be EvoFlowSpec')
        self._gate = gate
        self._adapter_factory = adapter_factory
        self._spec = spec

    def snapshot(self, run_id: str) -> FlowSnapshot:
        _require_text(run_id, 'run_id')
        state = self._gate.get(run_id) or FlowRunState(run_id)
        adapter = self._adapter_factory()
        effective = adapter.effective_artifacts(run_id)
        checkpoint = checkpoint_projection(self._spec, effective, state.released_checkpoints, state.status)
        return FlowSnapshot(
            run_id,
            state.status,
            state.pending_checkpoint,
            dict(state.released_checkpoints),
            progress_view(adapter, self._spec, run_id),
            checkpoint,
        )

    def progress(self, run_id: str) -> tuple[StepProgress, ...]:
        _require_text(run_id, 'run_id')
        return progress_view(self._adapter_factory(), self._spec, run_id)

    def read(self, run_id: str, query: EvoQuery):
        _require_text(run_id, 'run_id')
        if not isinstance(query, EvoQuery):
            raise TypeError('query must be EvoQuery')
        return dispatch_evo_query(self._adapter_factory(), self._spec, run_id, query)


def _require_text(value: str, name: str) -> None:
    if not isinstance(value, str):
        raise TypeError(f'{name} must be str')
    if not value.strip():
        raise ValueError(f'{name} must be non-empty')


__all__ = ['FlowQueryService', 'FlowSnapshot']
