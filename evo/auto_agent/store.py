from __future__ import annotations

from contextlib import contextmanager
from dataclasses import dataclass
import os
import time
import uuid
from pathlib import Path
import sqlite3
from threading import RLock
from typing import Iterator

from pydantic import ValidationError

from evo.artifact_runtime.utils import validate_nonempty

from .models import AutoActionRecord, AutoAgentConfig, AutoAgentState


@dataclass(frozen=True)
class AutoAgentLease:
    thread_id: str
    owner_id: str
    fencing_token: str
    expires_at: float


class AutoAgentLeaseError(RuntimeError):
    pass


class AutoAgentStateError(RuntimeError):
    pass


class AutoAgentStore:
    def __init__(self, base_dir: str | Path, *, lease_seconds: float = 30.0) -> None:
        self.base_dir = Path(base_dir)
        self.lease_seconds = lease_seconds
        self._lock = RLock()
        self._lease_path.parent.mkdir(parents=True, exist_ok=True)
        self._connection = sqlite3.connect(str(self._lease_path), check_same_thread=False, isolation_level=None)
        self._connection.row_factory = sqlite3.Row
        self._connection.execute('PRAGMA journal_mode=WAL')
        self._init_schema()

    def close(self) -> None:
        self._connection.close()

    @contextmanager
    def lease(self, thread_id: str, *, owner_id: str) -> Iterator[AutoAgentLease]:
        lease = self.claim_lease(thread_id, owner_id=owner_id)
        try:
            yield lease
        finally:
            self.release_lease(lease)

    def claim_lease(self, thread_id: str, *, owner_id: str) -> AutoAgentLease:
        validate_nonempty(thread_id, 'thread_id')
        validate_nonempty(owner_id, 'owner_id')
        now = time.time()
        expires_at = now + self.lease_seconds
        token = uuid.uuid4().hex
        with self._transaction() as conn:
            row = conn.execute('SELECT * FROM auto_agent_lease WHERE thread_id = ?', (thread_id,)).fetchone()
            if row is not None and float(row['expires_at']) > now and str(row['owner_id']) != owner_id:
                raise AutoAgentLeaseError('auto agent lease is held')
            conn.execute(
                """
                INSERT INTO auto_agent_lease(thread_id, owner_id, fencing_token, expires_at, heartbeat_at)
                VALUES (?, ?, ?, ?, ?)
                ON CONFLICT(thread_id) DO UPDATE SET
                    owner_id = excluded.owner_id,
                    fencing_token = excluded.fencing_token,
                    expires_at = excluded.expires_at,
                    heartbeat_at = excluded.heartbeat_at
                """,
                (thread_id, owner_id, token, expires_at, now),
            )
        return AutoAgentLease(thread_id, owner_id, token, expires_at)

    def heartbeat(self, lease: AutoAgentLease) -> AutoAgentLease:
        now = time.time()
        expires_at = now + self.lease_seconds
        with self._transaction() as conn:
            self._require_lease(conn, lease)
            conn.execute(
                (
                    'UPDATE auto_agent_lease SET expires_at = ?, heartbeat_at = ? '
                    'WHERE thread_id = ? AND fencing_token = ?'
                ),
                (expires_at, now, lease.thread_id, lease.fencing_token),
            )
        return AutoAgentLease(lease.thread_id, lease.owner_id, lease.fencing_token, expires_at)

    def release_lease(self, lease: AutoAgentLease) -> None:
        with self._transaction() as conn:
            conn.execute(
                'DELETE FROM auto_agent_lease WHERE thread_id = ? AND fencing_token = ?',
                (lease.thread_id, lease.fencing_token),
            )

    def lease_status(self, thread_id: str) -> dict[str, object]:
        now = time.time()
        with self._lock:
            row = self._connection.execute(
                'SELECT owner_id, fencing_token, expires_at, heartbeat_at FROM auto_agent_lease WHERE thread_id = ?',
                (thread_id,),
            ).fetchone()
        if row is None:
            return {'active': False}
        return {
            'active': float(row['expires_at']) > now,
            'owner_id': str(row['owner_id']),
            'expires_at': float(row['expires_at']),
            'heartbeat_at': float(row['heartbeat_at']),
        }

    def load(self, thread_id: str, *, config: AutoAgentConfig | None = None) -> AutoAgentState:
        with self._lock:
            path = self._path(thread_id)
            if not path.exists():
                return AutoAgentState(thread_id=thread_id, config=config or AutoAgentConfig())
            try:
                state = AutoAgentState.model_validate_json(path.read_text(encoding='utf-8'))
            except (OSError, UnicodeError, ValidationError) as exc:
                raise AutoAgentStateError(
                    f'invalid auto agent state for {thread_id}: {path} preserved and not overwritten'
                ) from exc
            if state.thread_id != thread_id:
                raise AutoAgentStateError(
                    f'auto agent state thread mismatch for {thread_id}: found {state.thread_id}'
                )
            if config is not None:
                state = state.model_copy(update={'config': config})
            return state

    def save(
        self,
        state: AutoAgentState,
        *,
        lease: AutoAgentLease | None = None,
        preserve_stopped: bool = False,
    ) -> AutoAgentState:
        with self._lock:
            if lease is not None:
                self._require_lease(self._connection, lease)
            path = self._path(state.thread_id)
            if path.exists():
                try:
                    current = AutoAgentState.model_validate_json(path.read_text(encoding='utf-8'))
                except (OSError, UnicodeError, ValidationError) as exc:
                    raise AutoAgentStateError(
                        f'refusing to overwrite invalid auto agent state for {state.thread_id}: {path}'
                    ) from exc
                if current.thread_id != state.thread_id:
                    raise AutoAgentStateError(
                        f'refusing to overwrite mismatched auto agent state for {state.thread_id}: '
                        f'found {current.thread_id}'
                    )
                if preserve_stopped and not current.running and state.running:
                    state = state.model_copy(update={'running': False, 'stop_reason': current.stop_reason})
            path.parent.mkdir(parents=True, exist_ok=True)
            tmp = path.with_name(f'{path.name}.{uuid.uuid4().hex}.tmp')
            tmp.write_text(state.model_dump_json(indent=2), encoding='utf-8')
            os.replace(tmp, path)
            return state

    def mark_running(
        self,
        thread_id: str,
        config: AutoAgentConfig,
        *,
        lease: AutoAgentLease | None = None,
    ) -> AutoAgentState:
        state = self.load(thread_id, config=config).model_copy(update={'running': True, 'stop_reason': ''})
        return self.save(state, lease=lease)

    def mark_stopped(self, thread_id: str, reason: str) -> AutoAgentState:
        state = self.load(thread_id).model_copy(update={'running': False, 'stop_reason': reason})
        return self.save(state)

    def record_action(
        self,
        state: AutoAgentState,
        *,
        action_id: str,
        kind: str,
        target: str,
        status: str,
        reason: str,
        response: dict,
        lease: AutoAgentLease | None = None,
    ) -> AutoAgentState:
        records = (
            *state.records[-199:],
            AutoActionRecord(
                action_id=action_id,
                kind=kind,
                target=target,
                status=status,
                reason=reason,
                response=response,
                created_at=time.time(),
            ),
        )
        completed = state.completed_action_ids
        if status in {'ok', 'duplicate'}:
            completed = tuple(dict.fromkeys((*state.completed_action_ids, action_id)))[-500:]
        return self.save(
            state.model_copy(update={'records': records, 'completed_action_ids': completed}),
            lease=lease,
            preserve_stopped=lease is not None,
        )

    def assert_lease(self, lease: AutoAgentLease) -> None:
        with self._lock:
            self._require_lease(self._connection, lease)

    def _path(self, thread_id: str) -> Path:
        validate_nonempty(thread_id, 'thread_id')
        parts = Path(thread_id).parts
        if Path(thread_id).is_absolute() or len(parts) != 1 or parts[0] in {'.', '..'}:
            raise AutoAgentStateError(f'invalid auto agent thread_id path segment: {thread_id}')
        return self.base_dir / 'state' / 'threads' / thread_id / 'auto_agent.json'

    @property
    def _lease_path(self) -> Path:
        return self.base_dir / 'state' / 'auto-agent-leases.sqlite'

    @contextmanager
    def _transaction(self) -> Iterator[sqlite3.Connection]:
        with self._lock:
            self._connection.execute('BEGIN IMMEDIATE')
            try:
                yield self._connection
            except Exception:
                self._connection.rollback()
                raise
            else:
                self._connection.commit()

    def _require_lease(self, conn: sqlite3.Connection, lease: AutoAgentLease) -> None:
        row = conn.execute(
            'SELECT * FROM auto_agent_lease WHERE thread_id = ? AND fencing_token = ?',
            (lease.thread_id, lease.fencing_token),
        ).fetchone()
        if row is None or str(row['owner_id']) != lease.owner_id or float(row['expires_at']) <= time.time():
            raise AutoAgentLeaseError('stale auto agent lease')

    def _init_schema(self) -> None:
        self._connection.execute(
            """
            CREATE TABLE IF NOT EXISTS auto_agent_lease (
                thread_id TEXT PRIMARY KEY,
                owner_id TEXT NOT NULL,
                fencing_token TEXT NOT NULL,
                expires_at REAL NOT NULL,
                heartbeat_at REAL NOT NULL
            )
            """
        )
