from __future__ import annotations

import hashlib
import json
import os
import sqlite3
import tempfile
import time
from pathlib import Path
from typing import Any

from .schemas import MessageContentRef


class MessageConflictError(RuntimeError):
    pass


class MessageInProgressError(RuntimeError):
    pass


def json_bytes(value: object) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode()


class MessageBlobStore:
    def __init__(self, root: Path) -> None:
        self.root = root
        self.blob_root = root / 'message-store' / 'blobs'
        _mkdir(self.blob_root)

    def append(self, thread_id: str, turn_id: str, kind: str, payload: bytes) -> MessageContentRef:
        digest = hashlib.sha256(payload).hexdigest()
        safe = ''.join(c if c.isalnum() or c in '._-' else '_' for c in kind)[:48] or 'blob'
        folder = self.blob_root / _hash(thread_id) / turn_id
        _mkdir(folder)
        fd, tmp = tempfile.mkstemp(prefix='.tmp-', suffix='.part', dir=folder)
        try:
            with os.fdopen(fd, 'wb') as handle:
                handle.write(payload)
                handle.flush()
                os.fsync(handle.fileno())
            target = folder / f'{int(time.time() * 1000)}-{safe}-{digest[:12]}.blob'
            os.replace(tmp, target)
            _fsync(folder)
        finally:
            if os.path.exists(tmp):
                os.unlink(tmp)
        return MessageContentRef(uri=str(target.relative_to(self.root)), sha256=digest, byte_size=len(payload))

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


class MessageAuditStore:
    def __init__(self, root: Path) -> None:
        self.db = root / 'artifact-store' / 'artifact_store.sqlite3'

    def begin_turn(self, thread_id: str, turn_id: str, message_id: str,
                   request_hash: str) -> MessageContentRef | None:
        with self._conn() as conn:
            row = conn.execute(
                'select request_sha256, status, result_ref_json from message_turns '
                'where thread_id = ? and message_id = ?',
                (thread_id, message_id),
            ).fetchone()
            if row:
                if row[0] != request_hash:
                    raise MessageConflictError('message_id reused with different payload')
                if row[1] == 'done' and row[2]:
                    return MessageContentRef.model_validate(json.loads(row[2]))
                raise MessageInProgressError('message_id is already open or failed; send a new message_id')
            conn.execute(
                'insert into message_turns values (?, ?, ?, ?, ?, ?, ?, ?)',
                (thread_id, message_id, turn_id, request_hash, 'open', '', time.time(), time.time()),
            )
        return None

    def abort_turn(self, thread_id: str, turn_id: str) -> None:
        with self._conn() as conn:
            conn.execute(
                'update message_turns set status = ?, updated_at = ? where thread_id = ? and turn_id = ? '
                'and status = ?',
                ('failed', time.time(), thread_id, turn_id, 'open'),
            )

    def finish_turn(self, thread_id: str, turn_id: str, result_ref: MessageContentRef,
                    projection: dict[str, Any]) -> None:
        with self._conn() as conn:
            conn.execute('begin immediate')
            row = conn.execute('select data_json from message_projection where thread_id = ?', (thread_id,)).fetchone()
            data = json.loads(row[0]) if row and row[0] else {}
            data.update(projection)
            conn.execute(
                'update message_turns set status = ?, result_ref_json = ?, updated_at = ? '
                'where thread_id = ? and turn_id = ?',
                ('done', json_bytes(result_ref.model_dump()).decode(), time.time(), thread_id, turn_id),
            )
            conn.execute('insert or replace into message_projection values (?, ?, ?)',
                         (thread_id, json_bytes(data).decode(), time.time()))

    def projection(self, thread_id: str) -> dict[str, Any]:
        with self._conn() as conn:
            row = conn.execute('select data_json from message_projection where thread_id = ?', (thread_id,)).fetchone()
        return json.loads(row[0]) if row and row[0] else {}

    def _conn(self) -> sqlite3.Connection:
        if not self.db.is_file():
            raise ValueError('artifact store is not initialized; create a thread first')
        conn = sqlite3.connect(self.db, timeout=30)
        for pragma in ('journal_mode=wal', 'synchronous=full', 'busy_timeout=30000'):
            conn.execute(f'pragma {pragma}')
        row = conn.execute(
            "select 1 from sqlite_master where type = 'table' and name = 'artifact_records'",
        ).fetchone()
        if row is None:
            conn.close()
            raise ValueError('artifact store schema is missing artifact_records')
        conn.execute(
            'create table if not exists message_turns('
            'thread_id text not null, message_id text not null, turn_id text not null, '
            'request_sha256 text not null, status text not null, result_ref_json text not null, '
            'created_at real not null, updated_at real not null, '
            'primary key(thread_id, turn_id), unique(thread_id, message_id))',
        )
        conn.execute(
            'create table if not exists message_projection('
            'thread_id text primary key, data_json text not null, updated_at real not null)',
        )
        return conn


def _hash(value: str) -> str:
    return hashlib.sha256(value.encode()).hexdigest()


def _fsync(path: Path) -> None:
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _mkdir(path: Path) -> None:
    if path.exists():
        return
    _mkdir(path.parent)
    path.mkdir()
    _fsync(path.parent)
