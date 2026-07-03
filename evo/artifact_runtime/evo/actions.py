from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from hashlib import sha256
from pickle import HIGHEST_PROTOCOL, dumps
from typing import TypeAlias

from ..kernel import ArtifactRef, StoreResult

from .flow import EvoFlowSpec
from .progress import progress_view
from .use_cases import (
    EvoArtifactAccess,
    EvoArtifactReader,
    edit_artifact,
    jump_to_step,
    read_case_artifact,
    read_step_root,
    rerun_case_stage,
    rerun_step,
)


@dataclass(frozen=True)
class ReadStepRoot:
    step: str


@dataclass(frozen=True)
class ReadCaseArtifact:
    case_id: str
    kind: str


@dataclass(frozen=True)
class ReadProgressSnapshot:
    pass


@dataclass(frozen=True)
class EditArtifact:
    ref: ArtifactRef
    pointer: str
    value: object
    idempotency_key: str


@dataclass(frozen=True)
class RerunCaseStage:
    case_id: str
    stage: str
    idempotency_key: str


@dataclass(frozen=True)
class RerunStep:
    step: str
    idempotency_key: str


@dataclass(frozen=True)
class InvalidateFromStep:
    step: str
    idempotency_key: str


EvoQuery: TypeAlias = ReadStepRoot | ReadCaseArtifact | ReadProgressSnapshot
EvoMutation: TypeAlias = EditArtifact | RerunCaseStage | RerunStep | InvalidateFromStep


def dispatch_evo_query(adapter: EvoArtifactReader, spec: EvoFlowSpec, run_id: str, query: EvoQuery):
    match query:
        case ReadStepRoot(step):
            return read_step_root(adapter, spec, run_id, step)
        case ReadCaseArtifact(case_id, kind):
            return read_case_artifact(adapter, spec, run_id, case_id, kind)
        case ReadProgressSnapshot():
            return progress_view(adapter, spec, run_id)
    raise TypeError(f'unsupported EvoQuery: {type(query).__name__}')


def dispatch_evo_mutation(adapter: EvoArtifactAccess, spec: EvoFlowSpec,
                          run_id: str, mutation: EvoMutation) -> StoreResult:
    match mutation:
        case EditArtifact(ref, pointer, value, idempotency_key):
            return edit_artifact(adapter, spec, run_id, ref, pointer, value, idempotency_key=idempotency_key)
        case RerunCaseStage(case_id, stage, idempotency_key):
            return rerun_case_stage(adapter, spec, run_id, case_id, stage, idempotency_key=idempotency_key)
        case RerunStep(step, idempotency_key):
            return rerun_step(adapter, spec, run_id, step, idempotency_key=idempotency_key)
        case InvalidateFromStep(step, idempotency_key):
            return jump_to_step(adapter, spec, run_id, step, idempotency_key=idempotency_key)
    raise TypeError(f'unsupported EvoMutation: {type(mutation).__name__}')


def mutation_idempotency_key(mutation: EvoMutation) -> str:
    match mutation:
        case EditArtifact(_, _, _, idempotency_key):
            return idempotency_key
        case RerunCaseStage(_, _, idempotency_key):
            return idempotency_key
        case RerunStep(_, idempotency_key):
            return idempotency_key
        case InvalidateFromStep(_, idempotency_key):
            return idempotency_key
    raise TypeError(f'unsupported EvoMutation: {type(mutation).__name__}')


def mutation_request_fingerprint(mutation: EvoMutation) -> Mapping[str, object]:
    match mutation:
        case EditArtifact(ref, pointer, value, idempotency_key):
            return {
                'idempotency_key': idempotency_key,
                'kind': 'EditArtifact',
                'pointer': pointer,
                'ref': _ref_json(ref),
                'value_pickle_sha256': sha256(dumps(value, protocol=HIGHEST_PROTOCOL)).hexdigest(),
            }
        case RerunCaseStage(case_id, stage, idempotency_key):
            return {'case_id': case_id, 'idempotency_key': idempotency_key, 'kind': 'RerunCaseStage', 'stage': stage}
        case RerunStep(step, idempotency_key):
            return {'idempotency_key': idempotency_key, 'kind': 'RerunStep', 'step': step}
        case InvalidateFromStep(step, idempotency_key):
            return {'idempotency_key': idempotency_key, 'kind': 'InvalidateFromStep', 'step': step}
    raise TypeError(f'unsupported EvoMutation: {type(mutation).__name__}')


def mutation_receipt_outcome(result: StoreResult) -> Mapping[str, object]:
    if not isinstance(result, StoreResult):
        raise TypeError('mutation result must be StoreResult')
    return {'refs': [_ref_json(ref) for ref in result.refs], 'status': result.status}


def _ref_json(ref: ArtifactRef) -> list[object]:
    return [ref.key.artifact_id, ref.key.partition, ref.version]


__all__ = [
    'EditArtifact',
    'EvoMutation',
    'EvoQuery',
    'InvalidateFromStep',
    'ReadCaseArtifact',
    'ReadProgressSnapshot',
    'ReadStepRoot',
    'RerunCaseStage',
    'RerunStep',
    'dispatch_evo_mutation',
    'dispatch_evo_query',
    'mutation_idempotency_key',
    'mutation_receipt_outcome',
    'mutation_request_fingerprint',
]
