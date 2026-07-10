from __future__ import annotations

import json
import sqlite3
import time
from collections.abc import Mapping
from contextlib import closing, contextmanager
from dataclasses import dataclass, replace
from pathlib import Path
from typing import Iterator, Literal

from evo.artifact_runtime.kernel.artifact import ArtifactKey, ArtifactRef

from .state import Checkpoint, FlowRunState


SQLITE_TIMEOUT_SECONDS = 30.0
SQLITE_BUSY_TIMEOUT_MS = 30000
SQLITE_BOOTSTRAP_ATTEMPTS = 5


@dataclass(frozen=True)
class CommandReceipt:
    status: Literal['new', 'done', 'conflict']
    state: FlowRunState
    outcome: Mapping[str, object] | None = None


class SQLiteFlowGate:
    def __init__(self, root: str | Path) -> None:
        self._root = Path(root)
        self._root.mkdir(parents=True, exist_ok=True)
        self._db_path = self._root / 'artifact_store.sqlite3'
        _retry_sqlite(self._create_schema)

    def get(self, run_id: str) -> FlowRunState | None:
        _require_text(run_id, 'run_id')
        with closing(self._connect()) as conn:
            row = conn.execute('SELECT * FROM flow_gates WHERE run_id = ?', (run_id,)).fetchone()
        return None if row is None else _row_state(row)

    def ensure(self, run_id: str) -> FlowRunState:
        _require_text(run_id, 'run_id')
        with self._transaction() as conn:
            return self._state(conn, run_id, create=True)

    def delete_run_state(self, run_id: str) -> None:
        _require_text(run_id, 'run_id')
        with self._transaction() as conn:
            conn.execute('DELETE FROM flow_gates WHERE run_id = ?', (run_id,))
            conn.execute('DELETE FROM flow_command_receipts WHERE run_id = ?', (run_id,))

    def read_command(self, run_id: str, command_id: str, request_hash: str) -> CommandReceipt:
        _require_command(run_id, command_id, request_hash)
        with self._transaction() as conn:
            state = self._state(conn, run_id, create=True)
            replay = self._receipt_replay(conn, run_id, command_id, request_hash, state)
            return replay or CommandReceipt('new', state)

    def record_command(self, run_id: str, command_id: str, request_hash: str,
                       outcome: Mapping[str, object], *, next_state: FlowRunState | None = None,
                       expected_version: int | None = None) -> CommandReceipt:
        _require_command(run_id, command_id, request_hash)
        if next_state is not None:
            if next_state.run_id != run_id:
                raise ValueError('next_state run_id must match run_id')
            _require_expected_version(expected_version)
        with self._transaction() as conn:
            state = self._state(conn, run_id, create=True)
            replay = self._receipt_replay(conn, run_id, command_id, request_hash, state)
            if replay is not None:
                return replay

            stored_outcome = dict(outcome)
            if next_state is not None:
                if state.status_version == expected_version:
                    self._write_state(conn, next_state, expected_version + 1)
                    state = self._state(conn, run_id)
                else:
                    stored_outcome = {'receipt_status': 'stale', 'status': state.status}
            self._insert_receipt(conn, run_id, command_id, request_hash, stored_outcome)
            return CommandReceipt('done', state, stored_outcome)

    def apply_gate_command(self, run_id: str, command_id: str,
                           request_hash: str, command_kind: str) -> CommandReceipt:
        _require_command(run_id, command_id, request_hash)
        if command_kind not in {'pause', 'resume', 'cancel', 'retry'}:
            raise ValueError('command_kind must be pause, resume, cancel, or retry')
        with self._transaction() as conn:
            state = self._state(conn, run_id, create=True)
            replay = self._receipt_replay(conn, run_id, command_id, request_hash, state)
            if replay is not None:
                return replay

            next_state = _apply_gate_transition(state, command_kind)
            if next_state != state:
                self._write_state(conn, next_state, state.status_version + 1)
                state = self._state(conn, run_id)
            outcome = {'status': state.status}
            self._insert_receipt(conn, run_id, command_id, request_hash, outcome)
            return CommandReceipt('done', state, outcome)

    def _create_schema(self) -> None:
        with closing(self._connect()) as conn:
            conn.executescript(
                """
                CREATE TABLE IF NOT EXISTS flow_gates(
                  run_id TEXT PRIMARY KEY NOT NULL,
                  status TEXT NOT NULL CHECK(status IN ('idle', 'paused', 'cancelled', 'failed')),
                  status_version INTEGER NOT NULL CHECK(typeof(status_version) = 'integer' AND status_version >= 0),
                  pending_checkpoint_json TEXT,
                  released_checkpoints_json TEXT NOT NULL,
                  last_error TEXT NOT NULL,
                  updated_at REAL NOT NULL
                );

                CREATE TABLE IF NOT EXISTS flow_command_receipts(
                  run_id TEXT NOT NULL,
                  command_id TEXT NOT NULL,
                  request_hash TEXT NOT NULL,
                  outcome_json TEXT NOT NULL,
                  updated_at REAL NOT NULL,
                  PRIMARY KEY (run_id, command_id)
                );
                """
            )

    def _connect(self) -> sqlite3.Connection:
        conn = sqlite3.connect(self._db_path, timeout=SQLITE_TIMEOUT_SECONDS)
        conn.row_factory = sqlite3.Row
        conn.execute('PRAGMA foreign_keys = ON')
        conn.execute(f'PRAGMA busy_timeout = {SQLITE_BUSY_TIMEOUT_MS}')
        conn.execute('PRAGMA synchronous = FULL')
        row = conn.execute('PRAGMA journal_mode = WAL').fetchone()
        if str(row[0]).lower() != 'wal':
            conn.close()
            raise sqlite3.OperationalError('failed to enable SQLite WAL mode')
        return conn

    @contextmanager
    def _transaction(self) -> Iterator[sqlite3.Connection]:
        with closing(self._connect()) as conn:
            conn.execute('BEGIN IMMEDIATE')
            try:
                yield conn
            except Exception:
                conn.rollback()
                raise
            else:
                conn.commit()

    def _state(self, conn: sqlite3.Connection, run_id: str, *, create: bool = False) -> FlowRunState:
        if create:
            conn.execute(
                """
                INSERT INTO flow_gates(
                  run_id, status, status_version,
                  pending_checkpoint_json, released_checkpoints_json, last_error, updated_at
                )
                VALUES (?, 'idle', 0, NULL, '{}', '', ?)
                ON CONFLICT(run_id) DO NOTHING
                """,
                (run_id, time.time()),
            )
        row = conn.execute('SELECT * FROM flow_gates WHERE run_id = ?', (run_id,)).fetchone()
        if row is None:
            raise RuntimeError('flow gate row is missing')
        return _row_state(row)

    def _receipt(self, conn: sqlite3.Connection, run_id: str, command_id: str) -> sqlite3.Row | None:
        return conn.execute(
            """
            SELECT request_hash, outcome_json
            FROM flow_command_receipts
            WHERE run_id = ? AND command_id = ?
            """,
            (run_id, command_id),
        ).fetchone()

    def _receipt_replay(self, conn: sqlite3.Connection, run_id: str, command_id: str,
                        request_hash: str, state: FlowRunState) -> CommandReceipt | None:
        receipt = self._receipt(conn, run_id, command_id)
        if receipt is None:
            return None
        if receipt['request_hash'] != request_hash:
            return CommandReceipt('conflict', state)
        return CommandReceipt('done', state, _outcome_from_json(receipt['outcome_json']))

    def _insert_receipt(self, conn: sqlite3.Connection, run_id: str, command_id: str,
                        request_hash: str, outcome: Mapping[str, object]) -> None:
        conn.execute(
            """
            INSERT INTO flow_command_receipts(
              run_id, command_id, request_hash, outcome_json, updated_at
            )
            VALUES (?, ?, ?, ?, ?)
            """,
            (run_id, command_id, request_hash, _json(dict(outcome)), time.time()),
        )

    def _write_state(self, conn: sqlite3.Connection, state: FlowRunState, version: int) -> None:
        changed = conn.execute(
            """
            UPDATE flow_gates
            SET status = ?,
                status_version = ?,
                pending_checkpoint_json = ?,
                released_checkpoints_json = ?,
                last_error = ?,
                updated_at = ?
            WHERE run_id = ?
            """,
            (
                state.status,
                version,
                _checkpoint_json(state.pending_checkpoint),
                _released_json(state.released_checkpoints),
                state.last_error,
                time.time(),
                state.run_id,
            ),
        ).rowcount
        if changed != 1:
            raise RuntimeError('flow gate row is missing')


