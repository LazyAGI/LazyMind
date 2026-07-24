from __future__ import annotations

import hashlib
import json
import os
import shutil
import sqlite3
import tempfile
import time
from pathlib import Path
from typing import Any

from .schemas import (
    MessageContentRef,
    MessageHistoryItem,
    MessageHistoryResponse,
    MessageRequest,
    MessageTurnResult,
)


class MessageConflictError(RuntimeError):
    pass


class MessageInProgressError(RuntimeError):
    pass


def json_bytes(value: object) -> bytes:
    return json.dumps(
        value,
        ensure_ascii=False,
        sort_keys=True,
        separators=(',', ':'),
    ).encode()


class MessageBlobStore:
    def __init__(self, root: Path) -> None:
        self.root = Path(root)
        self.blob_root = self.root / 'message-store' / 'blobs'
        self.blob_root.mkdir(parents=True, exist_ok=True)

    def append(self, thread_id: str, turn_id: str, kind: str,
               payload: bytes
               ) -> MessageContentRef:
        digest = hashlib.sha256(payload).hexdigest()
        safe_kind = ''.join(
            character if character.isalnum() or character in '._-' else '_'
            for character in kind
        )[:48] or 'blob'
        folder = self.blob_root / _hash(thread_id) / turn_id
        folder.mkdir(parents=True, exist_ok=True)
        target = folder / f'{safe_kind}-{digest}.json'
        if not target.exists():
            handle = tempfile.NamedTemporaryFile(dir=folder, delete=False)
            temporary = Path(handle.name)
            try:
                with handle:
                    handle.write(payload)
                    handle.flush()
                    os.fsync(handle.fileno())
                temporary.replace(target)
            finally:
                temporary.unlink(missing_ok=True)
        return MessageContentRef(
            uri=str(target.relative_to(self.root)),
            sha256=digest,
            byte_size=len(payload),
        )

    def load(self, ref: MessageContentRef, thread_id: str = '') -> bytes:
        path = (self.root / ref.uri).resolve()
        root = self.blob_root.resolve()
        if not path.is_file() or not path.is_relative_to(root):
            raise ValueError('message blob path is outside message-store')
        if thread_id and path.parent.parent.name != _hash(thread_id):
            raise ValueError('message blob belongs to a different thread')
        payload = path.read_bytes()
        if len(payload) != ref.byte_size or hashlib.sha256(payload).hexdigest() != ref.sha256:
            raise ValueError('message blob checksum mismatch')
        return payload

    def delete_thread(self, thread_id: str) -> None:
        shutil.rmtree(self.blob_root / _hash(thread_id), ignore_errors=True)


