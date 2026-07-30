from __future__ import annotations

import json
from collections.abc import Mapping
from dataclasses import dataclass, field
from types import MappingProxyType
from typing import Literal

from .artifact import ArtifactKey, ArtifactRef, PartitionSet
from .errors import DefinitionError
from .utils import _string, _text


RunStatus = Literal[
    'created',
    'running',
    'pausing',
    'paused',
    'cancelling',
    'cancelled',
    'failed',
    'completed',
]

AttemptStatus = Literal[
    'scheduled',
    'running',
    'cancelling',
    'cancelled',
    'succeeded',
    'failed',
    'interrupted',
    'discarded',
]

RetryStatus = Literal['pending', 'fulfilled', 'cancelled']


@dataclass(frozen=True)
class InvocationSnapshot:
    invocation_id: str
    operation_id: str
    partition_key: str = ''

    def __post_init__(self) -> None:
        _text(self.invocation_id, 'invocation_id')
        _text(self.operation_id, 'operation_id')
        _string(self.partition_key, 'partition_key')


@dataclass(frozen=True)
class RuntimeErrorInfo:
    kind: str
    message: str

    def __post_init__(self) -> None:
        _text(self.kind, 'runtime error kind')
        _text(self.message, 'runtime error message')


@dataclass(frozen=True)
class ProgressUpdate:
    phase: str
    message: str = ''
    current: int | None = None
    total: int | None = None
    detail: Mapping[str, object] = field(default_factory=dict)

    def __post_init__(self) -> None:
        _text(self.phase, 'progress phase')
        _string(self.message, 'progress message')
        if self.current is not None and (
            not isinstance(self.current, int) or isinstance(self.current, bool) or self.current < 0
        ):
            raise DefinitionError('progress current must be a non-negative int or None')
        if self.total is not None and (
            not isinstance(self.total, int) or isinstance(self.total, bool) or self.total < 0
        ):
            raise DefinitionError('progress total must be a non-negative int or None')
        if self.current is not None and self.total is not None and self.current > self.total:
            raise DefinitionError('progress current cannot exceed total')
        detail = dict(self.detail)
        try:
            json.dumps(detail, ensure_ascii=False, sort_keys=True)
        except (TypeError, ValueError) as exc:
            raise DefinitionError('progress detail must be JSON-serializable') from exc
        object.__setattr__(self, 'detail', MappingProxyType(detail))


@dataclass(frozen=True)
class AttemptSnapshot:
    attempt_id: str
    invocation_id: str
    operation_id: str
    partition_key: str
    status: AttemptStatus
    created_at: float
    started_at: float | None = None
    finished_at: float | None = None
    error: RuntimeErrorInfo | None = None
    input_refs: tuple[ArtifactRef, ...] = ()
    output_keys: tuple[ArtifactKey, ...] = ()
    retry_request_id: str = ''

    def __post_init__(self) -> None:
        _text(self.attempt_id, 'attempt_id')
        _text(self.invocation_id, 'invocation_id')
        _text(self.operation_id, 'operation_id')
        _string(self.partition_key, 'partition_key')
        if self.status not in {
            'scheduled', 'running', 'cancelling', 'cancelled', 'succeeded',
            'failed', 'interrupted', 'discarded',
        }:
            raise DefinitionError(f'unknown attempt status: {self.status}')
        for name, value in (
            ('created_at', self.created_at),
            ('started_at', self.started_at),
            ('finished_at', self.finished_at),
        ):
            if value is not None and (not isinstance(value, (int, float)) or isinstance(value, bool)):
                raise TypeError(f'{name} must be a number or None')
        if self.status == 'failed' and self.error is None:
            raise DefinitionError('failed attempt requires error details')
        if self.status != 'failed' and self.error is not None:
            raise DefinitionError('attempt error details are only valid for failed status')
        input_refs = tuple(self.input_refs)
        output_keys = tuple(self.output_keys)
        if not all(isinstance(ref, ArtifactRef) for ref in input_refs):
            raise TypeError('attempt input_refs must contain ArtifactRef values')
        if not all(isinstance(key, ArtifactKey) for key in output_keys):
            raise TypeError('attempt output_keys must contain ArtifactKey values')
        _string(self.retry_request_id, 'retry_request_id')
        object.__setattr__(self, 'input_refs', input_refs)
        object.__setattr__(self, 'output_keys', output_keys)


@dataclass(frozen=True)
class ArtifactRetryRequest:
    request_id: str
    artifact_key: ArtifactKey
    base_ref: ArtifactRef
    status: RetryStatus
    created_at: float
    result_ref: ArtifactRef | None = None

    def __post_init__(self) -> None:
        _text(self.request_id, 'retry request_id')
        if not isinstance(self.artifact_key, ArtifactKey):
            raise TypeError('retry artifact_key must be ArtifactKey')
        if not isinstance(self.base_ref, ArtifactRef):
            raise TypeError('retry base_ref must be ArtifactRef')
        if self.base_ref.key != self.artifact_key:
            raise DefinitionError('retry base_ref must identify artifact_key')
        if self.status not in {'pending', 'fulfilled', 'cancelled'}:
            raise DefinitionError(f'unknown retry status: {self.status}')
        if not isinstance(self.created_at, (int, float)) or isinstance(self.created_at, bool):
            raise TypeError('retry created_at must be a number')
        if self.status == 'fulfilled':
            if not isinstance(self.result_ref, ArtifactRef):
                raise DefinitionError('fulfilled retry requires result_ref')
            if self.result_ref.key != self.artifact_key:
                raise DefinitionError('retry result_ref must identify artifact_key')
            if self.result_ref.version <= self.base_ref.version:
                raise DefinitionError('retry result_ref must be newer than base_ref')
        elif self.result_ref is not None:
            raise DefinitionError('only fulfilled retry can contain result_ref')


