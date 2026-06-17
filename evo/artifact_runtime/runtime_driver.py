from __future__ import annotations

import time
from collections.abc import Callable
from dataclasses import dataclass, replace
from threading import RLock
from typing import Literal, Protocol

from .supervisor import RuntimeSupervisor, TickResult
from .utils import validate_nonempty

DEFAULT_CHECKPOINT_ID = 'default'

CheckpointSaveStatus = Literal['saved', 'stale']
DriverTickStatus = Literal['saved', 'checkpoint_stale']
DriverRunStatus = Literal['idle', 'max_ticks', 'cancelled', 'checkpoint_stale']
_CHECKPOINT_SAVE_STATUSES = frozenset({'saved', 'stale'})
_DRIVER_TICK_STATUSES = frozenset({'saved', 'checkpoint_stale'})
_DRIVER_RUN_STATUSES = frozenset({'idle', 'max_ticks', 'cancelled', 'checkpoint_stale'})


@dataclass(frozen=True)
class RuntimeDriverPolicy:
    idle_sleep_seconds: float = 1.0
    busy_sleep_seconds: float = 0.0
    max_ticks: int = 100
    max_consecutive_idle_ticks: int = 1

    def __post_init__(self) -> None:
        if self.idle_sleep_seconds < 0:
            raise ValueError('idle_sleep_seconds must be >= 0')
        if self.busy_sleep_seconds < 0:
            raise ValueError('busy_sleep_seconds must be >= 0')
        if self.max_ticks < 1:
            raise ValueError('max_ticks must be >= 1')
        if self.max_consecutive_idle_ticks < 1:
            raise ValueError('max_consecutive_idle_ticks must be >= 1')


@dataclass(frozen=True)
class RuntimeDriverCheckpoint:
    checkpoint_id: str = DEFAULT_CHECKPOINT_ID
    revision: int = 0
    cursor: int = 0
    last_tick_id: str = ''
    last_tick_started_at: float = 0.0
    last_tick_finished_at: float = 0.0
    consecutive_idle_ticks: int = 0

    def __post_init__(self) -> None:
        validate_nonempty(self.checkpoint_id, 'checkpoint_id')
        if self.revision < 0:
            raise ValueError('revision must be >= 0')
        if self.cursor < 0:
            raise ValueError('cursor must be >= 0')
        if self.last_tick_started_at < 0:
            raise ValueError('last_tick_started_at must be >= 0')
        if self.last_tick_finished_at < 0:
            raise ValueError('last_tick_finished_at must be >= 0')
        if self.consecutive_idle_ticks < 0:
            raise ValueError('consecutive_idle_ticks must be >= 0')


@dataclass(frozen=True)
class RuntimeDriverCheckpointSaveResult:
    status: CheckpointSaveStatus
    checkpoint: RuntimeDriverCheckpoint | None = None

    def __post_init__(self) -> None:
        if self.status not in _CHECKPOINT_SAVE_STATUSES:
            raise ValueError(f'invalid checkpoint save status: {self.status}')
        if self.status == 'saved' and self.checkpoint is None:
            raise ValueError('saved checkpoint result requires checkpoint')
        if self.status == 'stale' and self.checkpoint is not None:
            raise ValueError('stale checkpoint result must not include checkpoint')


class RuntimeDriverCheckpointStore(Protocol):
    def load(self, checkpoint_id: str = DEFAULT_CHECKPOINT_ID) -> RuntimeDriverCheckpoint:
        ...

    def save(
        self,
        checkpoint: RuntimeDriverCheckpoint,
        *,
        expected_revision: int,
    ) -> RuntimeDriverCheckpointSaveResult:
        ...


@dataclass(frozen=True)
class RuntimeDriverTickResult:
    status: DriverTickStatus
    tick_result: TickResult
    checkpoint: RuntimeDriverCheckpoint | None = None

    def __post_init__(self) -> None:
        if self.status not in _DRIVER_TICK_STATUSES:
            raise ValueError(f'invalid runtime driver tick status: {self.status}')
        if self.status == 'saved' and self.checkpoint is None:
            raise ValueError('saved tick result requires checkpoint')
        if self.status == 'checkpoint_stale' and self.checkpoint is not None:
            raise ValueError('stale tick result must not include checkpoint')


@dataclass(frozen=True)
class RuntimeDriverResult:
    status: DriverRunStatus
    ticks: int
    cursor: int
    partial_sync: bool = False
    recovered_run_ids: tuple[str, ...] = ()
    dispatched_run_ids: tuple[str, ...] = ()
    tick_results: tuple[RuntimeDriverTickResult, ...] = ()

    def __post_init__(self) -> None:
        if self.status not in _DRIVER_RUN_STATUSES:
            raise ValueError(f'invalid runtime driver result status: {self.status}')
        if self.ticks < 0:
            raise ValueError('ticks must be >= 0')
        if self.cursor < 0:
            raise ValueError('cursor must be >= 0')
        object.__setattr__(self, 'recovered_run_ids', tuple(sorted(set(self.recovered_run_ids))))
        object.__setattr__(self, 'dispatched_run_ids', tuple(sorted(set(self.dispatched_run_ids))))
        object.__setattr__(self, 'tick_results', tuple(self.tick_results))


