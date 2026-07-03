from __future__ import annotations

import copy
from typing import Protocol

from jsonpointer import JsonPointer, JsonPointerException

from .flow import EvoFlowSpec
from ..kernel import ArtifactKey, ArtifactRef


class EvoArtifactReader(Protocol):
    def effective_artifacts(self, run_id: str) -> dict[ArtifactKey, ArtifactRef]:
        ...

    def get(self, run_id: str, ref: ArtifactRef):
        ...


class EvoArtifactAccess(EvoArtifactReader, Protocol):

    def commit_external(self, run_id: str, key: ArtifactKey, value: object,
                        *, idempotency_key: str, expected_ref: ArtifactRef | None = None,
                        ):
        ...

    def invalidate(self, run_id: str, keys=(), refs=(), *, idempotency_key: str):
        ...


def read_step_root(adapter: EvoArtifactReader, spec: EvoFlowSpec, run_id: str, step: str):
    return _read_effective(adapter, run_id, spec.read_step_root(step))


def read_case_artifact(adapter: EvoArtifactReader, spec: EvoFlowSpec, run_id: str, case_id: str, kind: str):
    return _read_effective(adapter, run_id, spec.read_case_artifact(case_id, kind))


def edit_artifact(adapter: EvoArtifactAccess, spec: EvoFlowSpec, run_id: str, ref: ArtifactRef,
                  pointer: str, value: object, *, idempotency_key: str):
    target_ref, target_pointer = spec.edit_target(ref, pointer)
    record = adapter.get(run_id, target_ref)
    if record is None:
        raise ValueError('artifact ref is not readable')
    return adapter.commit_external(
        run_id,
        target_ref.key,
        _replace_json_pointer(record.value, target_pointer, value),
        idempotency_key=idempotency_key,
        expected_ref=target_ref,
    )


def rerun_case_stage(adapter: EvoArtifactAccess, spec: EvoFlowSpec, run_id: str,
                     case_id: str, stage: str, *, idempotency_key: str):
    return adapter.invalidate(run_id, keys=spec.rerun_case_stage(case_id, stage), idempotency_key=idempotency_key)


def rerun_step(adapter: EvoArtifactAccess, spec: EvoFlowSpec, run_id: str, step: str, *, idempotency_key: str):
    return adapter.invalidate(run_id, keys=spec.rerun_step(step), idempotency_key=idempotency_key)


def jump_to_step(adapter: EvoArtifactAccess, spec: EvoFlowSpec, run_id: str, step: str, *, idempotency_key: str):
    return adapter.invalidate(run_id, keys=spec.jump_to_step(step), idempotency_key=idempotency_key)


def _read_effective(adapter: EvoArtifactReader, run_id: str, key: ArtifactKey):
    ref = adapter.effective_artifacts(run_id).get(key)
    return None if ref is None else adapter.get(run_id, ref)


def _replace_json_pointer(value: object, pointer: str, replacement: object) -> object:
    target = JsonPointer(pointer)
    if not target.parts:
        raise ValueError('root replacement is not allowed')
    if target.parts[-1] == '-':
        raise ValueError('array append is not supported')
    clone = copy.deepcopy(value)
    try:
        target.resolve(clone)
    except JsonPointerException as exc:
        message = str(exc)
        if 'out of bounds' in message:
            raise ValueError('array index out of bounds') from exc
        if 'member' in message and 'not found' in message:
            raise ValueError(f'path does not exist: {pointer}') from exc
        raise
    target.set(clone, replacement, inplace=True)
    return clone


__all__ = [
    'EvoArtifactAccess',
    'EvoArtifactReader',
    'edit_artifact',
    'jump_to_step',
    'read_case_artifact',
    'read_step_root',
    'rerun_case_stage',
    'rerun_step',
]
