from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator, Awaitable, Callable, Iterable, Mapping, Sequence
from contextlib import asynccontextmanager
from dataclasses import dataclass
from pathlib import Path
from types import TracebackType
from typing import Self
from weakref import WeakSet

from .artifact import ArtifactCommit, ArtifactKey, ArtifactRecord, ArtifactRef
from .errors import DefinitionError
from .operation import Operation
from .planning import (
    PlanAwaiting,
    PlanReady,
    RuntimeDefinition,
    compile_operations,
    obsolete_retries,
    plan_next,
)
from .session import RunSession
from .state import (
    ArtifactRetryRequest,
    AttemptSnapshot,
    InvocationSnapshot,
    ProgressEvent,
    RuntimeSnapshot,
)
from .store import ArtifactStore
from .utils import _as_exception, _positive_int, _positive_number, _text


_ACTIVE_STATUSES = frozenset({'running', 'pausing', 'cancelling'})


@dataclass(frozen=True, slots=True)
class _SessionEntry:
    session: RunSession
    task: asyncio.Task[None]


class ArtifactRuntime:
    def __init__(self, store: ArtifactStore, definition: RuntimeDefinition, *,
                 max_concurrency: int, terminate_timeout: float
                 ) -> None:
        self._store = store
        self._definition = definition
        self._max_run_concurrency = max_concurrency
        self._terminate_timeout = terminate_timeout
        self._sessions: dict[str, _SessionEntry] = {}
        self._reported_session_tasks: WeakSet[asyncio.Task[None]] = WeakSet()
        self._run_locks: dict[str, asyncio.Lock] = {}
        self._lifecycle = asyncio.Condition()
        self._close_lock = asyncio.Lock()
        self._active_accesses = 0
        self._closing = False
        self._closed = False

    @classmethod
    async def open(cls, root: str | Path, operations: Sequence[Operation], *,
                   max_concurrency: int = 4, terminate_timeout: float = 1.0
                   ) -> ArtifactRuntime:
        _positive_int(max_concurrency, 'max_concurrency')
        _positive_number(terminate_timeout, 'terminate_timeout')
        definition = compile_operations(operations)
        store = await ArtifactStore.open(root)
        try:
            await store.recover_runs()
            for run_id in await store.run_ids():
                artifacts = await store.snapshot(run_id, definition.partition_set_ids)
                retries = await store.retry_requests(run_id, pending_only=True)
                for request in obsolete_retries(definition, artifacts, retries):
                    await store.cancel_retry(run_id, request.request_id)
        except BaseException:
            await store.close()
            raise
        return cls(
            store,
            definition,
            max_concurrency=max_concurrency,
            terminate_timeout=terminate_timeout,
        )

    async def __aenter__(self) -> Self:
        async with self._access():
            return self

    async def __aexit__(self, exc_type: type[BaseException] | None,
                        exc: BaseException | None,
                        traceback: TracebackType | None
                        ) -> None:
        await self.close()

    async def create(self, run_id: str, initial_commit: ArtifactCommit | None = None
                     ) -> RuntimeSnapshot:
        _text(run_id, 'run_id')
        if initial_commit is not None:
            self._definition.validate_commit(initial_commit)
        async with self._access(), self._run_lock(run_id):
            await self._store.create_run(run_id, initial_commit)
            try:
                return await self._inspect(run_id)
            except BaseException:
                await self._store.delete_run(run_id)
                raise

    async def start(self, run_id: str) -> RuntimeSnapshot:
        return await self._session_command(run_id, RunSession.start)

    async def pause(self, run_id: str) -> RuntimeSnapshot:
        return await self._session_command(run_id, RunSession.pause)

    async def resume(self, run_id: str) -> RuntimeSnapshot:
        return await self._session_command(run_id, RunSession.resume)

    async def retry(self, run_id: str, artifact_keys: Iterable[ArtifactKey] = (), *,
                    request_id: str = ''
                    ) -> RuntimeSnapshot:
        keys = tuple(artifact_keys)
        if not all(isinstance(key, ArtifactKey) for key in keys):
            raise TypeError('retry artifact_keys must contain ArtifactKey values')
        if keys:
            _text(request_id, 'retry request_id')
        return await self._session_command(
            run_id,
            lambda session: session.retry(keys, request_id),
        )

    async def cancel(self, run_id: str) -> RuntimeSnapshot:
        return await self._session_command(run_id, RunSession.cancel)

    async def commit(self, run_id: str, commit: ArtifactCommit) -> RuntimeSnapshot:
        if not isinstance(commit, ArtifactCommit):
            raise TypeError('commit must be ArtifactCommit')
        return await self._session_command(
            run_id,
            lambda session: session.commit(commit),
        )

    async def retry_artifact(self, run_id: str, artifact_key: ArtifactKey, *,
                             request_id: str
                             ) -> RuntimeSnapshot:
        if not isinstance(artifact_key, ArtifactKey):
            raise TypeError('artifact_key must be ArtifactKey')
        _text(request_id, 'retry request_id')
        return await self._session_command(
            run_id,
            lambda session: session.retry_artifact(artifact_key, request_id),
        )

    async def snapshot(self, run_id: str) -> RuntimeSnapshot:
        async with self._access():
            return await self._inspect(run_id)

    async def inspect(self, run_id: str) -> RuntimeSnapshot:
        return await self.snapshot(run_id)

    async def wait_for_status(self, run_id: str, statuses: str | tuple[str, ...], *,
                              timeout: float = 10.0
                              ) -> RuntimeSnapshot:
        async with self._access(), self._run_lock(run_id):
            session = await self._session(run_id)
        return await session.wait_for_status(statuses, timeout=timeout)

    async def wait_until_settled(self, run_id: str, *, timeout: float = 10.0
                                 ) -> RuntimeSnapshot:
        async with self._access(), self._run_lock(run_id):
            session = await self._session(run_id)
        return await session.wait_until_settled(timeout=timeout)

    async def attempts(self, run_id: str) -> tuple[AttemptSnapshot, ...]:
        async with self._access():
            return await self._store.attempts(run_id)

    async def progress_events(self, run_id: str, attempt_id: str | None = None
                              ) -> tuple[ProgressEvent, ...]:
        async with self._access():
            return await self._store.progress_events(run_id, attempt_id)

    async def retry_requests(self, run_id: str) -> tuple[ArtifactRetryRequest, ...]:
        async with self._access():
            return await self._store.retry_requests(run_id)

    async def read(self, run_id: str, ref: ArtifactRef) -> object:
        async with self._access():
            return await self._store.read(run_id, ref)

    async def read_many(self, run_id: str, refs: Iterable[ArtifactRef]
                        ) -> Mapping[ArtifactRef, object]:
        async with self._access():
            return await self._store.read_many(run_id, refs)

    async def record(self, run_id: str, ref: ArtifactRef) -> ArtifactRecord | None:
        async with self._access():
            return await self._store.record(run_id, ref)

    async def head(self, run_id: str, key: ArtifactKey) -> ArtifactRecord | None:
        async with self._access():
            return await self._store.head(run_id, key)

    async def history(self, run_id: str, key: ArtifactKey) -> tuple[ArtifactRecord, ...]:
        async with self._access():
            return await self._store.history(run_id, key)

    async def run_ids(self) -> tuple[str, ...]:
        async with self._access():
            return await self._store.run_ids()

    async def has_run(self, run_id: str) -> bool:
        _text(run_id, 'run_id')
        async with self._access():
            return await self._store.run_state(run_id) is not None

    async def release(self, run_id: str) -> None:
        _text(run_id, 'run_id')
        async with self._access(), self._run_lock(run_id):
            entry = self._sessions.get(run_id)
            if entry is not None and entry.task.done():
                self._consume_session_task(run_id, entry)
                entry = None
            if entry is None:
                await self._require_run(run_id)
                return
            await entry.session.release()
            await entry.task
            if self._sessions.get(run_id) is entry:
                del self._sessions[run_id]

    async def delete_run(self, run_id: str) -> None:
        _text(run_id, 'run_id')
        async with self._access(), self._run_lock(run_id):
            entry = self._sessions.get(run_id)
            if entry is not None and entry.task.done():
                self._consume_session_task(run_id, entry)
                entry = None
            if entry is not None:
                await entry.session.release()
                await entry.task
                if self._sessions.get(run_id) is entry:
                    del self._sessions[run_id]
            else:
                state = await self._require_run(run_id)
                if state.status in _ACTIVE_STATUSES:
                    snapshot = await self._inspect(run_id)
                    if (
                        state.status != 'running'
                        or snapshot.running
                        or snapshot.ready_count
                        or not snapshot.awaiting_artifacts
                    ):
                        raise RuntimeError('cannot delete a run with active persisted state')
            await self._store.delete_run(run_id)

    async def close(self) -> None:
        async with self._close_lock:
            async with self._lifecycle:
                if self._closed:
                    return
                if self._closing:
                    await self._lifecycle.wait_for(lambda: not self._closing)
                    if self._closed:
                        return
                self._closing = True
                await self._lifecycle.wait_for(lambda: self._active_accesses == 0)

            entries = tuple(self._sessions.items())
            results = await asyncio.gather(
                *(entry.session.close() for _, entry in entries),
                return_exceptions=True,
            )
            failures: list[Exception] = []
            closed_entries: list[tuple[str, _SessionEntry]] = []
            for (run_id, entry), result in zip(entries, results, strict=True):
                if isinstance(result, BaseException):
                    failures.append(_as_exception(result))
                else:
                    closed_entries.append((run_id, entry))

            task_results = await asyncio.gather(
                *(entry.task for _, entry in closed_entries),
                return_exceptions=True,
            )
            failures.extend(
                _as_exception(result)
                for result in task_results
                if isinstance(result, BaseException)
            )
            for run_id, entry in closed_entries:
                if self._sessions.get(run_id) is entry:
                    del self._sessions[run_id]

            if failures:
                async with self._lifecycle:
                    self._closing = False
                    self._lifecycle.notify_all()
                raise ExceptionGroup('artifact runtime failed to close cleanly', failures)  # noqa: F821

            try:
                await self._store.close()
            except BaseException:
                async with self._lifecycle:
                    self._closing = False
                    self._lifecycle.notify_all()
                raise
            self._sessions.clear()
            async with self._lifecycle:
                self._closed = True
                self._closing = False
                self._lifecycle.notify_all()

    async def _session_command(self, run_id: str,
                               command: Callable[[RunSession], Awaitable[RuntimeSnapshot]]
                               ) -> RuntimeSnapshot:
        _text(run_id, 'run_id')
        async with self._access(), self._run_lock(run_id):
            session = await self._session(run_id)
            return await command(session)

    async def _session(self, run_id: str) -> RunSession:
        entry = self._sessions.get(run_id)
        if entry is not None and entry.task.done():
            self._consume_session_task(run_id, entry)
            entry = None
        if entry is None:
            await self._require_run(run_id)
            session = RunSession(
                run_id,
                self._definition,
                self._store,
                max_concurrency=self._max_run_concurrency,
                terminate_timeout=self._terminate_timeout,
            )
            task = asyncio.create_task(session.serve(), name=f'artifact-run:{run_id}')
            entry = _SessionEntry(session, task)
            self._sessions[run_id] = entry
            task.add_done_callback(
                lambda completed, key=run_id, current=entry:
                self._discard_session(key, current)
            )
        try:
            await entry.session.wait_ready()
        except BaseException:
            if self._sessions.get(run_id) is entry:
                del self._sessions[run_id]
            await asyncio.gather(entry.task, return_exceptions=True)
            raise
        if entry.task.done():
            self._consume_session_task(run_id, entry)
        return entry.session

    async def _inspect(self, run_id: str) -> RuntimeSnapshot:
        state, artifacts, attempts, retries = await self._store.inspect(
            run_id,
            self._definition.partition_set_ids,
        )
        decision = plan_next(self._definition, artifacts, retries)
        view = decision.view
        active = tuple(
            attempt
            for attempt in attempts
            if attempt.status in {'scheduled', 'running', 'cancelling'}
        )
        active_ids = {attempt.invocation_id for attempt in active}
        ready_count = 0
        awaiting: tuple[ArtifactKey, ...] = ()
        terminal = state.status in {'cancelled', 'failed', 'completed'}
        if not terminal:
            if isinstance(decision, PlanReady):
                ready_count = sum(
                    invocation.invocation_id not in active_ids
                    for invocation in decision.invocations
                )
            elif isinstance(decision, PlanAwaiting):
                awaiting = decision.artifact_keys
        return RuntimeSnapshot(
            run_id,
            state.status,
            tuple(
                InvocationSnapshot(
                    attempt.invocation_id,
                    attempt.operation_id,
                    attempt.partition_key,
                )
                for attempt in active
                if not terminal and attempt.status in {'scheduled', 'running'}
            ),
            ready_count,
            {key: record.ref for key, record in view.records.items()},
            view.partition_sets,
            state.error,
            active,
            awaiting,
        )

    async def _require_run(self, run_id: str):
        state = await self._store.run_state(run_id)
        if state is None:
            raise DefinitionError(f'run not found: {run_id}')
        return state

    def _run_lock(self, run_id: str) -> asyncio.Lock:
        return self._run_locks.setdefault(run_id, asyncio.Lock())

    @asynccontextmanager
    async def _access(self) -> AsyncIterator[None]:
        async with self._lifecycle:
            if self._closed:
                raise RuntimeError('artifact runtime is closed')
            if self._closing:
                raise RuntimeError('artifact runtime is closing')
            self._active_accesses += 1
        try:
            yield
        finally:
            async with self._lifecycle:
                self._active_accesses -= 1
                if self._active_accesses == 0:
                    self._lifecycle.notify_all()

    def _consume_session_task(self, run_id: str, entry: _SessionEntry) -> None:
        if self._sessions.get(run_id) is entry:
            del self._sessions[run_id]
        if entry.task.cancelled():
            return
        error = entry.task.exception()
        if error is not None:
            raise RuntimeError(f'artifact run session failed: {run_id}') from error

    def _discard_session(self, run_id: str, entry: _SessionEntry) -> None:
        if self._sessions.get(run_id) is entry:
            del self._sessions[run_id]
        task = entry.task
        if task in self._reported_session_tasks:
            return
        self._reported_session_tasks.add(task)
        if task.cancelled():
            return
        error = task.exception()
        if error is not None:
            task.get_loop().call_exception_handler({
                'message': f'artifact run session failed: {run_id}',
                'exception': error,
                'task': task,
            })


__all__ = ['ArtifactRuntime']
