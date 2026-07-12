from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass

from evo.artifact_runtime.evo.flow import EvoFlowSpec
from evo.artifact_runtime.kernel.artifact import ArtifactKey, ArtifactRef

from .state import FlowStatus


@dataclass(frozen=True)
class StaleCheckpoint:
    step: str
    released_ref: ArtifactRef
    effective_ref: ArtifactRef | None


@dataclass(frozen=True)
class CheckpointProjection:
    checkpoint_state: str
    first_missing_step: str
    first_stale_step: str
    last_released_step: str
    retry_from_step: str
    current_step: str
    valid_released: Mapping[str, ArtifactRef]
    stale_released: tuple[StaleCheckpoint, ...]


def checkpoint_projection(
    spec: EvoFlowSpec,
    effective: Mapping[ArtifactKey, ArtifactRef],
    released: Mapping[str, ArtifactRef],
    status: FlowStatus = 'idle',
) -> CheckpointProjection:
    if not isinstance(spec, EvoFlowSpec):
        raise TypeError('spec must be EvoFlowSpec')
    if not isinstance(effective, Mapping):
        raise TypeError('effective must be mapping')
    if not isinstance(released, Mapping):
        raise TypeError('released must be mapping')

    valid: dict[str, ArtifactRef] = {}
    stale: list[StaleCheckpoint] = []
    first_missing = ''
    last_released = ''
    roots = spec.step_roots
    known_steps = set(spec.steps)

    for step in spec.steps:
        root_ref = effective.get(roots[step])
        released_ref = released.get(step)
        if root_ref is None and not first_missing:
            first_missing = step
        if released_ref is None:
            continue
        if root_ref == released_ref:
            valid[step] = released_ref
            last_released = step
            continue
        stale.append(StaleCheckpoint(step, released_ref, root_ref))
    for step, released_ref in released.items():
        if step not in known_steps:
            stale.append(StaleCheckpoint(step, released_ref, None))

    first_stale = stale[0].step if stale else ''
    retry_from = _retry_from_step(spec, last_released, first_missing)
    current = _current_step(spec, status, first_missing, first_stale, retry_from)
    checkpoint_state = 'stale' if stale else ('valid' if released else 'none')
    return CheckpointProjection(
        checkpoint_state=checkpoint_state,
        first_missing_step=first_missing,
        first_stale_step=first_stale,
        last_released_step=last_released,
        retry_from_step=retry_from,
        current_step=current,
        valid_released=valid,
        stale_released=tuple(stale),
    )


def trim_released_from_step(
    spec: EvoFlowSpec,
    released: Mapping[str, ArtifactRef],
    step: str,
) -> dict[str, ArtifactRef]:
    if step not in spec.steps:
        raise ValueError(f'unknown step: {step}')
    step_index = {current: index for index, current in enumerate(spec.steps)}
    cutoff = step_index[step]
    return {
        current: ref
        for current, ref in released.items()
        if current in step_index and step_index[current] < cutoff
    }


def next_step_after(spec: EvoFlowSpec, step: str) -> str:
    if step not in spec.steps:
        raise ValueError(f'unknown step: {step}')
    step_index = {current: index for index, current in enumerate(spec.steps)}
    index = step_index[step] + 1
    return spec.steps[index] if index < len(spec.steps) else ''


def _retry_from_step(spec: EvoFlowSpec, last_released: str, first_missing: str) -> str:
    if last_released:
        return next_step_after(spec, last_released)
    return first_missing


def _current_step(
    spec: EvoFlowSpec,
    status: FlowStatus,
    first_missing: str,
    first_stale: str,
    retry_from: str,
) -> str:
    if first_stale:
        return first_stale
    if status == 'failed' and retry_from:
        return retry_from
    if first_missing:
        return first_missing
    return spec.steps[-1] if spec.steps else ''


__all__ = [
    'CheckpointProjection',
    'StaleCheckpoint',
    'checkpoint_projection',
    'next_step_after',
    'trim_released_from_step',
]
