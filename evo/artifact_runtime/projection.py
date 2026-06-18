from __future__ import annotations

from collections import Counter
from dataclasses import dataclass
from threading import RLock
from types import MappingProxyType
from typing import Protocol

from .artifact import ArtifactKey, ArtifactRef
from .controller import AttemptStatus, ControllerState, RunController, RunStatus
from .utils import validate_nonempty

_ACTIVE_LEASE_RUN_STATUSES = frozenset({'running', 'paused', 'cancel_requested'})
_RUN_STATUSES = frozenset({'pending', 'running', 'paused', 'completed', 'failed', 'cancel_requested', 'cancelled'})
_LEASE_ATTEMPT_STATUSES = frozenset({'claimed', 'cancel_requested'})
_OPEN_ATTEMPT_STATUSES = frozenset({'pending', 'claimed', 'cancel_requested'})
_SUMMARY_REASON_PRIORITY = ('failed', 'stale', 'cancelled')


@dataclass(frozen=True)
class AttemptProjection:
    run_id: str
    attempt_id: str
    op_id: str
    plan_version: int
    status: AttemptStatus
    output_artifacts: tuple[ArtifactKey, ...]
    claim_id: str = ''
    worker_id: str = ''
    lease_expires_at: float = 0.0
    reason: str = ''

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')
        validate_nonempty(self.attempt_id, 'attempt_id')
        validate_nonempty(self.op_id, 'op_id')
        object.__setattr__(self, 'output_artifacts', tuple(sorted(self.output_artifacts)))


@dataclass(frozen=True, init=False)
class ArtifactProducerProjection:
    key: ArtifactKey
    ref: ArtifactRef
    run_id: str
    attempt_id: str
    op_id: str
    plan_version: int

    def __init__(
        self,
        key: ArtifactKey,
        ref: ArtifactRef,
        run_id: str,
        attempt_id: str,
        op_id: str,
        plan_version: int,
    ) -> None:
        object.__setattr__(self, 'key', key)
        object.__setattr__(self, 'ref', ref)
        object.__setattr__(self, 'run_id', run_id)
        object.__setattr__(self, 'attempt_id', attempt_id)
        object.__setattr__(self, 'op_id', op_id)
        object.__setattr__(self, 'plan_version', plan_version)
        self.__post_init__()

    def __post_init__(self) -> None:
        if not isinstance(self.key, ArtifactKey):
            raise TypeError('artifact producer key must be an ArtifactKey')
        validate_nonempty(self.run_id, 'run_id')
        validate_nonempty(self.attempt_id, 'attempt_id')
        validate_nonempty(self.op_id, 'op_id')

    @property
    def artifact_id(self) -> str:
        return self.key.artifact_id


@dataclass(frozen=True)
class ExpiredLeaseCandidate:
    run_id: str
    attempt_id: str
    lease_expires_at: float
    worker_id: str = ''

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')
        validate_nonempty(self.attempt_id, 'attempt_id')


@dataclass(frozen=True)
class RunProjection:
    run_id: str
    status: RunStatus
    active_plan_version: int | None
    epoch: int
    active_attempt_counts: dict[AttemptStatus, int]
    active_open_attempt_ids: tuple[str, ...]
    target_artifacts: tuple[ArtifactKey, ...]
    attempts: tuple[AttemptProjection, ...] = ()
    artifact_producers: tuple[ArtifactProducerProjection, ...] = ()
    summary_reason: str = ''
    updated_seq: int = 0

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')
        object.__setattr__(self, 'active_attempt_counts', MappingProxyType(dict(self.active_attempt_counts)))
        object.__setattr__(self, 'active_open_attempt_ids', tuple(sorted(self.active_open_attempt_ids)))
        object.__setattr__(self, 'target_artifacts', tuple(sorted(self.target_artifacts)))
        object.__setattr__(self, 'attempts', tuple(
            sorted(self.attempts, key=lambda item: (item.plan_version, item.op_id, item.attempt_id))))
        object.__setattr__(
            self,
            'artifact_producers',
            tuple(sorted(self.artifact_producers, key=lambda item: (item.key, item.ref.version, item.attempt_id))),
        )


