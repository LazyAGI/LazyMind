from __future__ import annotations

import asyncio
import hashlib
import json
import pickle
import time
from collections.abc import AsyncIterator, Iterable, Mapping
from contextlib import asynccontextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Literal, cast, get_args

import aiosqlite

from .artifact import (
    ArtifactCommit,
    ArtifactKey,
    ArtifactRecord,
    ArtifactRef,
    ArtifactSnapshot,
    PartitionSet,
)
from .errors import DefinitionError
from .state import (
    ArtifactRetryRequest,
    AttemptSnapshot,
    AttemptStatus,
    ProgressEvent,
    ProgressUpdate,
    RunStatus,
    RuntimeErrorInfo,
)
from .utils import _string, _text


_SCHEMA_VERSION = 3
_SCHEMA_TABLES = frozenset({
    'artifacts', 'attempts', 'commits', 'progress_events', 'retry_requests', 'runs',
})
_RUN_STATUSES = frozenset(get_args(RunStatus))
_ACTIVE_ATTEMPT_STATUSES = ('scheduled', 'running', 'cancelling')
_ATTEMPT_TRANSITIONS = {
    'scheduled': frozenset({'running', 'cancelling', 'cancelled', 'failed', 'interrupted'}),
    'running': frozenset({'cancelling', 'cancelled', 'failed', 'interrupted'}),
    'cancelling': frozenset({'cancelled', 'interrupted'}),
    'cancelled': frozenset(),
    'succeeded': frozenset(),
    'failed': frozenset(),
    'interrupted': frozenset(),
    'discarded': frozenset(),
}


@dataclass(frozen=True)
class StoredRunState:
    status: RunStatus
    error: RuntimeErrorInfo | None = None

    def __post_init__(self) -> None:
        if self.status not in _RUN_STATUSES:
            raise DefinitionError(f'unknown run status: {self.status}')
        if self.error is not None and not isinstance(self.error, RuntimeErrorInfo):
            raise TypeError('run error must be RuntimeErrorInfo or None')
        if self.status == 'failed' and self.error is None:
            raise DefinitionError('failed run requires error details')
        if self.status != 'failed' and self.error is not None:
            raise DefinitionError('run error is only valid for failed status')


@dataclass(frozen=True)
class CommitResult:
    status: Literal['ok', 'stale']
    refs: tuple[ArtifactRef, ...] = ()
    replayed: bool = False

    def __post_init__(self) -> None:
        if self.status not in {'ok', 'stale'}:
            raise DefinitionError(f'unknown commit status: {self.status}')
        refs = tuple(self.refs)
        if not all(isinstance(ref, ArtifactRef) for ref in refs):
            raise TypeError('commit result refs must contain ArtifactRef values')
        object.__setattr__(self, 'refs', refs)


@dataclass(frozen=True, slots=True)
class _PreparedCommit:
    run_id: str
    command: ArtifactCommit
    payloads: tuple[bytes, ...]
    request_hash: str


