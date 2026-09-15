from __future__ import annotations

import asyncio
import itertools
import uuid
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Literal

from .artifact import ArtifactCommit, ArtifactKey, ArtifactRef, ArtifactSnapshot
from .errors import DefinitionError, OperationExecutionError
from .execution import ExecutionCleanupError, ExecutionHandle, start_execution
from .operation import OperationContext, OperationInvocation, OperationResult
from .planning import (
    PlanAwaiting,
    PlanComplete,
    PlanReady,
    PlanningResult,
    RuntimeDefinition,
    obsolete_retries,
    plan_next,
)
from .state import (
    ArtifactRetryRequest,
    AttemptSnapshot,
    InvocationSnapshot,
    ProgressUpdate,
    RunStatus,
    RuntimeErrorInfo,
    RuntimeSnapshot,
)
from .store import ArtifactStore
from .utils import _as_exception, _positive_int, _positive_number, _text


_CONTROL_PRIORITY = 0
_COMPLETION_PRIORITY = 2
_PROGRESS_PRIORITY = 2
_PROGRESS_CAPACITY = 256


class _TerminationFailure(ExceptionGroup):  # noqa: F821
    pass


@dataclass(frozen=True, slots=True)
class _Command:
    kind: Literal['start', 'pause', 'resume', 'retry', 'cancel', 'release', 'close']
    reply: asyncio.Future[RuntimeSnapshot]
    retry_keys: tuple[ArtifactKey, ...] = ()
    request_id: str = ''


@dataclass(frozen=True, slots=True)
class _CommitCommand:
    commit: ArtifactCommit
    reply: asyncio.Future[RuntimeSnapshot]


@dataclass(frozen=True, slots=True)
class _RetryArtifactCommand:
    artifact_key: ArtifactKey
    request_id: str
    reply: asyncio.Future[RuntimeSnapshot]


@dataclass(frozen=True, slots=True)
class _ExecutionProgress:
    attempt_id: str
    update: ProgressUpdate


@dataclass(frozen=True, slots=True)
class _ExecutionDone:
    attempt_id: str
    result: OperationResult | None
    error: BaseException | None


@dataclass(slots=True)
class _ActiveExecution:
    invocation: OperationInvocation
    attempt: AttemptSnapshot
    handle: ExecutionHandle
    waiter: asyncio.Task[None]


_Event = _Command | _CommitCommand | _RetryArtifactCommand | _ExecutionProgress | _ExecutionDone