@dataclass(frozen=True)
class RunQuery:
    statuses: tuple[RunStatus, ...] = ()
    has_open_attempts: bool | None = None
    updated_after_seq: int | None = None
    limit: int = 100

    def __post_init__(self) -> None:
        if self.limit < 1:
            raise ValueError('limit must be >= 1')
        if self.updated_after_seq is not None and self.updated_after_seq < 0:
            raise ValueError('updated_after_seq must be >= 0')
        unknown = set(self.statuses) - _RUN_STATUSES
        if unknown:
            raise ValueError(f'unknown run status: {sorted(unknown)[0]}')
        object.__setattr__(self, 'statuses', tuple(sorted(set(self.statuses))))


class RunProjectionStore(Protocol):
    def upsert(self, projection: RunProjection) -> None:
        ...

    def get(self, run_id: str) -> RunProjection | None:
        ...

    def list_runs(self, query: RunQuery) -> tuple[RunProjection, ...]:
        ...

    def list_open_attempts(self, run_id: str) -> tuple[AttemptProjection, ...]:
        ...

    def list_expired_leases(self, now: float) -> tuple[ExpiredLeaseCandidate, ...]:
        ...

    def list_artifact_producers(
        self,
        artifact_id: str | None = None,
        *,
        key: ArtifactKey | None = None,
        partition: str | None = None,
    ) -> tuple[ArtifactProducerProjection, ...]:
        ...


class InMemoryRunProjectionStore:
    def __init__(self) -> None:
        self._lock = RLock()
        self._runs: dict[str, RunProjection] = {}

    def upsert(self, projection: RunProjection) -> None:
        with self._lock:
            self._runs[projection.run_id] = projection

    def get(self, run_id: str) -> RunProjection | None:
        with self._lock:
            return self._runs.get(run_id)

    def list_runs(self, query: RunQuery) -> tuple[RunProjection, ...]:
        with self._lock:
            items = sorted(self._runs.values(), key=lambda item: (item.updated_seq, item.run_id))
        if query.statuses:
            statuses = set(query.statuses)
            items = [item for item in items if item.status in statuses]
        if query.has_open_attempts is not None:
            items = [item for item in items if bool(item.active_open_attempt_ids) == query.has_open_attempts]
        if query.updated_after_seq is not None:
            items = [item for item in items if item.updated_seq > query.updated_after_seq]
        return tuple(items[: query.limit])

    def list_open_attempts(self, run_id: str) -> tuple[AttemptProjection, ...]:
        projection = self.get(run_id)
        if projection is None:
            return ()
        ids = set(projection.active_open_attempt_ids)
        return tuple(item for item in projection.attempts if item.attempt_id in ids)

    def list_expired_leases(self, now: float) -> tuple[ExpiredLeaseCandidate, ...]:
        with self._lock:
            projections = tuple(self._runs.values())
        candidates: list[ExpiredLeaseCandidate] = []
        for projection in projections:
            if projection.status not in _ACTIVE_LEASE_RUN_STATUSES:
                continue
            active_ids = set(projection.active_open_attempt_ids)
            for attempt in projection.attempts:
                if attempt.attempt_id not in active_ids:
                    continue
                if attempt.status not in _LEASE_ATTEMPT_STATUSES:
                    continue
                if attempt.lease_expires_at <= 0 or attempt.lease_expires_at > now:
                    continue
                candidates.append(ExpiredLeaseCandidate(attempt.run_id, attempt.attempt_id,
                                  attempt.lease_expires_at, attempt.worker_id))
        return tuple(sorted(candidates, key=lambda item: (item.lease_expires_at, item.run_id, item.attempt_id)))

    def list_artifact_producers(
        self,
        artifact_id: str | None = None,
        *,
        key: ArtifactKey | None = None,
        partition: str | None = None,
    ) -> tuple[ArtifactProducerProjection, ...]:
        with self._lock:
            producers = tuple(producer for projection in self._runs.values()
                              for producer in projection.artifact_producers)
        if key is not None:
            producers = tuple(item for item in producers if item.key == key)
        if artifact_id is not None:
            producers = tuple(item for item in producers if item.artifact_id == artifact_id)
        if partition is not None:
            producers = tuple(item for item in producers if item.key.partition == partition)
        return tuple(sorted(producers, key=lambda item: (item.key, item.ref.version, item.attempt_id)))


