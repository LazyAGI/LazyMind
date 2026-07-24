from __future__ import annotations

from collections.abc import Iterable

from evo.artifact_runtime import (
    ArtifactRef,
    ArtifactRetryRequest,
    DefinitionError,
    RuntimeSnapshot,
)

from .definition import FlowDefinition
from .state import FlowSnapshot, StageProgress, StageStatus


def project_flow(definition: FlowDefinition, runtime: RuntimeSnapshot,
                 retries: Iterable[ArtifactRetryRequest] = ()
                 ) -> FlowSnapshot:
    if not isinstance(definition, FlowDefinition):
        raise TypeError('definition must be FlowDefinition')
    if not isinstance(runtime, RuntimeSnapshot):
        raise TypeError('runtime must be RuntimeSnapshot')

    requests = tuple(retries)
    if not all(isinstance(request, ArtifactRetryRequest) for request in requests):
        raise TypeError('retries must contain ArtifactRetryRequest values')

    refs = tuple(
        (
            runtime.completed_artifacts.get(stage.result_key),
            None if stage.approval_key is None else runtime.completed_artifacts.get(
                stage.approval_key
            ),
        )
        for stage in definition.stages
    )
    approval_index = _approval_index(definition, runtime, refs)
    incomplete_index = _incomplete_index(definition, refs)
    active_index = _active_index(definition, runtime, requests)
    frontier = _first_index(approval_index, incomplete_index, active_index)

    return FlowSnapshot(
        runtime,
        tuple(
            StageProgress(
                stage.name,
                stage.result_key,
                refs[index][0],
                stage.approval_key,
                refs[index][1],
                _stage_status(
                    index,
                    frontier,
                    active_index,
                    approval_index,
                    runtime,
                ),
            )
            for index, stage in enumerate(definition.stages)
        ),
    )


def _approval_index(definition: FlowDefinition, runtime: RuntimeSnapshot,
                    refs: tuple[tuple[ArtifactRef | None, ArtifactRef | None], ...]
                    ) -> int | None:
    return next(
        (
            index
            for index, stage in enumerate(definition.stages)
            if (
                refs[index][0] is not None
                and refs[index][1] is None
                and stage.approval_key in runtime.awaiting_artifacts
            )
        ),
        None,
    )


def _incomplete_index(definition: FlowDefinition,
                      refs: tuple[tuple[ArtifactRef | None, ArtifactRef | None], ...]
                      ) -> int | None:
    missing = next(
        (
            index
            for index in range(len(definition.stages))
            if refs[index][0] is None
        ),
        None,
    )
    if missing is None or missing == 0:
        return missing
    previous = missing - 1
    if (
        definition.stages[previous].approval_key is not None
        and refs[previous][1] is None
    ):
        return previous
    return missing


def _active_index(definition: FlowDefinition, runtime: RuntimeSnapshot,
                  retries: tuple[ArtifactRetryRequest, ...]
                  ) -> int | None:
    indices = [
        _operation_stage(definition, attempt.operation_id)
        for attempt in runtime.active_attempts
    ]
    indices.extend(
        _artifact_stage(definition, request.artifact_key.artifact_id)
        for request in retries
        if request.status == 'pending'
    )
    if runtime.status == 'cancelled':
        indices.extend(
            _artifact_stage(definition, request.artifact_key.artifact_id)
            for request in retries
            if (
                request.status == 'cancelled'
                and runtime.completed_artifacts.get(request.artifact_key) == request.base_ref
            )
        )
    return min(indices, default=None)


def _operation_stage(definition: FlowDefinition, operation_id: str) -> int:
    index = definition.stage_index_for_operation(operation_id)
    if index is None:
        raise DefinitionError(f'operation does not belong to a flow stage: {operation_id}')
    return index


def _artifact_stage(definition: FlowDefinition, artifact_id: str) -> int:
    index = definition.stage_index_for_artifact(artifact_id)
    if index is None:
        raise DefinitionError(f'artifact does not belong to a flow stage: {artifact_id}')
    return index


def _first_index(*indices: int | None) -> int | None:
    return min((index for index in indices if index is not None), default=None)


def _stage_status(index: int, frontier: int | None, active: int | None,
                  approval: int | None, runtime: RuntimeSnapshot
                  ) -> StageStatus:
    if frontier is None or index < frontier:
        return 'completed'
    if index > frontier:
        return 'pending'
    if active == frontier:
        return _active_status(runtime)
    if runtime.status in {'cancelling', 'cancelled', 'failed'}:
        return runtime.status
    if approval == frontier:
        return 'awaiting_approval'
    if runtime.status in {'pausing', 'paused'}:
        return runtime.status
    return 'pending'


def _active_status(runtime: RuntimeSnapshot) -> StageStatus:
    if runtime.status == 'created':
        return 'pending'
    if runtime.status == 'completed':
        return 'running'
    return runtime.status


__all__ = ['project_flow']