class RunSession:
    def __init__(self, run_id: str, definition: RuntimeDefinition, store: ArtifactStore, *,
                 max_concurrency: int, terminate_timeout: float
                 ) -> None:
        _text(run_id, 'run_id')
        if not isinstance(definition, RuntimeDefinition):
            raise TypeError('definition must be RuntimeDefinition')
        if not isinstance(store, ArtifactStore):
            raise TypeError('store must be ArtifactStore')
        _positive_int(max_concurrency, 'max_concurrency')
        _positive_number(terminate_timeout, 'terminate_timeout')

        self.run_id = run_id
        self._definition = definition
        self._store = store
        self._max_concurrency = max_concurrency
        self._terminate_timeout = terminate_timeout
        self._events: asyncio.PriorityQueue[tuple[int, int, _Event]] = asyncio.PriorityQueue()
        self._event_sequence = itertools.count()
        self._progress_slots = asyncio.Semaphore(_PROGRESS_CAPACITY)
        self._ready = asyncio.Event()
        self._condition = asyncio.Condition()
        self._serve_task: asyncio.Task[None] | None = None
        self._initialization_error: BaseException | None = None
        self._stopping = False
        self._closed = False

        self._status: RunStatus = 'created'
        self._error: RuntimeErrorInfo | None = None
        self._failure_pending: RuntimeErrorInfo | None = None
        self._artifacts = ArtifactSnapshot()
        self._retries: tuple[ArtifactRetryRequest, ...] = ()
        self._decision: PlanningResult | None = None
        self._active: dict[str, _ActiveExecution] = {}
        self._snapshot = RuntimeSnapshot(run_id)

    async def serve(self) -> None:
        if self._serve_task is not None:
            raise RuntimeError('run session is already serving')
        self._serve_task = asyncio.current_task()
        try:
            await self._initialize()
        except Exception as exc:
            self._initialization_error = exc
            self._ready.set()
            self._closed = True
            await self._notify()
            raise
        self._ready.set()

        try:
            while not self._stopping:
                _, _, event = await self._events.get()
                try:
                    try:
                        await self._handle_event(event)
                    except Exception as exc:
                        await self._handle_internal_error(exc)
                finally:
                    if isinstance(event, _ExecutionProgress):
                        self._progress_slots.release()
                    self._events.task_done()
        except BaseException:
            await asyncio.gather(
                *(execution.handle.terminate() for execution in self._active.values()),
                return_exceptions=True,
            )
            raise
        finally:
            self._closed = True
            await self._finish_pending_events()
            await self._notify()

    async def wait_ready(self) -> None:
        await self._ready.wait()
        if self._initialization_error is not None:
            raise self._initialization_error

    async def start(self) -> RuntimeSnapshot:
        return await self._request('start')

    async def pause(self) -> RuntimeSnapshot:
        return await self._request('pause')

    async def resume(self) -> RuntimeSnapshot:
        return await self._request('resume')

    async def retry(self, artifact_keys: tuple[ArtifactKey, ...] = (),
                    request_id: str = ''
                    ) -> RuntimeSnapshot:
        if not all(isinstance(key, ArtifactKey) for key in artifact_keys):
            raise TypeError('retry artifact_keys must contain ArtifactKey values')
        if artifact_keys:
            _text(request_id, 'retry request_id')
        return await self._request(
            'retry',
            retry_keys=artifact_keys,
            request_id=request_id,
        )

    async def cancel(self) -> RuntimeSnapshot:
        return await self._request('cancel')

    async def release(self) -> RuntimeSnapshot:
        return await self._request('release')

    async def close(self) -> RuntimeSnapshot:
        if self._closed:
            return self._snapshot
        return await self._request('close')

    async def commit(self, commit: ArtifactCommit) -> RuntimeSnapshot:
        if not isinstance(commit, ArtifactCommit):
            raise TypeError('commit must be ArtifactCommit')
        reply = self._reply()
        await self._enqueue(_CommitCommand(commit, reply), _CONTROL_PRIORITY)
        return await reply

    async def retry_artifact(self, artifact_key: ArtifactKey, request_id: str
                             ) -> RuntimeSnapshot:
        if not isinstance(artifact_key, ArtifactKey):
            raise TypeError('artifact_key must be ArtifactKey')
        _text(request_id, 'retry request_id')
        reply = self._reply()
        await self._enqueue(
            _RetryArtifactCommand(artifact_key, request_id, reply),
            _CONTROL_PRIORITY,
        )
        return await reply

    def snapshot(self) -> RuntimeSnapshot:
        if not self._ready.is_set():
            raise RuntimeError('run session is not ready')
        if self._initialization_error is not None:
            raise self._initialization_error
        return self._snapshot

    async def wait_for_status(self, statuses: str | tuple[str, ...], *,
                              timeout: float = 10.0
                              ) -> RuntimeSnapshot:
        expected = (statuses,) if isinstance(statuses, str) else tuple(statuses)
        if not expected:
            raise DefinitionError('statuses must not be empty')
        async with asyncio.timeout(timeout):
            async with self._condition:
                await self._condition.wait_for(
                    lambda: self._snapshot.status in expected or self._closed
                )
        if self._snapshot.status not in expected:
            raise RuntimeError('run session closed before reaching requested status')
        return self._snapshot

    async def wait_until_settled(self, *, timeout: float = 10.0) -> RuntimeSnapshot:
        async with asyncio.timeout(timeout):
            async with self._condition:
                await self._condition.wait_for(
                    lambda: self._settled() or self._closed
                )
        if not self._settled():
            raise RuntimeError('run session closed before becoming settled')
        return self._snapshot

    async def _initialize(self) -> None:
        state, artifacts, attempts, retries = await self._store.inspect(
            self.run_id,
            self._definition.partition_set_ids,
        )
        active_attempts = tuple(
            attempt
            for attempt in attempts
            if attempt.status in {'scheduled', 'running', 'cancelling'}
        )
        if active_attempts:
            raise RuntimeError('artifact store contains unrecovered execution attempts')
        self._status = state.status
        self._error = state.error
        self._artifacts = artifacts
        self._retries = retries
        self._decision = plan_next(self._definition, self._artifacts, self._retries)

        if self._status == 'completed' and not isinstance(self._decision, PlanComplete):
            await self._persist_status('paused')
        if self._status == 'running':
            await self._schedule()
        else:
            await self._publish()

    async def _request(self, kind: Literal[
        'start', 'pause', 'resume', 'retry', 'cancel', 'release', 'close'
    ], *, retry_keys: tuple[ArtifactKey, ...] = (),
        request_id: str = ''
    ) -> RuntimeSnapshot:
        reply = self._reply()
        await self._enqueue(
            _Command(kind, reply, retry_keys, request_id),
            _CONTROL_PRIORITY,
        )
        return await reply

    def _reply(self) -> asyncio.Future[RuntimeSnapshot]:
        if self._closed or self._stopping:
            raise RuntimeError('run session is closed')
        return asyncio.get_running_loop().create_future()

    async def _enqueue(self, event: _Event, priority: int) -> None:
        if self._closed or self._stopping:
            raise RuntimeError('run session is closed')
        await self._events.put((priority, next(self._event_sequence), event))

    async def _handle_event(self, event: _Event) -> None:
        if isinstance(event, _Command):
            await self._handle_command(event)
        elif isinstance(event, _CommitCommand):
            await self._handle_commit(event)
        elif isinstance(event, _RetryArtifactCommand):
            await self._handle_retry_artifact(event)
        elif isinstance(event, _ExecutionProgress):
            await self._handle_progress(event)
        else:
            await self._handle_done(event)

    async def _handle_command(self, command: _Command) -> None:
        actions: dict[str, Callable[[], Awaitable[None]]] = {
            'start': self._start,
            'pause': self._pause,
            'resume': self._resume,
            'cancel': self._cancel,
            'release': self._release,
            'close': self._close,
        }
        try:
            await self._flush_failure()
            if command.kind == 'retry':
                await self._retry(command.retry_keys, command.request_id)
            else:
                await actions[command.kind]()
        except Exception as exc:
            reply_error: Exception = exc
            if self._failure_pending is not None:
                try:
                    await self._flush_failure()
                except Exception as persistence_error:
                    reply_error = ExceptionGroup(  # noqa: F821
                        'command and failure persistence both failed',
                        [exc, persistence_error],
                    )
            if not command.reply.done():
                command.reply.set_exception(reply_error)
        else:
            if not command.reply.done():
                command.reply.set_result(self._snapshot)

    async def _handle_commit(self, command: _CommitCommand) -> None:
        try:
            await self._commit_artifacts(command.commit)
        except Exception as exc:
            if not command.reply.done():
                command.reply.set_exception(exc)
        else:
            if not command.reply.done():
                command.reply.set_result(self._snapshot)

    async def _handle_retry_artifact(self, command: _RetryArtifactCommand) -> None:
        try:
            await self._request_artifact_retry(command.artifact_key, command.request_id)
        except Exception as exc:
            if not command.reply.done():
                command.reply.set_exception(exc)
        else:
            if not command.reply.done():
                command.reply.set_result(self._snapshot)

    async def _start(self) -> None:
        if self._status != 'created':
            raise DefinitionError(f'cannot start run from {self._status}')
        await self._enter_running()

    async def _pause(self) -> None:
        if self._status == 'paused':
            return
        if self._status == 'running':
            await self._persist_status('pausing')
        elif self._status != 'pausing':
            raise DefinitionError(f'cannot pause run from {self._status}')
        await self._terminate(tuple(self._active.values()), final='paused')
        self._status = 'paused'
        await self._publish()

    async def _resume(self) -> None:
        if self._status != 'paused':
            raise DefinitionError(f'cannot resume run from {self._status}')
        await self._enter_running()

    async def _retry(self, artifact_keys: tuple[ArtifactKey, ...] = (),
                     request_id: str = ''
                     ) -> None:
        if self._status != 'failed':
            raise DefinitionError(f'cannot retry run from {self._status}')
        if self._active:
            await self._terminate(tuple(self._active.values()))
        if artifact_keys:
            await self._replace_artifact_retries(
                artifact_keys,
                request_id,
            )
        await self._enter_running()

    async def _cancel(self) -> None:
        if self._status == 'cancelled':
            return
        if self._status == 'completed':
            raise DefinitionError('cannot cancel run from completed')
        if self._status != 'cancelling':
            await self._persist_status('cancelling')
        await self._terminate(tuple(self._active.values()), final='cancelled')
        self._status = 'cancelled'
        self._retries = ()
        await self._publish()

    async def _release(self) -> None:
        if self._status in {'pausing', 'cancelling'} or self._active or not self._settled():
            raise RuntimeError('cannot release a run while it is executing')
        self._stopping = True

    async def _close(self) -> None:
        if self._status in {'running', 'pausing'}:
            if self._status == 'running':
                await self._persist_status('pausing')
            await self._terminate(tuple(self._active.values()), final='interrupted')
            self._status = 'paused'
        elif self._status == 'cancelling':
            await self._terminate(tuple(self._active.values()), final='cancelled')
            self._status = 'cancelled'
            self._retries = ()
        elif self._active:
            await self._terminate(tuple(self._active.values()))
        await self._publish()
        self._stopping = True

    async def _enter_running(self) -> None:
        await self._refresh_plan()
        await self._persist_status('running')
        await self._schedule()

    async def _commit_artifacts(self, commit: ArtifactCommit) -> None:
        if self._status not in {'created', 'paused', 'completed', 'running'}:
            raise DefinitionError(f'cannot commit artifact from {self._status}')
        self._definition.validate_commit(commit)
        previous_status = self._status
        result = await self._store.commit(self.run_id, commit)
        if result.status == 'stale':
            raise DefinitionError('artifact commit precondition is stale')

        await self._refresh_plan()
        try:
            await self._cancel_invalidated()
        except _TerminationFailure:
            await self._publish()
            return
        if previous_status == 'completed' and not isinstance(self._decision, PlanComplete):
            await self._persist_status('running')
            await self._schedule()
        elif previous_status == 'running':
            await self._schedule()
        else:
            await self._publish()

    async def _request_artifact_retry(self, key: ArtifactKey, request_id: str) -> None:
        if self._status not in {'created', 'paused', 'completed', 'running'}:
            raise DefinitionError(f'cannot retry artifact from {self._status}')
        operation = self._definition.producer_by_artifact.get(key.artifact_id)
        if operation is None:
            raise DefinitionError(f'artifact has no producer operation: {key}')
        current = self._decision.view.records.get(key) if self._decision is not None else None
        if current is None:
            raise DefinitionError(f'artifact is not currently effective: {key}')

        pending = await self._store.retry_requests(self.run_id, pending_only=True)
        logical_key = self._retry_invocation(key)
        for pending_request in pending:
            other_key = self._retry_invocation(pending_request.artifact_key)
            if pending_request.request_id != request_id and other_key == logical_key:
                raise DefinitionError('one invocation already has a pending artifact retry')

        request = await self._store.request_retry(
            self.run_id,
            request_id,
            key,
            current.ref,
        )
        await self._refresh_plan()
        if request.status != 'pending':
            await self._publish()
        elif self._status == 'completed':
            await self._persist_status('running')
            await self._schedule()
        elif self._status == 'running':
            await self._schedule()
        else:
            await self._publish()

    async def _replace_artifact_retries(
        self,
        artifact_keys: tuple[ArtifactKey, ...],
        request_id: str,
    ) -> tuple[ArtifactRetryRequest, ...]:
        entries = await self._retry_entries(artifact_keys, request_id)
        return await self._store.replace_pending_retries(self.run_id, entries)

    async def _retry_entries(
        self,
        artifact_keys: tuple[ArtifactKey, ...],
        request_id: str,
    ) -> tuple[tuple[str, ArtifactKey, ArtifactRef], ...]:
        if self._decision is None:
            await self._refresh_plan()
        if self._decision is None:
            raise RuntimeError('retry planning state is unavailable')

        entries: list[tuple[str, ArtifactKey, ArtifactRef]] = []
        seen: set[tuple[str, str]] = set()
        for key in artifact_keys:
            operation = self._definition.producer_by_artifact.get(key.artifact_id)
            if operation is None:
                raise DefinitionError(f'artifact has no producer operation: {key}')
            current = self._decision.view.records.get(key)
            if current is None:
                raise DefinitionError(f'artifact is not currently effective: {key}')
            invocation = self._retry_invocation(key)
            if invocation in seen:
                raise DefinitionError('one invocation cannot have multiple retry targets')
            seen.add(invocation)
            child_id = ':'.join((
                request_id,
                key.artifact_id,
                key.partition_key or '_',
                str(current.ref.version),
            ))
            entries.append((child_id, key, current.ref))
        return tuple(entries)

    def _retry_invocation(self, key: ArtifactKey) -> tuple[str, str]:
        operation = self._definition.producer_by_artifact.get(key.artifact_id)
        if operation is None:
            raise DefinitionError(f'artifact has no producer operation: {key}')
        return (
            operation.spec.op_id,
            key.partition_key if operation.spec.driver_input else '',
        )

    async def _refresh_plan(self) -> None:
        self._artifacts = await self._store.snapshot(
            self.run_id,
            self._definition.partition_set_ids,
        )
        self._retries = await self._store.retry_requests(self.run_id, pending_only=True)
        if self._retries:
            obsolete = obsolete_retries(self._definition, self._artifacts, self._retries)
            for request in obsolete:
                await self._store.cancel_retry(self.run_id, request.request_id)
            if obsolete:
                obsolete_ids = {request.request_id for request in obsolete}
                self._retries = tuple(
                    request
                    for request in self._retries
                    if request.request_id not in obsolete_ids
                )
        self._decision = plan_next(self._definition, self._artifacts, self._retries)

    async def _schedule(self) -> None:
        if self._status != 'running':
            await self._publish()
            return
        if self._decision is None:
            await self._refresh_plan()

        if isinstance(self._decision, PlanComplete):
            if not self._active:
                await self._persist_status('completed')
            await self._publish()
            return
        if isinstance(self._decision, PlanAwaiting):
            await self._publish()
            return

        active_invocations = {
            execution.invocation.invocation_id
            for execution in self._active.values()
        }
        per_operation: dict[str, int] = {}
        for execution in self._active.values():
            operation_id = execution.invocation.operation.spec.op_id
            per_operation[operation_id] = per_operation.get(operation_id, 0) + 1

        for invocation in self._decision.invocations:
            if len(self._active) >= self._max_concurrency:
                break
            if invocation.invocation_id in active_invocations:
                continue
            operation_id = invocation.operation.spec.op_id
            if per_operation.get(operation_id, 0) >= invocation.operation.spec.max_concurrency:
                continue
            await self._launch(invocation)
            active_invocations.add(invocation.invocation_id)
            per_operation[operation_id] = per_operation.get(operation_id, 0) + 1
            if self._status == 'failed':
                break
        await self._publish()

    async def _launch(self, invocation: OperationInvocation) -> None:
        try:
            values = await self._store.read_many(self.run_id, invocation.value_refs())
        except Exception as exc:
            await self._fail_run(exc)
            await self._terminate_failed_siblings()
            return

        attempt_id = uuid.uuid4().hex
        try:
            attempt = await self._store.create_attempt(
                self.run_id,
                attempt_id,
                invocation.invocation_id,
                invocation.operation.spec.op_id,
                invocation.partition_key,
                invocation.lineage_refs(),
                tuple(key for key in invocation.output_keys.values() if key is not None),
                retry_request_id=invocation.retry_request_id,
            )
        except Exception as exc:
            await self._fail_run(exc)
            await self._terminate_failed_siblings()
            return
        try:
            attempt = await self._store.set_attempt_status(
                self.run_id,
                attempt_id,
                'running',
            )
            context = OperationContext(
                self.run_id,
                invocation.invocation_id,
                invocation.partition_key,
                self._reporter(attempt_id),
            )
            handle = await start_execution(
                invocation,
                context,
                invocation.bind_values(values),
                terminate_timeout=self._terminate_timeout,
            )
        except Exception as exc:
            await self._fail_attempt(attempt, exc)
            await self._terminate_failed_siblings()
            return

        waiter = asyncio.create_task(
            self._wait_execution(attempt_id, handle),
            name=f'artifact-attempt:{attempt_id}',
        )
        self._active[attempt_id] = _ActiveExecution(invocation, attempt, handle, waiter)

    def _reporter(self, attempt_id: str) -> Callable[[ProgressUpdate], Awaitable[None]]:
        async def report(update: ProgressUpdate) -> None:
            await self._progress_slots.acquire()
            try:
                await self._enqueue(
                    _ExecutionProgress(attempt_id, update),
                    _PROGRESS_PRIORITY,
                )
            except BaseException:
                self._progress_slots.release()
                raise
        return report

    async def _wait_execution(self, attempt_id: str, handle: ExecutionHandle) -> None:
        result = None
        error = None
        try:
            result = await handle.wait()
        except asyncio.CancelledError as exc:
            error = exc
        except Exception as exc:
            error = exc
        try:
            await self._enqueue(
                _ExecutionDone(attempt_id, result, error),
                _COMPLETION_PRIORITY,
            )
        except RuntimeError:
            return

    async def _handle_progress(self, event: _ExecutionProgress) -> None:
        execution = self._active.get(event.attempt_id)
        if execution is None or execution.attempt.status != 'running':
            return
        try:
            await self._store.append_progress(self.run_id, event.attempt_id, event.update)
        except Exception as exc:
            await self._fail_running(exc)

    async def _handle_done(self, event: _ExecutionDone) -> None:
        execution = self._active.get(event.attempt_id)
        if execution is None:
            return
        if execution.attempt.status == 'cancelling':
            return
        if event.error is not None:
            if isinstance(event.error, asyncio.CancelledError):
                error = OperationExecutionError('operation ended without a cancellation request')
            else:
                error = _as_exception(event.error)
            execution.attempt = await self._fail_attempt(execution.attempt, error)
            if not (
                isinstance(event.error, ExecutionCleanupError)
                and event.error.cleanup_pending
            ):
                self._active.pop(event.attempt_id, None)
            await self._terminate_failed_siblings()
            await self._publish()
            return

        try:
            if event.result is None:
                raise OperationExecutionError('operation returned no result')
            commit = execution.invocation.artifact_commit(event.result)
            self._definition.validate_commit(commit)
            await self._store.commit(
                self.run_id,
                commit,
                attempt_id=event.attempt_id,
            )
        except Exception as exc:
            execution.attempt = await self._fail_attempt(execution.attempt, exc)
            self._active.pop(event.attempt_id, None)
            await self._terminate_failed_siblings()
            await self._publish()
            return

        self._active.pop(event.attempt_id, None)
        await self._refresh_plan()
        await self._schedule()

    async def _fail_attempt(self, attempt: AttemptSnapshot,
                            error: Exception
                            ) -> AttemptSnapshot:
        info = RuntimeErrorInfo(type(error).__name__, str(error) or type(error).__name__)
        failed = await self._store.fail_attempt_and_run(self.run_id, attempt.attempt_id, info)
        self._status = 'failed'
        self._error = info
        return failed

    async def _fail_run(self, error: Exception) -> None:
        info = RuntimeErrorInfo(type(error).__name__, str(error) or type(error).__name__)
        self._status = 'failed'
        self._error = info
        self._failure_pending = info
        await self._flush_failure()

    async def _flush_failure(self) -> None:
        info = self._failure_pending
        if info is None:
            return
        await self._store.set_run_state(self.run_id, 'failed', error=info)
        self._failure_pending = None

    async def _fail_running(self, error: Exception) -> None:
        persistence_error: Exception | None = None
        try:
            await self._fail_run(error)
        except Exception as exc:
            persistence_error = exc
        try:
            await self._terminate(tuple(self._active.values()))
        except _TerminationFailure:
            pass
        await self._publish()
        if persistence_error is not None:
            raise persistence_error

    async def _handle_internal_error(self, error: Exception) -> None:
        if self._status == 'failed':
            try:
                await self._flush_failure()
            except Exception:
                pass
            try:
                await self._terminate(tuple(self._active.values()))
            except _TerminationFailure:
                pass
            await self._publish()
            return
        await self._fail_running(error)

    async def _terminate_failed_siblings(self) -> None:
        siblings = tuple(self._active.values())
        if not siblings:
            return
        try:
            await self._terminate(siblings)
        except _TerminationFailure:
            return

    async def _cancel_invalidated(self) -> None:
        if self._decision is None:
            return
        targets = tuple(
            execution
            for execution in self._active.values()
            if not execution.invocation.is_current(
                self._artifacts.records,
                self._decision.view.records,
                self._decision.view.partition_sets,
            )
        )
        if targets:
            await self._terminate(targets)

    async def _terminate(
        self,
        executions: tuple[_ActiveExecution, ...],
        *,
        final: Literal['paused', 'cancelled', 'interrupted'] | None = None,
    ) -> None:
        failures: list[Exception] = []
        for execution in executions:
            if execution.attempt.status in {'scheduled', 'running'}:
                try:
                    execution.attempt = await self._store.set_attempt_status(
                        self.run_id,
                        execution.attempt.attempt_id,
                        'cancelling',
                    )
                except Exception:
                    pass

        results = await asyncio.gather(
            *(execution.handle.terminate() for execution in executions),
            return_exceptions=True,
        )
        for execution, result in zip(executions, results, strict=True):
            if isinstance(result, BaseException):
                failures.append(_as_exception(result))
                continue
            await asyncio.gather(execution.waiter, return_exceptions=True)
            try:
                if execution.attempt.status != 'failed':
                    execution.attempt = await self._store.set_attempt_status(
                        self.run_id,
                        execution.attempt.attempt_id,
                        (
                            'interrupted'
                            if final == 'interrupted'
                            else 'cancelled'
                        ),
                    )
            except Exception as exc:
                failures.append(exc)
                continue
            self._active.pop(execution.attempt.attempt_id, None)

        if not failures:
            try:
                if final in {'paused', 'interrupted'}:
                    await self._store.finish_pause(self.run_id)
                elif final == 'cancelled':
                    await self._store.finish_cancel(self.run_id)
            except Exception as exc:
                failures.append(exc)
        if failures:
            failure = _TerminationFailure(
                'operation cleanup did not reach a verified terminal state',
                failures,
            )
            if self._status != 'failed':
                try:
                    await self._fail_run(failure)
                except Exception as exc:
                    failure = _TerminationFailure(
                        'operation cleanup and failure persistence both failed',
                        [failure, exc],
                    )
            await self._publish()
            raise failure

    async def _persist_status(self, status: RunStatus) -> None:
        if status == 'failed':
            raise ValueError('failed status must be persisted through _fail_run')
        await self._store.set_run_state(self.run_id, status)
        self._status = status
        self._error = None
        self._failure_pending = None

    async def _publish(self) -> None:
        view = self._artifacts if self._decision is None else self._decision.view
        running = ()
        if self._status not in {'cancelled', 'failed', 'completed'}:
            running = tuple(
                InvocationSnapshot(
                    execution.invocation.invocation_id,
                    execution.invocation.operation.spec.op_id,
                    execution.invocation.partition_key,
                )
                for execution in self._active.values()
                if execution.attempt.status in {'scheduled', 'running'}
            )
        ready_count = 0
        awaiting: tuple[ArtifactKey, ...] = ()
        if (
            isinstance(self._decision, PlanReady)
            and self._status not in {'cancelled', 'failed', 'completed'}
        ):
            active_ids = {execution.invocation.invocation_id for execution in self._active.values()}
            ready_count = sum(
                invocation.invocation_id not in active_ids
                for invocation in self._decision.invocations
            )
        elif isinstance(self._decision, PlanAwaiting):
            awaiting = self._decision.artifact_keys

        self._snapshot = RuntimeSnapshot(
            self.run_id,
            self._status,
            running,
            ready_count,
            {key: record.ref for key, record in view.records.items()},
            view.partition_sets,
            self._error,
            tuple(execution.attempt for execution in self._active.values()),
            awaiting,
        )
        await self._notify()

    def _settled(self) -> bool:
        if self._snapshot.status in {'created', 'paused', 'cancelled', 'failed', 'completed'}:
            if self._snapshot.active_attempts:
                return False
            return True
        return (
            self._snapshot.status == 'running'
            and not self._snapshot.running
            and self._snapshot.ready_count == 0
            and not self._snapshot.active_attempts
        )

    async def _notify(self) -> None:
        async with self._condition:
            self._condition.notify_all()

    async def _finish_pending_events(self) -> None:
        error = RuntimeError('run session is closed')
        while not self._events.empty():
            _, _, event = self._events.get_nowait()
            if isinstance(event, (_Command, _CommitCommand, _RetryArtifactCommand)):
                if not event.reply.done():
                    event.reply.set_exception(error)
            if isinstance(event, _ExecutionProgress):
                self._progress_slots.release()
            self._events.task_done()


__all__ = ['RunSession']
