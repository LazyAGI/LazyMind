from __future__ import annotations

from collections.abc import Mapping
from contextlib import contextmanager
from dataclasses import dataclass
import json
import os
from pathlib import Path
import sqlite3
import time
import uuid
from threading import RLock
from typing import Any, Iterator, Literal

from evo.artifact_runtime import prepared_intent_payload_fingerprint
from evo.artifact_runtime.utils import canonical_json, normalize_json_value, validate_nonempty

MessageEventType = Literal[
    'message_received',
    'intent_parsed',
    'artifact_view',
    'clarification_required',
    'confirmation_required',
    'approval_resolved',
    'command_applied',
    'assistant_response',
    'done',
    'error',
]

PendingApprovalStatus = Literal['active', 'approved', 'rejected', 'cancelled', 'superseded', 'expired']


@dataclass(frozen=True)
class MessageEvent:
    seq: int
    thread_id: str
    event_type: str
    payload: dict[str, Any]
    turn_id: str
    message_id: str
    created_at: float


@dataclass(frozen=True)
class MessageLease:
    thread_id: str
    owner_id: str
    fencing_token: str
    expires_at: float


@dataclass(frozen=True)
class PendingApproval:
    approval_token: str
    thread_id: str
    command_id: str
    run_id: str
    intent_kind: str
    prepared_payload: dict[str, Any]
    request_fingerprint: str
    preview_hash: str
    expected_refs: tuple[str, ...]
    risk_level: str
    status: PendingApprovalStatus
    created_at: float
    expires_at: float
    superseded_by: str


@dataclass(frozen=True)
class PendingTurnBuffer:
    turn_id: str
    message_id: str
    content: str
    processed_cursor: int
    remaining: str
    message_seq: int


class MessageStoreConflict(RuntimeError):
    pass


class MessageLeaseError(RuntimeError):
    pass