class ArtifactStore:
    def __init__(self, connection: aiosqlite.Connection) -> None:
        self._connection = connection
        self._lock = asyncio.Lock()
        self._closed = False

    @classmethod
    async def open(cls, root: str | Path) -> ArtifactStore:
        path = Path(root)
        path.mkdir(parents=True, exist_ok=True)
        connection = await aiosqlite.connect(path / 'artifact-runtime.sqlite3')
        try:
            connection.row_factory = aiosqlite.Row
            await connection.execute('PRAGMA foreign_keys = ON')
            await connection.execute('PRAGMA journal_mode = WAL')
            await connection.execute('PRAGMA synchronous = FULL')
            store = cls(connection)
            await store._create_schema()
            return store
        except BaseException:
            await connection.close()
            raise

    async def close(self) -> None:
        async with self._lock:
            if self._closed:
                return
            await self._connection.close()
            self._closed = True

    async def create_run(self, run_id: str, initial_commit: ArtifactCommit | None = None
                         ) -> StoredRunState:
        _text(run_id, 'run_id')
        prepared = None
        if initial_commit is not None:
            if not isinstance(initial_commit, ArtifactCommit):
                raise TypeError('initial_commit must be ArtifactCommit or None')
            if initial_commit.producer.startswith('operation:'):
                raise DefinitionError('initial commit cannot be produced by an operation')
            prepared = await asyncio.to_thread(_prepare_commit, run_id, initial_commit)

        async with self._transaction():
            try:
                await self._connection.execute(
                    """
                    INSERT INTO runs(run_id, status, error_kind, error_message)
                    VALUES (?, 'created', '', '')
                    """,
                    (run_id,),
                )
            except aiosqlite.IntegrityError as exc:
                raise DefinitionError(f'run already exists: {run_id}') from exc

            if prepared is not None:
                snapshot = ArtifactSnapshot()
                if not await self._commit_is_current(run_id, prepared.command, snapshot):
                    raise DefinitionError('initial artifact commit precondition is stale')
                result = await self._apply_commit(prepared)
                await self._write_receipt(prepared, result.refs)
        return StoredRunState('created')

    async def commit(self, run_id: str, commit: ArtifactCommit, *, attempt_id: str | None = None
                     ) -> CommitResult:
        _text(run_id, 'run_id')
        if not isinstance(commit, ArtifactCommit):
            raise TypeError('commit must be ArtifactCommit')
        if attempt_id is None and commit.producer.startswith('operation:'):
            raise DefinitionError('operation commit requires attempt_id')
        if attempt_id is not None:
            _text(attempt_id, 'attempt_id')
        prepared = await asyncio.to_thread(_prepare_commit, run_id, commit)
        return await self._commit(prepared, attempt_id)

    async def snapshot(self, run_id: str, partition_set_ids: Iterable[str] = ()) -> ArtifactSnapshot:
        _text(run_id, 'run_id')
        ids = frozenset(partition_set_ids)
        for artifact_id in ids:
            _text(artifact_id, 'partition set artifact_id')
        async with self._lock:
            await self._require_run(run_id)
            rows = await self._head_rows(run_id)
        return await asyncio.to_thread(_snapshot_from_rows, rows, ids)

    async def read(self, run_id: str, ref: ArtifactRef) -> object:
        _text(run_id, 'run_id')
        if not isinstance(ref, ArtifactRef):
            raise TypeError('ref must be ArtifactRef')
        async with self._lock:
            await self._require_run(run_id)
            payload = await self._payload(run_id, ref)
        if payload is None:
            raise KeyError(ref)
        return await asyncio.to_thread(pickle.loads, payload)

    async def read_many(self, run_id: str, refs: Iterable[ArtifactRef]) -> Mapping[ArtifactRef, object]:
        _text(run_id, 'run_id')
        requested = tuple(refs)
        if not all(isinstance(ref, ArtifactRef) for ref in requested):
            raise TypeError('refs must contain ArtifactRef values')

        payloads: dict[ArtifactRef, bytes] = {}
        async with self._lock:
            await self._require_run(run_id)
            for offset in range(0, len(requested), 250):
                chunk = requested[offset:offset + 250]
                if not chunk:
                    continue
                placeholders = ','.join('(?, ?, ?)' for _ in chunk)
                parameters: list[object] = [run_id]
                for ref in chunk:
                    parameters.extend((
                        ref.key.artifact_id,
                        ref.key.partition_key,
                        ref.version,
                    ))
                cursor = await self._connection.execute(
                    f"""
                    SELECT artifact_id, partition_key, version, payload
                    FROM artifacts
                    WHERE run_id = ?
                      AND (artifact_id, partition_key, version) IN ({placeholders})
                    """,
                    parameters,
                )
                for row in await cursor.fetchall():
                    ref = ArtifactRef(
                        ArtifactKey(row['artifact_id'], row['partition_key']),
                        row['version'],
                    )
                    payloads[ref] = row['payload']

        missing = next((ref for ref in requested if ref not in payloads), None)
        if missing is not None:
            raise DefinitionError(f'input artifact is missing: {missing}')
        return await asyncio.to_thread(_deserialize_many, requested, payloads)

    async def record(self, run_id: str, ref: ArtifactRef) -> ArtifactRecord | None:
        _text(run_id, 'run_id')
        if not isinstance(ref, ArtifactRef):
            raise TypeError('ref must be ArtifactRef')
        async with self._lock:
            await self._require_run(run_id)
            cursor = await self._connection.execute(
                """
                SELECT producer, input_refs_json FROM artifacts
                WHERE run_id = ? AND artifact_id = ? AND partition_key = ? AND version = ?
                """,
                (run_id, ref.key.artifact_id, ref.key.partition_key, ref.version),
            )
            row = await cursor.fetchone()
        return None if row is None else _record_from_row(ref, row)

    async def head(self, run_id: str, key: ArtifactKey) -> ArtifactRecord | None:
        _text(run_id, 'run_id')
        if not isinstance(key, ArtifactKey):
            raise TypeError('key must be ArtifactKey')
        async with self._lock:
            await self._require_run(run_id)
            cursor = await self._connection.execute(
                """
                SELECT version, producer, input_refs_json FROM artifacts
                WHERE run_id = ? AND artifact_id = ? AND partition_key = ?
                ORDER BY version DESC LIMIT 1
                """,
                (run_id, key.artifact_id, key.partition_key),
            )
            row = await cursor.fetchone()
        if row is None:
            return None
        return _record_from_row(ArtifactRef(key, row['version']), row)

    async def history(self, run_id: str, key: ArtifactKey) -> tuple[ArtifactRecord, ...]:
        _text(run_id, 'run_id')
        if not isinstance(key, ArtifactKey):
            raise TypeError('key must be ArtifactKey')
        async with self._lock:
            await self._require_run(run_id)
            cursor = await self._connection.execute(
                """
                SELECT version, producer, input_refs_json FROM artifacts
                WHERE run_id = ? AND artifact_id = ? AND partition_key = ?
                ORDER BY version
                """,
                (run_id, key.artifact_id, key.partition_key),
            )
            rows = await cursor.fetchall()
        return tuple(
            _record_from_row(ArtifactRef(key, row['version']), row)
            for row in rows
        )

    async def request_retry(self, run_id: str, request_id: str, artifact_key: ArtifactKey,
                            base_ref: ArtifactRef
                            ) -> ArtifactRetryRequest:
        _text(run_id, 'run_id')
        _text(request_id, 'retry request_id')
        if not isinstance(artifact_key, ArtifactKey):
            raise TypeError('artifact_key must be ArtifactKey')
        if not isinstance(base_ref, ArtifactRef) or base_ref.key != artifact_key:
            raise DefinitionError('base_ref must identify artifact_key')

        async with self._transaction():
            await self._require_run(run_id)
            existing = await self._retry_row(run_id, request_id)
            if existing is not None:
                request = _retry_request(existing)
                if request.artifact_key != artifact_key or request.base_ref != base_ref:
                    raise DefinitionError(f'retry request id reused: {request_id}')
                return request

            current = await self._head_ref(run_id, artifact_key)
            if current != base_ref:
                raise DefinitionError('retry target is no longer the current artifact version')
            cursor = await self._connection.execute(
                """
                SELECT request_id FROM retry_requests
                WHERE run_id = ? AND artifact_id = ? AND partition_key = ? AND status = 'pending'
                """,
                (run_id, artifact_key.artifact_id, artifact_key.partition_key),
            )
            conflict = await cursor.fetchone()
            if conflict is not None:
                raise DefinitionError(
                    f'artifact already has a pending retry: {conflict["request_id"]}'
                )

            created_at = time.time()
            await self._connection.execute(
                """
                INSERT INTO retry_requests(
                  run_id, request_id, artifact_id, partition_key, base_version,
                  status, created_at, result_version
                ) VALUES (?, ?, ?, ?, ?, 'pending', ?, NULL)
                """,
                (
                    run_id, request_id, artifact_key.artifact_id,
                    artifact_key.partition_key, base_ref.version, created_at,
                ),
            )
        return ArtifactRetryRequest(
            request_id,
            artifact_key,
            base_ref,
            'pending',
            created_at,
        )

    async def retry_requests(self, run_id: str, *, pending_only: bool = False
                             ) -> tuple[ArtifactRetryRequest, ...]:
        _text(run_id, 'run_id')
        statement = 'SELECT * FROM retry_requests WHERE run_id = ?'
        if pending_only:
            statement += " AND status = 'pending'"
        statement += ' ORDER BY created_at, request_id'
        async with self._lock:
            await self._require_run(run_id)
            cursor = await self._connection.execute(statement, (run_id,))
            rows = await cursor.fetchall()
        return tuple(_retry_request(row) for row in rows)

    async def cancel_retry(self, run_id: str, request_id: str) -> ArtifactRetryRequest:
        _text(run_id, 'run_id')
        _text(request_id, 'retry request_id')
        async with self._transaction():
            row = await self._retry_row(run_id, request_id)
            if row is None:
                raise DefinitionError(f'retry request not found: {request_id}')
            if row['status'] == 'pending':
                await self._connection.execute(
                    """
                    UPDATE retry_requests SET status = 'cancelled'
                    WHERE run_id = ? AND request_id = ? AND status = 'pending'
                    """,
                    (run_id, request_id),
                )
                row = dict(row)
                row['status'] = 'cancelled'
        return _retry_request(row)

    async def set_run_state(self, run_id: str, status: RunStatus, *,
                            error: RuntimeErrorInfo | None = None
                            ) -> None:
        _text(run_id, 'run_id')
        state = StoredRunState(status, error)
        error_kind = '' if state.error is None else state.error.kind
        error_message = '' if state.error is None else state.error.message
        async with self._transaction():
            cursor = await self._connection.execute(
                """
                UPDATE runs SET status = ?, error_kind = ?, error_message = ?
                WHERE run_id = ?
                """,
                (status, error_kind, error_message, run_id),
            )
            if cursor.rowcount != 1:
                raise DefinitionError(f'run not found: {run_id}')

    async def run_state(self, run_id: str) -> StoredRunState | None:
        _text(run_id, 'run_id')
        async with self._lock:
            self._require_open()
            cursor = await self._connection.execute(
                'SELECT status, error_kind, error_message FROM runs WHERE run_id = ?',
                (run_id,),
            )
            row = await cursor.fetchone()
        return None if row is None else _run_state(row)

    async def run_ids(self) -> tuple[str, ...]:
        async with self._lock:
            self._require_open()
            cursor = await self._connection.execute('SELECT run_id FROM runs ORDER BY run_id')
            return tuple(row['run_id'] for row in await cursor.fetchall())

    async def inspect(self, run_id: str, partition_set_ids: Iterable[str] = ()
                      ) -> tuple[
        StoredRunState,
        ArtifactSnapshot,
        tuple[AttemptSnapshot, ...],
        tuple[ArtifactRetryRequest, ...],
    ]:
        _text(run_id, 'run_id')
        ids = frozenset(partition_set_ids)
        async with self._lock:
            cursor = await self._connection.execute(
                'SELECT status, error_kind, error_message FROM runs WHERE run_id = ?',
                (run_id,),
            )
            state_row = await cursor.fetchone()
            if state_row is None:
                raise DefinitionError(f'run not found: {run_id}')
            artifact_rows = await self._head_rows(run_id)
            cursor = await self._connection.execute(
                'SELECT * FROM attempts WHERE run_id = ? ORDER BY created_at, attempt_id',
                (run_id,),
            )
            attempt_rows = await cursor.fetchall()
            cursor = await self._connection.execute(
                """
                SELECT * FROM retry_requests
                WHERE run_id = ? AND status = 'pending'
                ORDER BY created_at, request_id
                """,
                (run_id,),
            )
            retry_rows = await cursor.fetchall()
        snapshot = await asyncio.to_thread(_snapshot_from_rows, artifact_rows, ids)
        return (
            _run_state(state_row),
            snapshot,
            tuple(_attempt_snapshot(row) for row in attempt_rows),
            tuple(_retry_request(row) for row in retry_rows),
        )

    async def create_attempt(self, run_id: str, attempt_id: str, invocation_id: str,
                             operation_id: str, partition_key: str,
                             input_refs: Iterable[ArtifactRef] = (),
                             output_keys: Iterable[ArtifactKey] = (), *,
                             retry_request_id: str = ''
                             ) -> AttemptSnapshot:
        for value, name in (
            (run_id, 'run_id'),
            (attempt_id, 'attempt_id'),
            (invocation_id, 'invocation_id'),
            (operation_id, 'operation_id'),
        ):
            _text(value, name)
        _string(partition_key, 'partition_key')
        _string(retry_request_id, 'retry_request_id')
        inputs = tuple(input_refs)
        outputs = tuple(output_keys)
        created_at = time.time()
        snapshot = AttemptSnapshot(
            attempt_id,
            invocation_id,
            operation_id,
            partition_key,
            'scheduled',
            created_at,
            input_refs=inputs,
            output_keys=outputs,
            retry_request_id=retry_request_id,
        )

        async with self._transaction():
            await self._require_run(run_id)
            if retry_request_id:
                row = await self._retry_row(run_id, retry_request_id)
                if row is None or row['status'] != 'pending':
                    raise DefinitionError(f'pending retry request not found: {retry_request_id}')
                retry_key = ArtifactKey(row['artifact_id'], row['partition_key'])
                if retry_key not in outputs:
                    raise DefinitionError('retry target must be an invocation output')
            try:
                await self._connection.execute(
                    """
                    INSERT INTO attempts(
                      run_id, attempt_id, invocation_id, operation_id, partition_key,
                      retry_request_id, status, created_at, started_at, finished_at,
                      error_kind, error_message, input_refs_json, output_keys_json
                    ) VALUES (?, ?, ?, ?, ?, ?, 'scheduled', ?, NULL, NULL, '', '', ?, ?)
                    """,
                    (
                        run_id, attempt_id, invocation_id, operation_id, partition_key,
                        retry_request_id or None, created_at, _refs_json(inputs),
                        json.dumps([_key_data(key) for key in outputs], separators=(',', ':')),
                    ),
                )
            except aiosqlite.IntegrityError as exc:
                raise DefinitionError(f'attempt conflicts with existing execution: {attempt_id}') from exc
        return snapshot

    async def set_attempt_status(self, run_id: str, attempt_id: str, status: AttemptStatus, *,
                                 error: RuntimeErrorInfo | None = None
                                 ) -> AttemptSnapshot:
        _text(run_id, 'run_id')
        _text(attempt_id, 'attempt_id')
        if status in {'succeeded', 'discarded'}:
            raise DefinitionError(f'{status} attempt status is owned by artifact commit')
        async with self._transaction():
            row = await self._attempt_row(run_id, attempt_id)
            if row is None:
                raise DefinitionError(f'attempt not found: {attempt_id}')
            current = row['status']
            _validate_attempt_transition(current, status)
            if current == status:
                snapshot = _attempt_snapshot(row)
                if error is not None and error != snapshot.error:
                    raise DefinitionError('attempt terminal state cannot change its error')
                return snapshot
            updated = _attempt_status_values(row, status, error)
            await self._update_attempt(run_id, attempt_id, updated)
        return _attempt_snapshot(updated)

    async def fail_attempt_and_run(self, run_id: str, attempt_id: str,
                                   error: RuntimeErrorInfo
                                   ) -> AttemptSnapshot:
        if not isinstance(error, RuntimeErrorInfo):
            raise TypeError('error must be RuntimeErrorInfo')
        async with self._transaction():
            row = await self._attempt_row(run_id, attempt_id)
            if row is None:
                raise DefinitionError(f'attempt not found: {attempt_id}')
            updated = _attempt_status_values(row, 'failed', error)
            await self._update_attempt(run_id, attempt_id, updated)
            cursor = await self._connection.execute(
                """
                UPDATE runs SET status = 'failed', error_kind = ?, error_message = ?
                WHERE run_id = ?
                """,
                (error.kind, error.message, run_id),
            )
            if cursor.rowcount != 1:
                raise DefinitionError(f'run not found: {run_id}')
        return _attempt_snapshot(updated)

    async def finish_pause(self, run_id: str) -> None:
        await self._finish_stopping(run_id, 'paused', cancel_retries=False)

    async def finish_cancel(self, run_id: str) -> None:
        await self._finish_stopping(run_id, 'cancelled', cancel_retries=True)

    async def attempts(self, run_id: str) -> tuple[AttemptSnapshot, ...]:
        _text(run_id, 'run_id')
        async with self._lock:
            await self._require_run(run_id)
            cursor = await self._connection.execute(
                'SELECT * FROM attempts WHERE run_id = ? ORDER BY created_at, attempt_id',
                (run_id,),
            )
            rows = await cursor.fetchall()
        return tuple(_attempt_snapshot(row) for row in rows)

    async def append_progress(self, run_id: str, attempt_id: str,
                              update: ProgressUpdate
                              ) -> ProgressEvent:
        _text(run_id, 'run_id')
        _text(attempt_id, 'attempt_id')
        if not isinstance(update, ProgressUpdate):
            raise TypeError('update must be ProgressUpdate')
        created_at = time.time()
        detail_json = json.dumps(dict(update.detail), ensure_ascii=False, sort_keys=True)
        async with self._transaction():
            row = await self._attempt_row(run_id, attempt_id)
            if row is None:
                raise DefinitionError(f'attempt not found: {attempt_id}')
            if row['status'] != 'running':
                raise DefinitionError('progress can only be appended to a running attempt')
            cursor = await self._connection.execute(
                """
                SELECT COALESCE(MAX(sequence), 0) + 1 AS sequence
                FROM progress_events WHERE run_id = ? AND attempt_id = ?
                """,
                (run_id, attempt_id),
            )
            sequence = int((await cursor.fetchone())['sequence'])
            await self._connection.execute(
                """
                INSERT INTO progress_events(
                  run_id, attempt_id, sequence, phase, message,
                  current_value, total_value, detail_json, created_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    run_id, attempt_id, sequence, update.phase, update.message,
                    update.current, update.total, detail_json, created_at,
                ),
            )
        return ProgressEvent(attempt_id, sequence, update, created_at)

    async def progress_events(self, run_id: str, attempt_id: str | None = None
                              ) -> tuple[ProgressEvent, ...]:
        _text(run_id, 'run_id')
        statement = 'SELECT * FROM progress_events WHERE run_id = ?'
        parameters: tuple[object, ...] = (run_id,)
        if attempt_id is not None:
            _text(attempt_id, 'attempt_id')
            statement += ' AND attempt_id = ?'
            parameters = (run_id, attempt_id)
        statement += ' ORDER BY created_at, attempt_id, sequence'
        async with self._lock:
            await self._require_run(run_id)
            cursor = await self._connection.execute(statement, parameters)
            rows = await cursor.fetchall()
        return tuple(_progress_event(row) for row in rows)

    async def recover_runs(self) -> tuple[str, ...]:
        recovered: list[str] = []
        async with self._transaction():
            cursor = await self._connection.execute(
                """
                SELECT run_id, status FROM runs AS run
                WHERE status IN ('running', 'pausing', 'cancelling')
                   OR (
                     status = 'failed' AND EXISTS (
                       SELECT 1 FROM attempts AS attempt
                       WHERE attempt.run_id = run.run_id
                         AND attempt.status IN ('scheduled', 'running', 'cancelling')
                     )
                   )
                ORDER BY run_id
                """
            )
            rows = await cursor.fetchall()
            now = time.time()
            for row in rows:
                run_id = row['run_id']
                cancelling = row['status'] == 'cancelling'
                cursor = await self._connection.execute(
                    """
                    SELECT 1 FROM attempts
                    WHERE run_id = ? AND status IN ('scheduled', 'running', 'cancelling')
                    LIMIT 1
                    """,
                    (run_id,),
                )
                has_active_attempt = await cursor.fetchone() is not None
                if row['status'] == 'running' and not has_active_attempt:
                    continue
                if row['status'] == 'failed':
                    await self._connection.execute(
                        """
                        UPDATE attempts SET status = 'interrupted', finished_at = ?
                        WHERE run_id = ? AND status IN ('scheduled', 'running', 'cancelling')
                        """,
                        (now, run_id),
                    )
                    recovered.append(run_id)
                    continue
                attempt_status = 'cancelled' if cancelling else 'interrupted'
                run_status = 'cancelled' if cancelling else 'paused'
                await self._connection.execute(
                    """
                    UPDATE attempts SET status = ?, finished_at = ?
                    WHERE run_id = ? AND status IN ('scheduled', 'running', 'cancelling')
                    """,
                    (attempt_status, now, run_id),
                )
                await self._connection.execute(
                    "UPDATE runs SET status = ?, error_kind = '', error_message = '' WHERE run_id = ?",
                    (run_status, run_id),
                )
                if cancelling:
                    await self._cancel_pending_retries(run_id)
                recovered.append(run_id)
        return tuple(recovered)

    async def delete_run(self, run_id: str) -> None:
        _text(run_id, 'run_id')
        async with self._transaction():
            cursor = await self._connection.execute('DELETE FROM runs WHERE run_id = ?', (run_id,))
            if cursor.rowcount != 1:
                raise DefinitionError(f'run not found: {run_id}')

    async def _commit(self, prepared: _PreparedCommit, attempt_id: str | None) -> CommitResult:
        async with self._transaction():
            await self._require_run(prepared.run_id)
            attempt = None
            if attempt_id is not None:
                attempt = await self._attempt_row(prepared.run_id, attempt_id)
                if attempt is None:
                    raise DefinitionError(f'attempt not found: {attempt_id}')
                _validate_attempt_commit(attempt, prepared.command)

            replay = await self._replay(prepared)
            if replay is not None:
                if attempt is not None:
                    await self._reconcile_replayed_attempt(prepared.run_id, attempt, replay)
                return replay
            if attempt is not None and attempt['status'] != 'running':
                return CommitResult('stale')

            if attempt is not None and attempt['retry_request_id']:
                await self._validate_retry_attempt(prepared, attempt)

            retry_conflict = await self._pending_retry_conflict(prepared, attempt)
            if retry_conflict is not None:
                if attempt is None:
                    raise DefinitionError(
                        f'artifact has pending retry {retry_conflict}; '
                        'cancel the run or wait for it'
                    )
                result = CommitResult('stale')
                await self._finish_attempt_commit(prepared.run_id, attempt, result)
                return result

            rows = await self._head_rows(prepared.run_id)
            snapshot = await asyncio.to_thread(_snapshot_from_rows, rows, frozenset())
            if not await self._commit_is_current(prepared.run_id, prepared.command, snapshot):
                result = CommitResult('stale')
            else:
                result = await self._apply_commit(prepared)
                await self._write_receipt(prepared, result.refs)

            if attempt is not None:
                await self._finish_attempt_commit(prepared.run_id, attempt, result)
            return result

    async def _commit_is_current(self, run_id: str, commit: ArtifactCommit,
                                 snapshot: ArtifactSnapshot
                                 ) -> bool:
        for key, expected in commit.expected_heads.items():
            current = snapshot.records.get(key)
            if expected is None:
                if current is not None:
                    return False
            elif current is None or current.ref != expected:
                return False

        effective = snapshot.effective_records()
        if any(
            effective.get(ref.key) is None or effective[ref.key].ref != ref
            for write in commit.writes
            for ref in write.input_refs
        ):
            return False

        new_partition_sets = {
            write.key: write.value
            for write in commit.writes
            if isinstance(write.value, PartitionSet)
        }
        for guard in commit.partition_guards:
            partitions = new_partition_sets.get(guard.partition_set_key)
            if partitions is None:
                record = effective.get(guard.partition_set_key)
                if record is None:
                    return False
                payload = await self._payload(run_id, record.ref)
                if payload is None:
                    return False
                partitions = await asyncio.to_thread(pickle.loads, payload)
            if not isinstance(partitions, PartitionSet) or guard.partition_key not in partitions:
                return False
        return True

    async def _apply_commit(self, prepared: _PreparedCommit) -> CommitResult:
        records: list[ArtifactRecord] = []
        for write, payload in zip(prepared.command.writes, prepared.payloads, strict=True):
            ref = await self._next_ref(prepared.run_id, write.key)
            record = ArtifactRecord(ref, prepared.command.producer, write.input_refs)
            await self._connection.execute(
                """
                INSERT INTO artifacts(
                  run_id, artifact_id, partition_key, version,
                  producer, input_refs_json, payload
                ) VALUES (?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    prepared.run_id, ref.key.artifact_id, ref.key.partition_key,
                    ref.version, record.producer, _refs_json(record.input_refs), payload,
                ),
            )
            records.append(record)
        return CommitResult('ok', tuple(record.ref for record in records))

    async def _write_receipt(self, prepared: _PreparedCommit,
                             refs: tuple[ArtifactRef, ...]
                             ) -> None:
        await self._connection.execute(
            """
            INSERT INTO commits(run_id, commit_id, request_hash, refs_json)
            VALUES (?, ?, ?, ?)
            """,
            (prepared.run_id, prepared.command.commit_id, prepared.request_hash, _refs_json(refs)),
        )

    async def _replay(self, prepared: _PreparedCommit) -> CommitResult | None:
        cursor = await self._connection.execute(
            'SELECT request_hash, refs_json FROM commits WHERE run_id = ? AND commit_id = ?',
            (prepared.run_id, prepared.command.commit_id),
        )
        row = await cursor.fetchone()
        if row is None:
            return None
        if row['request_hash'] != prepared.request_hash:
            raise DefinitionError(f'commit id reused with different request: {prepared.command.commit_id}')
        return CommitResult('ok', _refs_from_json(row['refs_json']), True)

    async def _pending_retry_conflict(self, prepared: _PreparedCommit,
                                      attempt: Mapping[str, object] | None
                                      ) -> str | None:
        allowed = '' if attempt is None else cast(str | None, attempt['retry_request_id']) or ''
        for write in prepared.command.writes:
            cursor = await self._connection.execute(
                """
                SELECT request_id FROM retry_requests
                WHERE run_id = ? AND artifact_id = ? AND partition_key = ? AND status = 'pending'
                """,
                (prepared.run_id, write.key.artifact_id, write.key.partition_key),
            )
            row = await cursor.fetchone()
            if row is not None and row['request_id'] != allowed:
                return cast(str, row['request_id'])
        return None

    async def _validate_retry_attempt(self, prepared: _PreparedCommit,
                                      attempt: Mapping[str, object]
                                      ) -> None:
        request_id = cast(str, attempt['retry_request_id'])
        row = await self._retry_row(prepared.run_id, request_id)
        if row is None or row['status'] != 'pending':
            raise DefinitionError(f'pending retry request not found: {request_id}')
        key = ArtifactKey(cast(str, row['artifact_id']), cast(str, row['partition_key']))
        base = ArtifactRef(key, cast(int, row['base_version']))
        if prepared.command.expected_heads.get(key) != base:
            raise DefinitionError('retry commit must compare against its requested base version')

    async def _finish_attempt_commit(self, run_id: str, attempt: Mapping[str, object],
                                     result: CommitResult
                                     ) -> None:
        status = 'succeeded' if result.status == 'ok' else 'discarded'
        cursor = await self._connection.execute(
            """
            UPDATE attempts SET status = ?, finished_at = ?
            WHERE run_id = ? AND attempt_id = ? AND status = 'running'
            """,
            (status, time.time(), run_id, attempt['attempt_id']),
        )
        if cursor.rowcount != 1:
            raise DefinitionError(f'attempt is no longer running: {attempt["attempt_id"]}')

        request_id = cast(str | None, attempt['retry_request_id']) or ''
        if result.status == 'ok' and request_id:
            row = await self._retry_row(run_id, request_id)
            if row is None or row['status'] != 'pending':
                raise DefinitionError(f'pending retry request not found: {request_id}')
            key = ArtifactKey(row['artifact_id'], row['partition_key'])
            ref = next((ref for ref in result.refs if ref.key == key), None)
            if ref is None:
                raise DefinitionError('retry operation did not write its target artifact')
            await self._connection.execute(
                """
                UPDATE retry_requests SET status = 'fulfilled', result_version = ?
                WHERE run_id = ? AND request_id = ? AND status = 'pending'
                """,
                (ref.version, run_id, request_id),
            )

    async def _reconcile_replayed_attempt(self, run_id: str,
                                          attempt: Mapping[str, object], result: CommitResult
                                          ) -> None:
        if attempt['status'] == 'succeeded':
            return
        if attempt['status'] != 'running':
            raise DefinitionError(
                f'replayed commit conflicts with attempt state: '
                f'{attempt["attempt_id"]} is {attempt["status"]}'
            )
        await self._finish_attempt_commit(run_id, attempt, result)

    async def _finish_stopping(self, run_id: str, status: Literal['paused', 'cancelled'], *,
                               cancel_retries: bool
                               ) -> None:
        async with self._transaction():
            await self._require_run(run_id)
            await self._connection.execute(
                """
                UPDATE attempts SET status = 'cancelled', finished_at = ?
                WHERE run_id = ? AND status IN ('scheduled', 'running', 'cancelling')
                """,
                (time.time(), run_id),
            )
            await self._connection.execute(
                "UPDATE runs SET status = ?, error_kind = '', error_message = '' WHERE run_id = ?",
                (status, run_id),
            )
            if cancel_retries:
                await self._cancel_pending_retries(run_id)

    async def _cancel_pending_retries(self, run_id: str) -> None:
        await self._connection.execute(
            "UPDATE retry_requests SET status = 'cancelled' "
            "WHERE run_id = ? AND status = 'pending'",
            (run_id,),
        )

    async def _head_rows(self, run_id: str) -> list[aiosqlite.Row]:
        cursor = await self._connection.execute(
            """
            WITH heads AS (
              SELECT artifact_id, partition_key, MAX(version) AS version
              FROM artifacts WHERE run_id = ? GROUP BY artifact_id, partition_key
            )
            SELECT a.artifact_id, a.partition_key, a.version,
                   a.producer, a.input_refs_json, a.payload
            FROM artifacts a JOIN heads h
              ON h.artifact_id = a.artifact_id
             AND h.partition_key = a.partition_key
             AND h.version = a.version
            WHERE a.run_id = ?
            """,
            (run_id, run_id),
        )
        return list(await cursor.fetchall())

    async def _head_ref(self, run_id: str, key: ArtifactKey) -> ArtifactRef | None:
        cursor = await self._connection.execute(
            """
            SELECT MAX(version) AS version FROM artifacts
            WHERE run_id = ? AND artifact_id = ? AND partition_key = ?
            """,
            (run_id, key.artifact_id, key.partition_key),
        )
        row = await cursor.fetchone()
        return None if row['version'] is None else ArtifactRef(key, row['version'])

    async def _next_ref(self, run_id: str, key: ArtifactKey) -> ArtifactRef:
        current = await self._head_ref(run_id, key)
        return ArtifactRef(key, 1 if current is None else current.version + 1)

    async def _payload(self, run_id: str, ref: ArtifactRef) -> bytes | None:
        cursor = await self._connection.execute(
            """
            SELECT payload FROM artifacts
            WHERE run_id = ? AND artifact_id = ? AND partition_key = ? AND version = ?
            """,
            (run_id, ref.key.artifact_id, ref.key.partition_key, ref.version),
        )
        row = await cursor.fetchone()
        return None if row is None else row['payload']

    async def _attempt_row(self, run_id: str, attempt_id: str) -> aiosqlite.Row | None:
        cursor = await self._connection.execute(
            'SELECT * FROM attempts WHERE run_id = ? AND attempt_id = ?',
            (run_id, attempt_id),
        )
        return await cursor.fetchone()

    async def _retry_row(self, run_id: str, request_id: str) -> aiosqlite.Row | None:
        cursor = await self._connection.execute(
            'SELECT * FROM retry_requests WHERE run_id = ? AND request_id = ?',
            (run_id, request_id),
        )
        return await cursor.fetchone()

    async def _update_attempt(self, run_id: str, attempt_id: str,
                              values: Mapping[str, object]
                              ) -> None:
        await self._connection.execute(
            """
            UPDATE attempts SET status = ?, started_at = ?, finished_at = ?,
                                error_kind = ?, error_message = ?
            WHERE run_id = ? AND attempt_id = ?
            """,
            (
                values['status'], values['started_at'], values['finished_at'],
                values['error_kind'], values['error_message'], run_id, attempt_id,
            ),
        )

    async def _require_run(self, run_id: str) -> None:
        self._require_open()
        cursor = await self._connection.execute('SELECT 1 FROM runs WHERE run_id = ?', (run_id,))
        if await cursor.fetchone() is None:
            raise DefinitionError(f'run not found: {run_id}')

    def _require_open(self) -> None:
        if self._closed:
            raise RuntimeError('artifact store is closed')

    async def _create_schema(self) -> None:
        cursor = await self._connection.execute('PRAGMA user_version')
        version = int((await cursor.fetchone())[0])
        if version == _SCHEMA_VERSION:
            await self._validate_schema()
            return
        if version != 0:
            raise DefinitionError(
                f'unsupported artifact store schema version: {version}; delete and recreate it'
            )
        cursor = await self._connection.execute(
            "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' LIMIT 1"
        )
        if await cursor.fetchone() is not None:
            raise DefinitionError('unversioned artifact store; delete and recreate it')

        await self._connection.executescript(
            f"""
            BEGIN IMMEDIATE;
            CREATE TABLE runs(
              run_id TEXT PRIMARY KEY,
              status TEXT NOT NULL CHECK(status IN (
                'created', 'running', 'pausing', 'paused',
                'cancelling', 'cancelled', 'failed', 'completed'
              )),
              error_kind TEXT NOT NULL,
              error_message TEXT NOT NULL,
              CHECK(
                (status = 'failed' AND trim(error_kind) != '' AND trim(error_message) != '')
                OR (status != 'failed' AND error_kind = '' AND error_message = '')
              )
            );
            CREATE TABLE artifacts(
              run_id TEXT NOT NULL,
              artifact_id TEXT NOT NULL,
              partition_key TEXT NOT NULL,
              version INTEGER NOT NULL CHECK(version > 0),
              producer TEXT NOT NULL,
              input_refs_json TEXT NOT NULL,
              payload BLOB NOT NULL,
              PRIMARY KEY(run_id, artifact_id, partition_key, version),
              FOREIGN KEY(run_id) REFERENCES runs(run_id) ON DELETE CASCADE
            );
            CREATE TABLE commits(
              run_id TEXT NOT NULL,
              commit_id TEXT NOT NULL,
              request_hash TEXT NOT NULL,
              refs_json TEXT NOT NULL,
              PRIMARY KEY(run_id, commit_id),
              FOREIGN KEY(run_id) REFERENCES runs(run_id) ON DELETE CASCADE
            );
            CREATE TABLE retry_requests(
              run_id TEXT NOT NULL,
              request_id TEXT NOT NULL,
              artifact_id TEXT NOT NULL,
              partition_key TEXT NOT NULL,
              base_version INTEGER NOT NULL CHECK(base_version > 0),
              status TEXT NOT NULL CHECK(status IN ('pending', 'fulfilled', 'cancelled')),
              created_at REAL NOT NULL,
              result_version INTEGER,
              PRIMARY KEY(run_id, request_id),
              FOREIGN KEY(run_id) REFERENCES runs(run_id) ON DELETE CASCADE,
              FOREIGN KEY(run_id, artifact_id, partition_key, base_version)
                REFERENCES artifacts(run_id, artifact_id, partition_key, version),
              FOREIGN KEY(run_id, artifact_id, partition_key, result_version)
                REFERENCES artifacts(run_id, artifact_id, partition_key, version),
              CHECK(
                (status = 'fulfilled' AND result_version IS NOT NULL AND result_version > base_version)
                OR (status != 'fulfilled' AND result_version IS NULL)
              )
            );
            CREATE UNIQUE INDEX pending_retry_by_artifact
              ON retry_requests(run_id, artifact_id, partition_key)
              WHERE status = 'pending';
            CREATE TABLE attempts(
              run_id TEXT NOT NULL,
              attempt_id TEXT NOT NULL,
              invocation_id TEXT NOT NULL,
              operation_id TEXT NOT NULL,
              partition_key TEXT NOT NULL,
              retry_request_id TEXT,
              status TEXT NOT NULL CHECK(status IN (
                'scheduled', 'running', 'cancelling', 'cancelled',
                'succeeded', 'failed', 'interrupted', 'discarded'
              )),
              created_at REAL NOT NULL,
              started_at REAL,
              finished_at REAL,
              error_kind TEXT NOT NULL,
              error_message TEXT NOT NULL,
              input_refs_json TEXT NOT NULL,
              output_keys_json TEXT NOT NULL,
              PRIMARY KEY(run_id, attempt_id),
              FOREIGN KEY(run_id) REFERENCES runs(run_id) ON DELETE CASCADE,
              FOREIGN KEY(run_id, retry_request_id)
                REFERENCES retry_requests(run_id, request_id),
              CHECK(
                (status = 'failed' AND trim(error_kind) != '' AND trim(error_message) != '')
                OR (status != 'failed' AND error_kind = '' AND error_message = '')
              ),
              CHECK(
                (status IN ('scheduled', 'running', 'cancelling') AND finished_at IS NULL)
                OR (status IN ('cancelled', 'succeeded', 'failed', 'interrupted', 'discarded')
                    AND finished_at IS NOT NULL)
              ),
              CHECK(status NOT IN ('running', 'succeeded', 'discarded')
                    OR started_at IS NOT NULL)
            );
            CREATE UNIQUE INDEX active_attempt_by_invocation
              ON attempts(run_id, invocation_id)
              WHERE status IN ('scheduled', 'running', 'cancelling');
            CREATE TABLE progress_events(
              run_id TEXT NOT NULL,
              attempt_id TEXT NOT NULL,
              sequence INTEGER NOT NULL CHECK(sequence > 0),
              phase TEXT NOT NULL,
              message TEXT NOT NULL,
              current_value INTEGER,
              total_value INTEGER,
              detail_json TEXT NOT NULL,
              created_at REAL NOT NULL,
              PRIMARY KEY(run_id, attempt_id, sequence),
              FOREIGN KEY(run_id, attempt_id)
                REFERENCES attempts(run_id, attempt_id) ON DELETE CASCADE,
              CHECK(current_value IS NULL OR current_value >= 0),
              CHECK(total_value IS NULL OR total_value >= 0),
              CHECK(current_value IS NULL OR total_value IS NULL OR current_value <= total_value)
            );
            PRAGMA user_version = {_SCHEMA_VERSION};
            COMMIT;
            """
        )

    async def _validate_schema(self) -> None:
        cursor = await self._connection.execute(
            "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'"
        )
        tables = frozenset(row[0] for row in await cursor.fetchall())
        if tables != _SCHEMA_TABLES:
            raise DefinitionError('invalid artifact store schema; delete and recreate it')
        cursor = await self._connection.execute('PRAGMA foreign_key_check')
        if await cursor.fetchone() is not None:
            raise DefinitionError('artifact store contains invalid references')

    @asynccontextmanager
    async def _transaction(self) -> AsyncIterator[None]:
        async with self._lock:
            self._require_open()
            try:
                await self._connection.execute('BEGIN IMMEDIATE')
                yield
                await self._connection.commit()
            except BaseException:
                rollback = asyncio.create_task(self._connection.rollback())
                while not rollback.done():
                    try:
                        await asyncio.shield(rollback)
                    except asyncio.CancelledError:
                        continue
                await rollback
                raise