class ProjectionBuilder:
    def from_state(self, state: ControllerState, *, updated_seq: int) -> RunProjection:
        if not state.run_exists:
            raise ValueError(f'unknown run: {state.run.run_id}')
        attempts = tuple(_attempt_projection(attempt) for attempt in state.attempts.values())
        active_attempts = _active_latest_attempts(state)
        counts = Counter(attempt.status for attempt in active_attempts)
        open_ids = tuple(
            sorted(attempt.attempt_id for attempt in active_attempts if attempt.status in _OPEN_ATTEMPT_STATUSES))
        target_artifacts = state.active_plan.target_artifacts if state.active_plan is not None else ()
        producers = tuple(
            ArtifactProducerProjection(key, ref, attempt.run_id, attempt.attempt_id,
                                       attempt.op_id, attempt.plan_version)
            for attempt in state.attempts.values()
            if attempt.status == 'completed'
            for key, ref in attempt.output_refs.items()
        )
        return RunProjection(
            state.run.run_id,
            state.run.status,
            state.run.active_plan_version,
            state.run.epoch,
            dict(counts),
            open_ids,
            target_artifacts,
            attempts,
            producers,
            _summary_reason(active_attempts),
            updated_seq,
        )


class ProjectionSynchronizer:
    """Best-effort visibility sync, not a strong snapshot under concurrent appends.

    FC-8 projections are rebuildable read models. If events are appended between
    dirty-run discovery and ``controller.state(run_id)``, a later sync will
    converge the projection.
    """

    def __init__(self, controller: RunController, store: RunProjectionStore) -> None:
        self.controller = controller
        self.store = store
        self.builder = ProjectionBuilder()

    def sync_run(self, run_id: str) -> RunProjection:
        events = tuple(self.controller.event_log.scan(run_id))
        if not events:
            raise ValueError(f'unknown run: {run_id}')
        projection = self.builder.from_state(self.controller.state(
            run_id), updated_seq=max(event.seq for event in events))
        self.store.upsert(projection)
        return projection

    def sync_since(self, seq: int = 0, *, limit: int = 1000) -> int:
        events = self.controller.event_log.scan_since(seq, limit=limit)
        if not events:
            return seq
        for run_id in sorted({event.run_id for event in events}):
            self.sync_run(run_id)
        return max(event.seq for event in events)


def _attempt_projection(attempt) -> AttemptProjection:
    return AttemptProjection(
        attempt.run_id,
        attempt.attempt_id,
        attempt.op_id,
        attempt.plan_version,
        attempt.status,
        tuple(attempt.output_artifact_keys),
        attempt.claim_id,
        attempt.worker_id,
        attempt.lease_expires_at,
        attempt.reason,
    )


def _active_latest_attempts(state: ControllerState):
    active = state.run.active_plan_version
    if active is None:
        return ()
    return tuple(
        attempt
        for (plan_version, _), attempt in state.latest_attempt_by_op.items()
        if plan_version == active
    )


def _summary_reason(attempts) -> str:
    for status in _SUMMARY_REASON_PRIORITY:
        for attempt in sorted(
            (item for item in attempts if item.status == status and item.reason),
                key=lambda item: item.attempt_id):
            return attempt.reason
    return ''