class MessageAuditStore:
    def __init__(self, root: Path) -> None:
        folder = Path(root) / 'message-store'
        folder.mkdir(parents=True, exist_ok=True)
        self.db = folder / 'message-audit.sqlite3'
        with self._connection() as connection:
            connection.executescript(
                """
                CREATE TABLE IF NOT EXISTS message_turns(
                  thread_id TEXT NOT NULL,
                  message_id TEXT NOT NULL,
                  turn_id TEXT NOT NULL,
                  request_sha256 TEXT NOT NULL,
                  status TEXT NOT NULL CHECK(status IN ('open', 'done', 'failed')),
                  request_ref_json TEXT NOT NULL,
                  result_ref_json TEXT NOT NULL,
                  created_at INTEGER NOT NULL,
                  updated_at INTEGER NOT NULL,
                  PRIMARY KEY(thread_id, turn_id),
                  UNIQUE(thread_id, message_id)
                );
                CREATE TABLE IF NOT EXISTS message_projection(
                  thread_id TEXT PRIMARY KEY,
                  data_json TEXT NOT NULL,
                  updated_at INTEGER NOT NULL
                );
                """
            )

    def begin_turn(self, thread_id: str, turn_id: str, message_id: str,
                   request_hash: str
                   ) -> MessageContentRef | None:
        with self._connection() as connection:
            row = connection.execute(
                'SELECT request_sha256, status, result_ref_json FROM message_turns '
                'WHERE thread_id = ? AND message_id = ?',
                (thread_id, message_id),
            ).fetchone()
            if row is not None:
                if row['request_sha256'] != request_hash:
                    raise MessageConflictError('message_id reused with different payload')
                if row['status'] == 'done' and row['result_ref_json']:
                    return MessageContentRef.model_validate(json.loads(row['result_ref_json']))
                raise MessageInProgressError('message_id already belongs to an unfinished turn')
            connection.execute(
                'INSERT INTO message_turns('
                'thread_id, message_id, turn_id, request_sha256, status, '
                'request_ref_json, result_ref_json, created_at, updated_at'
                ') VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)',
                (
                    thread_id, message_id, turn_id, request_hash, 'open', '', '',
                    time.time_ns(), time.time_ns(),
                ),
            )
        return None

    def record_request_ref(self, thread_id: str, turn_id: str,
                           request_ref: MessageContentRef
                           ) -> None:
        with self._connection() as connection:
            connection.execute(
                'UPDATE message_turns SET request_ref_json = ?, updated_at = ? '
                'WHERE thread_id = ? AND turn_id = ? AND status = ?',
                (_json(request_ref.model_dump()), time.time_ns(), thread_id, turn_id, 'open'),
            )

    def abort_turn(self, thread_id: str, turn_id: str) -> None:
        with self._connection() as connection:
            connection.execute(
                'UPDATE message_turns SET status = ?, updated_at = ? '
                'WHERE thread_id = ? AND turn_id = ? AND status = ?',
                ('failed', time.time_ns(), thread_id, turn_id, 'open'),
            )

    def finish_turn(self, thread_id: str, turn_id: str,
                    result_ref: MessageContentRef,
                    projection: dict[str, Any]
                    ) -> None:
        with self._connection() as connection:
            connection.execute('BEGIN IMMEDIATE')
            row = connection.execute(
                'SELECT data_json FROM message_projection WHERE thread_id = ?',
                (thread_id,),
            ).fetchone()
            data = json.loads(row['data_json']) if row is not None else {}
            data.update(projection)
            cursor = connection.execute(
                'UPDATE message_turns SET status = ?, result_ref_json = ?, '
                'updated_at = ? '
                'WHERE thread_id = ? AND turn_id = ? AND status = ?',
                (
                    'done', _json(result_ref.model_dump()), time.time_ns(),
                    thread_id, turn_id, 'open',
                ),
            )
            if cursor.rowcount != 1:
                raise MessageConflictError('message turn is no longer open')
            connection.execute(
                'INSERT INTO message_projection(thread_id, data_json, updated_at) '
                'VALUES (?, ?, ?) '
                'ON CONFLICT(thread_id) DO UPDATE SET '
                'data_json = excluded.data_json, updated_at = excluded.updated_at',
                (thread_id, _json(data), time.time_ns()),
            )

    def projection(self, thread_id: str) -> dict[str, Any]:
        with self._connection() as connection:
            row = connection.execute(
                'SELECT data_json FROM message_projection WHERE thread_id = ?',
                (thread_id,),
            ).fetchone()
        return json.loads(row['data_json']) if row is not None else {}

    def list_turns(self, thread_id: str, page_size: int,
                   page_token: str
                   ) -> tuple[list[sqlite3.Row], str]:
        offset = int(page_token or 0) if str(page_token or '0').isdigit() else -1
        if offset < 0:
            raise ValueError('page_token must be a non-negative integer offset')
        with self._connection() as connection:
            rows = connection.execute(
                'SELECT turn_id, message_id, status, request_ref_json, result_ref_json '
                'FROM message_turns WHERE thread_id = ? '
                'ORDER BY created_at, turn_id LIMIT ? OFFSET ?',
                (thread_id, page_size + 1, offset),
            ).fetchall()
        page = list(rows[:page_size])
        next_token = str(offset + page_size) if len(rows) > page_size else ''
        return page, next_token

    def recent_turns(self, thread_id: str, limit: int) -> list[sqlite3.Row]:
        with self._connection() as connection:
            rows = connection.execute(
                'SELECT turn_id, message_id, status, request_ref_json, result_ref_json '
                'FROM message_turns WHERE thread_id = ? AND status = ? '
                'ORDER BY created_at DESC, turn_id DESC LIMIT ?',
                (thread_id, 'done', limit),
            ).fetchall()
        return list(reversed(rows))

    def delete_thread(self, thread_id: str) -> None:
        with self._connection() as connection:
            connection.execute('BEGIN IMMEDIATE')
            connection.execute(
                'DELETE FROM message_turns WHERE thread_id = ?',
                (thread_id,),
            )
            connection.execute(
                'DELETE FROM message_projection WHERE thread_id = ?',
                (thread_id,),
            )

    def _connection(self) -> sqlite3.Connection:
        connection = sqlite3.connect(self.db, timeout=30)
        connection.row_factory = sqlite3.Row
        connection.execute('PRAGMA journal_mode = WAL')
        connection.execute('PRAGMA synchronous = FULL')
        connection.execute('PRAGMA busy_timeout = 30000')
        return connection


def message_history(root: Path, thread_id: str, page_size: int,
                    page_token: str
                    ) -> MessageHistoryResponse:
    audit = MessageAuditStore(root)
    blobs = MessageBlobStore(root)
    rows, next_token = audit.list_turns(thread_id, page_size, page_token)
    items = []
    for row in rows:
        request = _load_model(blobs, row['request_ref_json'], thread_id, MessageRequest)
        result = _load_model(blobs, row['result_ref_json'], thread_id, MessageTurnResult)
        items.append(MessageHistoryItem(
            turn_id=row['turn_id'],
            message_id=row['message_id'],
            command_id=result.command_id if result else '',
            status=row['status'],
            user_text=request.text if request else '',
            assistant_text=result.assistant_text if result else '',
            turn_decision=result.turn_decision if result else '',
            observation_ref=result.observation_ref if result else None,
            pending_confirmation_ref=result.pending_confirmation_ref if result else None,
            action_receipt_ref=result.action_receipt_ref if result else None,
        ))
    return MessageHistoryResponse(
        thread_id=thread_id,
        items=items,
        next_page_token=next_token,
    )


def _load_model(blobs: MessageBlobStore, ref_json: str, thread_id: str,
                model: type[Any]
                ) -> Any:
    if not ref_json:
        return None
    ref = MessageContentRef.model_validate(json.loads(ref_json))
    return model.model_validate_json(blobs.load(ref, thread_id))


def _json(value: object) -> str:
    return json_bytes(value).decode()


def _hash(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


__all__ = [
    'MessageAuditStore', 'MessageBlobStore', 'MessageConflictError',
    'MessageInProgressError', 'json_bytes', 'message_history',
]