class InMemoryRuntimeDriverCheckpointStore:
    def __init__(self) -> None:
        self._lock = RLock()
        self._checkpoints: dict[str, RuntimeDriverCheckpoint] = {}

    def load(self, checkpoint_id: str = DEFAULT_CHECKPOINT_ID) -> RuntimeDriverCheckpoint:
        validate_nonempty(checkpoint_id, 'checkpoint_id')
        with self._lock:
            checkpoint = self._checkpoints.get(checkpoint_id)
            if checkpoint is None:
                checkpoint = RuntimeDriverCheckpoint(checkpoint_id=checkpoint_id)
                self._checkpoints[checkpoint_id] = checkpoint
            return checkpoint

    def save(
        self,
        checkpoint: RuntimeDriverCheckpoint,
        *,
        expected_revision: int,
    ) -> RuntimeDriverCheckpointSaveResult:
        _validate_save_inputs(checkpoint, expected_revision)
        with self._lock:
            current = self.load(checkpoint.checkpoint_id)
            if current.revision != expected_revision:
                return RuntimeDriverCheckpointSaveResult('stale')
            if checkpoint.cursor < current.cursor:
                raise ValueError('cursor cannot move backwards')
            saved = replace(checkpoint, revision=current.revision + 1)
            self._checkpoints[checkpoint.checkpoint_id] = saved
            return RuntimeDriverCheckpointSaveResult('saved', saved)


class DurableRuntimeDriver:
    def __init__(
        self,
        supervisor: RuntimeSupervisor,
        checkpoint_store: RuntimeDriverCheckpointStore,
        *,
        driver_id: str,
        checkpoint_id: str = DEFAULT_CHECKPOINT_ID,
        policy: RuntimeDriverPolicy | None = None,
        clock: Callable[[], float] = time.time,
    ) -> None:
        validate_nonempty(driver_id, 'driver_id')
        validate_nonempty(checkpoint_id, 'checkpoint_id')
        self.supervisor = supervisor
        self.checkpoint_store = checkpoint_store
        self.driver_id = driver_id
        self.checkpoint_id = checkpoint_id
        self.policy = policy or RuntimeDriverPolicy()
        self.clock = clock

    def tick_once(self, *, run_ids: tuple[str, ...] = ()) -> RuntimeDriverTickResult:
        checkpoint = self.checkpoint_store.load(self.checkpoint_id)
        tick_id = self._tick_id(checkpoint)
        started_at = self.clock()
        tick_result = self.supervisor.tick(
            cursor=checkpoint.cursor,
            now=started_at,
            tick_id=tick_id,
            run_ids=run_ids,
        )
        finished_at = self.clock()
        busy = is_tick_busy(tick_result)
        next_checkpoint = RuntimeDriverCheckpoint(
            checkpoint.checkpoint_id,
            checkpoint.revision,
            tick_result.finished_cursor,
            tick_id,
            started_at,
            finished_at,
            0 if busy else checkpoint.consecutive_idle_ticks + 1,
        )
        saved = self.checkpoint_store.save(next_checkpoint, expected_revision=checkpoint.revision)
        if saved.status == 'stale':
            return RuntimeDriverTickResult('checkpoint_stale', tick_result)
        return RuntimeDriverTickResult('saved', tick_result, saved.checkpoint)

    def run_until_idle(
        self,
        *,
        run_ids: tuple[str, ...] = (),
        stop_requested: Callable[[], bool] | None = None,
        sleep_fn: Callable[[float], None] | None = time.sleep,
    ) -> RuntimeDriverResult:
        should_stop = stop_requested or (lambda: False)
        if should_stop():
            checkpoint = self.checkpoint_store.load(self.checkpoint_id)
            return RuntimeDriverResult('cancelled', 0, checkpoint.cursor)

        tick_results: list[RuntimeDriverTickResult] = []
        for _ in range(self.policy.max_ticks):
            tick = self.tick_once(run_ids=run_ids)
            tick_results.append(tick)
            if tick.status == 'checkpoint_stale':
                checkpoint = self.checkpoint_store.load(self.checkpoint_id)
                return _run_result('checkpoint_stale', tick_results, checkpoint.cursor)
            checkpoint = tick.checkpoint
            assert checkpoint is not None
            if checkpoint.consecutive_idle_ticks >= self.policy.max_consecutive_idle_ticks:
                return _run_result('idle', tick_results, checkpoint.cursor)
            if should_stop():
                return _run_result('cancelled', tick_results, checkpoint.cursor)
            sleep_seconds = self.policy.busy_sleep_seconds if is_tick_busy(
                tick.tick_result) else self.policy.idle_sleep_seconds
            if sleep_fn is not None and sleep_seconds > 0:
                sleep_fn(sleep_seconds)

        checkpoint = self.checkpoint_store.load(self.checkpoint_id)
        return _run_result('max_ticks', tick_results, checkpoint.cursor)

    def _tick_id(self, checkpoint: RuntimeDriverCheckpoint) -> str:
        return f'{self.driver_id}:tick:{checkpoint.revision + 1}'


def is_tick_busy(tick_result: TickResult) -> bool:
    return (
        tick_result.partial_sync
        or tick_result.finished_cursor > tick_result.started_cursor
        or bool(tick_result.recovered_run_ids)
        or bool(tick_result.dispatched_run_ids)
    )


def _validate_save_inputs(checkpoint: RuntimeDriverCheckpoint, expected_revision: int) -> None:
    if expected_revision < 0:
        raise ValueError('expected_revision must be >= 0')
    if checkpoint.revision < 0:
        raise ValueError('checkpoint.revision must be >= 0')
    if checkpoint.cursor < 0:
        raise ValueError('checkpoint.cursor must be >= 0')


def _run_result(status: DriverRunStatus, ticks: list[RuntimeDriverTickResult], cursor: int) -> RuntimeDriverResult:
    return RuntimeDriverResult(
        status,
        len(ticks),
        cursor,
        any(item.tick_result.partial_sync for item in ticks),
        tuple(run_id for item in ticks for run_id in item.tick_result.recovered_run_ids),
        tuple(run_id for item in ticks for run_id in item.tick_result.dispatched_run_ids),
        tuple(ticks),
    )
