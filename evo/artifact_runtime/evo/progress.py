from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol

from .flow import EvoFlowSpec
from ..kernel import ArtifactKey, ArtifactRef


class EffectiveArtifactAccess(Protocol):
    def effective_artifacts(self, run_id: str) -> dict[ArtifactKey, ArtifactRef]:
        ...


@dataclass(frozen=True)
class StepProgress:
    step: str
    root: ArtifactKey
    root_ref: ArtifactRef | None
    effective_outputs: tuple[ArtifactKey, ...]
    total_outputs: int

    @property
    def completed(self) -> bool:
        return self.root_ref is not None


def progress_view(adapter: EffectiveArtifactAccess, spec: EvoFlowSpec, run_id: str) -> tuple[StepProgress, ...]:
    effective = adapter.effective_artifacts(run_id)
    roots = spec.step_roots
    result: list[StepProgress] = []
    for step in spec.steps:
        output_keys = spec.step_output_keys(step)
        result.append(
            StepProgress(
                step,
                roots[step],
                effective.get(roots[step]),
                tuple(key for key in output_keys if key in effective),
                len(output_keys),
            )
        )
    return tuple(result)


__all__ = ['StepProgress', 'progress_view']