def _row_state(row: sqlite3.Row) -> FlowRunState:
    return FlowRunState(
        row['run_id'],
        status=row['status'],
        status_version=row['status_version'],
        pending_checkpoint=_checkpoint_from_json(row['pending_checkpoint_json']),
        released_checkpoints=_released_from_json(row['released_checkpoints_json']),
        last_error=row['last_error'],
    )


def _apply_gate_transition(state: FlowRunState, command_kind: str) -> FlowRunState:
    if command_kind == 'pause':
        if state.status in {'paused', 'cancelled', 'failed'}:
            return state
        return replace(state, status='paused')
    if command_kind == 'resume':
        if state.status != 'paused':
            return state
        if state.pending_checkpoint is not None:
            return state
        return replace(state, status='idle', last_error='')
    if command_kind == 'cancel':
        if state.status == 'cancelled':
            return state
        return replace(state, status='cancelled')
    if command_kind == 'retry':
        if state.status != 'failed':
            return state
        return replace(state, status='idle', pending_checkpoint=None, last_error='')
    raise ValueError('unknown gate command')


def _checkpoint_json(checkpoint: Checkpoint | None) -> str | None:
    if checkpoint is None:
        return None
    return _json({'step': checkpoint.step, 'root': _key_json(checkpoint.root), 'ref': _ref_json(checkpoint.ref)})