def _run_state(row: Mapping[str, object]) -> StoredRunState:
    status = cast(RunStatus, row['status'])
    error = None
    if status == 'failed':
        error = RuntimeErrorInfo(cast(str, row['error_kind']), cast(str, row['error_message']))
    return StoredRunState(status, error)


def _record_from_row(ref: ArtifactRef, row: Mapping[str, object]) -> ArtifactRecord:
    return ArtifactRecord(ref, cast(str, row['producer']), _refs_from_json(cast(str, row['input_refs_json'])))


def _snapshot_from_rows(rows: Iterable[Mapping[str, object]],
                        partition_set_ids: frozenset[str]
                        ) -> ArtifactSnapshot:
    records: dict[ArtifactKey, ArtifactRecord] = {}
    partition_sets: dict[ArtifactKey, PartitionSet] = {}
    for row in rows:
        key = ArtifactKey(cast(str, row['artifact_id']), cast(str, row['partition_key']))
        ref = ArtifactRef(key, cast(int, row['version']))
        records[key] = _record_from_row(ref, row)
        if key.artifact_id in partition_set_ids and not key.partition_key:
            value = pickle.loads(cast(bytes, row['payload']))
            if not isinstance(value, PartitionSet):
                raise DefinitionError(f'{key.artifact_id} must contain a PartitionSet value')
            partition_sets[key] = value
    return ArtifactSnapshot(records, partition_sets)


