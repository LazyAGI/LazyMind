from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol, runtime_checkable

from .controller import RunController
from .lease import ClaimLeaseReaper
from .projection import RunProjectionStore, RunQuery, ProjectionSynchronizer
from .utils import validate_nonempty
from .worker import WorkerDispatchResult

_SYNC_RUN_ID = '__sync__'


@dataclass(frozen=True)
class RuntimeSupervisorPolicy:
    max_recovery_runs_per_tick: int = 100
    dispatch_limit_per_run: int = 1
    max_runs_per_tick: int = 100
    max_projection_events_per_sync: int = 1000
    max_sync_batches_per_phase: int = 10

    def __post_init__(self) -> None:
        for name in (
            'max_recovery_runs_per_tick',
            'dispatch_limit_per_run',
            'max_runs_per_tick',
            'max_projection_events_per_sync',
            'max_sync_batches_per_phase',
        ):
            if getattr(self, name) < 1:
                raise ValueError(f'{name} must be >= 1')


@dataclass(frozen=True)
class TickNotice:
    run_id: str
    reason: str

    def __post_init__(self) -> None:
        validate_nonempty(self.run_id, 'run_id')
        validate_nonempty(self.reason, 'reason')


@dataclass(frozen=True)
class TickResult:
    started_cursor: int
    finished_cursor: int
    recovered_run_ids: tuple[str, ...]
    dispatched_run_ids: tuple[str, ...]
    dispatch_results: tuple[WorkerDispatchResult, ...]
    notices: tuple[TickNotice, ...] = ()
    partial_sync: bool = False

    def __post_init__(self) -> None:
        if self.started_cursor < 0:
            raise ValueError('started_cursor must be >= 0')
        if self.finished_cursor < self.started_cursor:
            raise ValueError('finished_cursor must be >= started_cursor')
        object.__setattr__(self, 'recovered_run_ids', tuple(sorted(set(self.recovered_run_ids))))
        object.__setattr__(self, 'dispatched_run_ids', tuple(sorted(set(self.dispatched_run_ids))))
        object.__setattr__(self, 'dispatch_results', tuple(sorted(self.dispatch_results, key=lambda item: item.run_id)))
        object.__setattr__(self, 'notices', tuple(self.notices))


@runtime_checkable
class WorkerDispatcher(Protocol):
    controller: RunController

    def dispatch_once(self, run_id: str, *, limit: int = 1) -> WorkerDispatchResult:
        ...


class RuntimeSupervisor:
    def __init__(
        self,
        controller: RunController,
        store: RunProjectionStore,
        reaper: ClaimLeaseReaper,
        worker: WorkerDispatcher,
        *,
        policy: RuntimeSupervisorPolicy | None = None,
    ) -> None:
        if reaper.controller is not controller:
            raise ValueError('reaper must share the supervisor controller')
        if worker.controller is not controller:
            raise ValueError('worker must share the supervisor controller')
        self.controller = controller
        self.store = store
        self.reaper = reaper
        self.worker = worker
        self.policy = policy or RuntimeSupervisorPolicy()
        self.synchronizer = ProjectionSynchronizer(controller, store)

    def tick(self, *, cursor: int = 0, now: float, tick_id: str, run_ids: tuple[str, ...] = ()) -> TickResult:
        if cursor < 0:
            raise ValueError('cursor must be >= 0')
        validate_nonempty(tick_id, 'tick_id')
        scope = tuple(sorted(set(run_ids)))
        started = cursor
        notices: list[TickNotice] = []
        recovered: list[str] = []
        dispatch_results: list[WorkerDispatchResult] = []
        partial_sync = False

        cursor, partial = self._sync_until_idle(cursor)
        if partial:
            notices.append(TickNotice(_SYNC_RUN_ID, 'partial_sync'))
            return TickResult(started, cursor, (), (), (), tuple(notices), True)

        recovery_runs = _apply_scope(
            sorted({candidate.run_id for candidate in self.store.list_expired_leases(now)}),
            scope,
            notices,
        )[: self.policy.max_recovery_runs_per_tick]
        for run_id in recovery_runs:
            result = self.reaper.recover_run(run_id, now=now, command_id=f'{tick_id}:lease_recovery:{run_id}')
            if result.recovered_attempt_ids or result.cancelled_attempt_ids or result.failed_attempt_ids:
                recovered.append(run_id)
            else:
                notices.append(TickNotice(run_id, 'stale_projection_hint'))

        cursor, partial = self._sync_until_idle(cursor)
        partial_sync = partial_sync or partial
        if partial:
            notices.append(TickNotice(_SYNC_RUN_ID, 'partial_sync'))
            return TickResult(started, cursor, tuple(recovered), (), (), tuple(notices), partial_sync)

        dispatch_runs = self._dispatch_candidate_run_ids(scope)
        for run_id in dispatch_runs:
            result = self.worker.dispatch_once(run_id, limit=self.policy.dispatch_limit_per_run)
            dispatch_results.append(result)
            if result.claimed == 0:
                notices.append(TickNotice(run_id, 'no_ready_work'))

        cursor, partial = self._sync_until_idle(cursor)
        partial_sync = partial_sync or partial
        if partial:
            notices.append(TickNotice(_SYNC_RUN_ID, 'partial_sync'))

        dispatched = tuple(result.run_id for result in dispatch_results if result.claimed > 0)
        return TickResult(
            started,
            cursor,
            tuple(recovered),
            dispatched,
            tuple(dispatch_results),
            tuple(notices),
            partial_sync)

    def _sync_until_idle(self, cursor: int) -> tuple[int, bool]:
        for _ in range(self.policy.max_sync_batches_per_phase):
            next_cursor = self.synchronizer.sync_since(cursor, limit=self.policy.max_projection_events_per_sync)
            if next_cursor == cursor:
                return cursor, False
            cursor = next_cursor
        return cursor, cursor < self.controller.event_log.max_seq()

    def _dispatch_candidate_run_ids(self, scope: tuple[str, ...]) -> tuple[str, ...]:
        if scope:
            return tuple(
                sorted(
                    run_id
                    for run_id in scope
                    if (projection := self.store.get(run_id)) is not None
                    and projection.status == 'running'
                    and projection.active_open_attempt_ids
                )
            )[: self.policy.max_runs_per_tick]
        projections = self.store.list_runs(
            RunQuery(statuses=('running',), has_open_attempts=True, limit=self.policy.max_runs_per_tick))
        return tuple(sorted(projection.run_id for projection in projections))


def _apply_scope(run_ids: list[str], scope: tuple[str, ...], notices: list[TickNotice]) -> list[str]:
    if not scope:
        return run_ids
    allowed = set(scope)
    scoped: list[str] = []
    for run_id in run_ids:
        if run_id in allowed:
            scoped.append(run_id)
        else:
            notices.append(TickNotice(run_id, 'scoped_out'))
    return scoped