class MessageSessionStore:
    def __init__(self, path: str | Path, *, lease_seconds: float = 30.0) -> None:
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.lease_seconds = lease_seconds
        self._connection = sqlite3.connect(str(self.path), check_same_thread=False, isolation_level=None)
        self._connection.row_factory = sqlite3.Row
        self._lock = RLock()
        self._connection.execute('PRAGMA journal_mode=WAL')
        self._connection.execute('PRAGMA foreign_keys=ON')
        self._init_schema()

    def close(self) -> None:
        self._connection.close()

    @contextmanager
    def lease(self, thread_id: str, *, owner_id: str | None = None) -> Iterator[MessageLease]:
        lease = self.claim_lease(thread_id, owner_id=owner_id)
        try:
            yield lease
        finally:
            self.release_lease(lease)

    def claim_lease(self, thread_id: str, *, owner_id: str | None = None) -> MessageLease:
        validate_nonempty(thread_id, 'thread_id')
        owner = owner_id or f'message-intent:{os.getpid()}:{uuid.uuid4().hex}'
        validate_nonempty(owner, 'owner_id')
        now = time.time()
        expires_at = now + self.lease_seconds
        token = uuid.uuid4().hex
        with self._transaction() as conn:
            row = conn.execute('SELECT * FROM thread_lease WHERE thread_id = ?', (thread_id,)).fetchone()
            if row is not None and float(row['expires_at']) > now and str(row['owner_id']) != owner:
                raise MessageLeaseError('thread lease is held')
            conn.execute(
                """
                INSERT INTO thread_lease(thread_id, owner_id, fencing_token, expires_at, heartbeat_at)
                VALUES (?, ?, ?, ?, ?)
                ON CONFLICT(thread_id) DO UPDATE SET
                    owner_id = excluded.owner_id,
                    fencing_token = excluded.fencing_token,
                    expires_at = excluded.expires_at,
                    heartbeat_at = excluded.heartbeat_at
                """,
                (thread_id, owner, token, expires_at, now),
            )
        return MessageLease(thread_id, owner, token, expires_at)

    def heartbeat(self, lease: MessageLease) -> MessageLease:
        now = time.time()
        expires_at = now + self.lease_seconds
        with self._transaction() as conn:
            self._require_lease(conn, lease)
            conn.execute(
                'UPDATE thread_lease SET expires_at = ?, heartbeat_at = ? WHERE thread_id = ? AND fencing_token = ?',
                (expires_at, now, lease.thread_id, lease.fencing_token),
            )
        return MessageLease(lease.thread_id, lease.owner_id, lease.fencing_token, expires_at)

    def release_lease(self, lease: MessageLease) -> None:
        with self._transaction() as conn:
            conn.execute(
                'DELETE FROM thread_lease WHERE thread_id = ? AND fencing_token = ?',
                (lease.thread_id, lease.fencing_token),
            )

    def append_event(
        self,
        lease: MessageLease,
        event_type: str,
        payload: Mapping[str, Any],
        *,
        turn_id: str = '',
        message_id: str = '',
    ) -> MessageEvent:
        validate_nonempty(event_type, 'event_type')
        normalized = _json_object(payload)
        now = time.time()
        with self._transaction() as conn:
            self._require_lease(conn, lease)
            cursor = conn.execute(
                """
                INSERT INTO message_events(thread_id, event_type, turn_id, message_id, payload_json, created_at)
                VALUES (?, ?, ?, ?, ?, ?)
                """,
                (lease.thread_id, event_type, turn_id, message_id, canonical_json(normalized), now),
            )
            seq = int(cursor.lastrowid)
        return MessageEvent(seq, lease.thread_id, event_type, normalized, turn_id, message_id, now)

    def scan_events(self, thread_id: str, since: int = 0, *, limit: int = 100) -> list[MessageEvent]:
        with self._lock:
            rows = self._connection.execute(
                """
                SELECT * FROM message_events
                WHERE thread_id = ? AND seq > ?
                ORDER BY seq ASC
                LIMIT ?
                """,
                (thread_id, max(0, since), limit),
            ).fetchall()
        return [_event_from_row(row) for row in rows]

    def events_as_history(self, thread_id: str) -> list[dict[str, Any]]:
        with self._lock:
            rows = self._connection.execute(
                """
                SELECT * FROM message_events
                WHERE thread_id = ? AND event_type IN ('message_received', 'assistant_response')
                ORDER BY seq ASC
                """,
                (thread_id,),
            ).fetchall()
        out: list[dict[str, Any]] = []
        for row in rows:
            payload = json.loads(str(row['payload_json']))
            role = 'user' if row['event_type'] == 'message_received' else 'assistant'
            content = str(payload.get('content') or payload.get('message') or '').strip()
            if content:
                out.append({'id': str(row['message_id'] or f"msg-{row['seq']}"),
                           'role': role, 'content': content, 'ts': row['created_at']})
        return out

    def begin_turn(self, lease: MessageLease, message_id: str, content: str) -> tuple[str, MessageEvent]:
        validate_nonempty(message_id, 'message_id')
        validate_nonempty(content, 'content')
        turn_id = f'turn_{uuid.uuid4().hex[:12]}'
        now = time.time()
        with self._transaction() as conn:
            self._require_lease(conn, lease)
            conn.execute(
                """
                INSERT INTO turns(
                    thread_id, turn_id, message_ids_json, processed_cursor,
                    status, request_fingerprint, created_at, updated_at
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (lease.thread_id, turn_id, canonical_json([message_id]), 0, 'active', '', now, now),
            )
            cursor = conn.execute(
                """
                INSERT INTO message_events(thread_id, event_type, turn_id, message_id, payload_json, created_at)
                VALUES (?, ?, ?, ?, ?, ?)
                """,
                (lease.thread_id, 'message_received', turn_id, message_id, canonical_json({'content': content}), now),
            )
            seq = int(cursor.lastrowid)
        return turn_id, MessageEvent(
            seq, lease.thread_id, 'message_received', {
                'content': content}, turn_id, message_id, now)

    def finish_turn(
        self,
        lease: MessageLease,
        turn_id: str,
        *,
        status: str,
        request_fingerprint: str = '',
        processed_cursor: int | None = None,
    ) -> None:
        validate_nonempty(turn_id, 'turn_id')
        now = time.time()
        with self._transaction() as conn:
            self._require_lease(conn, lease)
            assignments = ['status = ?', 'request_fingerprint = ?', 'updated_at = ?']
            values: list[Any] = [status, request_fingerprint, now]
            if processed_cursor is not None:
                assignments.append('processed_cursor = ?')
                values.append(processed_cursor)
            values.extend([lease.thread_id, turn_id])
            conn.execute(
                f"UPDATE turns SET {', '.join(assignments)} WHERE thread_id = ? AND turn_id = ?",
                tuple(values),
            )

    def update_turn_cursor(self, lease: MessageLease, turn_id: str, processed_cursor: int) -> None:
        validate_nonempty(turn_id, 'turn_id')
        if processed_cursor < 0:
            raise ValueError('processed_cursor must be >= 0')
        now = time.time()
        with self._transaction() as conn:
            self._require_lease(conn, lease)
            conn.execute(
                """
                UPDATE turns
                SET processed_cursor = ?, updated_at = ?
                WHERE thread_id = ? AND turn_id = ?
                """,
                (processed_cursor, now, lease.thread_id, turn_id),
            )

    def pending_turn_buffers(self, thread_id: str) -> list[PendingTurnBuffer]:
        with self._lock:
            rows = self._connection.execute(
                """
                SELECT
                    t.turn_id,
                    t.processed_cursor,
                    e.message_id,
                    e.payload_json,
                    e.seq
                FROM turns t
                JOIN message_events e
                    ON e.thread_id = t.thread_id
                    AND e.turn_id = t.turn_id
                    AND e.event_type = 'message_received'
                WHERE t.thread_id = ?
                ORDER BY e.seq ASC
                """,
                (thread_id,),
            ).fetchall()
        out: list[PendingTurnBuffer] = []
        for row in rows:
            payload = _json_object(json.loads(str(row['payload_json'])))
            content = str(payload.get('content') or payload.get('message') or '')
            cursor = max(0, min(int(row['processed_cursor']), len(content)))
            remaining = content[cursor:]
            if remaining.strip():
                out.append(PendingTurnBuffer(str(row['turn_id']), str(
                    row['message_id']), content, cursor, remaining, int(row['seq'])))
        return out

    def working_set(self, thread_id: str) -> dict[str, Any]:
        with self._lock:
            row = self._connection.execute(
                'SELECT data_json FROM working_set WHERE thread_id = ?', (thread_id,)).fetchone()
        if row is None:
            return {}
        return _json_object(json.loads(str(row['data_json'])))

    def update_working_set(self, lease: MessageLease, patch: Mapping[str, Any]) -> dict[str, Any]:
        current = self.working_set(lease.thread_id)
        current.update(_json_object(patch))
        now = time.time()
        with self._transaction() as conn:
            self._require_lease(conn, lease)
            conn.execute(
                """
                INSERT INTO working_set(thread_id, data_json, updated_at)
                VALUES (?, ?, ?)
                ON CONFLICT(thread_id) DO UPDATE SET
                    data_json = excluded.data_json,
                    updated_at = excluded.updated_at
                """,
                (lease.thread_id, canonical_json(current), now),
            )
        return current

    def active_approval(self, thread_id: str) -> PendingApproval | None:
        with self._lock:
            row = self._connection.execute(
                """
                SELECT * FROM pending_approval
                WHERE thread_id = ? AND status = 'active'
                ORDER BY created_at DESC
                LIMIT 1
                """,
                (thread_id,),
            ).fetchone()
        return None if row is None else _approval_from_row(row)

    def put_pending_approval(
        self,
        lease: MessageLease,
        *,
        approval_token: str,
        command_id: str,
        run_id: str,
        intent_kind: str,
        prepared_payload: Mapping[str, Any],
        request_fingerprint: str,
        preview_hash: str,
        expected_refs: tuple[str, ...],
        risk_level: str,
        expires_at: float,
        supersede_existing: bool = False,
    ) -> PendingApproval:
        validate_nonempty(approval_token, 'approval_token')
        validate_nonempty(command_id, 'command_id')
        validate_nonempty(run_id, 'run_id')
        validate_nonempty(intent_kind, 'intent_kind')
        validate_nonempty(request_fingerprint, 'request_fingerprint')
        prepared = _json_object(prepared_payload, reject_reserved_envelope=False)
        if prepared_intent_payload_fingerprint(prepared) != request_fingerprint:
            raise ValueError('prepared_payload fingerprint mismatch')
        now = time.time()
        with self._transaction() as conn:
            self._require_lease(conn, lease)
            existing = conn.execute(
                "SELECT approval_token FROM pending_approval WHERE thread_id = ? AND status = 'active' LIMIT 1",
                (lease.thread_id,),
            ).fetchone()
            if existing is not None and not supersede_existing:
                raise MessageStoreConflict('active approval already exists')
            if existing is not None:
                superseded = str(existing['approval_token'])
                conn.execute(
                    'UPDATE pending_approval '
                    "SET status = 'superseded', superseded_by = ? "
                    "WHERE thread_id = ? AND status = 'active'",
                    (approval_token, lease.thread_id),
                )
                conn.execute(
                    """
                    INSERT INTO message_events(thread_id, event_type, turn_id, message_id, payload_json, created_at)
                    VALUES (?, 'approval_resolved', ?, ?, ?, ?)
                    """,
                    (
                        lease.thread_id,
                        '',
                        '',
                        canonical_json({'approval_token': superseded, 'status': 'superseded',
                                       'superseded_by': approval_token}),
                        now,
                    ),
                )
            conn.execute(
                """
                INSERT INTO pending_approval(
                    approval_token, thread_id, command_id, run_id, intent_kind, prepared_payload_json,
                    request_fingerprint, preview_hash, expected_refs_json, risk_level, status,
                    created_at, expires_at, superseded_by
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, '')
                """,
                (
                    approval_token,
                    lease.thread_id,
                    command_id,
                    run_id,
                    intent_kind,
                    canonical_json(prepared),
                    request_fingerprint,
                    preview_hash,
                    canonical_json(list(expected_refs)),
                    risk_level,
                    now,
                    expires_at,
                ),
            )
        return PendingApproval(
            approval_token,
            lease.thread_id,
            command_id,
            run_id,
            intent_kind,
            prepared,
            request_fingerprint,
            preview_hash,
            tuple(expected_refs),
            risk_level,
            'active',
            now,
            expires_at,
            '',
        )

    def resolve_approval(
        self,
        lease: MessageLease,
        approval_token: str,
        *,
        status: PendingApprovalStatus,
        event_payload: Mapping[str, Any],
        turn_id: str = '',
        message_id: str = '',
    ) -> PendingApproval:
        if status not in {'approved', 'rejected', 'cancelled', 'superseded', 'expired'}:
            raise ValueError('approval resolution status must be terminal')
        now = time.time()
        payload = _json_object(event_payload)
        with self._transaction() as conn:
            self._require_lease(conn, lease)
            row = conn.execute(
                'SELECT * FROM pending_approval WHERE thread_id = ? AND approval_token = ?',
                (lease.thread_id, approval_token),
            ).fetchone()
            if row is None:
                raise MessageStoreConflict('pending approval not found')
            if str(row['status']) != 'active':
                raise MessageStoreConflict('pending approval is not active')
            conn.execute(
                'UPDATE pending_approval SET status = ? WHERE thread_id = ? AND approval_token = ?',
                (status, lease.thread_id, approval_token),
            )
            conn.execute(
                """
                INSERT INTO message_events(thread_id, event_type, turn_id, message_id, payload_json, created_at)
                VALUES (?, 'approval_resolved', ?, ?, ?, ?)
                """,
                (lease.thread_id, turn_id, message_id, canonical_json(
                    {'approval_token': approval_token, 'status': status, **payload}), now),
            )
        resolved = _approval_from_row(row)
        return PendingApproval(
            resolved.approval_token,
            resolved.thread_id,
            resolved.command_id,
            resolved.run_id,
            resolved.intent_kind,
            resolved.prepared_payload,
            resolved.request_fingerprint,
            resolved.preview_hash,
            resolved.expected_refs,
            resolved.risk_level,
            status,
            resolved.created_at,
            resolved.expires_at,
            resolved.superseded_by,
        )

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

    def _require_lease(self, conn: sqlite3.Connection, lease: MessageLease) -> None:
        row = conn.execute(
            'SELECT * FROM thread_lease WHERE thread_id = ? AND fencing_token = ?',
            (lease.thread_id, lease.fencing_token),
        ).fetchone()
        if row is None or str(row['owner_id']) != lease.owner_id or float(row['expires_at']) <= time.time():
            raise MessageLeaseError('stale message lease')

    def _init_schema(self) -> None:
        self._connection.executescript(
            """
            CREATE TABLE IF NOT EXISTS message_events (
                seq INTEGER PRIMARY KEY AUTOINCREMENT,
                thread_id TEXT NOT NULL,
                event_type TEXT NOT NULL,
                turn_id TEXT NOT NULL,
                message_id TEXT NOT NULL,
                payload_json TEXT NOT NULL,
                created_at REAL NOT NULL
            );
            CREATE INDEX IF NOT EXISTS idx_message_events_thread_seq
                ON message_events(thread_id, seq);

            CREATE TABLE IF NOT EXISTS turns (
                thread_id TEXT NOT NULL,
                turn_id TEXT NOT NULL,
                message_ids_json TEXT NOT NULL,
                processed_cursor INTEGER NOT NULL,
                status TEXT NOT NULL,
                request_fingerprint TEXT NOT NULL,
                created_at REAL NOT NULL,
                updated_at REAL NOT NULL,
                PRIMARY KEY(thread_id, turn_id)
            );

            CREATE TABLE IF NOT EXISTS pending_approval (
                approval_token TEXT PRIMARY KEY,
                thread_id TEXT NOT NULL,
                command_id TEXT NOT NULL,
                run_id TEXT NOT NULL,
                intent_kind TEXT NOT NULL,
                prepared_payload_json TEXT NOT NULL,
                request_fingerprint TEXT NOT NULL,
                preview_hash TEXT NOT NULL,
                expected_refs_json TEXT NOT NULL,
                risk_level TEXT NOT NULL,
                status TEXT NOT NULL,
                created_at REAL NOT NULL,
                expires_at REAL NOT NULL,
                superseded_by TEXT NOT NULL
            );
            CREATE UNIQUE INDEX IF NOT EXISTS uq_pending_approval_active
                ON pending_approval(thread_id)
                WHERE status = 'active';

            CREATE TABLE IF NOT EXISTS working_set (
                thread_id TEXT PRIMARY KEY,
                data_json TEXT NOT NULL,
                updated_at REAL NOT NULL
            );

            CREATE TABLE IF NOT EXISTS thread_lease (
                thread_id TEXT PRIMARY KEY,
                owner_id TEXT NOT NULL,
                fencing_token TEXT NOT NULL,
                expires_at REAL NOT NULL,
                heartbeat_at REAL NOT NULL
            );
            """
        )


def _json_object(value: Mapping[str, Any] | Any, *, reject_reserved_envelope: bool = True) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        raise TypeError('value must be a JSON object')
    normalized = normalize_json_value(dict(value), allow_tuple=True, reject_reserved_envelope=reject_reserved_envelope)
    if not isinstance(normalized, dict):
        raise TypeError('value must be a JSON object')
    return normalized


def _event_from_row(row: sqlite3.Row) -> MessageEvent:
    return MessageEvent(
        int(row['seq']),
        str(row['thread_id']),
        str(row['event_type']),
        _json_object(json.loads(str(row['payload_json']))),
        str(row['turn_id']),
        str(row['message_id']),
        float(row['created_at']),
    )


def _approval_from_row(row: sqlite3.Row) -> PendingApproval:
    refs = json.loads(str(row['expected_refs_json']))
    if not isinstance(refs, list):
        refs = []
    return PendingApproval(
        str(row['approval_token']),
        str(row['thread_id']),
        str(row['command_id']),
        str(row['run_id']),
        str(row['intent_kind']),
        _json_object(json.loads(str(row['prepared_payload_json'])), reject_reserved_envelope=False),
        str(row['request_fingerprint']),
        str(row['preview_hash']),
        tuple(str(item) for item in refs),
        str(row['risk_level']),
        str(row['status']),
        float(row['created_at']),
        float(row['expires_at']),
        str(row['superseded_by']),
    )