def _validate_attempt_transition(current: str, target: AttemptStatus) -> None:
    if current != target and target not in _ATTEMPT_TRANSITIONS.get(current, frozenset()):
        raise DefinitionError(f'cannot transition attempt from {current} to {target}')


def _attempt_status_values(row: Mapping[str, object], status: AttemptStatus,
                           error: RuntimeErrorInfo | None
                           ) -> dict[str, object]:
    _validate_attempt_transition(cast(str, row['status']), status)
    if status == 'failed' and error is None:
        raise DefinitionError('failed attempt requires error details')
    if status != 'failed' and error is not None:
        raise DefinitionError('attempt error is only valid for failed status')
    values = dict(row)
    now = time.time()
    if status == 'running' and values['started_at'] is None:
        values['started_at'] = now
    if status in {'cancelled', 'succeeded', 'failed', 'interrupted', 'discarded'}:
        values['finished_at'] = now
    values.update({
        'status': status,
        'error_kind': '' if error is None else error.kind,
        'error_message': '' if error is None else error.message,
    })
    return values


def _validate_attempt_commit(attempt: Mapping[str, object], commit: ArtifactCommit) -> None:
    attempt_id = attempt['attempt_id']
    if attempt['invocation_id'] != commit.commit_id:
        raise DefinitionError(f'attempt {attempt_id} does not belong to commit {commit.commit_id}')
    expected_producer = f'operation:{attempt["operation_id"]}'
    if commit.producer != expected_producer:
        raise DefinitionError(f'attempt {attempt_id} requires producer {expected_producer}')
    input_refs = _refs_from_json(cast(str, attempt['input_refs_json']))
    if any(write.input_refs != input_refs for write in commit.writes):
        raise DefinitionError(f'attempt {attempt_id} input refs do not match commit lineage')
    outputs = {
        ArtifactKey(item[0], item[1])
        for item in json.loads(cast(str, attempt['output_keys_json']))
    }
    if not outputs.issubset(commit.output_keys):
        raise DefinitionError(f'attempt {attempt_id} declared outputs are missing from commit')
    partition_key = cast(str, attempt['partition_key'])
    if partition_key and not any(guard.partition_key == partition_key for guard in commit.partition_guards):
        raise DefinitionError(f'attempt {attempt_id} partition guard does not match commit')