@dataclass(frozen=True)
class ProgressEvent:
    attempt_id: str
    sequence: int
    update: ProgressUpdate
    created_at: float

    def __post_init__(self) -> None:
        _text(self.attempt_id, 'attempt_id')
        if not isinstance(self.sequence, int) or isinstance(self.sequence, bool) or self.sequence <= 0:
            raise DefinitionError('progress sequence must be a positive int')
        if not isinstance(self.update, ProgressUpdate):
            raise TypeError('update must be ProgressUpdate')
        if not isinstance(self.created_at, (int, float)) or isinstance(self.created_at, bool):
            raise TypeError('created_at must be a number')


@dataclass(frozen=True)
class RuntimeSnapshot:
    run_id: str
    status: RunStatus = 'created'
    running: tuple[InvocationSnapshot, ...] = ()
    ready_count: int = 0
    completed_artifacts: Mapping[ArtifactKey, ArtifactRef] = field(default_factory=dict)
    partition_sets: Mapping[ArtifactKey, PartitionSet] = field(default_factory=dict)
    error: RuntimeErrorInfo | None = None
    active_attempts: tuple[AttemptSnapshot, ...] = ()
    awaiting_artifacts: tuple[ArtifactKey, ...] = ()

    def __post_init__(self) -> None:
        _text(self.run_id, 'run_id')
        if self.status not in {
            'created', 'running', 'pausing', 'paused', 'cancelling',
            'cancelled', 'failed', 'completed',
        }:
            raise DefinitionError(f'unknown run status: {self.status}')

        running = tuple(self.running)
        if not all(isinstance(item, InvocationSnapshot) for item in running):
            raise TypeError('running must contain InvocationSnapshot values')
        if len({item.invocation_id for item in running}) != len(running):
            raise DefinitionError('running invocation ids must be unique')

        if not isinstance(self.ready_count, int) or isinstance(self.ready_count, bool):
            raise TypeError('ready_count must be int')
        if self.ready_count < 0:
            raise DefinitionError('ready_count must be >= 0')

        completed = dict(self.completed_artifacts)
        for key, ref in completed.items():
            if not isinstance(key, ArtifactKey) or not isinstance(ref, ArtifactRef):
                raise TypeError('completed_artifacts must map ArtifactKey to ArtifactRef')
            if key != ref.key:
                raise DefinitionError('completed artifact key must match its ref')

        partition_sets = dict(self.partition_sets)
        for key, partitions in partition_sets.items():
            if not isinstance(key, ArtifactKey) or key.partition_key:
                raise TypeError('partition_sets keys must be scalar ArtifactKey values')
            if not isinstance(partitions, PartitionSet):
                raise TypeError('partition_sets values must be PartitionSet')

        if self.error is not None and not isinstance(self.error, RuntimeErrorInfo):
            raise TypeError('error must be RuntimeErrorInfo or None')
        if self.status == 'failed' and self.error is None:
            raise DefinitionError('failed runtime snapshot requires error details')
        if self.status != 'failed' and self.error is not None:
            raise DefinitionError('runtime error details are only valid for failed status')
        if self.status in {'created', 'paused', 'cancelled', 'failed', 'completed'} and running:
            raise DefinitionError(f'{self.status} runtime snapshot cannot contain running invocations')
        if self.status in {'cancelled', 'failed', 'completed'} and self.ready_count:
            raise DefinitionError(f'{self.status} runtime snapshot cannot contain ready invocations')

        attempts = tuple(self.active_attempts)
        if not all(isinstance(attempt, AttemptSnapshot) for attempt in attempts):
            raise TypeError('attempts must contain AttemptSnapshot values')
        if len({attempt.attempt_id for attempt in attempts}) != len(attempts):
            raise DefinitionError('attempt ids must be unique')

        awaiting = tuple(self.awaiting_artifacts)
        if not all(isinstance(key, ArtifactKey) for key in awaiting):
            raise TypeError('awaiting_artifacts must contain ArtifactKey values')
        if len(set(awaiting)) != len(awaiting):
            raise DefinitionError('awaiting artifact keys must be unique')

        object.__setattr__(self, 'running', running)
        object.__setattr__(self, 'completed_artifacts', MappingProxyType(completed))
        object.__setattr__(self, 'partition_sets', MappingProxyType(partition_sets))
        object.__setattr__(self, 'active_attempts', attempts)
        object.__setattr__(self, 'awaiting_artifacts', awaiting)


__all__ = [
    'ArtifactRetryRequest', 'AttemptSnapshot', 'AttemptStatus', 'InvocationSnapshot',
    'ProgressEvent', 'ProgressUpdate', 'RetryStatus', 'RunStatus', 'RuntimeErrorInfo',
    'RuntimeSnapshot',
]