def _checkpoint_from_json(value: str | None) -> Checkpoint | None:
    if value is None:
        return None
    item = json.loads(value)
    return Checkpoint(item['step'], ArtifactKey(*item['root']), _ref_from_json(item['ref']))


def _released_json(released: Mapping[str, ArtifactRef]) -> str:
    return _json({step: _ref_json(ref) for step, ref in released.items()})


def _released_from_json(value: str) -> dict[str, ArtifactRef]:
    return {step: _ref_from_json(ref) for step, ref in json.loads(value).items()}


def _outcome_from_json(value: str) -> dict[str, object]:
    return json.loads(value)


def _json(value: object) -> str:
    return json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False)


def _key_json(key: ArtifactKey) -> list[str]:
    return [key.artifact_id, key.partition]


def _ref_json(ref: ArtifactRef) -> list[object]:
    return [ref.key.artifact_id, ref.key.partition, ref.version]


def _ref_from_json(value: list[object]) -> ArtifactRef:
    return ArtifactRef(ArtifactKey(str(value[0]), str(value[1])), int(value[2]))


def _retry_sqlite(fn) -> None:
    for attempt in range(SQLITE_BOOTSTRAP_ATTEMPTS):
        try:
            fn()
            return
        except sqlite3.OperationalError as exc:
            if 'locked' not in str(exc).lower() or attempt == SQLITE_BOOTSTRAP_ATTEMPTS - 1:
                raise
            time.sleep(0.05 * (2 ** attempt))


def _require_command(run_id: str, command_id: str, request_hash: str) -> None:
    _require_text(run_id, 'run_id')
    _require_text(command_id, 'command_id')
    _require_text(request_hash, 'request_hash')


def _require_expected_version(value: int | None) -> None:
    if not isinstance(value, int) or isinstance(value, bool):
        raise TypeError('expected_version must be int')
    if value < 0:
        raise ValueError('expected_version must be >= 0')


def _require_text(value: str, name: str) -> None:
    if not isinstance(value, str):
        raise TypeError(f'{name} must be str')
    if not value.strip():
        raise ValueError(f'{name} must be non-empty')


__all__ = [
    'CommandReceipt',
    'SQLiteFlowGate',
]