def _attempt_snapshot(row: Mapping[str, object]) -> AttemptSnapshot:
    status = cast(AttemptStatus, row['status'])
    error = None
    if status == 'failed':
        error = RuntimeErrorInfo(cast(str, row['error_kind']), cast(str, row['error_message']))
    output_keys = tuple(
        ArtifactKey(item[0], item[1])
        for item in json.loads(cast(str, row['output_keys_json']))
    )
    return AttemptSnapshot(
        cast(str, row['attempt_id']),
        cast(str, row['invocation_id']),
        cast(str, row['operation_id']),
        cast(str, row['partition_key']),
        status,
        cast(float, row['created_at']),
        cast(float | None, row['started_at']),
        cast(float | None, row['finished_at']),
        error,
        _refs_from_json(cast(str, row['input_refs_json'])),
        output_keys,
        cast(str | None, row['retry_request_id']) or '',
    )


def _retry_request(row: Mapping[str, object]) -> ArtifactRetryRequest:
    key = ArtifactKey(cast(str, row['artifact_id']), cast(str, row['partition_key']))
    result_version = cast(int | None, row['result_version'])
    return ArtifactRetryRequest(
        cast(str, row['request_id']),
        key,
        ArtifactRef(key, cast(int, row['base_version'])),
        cast(Literal['pending', 'fulfilled', 'cancelled'], row['status']),
        cast(float, row['created_at']),
        None if result_version is None else ArtifactRef(key, result_version),
    )


