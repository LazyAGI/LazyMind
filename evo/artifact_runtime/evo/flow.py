from __future__ import annotations

from collections.abc import Container
from dataclasses import dataclass

from jsonpointer import JsonPointer

from ..kernel import ArtifactKey, ArtifactRef

from .catalog import OUTPUTS, READ_CASE, RERUN_CASE_STAGE, ROOTS, SEEDS, STEPS


@dataclass(frozen=True)
class EvoFlowSpec:
    cases: tuple[str, ...]

    def __post_init__(self) -> None:
        if not isinstance(self.cases, tuple):
            raise TypeError('cases must be tuple[str, ...]')
        if not self.cases:
            raise ValueError('cases must be non-empty')
        if len(set(self.cases)) != len(self.cases):
            raise ValueError('cases must be unique')
        for case_id in self.cases:
            self._require_member(case_id, 'case_id')

    @staticmethod
    def case_ids(count: int) -> tuple[str, ...]:
        if not isinstance(count, int) or isinstance(count, bool):
            raise TypeError('count must be int')
        if count < 1:
            raise ValueError('count must be >= 1')
        return tuple(f'case_{index:04d}' for index in range(1, count + 1))

    @property
    def steps(self) -> tuple[str, ...]:
        return STEPS

    @property
    def seed_keys(self) -> tuple[ArtifactKey, ...]:
        return tuple(ArtifactKey.of(artifact_id) for artifact_id in SEEDS)

    @property
    def step_roots(self) -> dict[str, ArtifactKey]:
        return {step: ArtifactKey.of(artifact_id) for step, artifact_id in ROOTS.items()}

    def step_output_keys(self, step: str) -> tuple[ArtifactKey, ...]:
        self._require_step(step)
        return tuple(
            ArtifactKey(output.artifact_id, case_id)
            for output in OUTPUTS[step]
            for case_id in (self.cases if output.partitioned else ('',))
        )

    def read_step_root(self, step: str) -> ArtifactKey:
        self._require_step(step)
        return ArtifactKey.of(ROOTS[step])

    def read_case_artifact(self, case_id: str, kind: str) -> ArtifactKey:
        self._require_case(case_id)
        self._require_known(kind, READ_CASE, 'case artifact kind')
        return ArtifactKey(READ_CASE[kind], case_id)

    def rerun_case_stage(self, case_id: str, stage: str) -> tuple[ArtifactKey, ...]:
        self._require_case(case_id)
        self._require_known(stage, RERUN_CASE_STAGE, 'case stage')
        return tuple(ArtifactKey(artifact_id, case_id) for artifact_id in RERUN_CASE_STAGE[stage])

    def rerun_step(self, step: str) -> tuple[ArtifactKey, ...]:
        return self.step_output_keys(step)

    def jump_to_step(self, step: str) -> tuple[ArtifactKey, ...]:
        self._require_step(step)
        start = STEPS.index(step)
        return tuple(key for current in STEPS[start:] for key in self.step_output_keys(current))

    def edit_target(self, ref: ArtifactRef, pointer: str) -> tuple[ArtifactRef, str]:
        if not isinstance(ref, ArtifactRef):
            raise TypeError('ref must be ArtifactRef')
        if not isinstance(pointer, str):
            raise TypeError('pointer must be str')
        JsonPointer(pointer)
        return ref, pointer

    @staticmethod
    def _require_member(value: str, name: str) -> None:
        if not isinstance(value, str):
            raise TypeError(f'{name} must be str')
        if not value or not value.strip():
            raise ValueError(f'{name} must be non-empty')

    def _require_case(self, case_id: str) -> None:
        self._require_member(case_id, 'case_id')
        if case_id not in self.cases:
            raise ValueError(f'unknown case_id: {case_id}')

    @staticmethod
    def _require_known(value: str, known: Container[str], name: str) -> None:
        EvoFlowSpec._require_member(value, name)
        if value not in known:
            raise ValueError(f'unknown {name}: {value}')

    def _require_step(self, step: str) -> None:
        self._require_known(step, OUTPUTS, 'step')


__all__ = ['EvoFlowSpec']