def _progress_event(row: Mapping[str, object]) -> ProgressEvent:
    update = ProgressUpdate(
        cast(str, row['phase']),
        cast(str, row['message']),
        cast(int | None, row['current_value']),
        cast(int | None, row['total_value']),
        json.loads(cast(str, row['detail_json'])),
    )
    return ProgressEvent(
        cast(str, row['attempt_id']),
        cast(int, row['sequence']),
        update,
        cast(float, row['created_at']),
    )


def _prepare_commit(run_id: str, commit: ArtifactCommit) -> _PreparedCommit:
    payloads = tuple(
        pickle.dumps(write.value, protocol=pickle.HIGHEST_PROTOCOL)
        for write in commit.writes
    )
    return _PreparedCommit(run_id, commit, payloads, _commit_fingerprint(run_id, commit, payloads))


def _commit_fingerprint(run_id: str, commit: ArtifactCommit, payloads: tuple[bytes, ...]) -> str:
    digest = hashlib.sha256()
    digest.update(run_id.encode())
    digest.update(commit.producer.encode())
    for write, payload in zip(commit.writes, payloads, strict=True):
        digest.update(json.dumps(_key_data(write.key), separators=(',', ':')).encode())
        digest.update(_refs_json(write.input_refs).encode())
        digest.update(payload)
    expected = [
        [_key_data(key), _ref_data(ref)]
        for key, ref in sorted(commit.expected_heads.items())
    ]
    guards = [
        [_key_data(guard.partition_set_key), guard.partition_key]
        for guard in sorted(commit.partition_guards)
    ]
    digest.update(json.dumps(expected, separators=(',', ':')).encode())
    digest.update(json.dumps(guards, separators=(',', ':')).encode())
    return digest.hexdigest()


def _deserialize_many(refs: tuple[ArtifactRef, ...], payloads: Mapping[ArtifactRef, bytes]
                      ) -> Mapping[ArtifactRef, object]:
    return {ref: pickle.loads(payloads[ref]) for ref in refs}


def _key_data(key: ArtifactKey) -> list[str]:
    return [key.artifact_id, key.partition_key]


def _ref_data(ref: ArtifactRef | None) -> list[object] | None:
    if ref is None:
        return None
    return [ref.key.artifact_id, ref.key.partition_key, ref.version]


def _refs_json(refs: Iterable[ArtifactRef]) -> str:
    return json.dumps([_ref_data(ref) for ref in refs], separators=(',', ':'))


def _refs_from_json(value: str) -> tuple[ArtifactRef, ...]:
    return tuple(
        ArtifactRef(ArtifactKey(item[0], item[1]), item[2])
        for item in json.loads(value)
    )


__all__ = ['ArtifactStore', 'CommitResult', 'StoredRunState']
