from __future__ import annotations

import hashlib
import json
import os
import sqlite3
import tempfile
import time
from pathlib import Path
from typing import Any

from .schemas import MessageContentRef, MessageHistoryItem, MessageHistoryResponse, MessageRequest, MessageTurnResult


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
                'insert into message_turns('
                'thread_id, message_id, turn_id, request_sha256, status, '
                'request_ref_json, result_ref_json, created_at, updated_at'
                ') values (?, ?, ?, ?, ?, ?, ?, ?, ?)',
                (thread_id, message_id, turn_id, request_hash, 'open', '', '', time.time(), time.time()),
            )
        return None

    def record_request_ref(self, thread_id: str, turn_id: str, request_ref: MessageContentRef) -> None:
        with self._conn() as conn:
            conn.execute(
                'update message_turns set request_ref_json = ?, updated_at = ? '
                'where thread_id = ? and turn_id = ? and request_ref_json = ?',
                (json_bytes(request_ref.model_dump()).decode(), time.time(), thread_id, turn_id, ''),
            )

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

    def list_turns(self, thread_id: str, page_size: int, page_token: str) -> tuple[list[sqlite3.Row], str]:
        offset = int(page_token or 0) if str(page_token or '0').isdigit() else -1
        if offset < 0:
            raise ValueError('page_token must be an integer offset')
        with self._conn() as conn:
            conn.row_factory = sqlite3.Row
            rows = conn.execute(
                'select turn_id, message_id, status, request_ref_json, result_ref_json '
                'from message_turns where thread_id = ? order by created_at, turn_id limit ? offset ?',
                (thread_id, page_size + 1, offset),
            ).fetchall()
        page = list(rows[:page_size])
        next_token = str(offset + page_size) if len(rows) > page_size else ''
        return page, next_token

    def recent_turns(self, thread_id: str, limit: int) -> list[sqlite3.Row]:
        with self._conn() as conn:
            conn.row_factory = sqlite3.Row
            rows = conn.execute(
                'select turn_id, message_id, status, request_ref_json, result_ref_json '
                'from message_turns where thread_id = ? and status = ? '
                'order by created_at desc, turn_id desc limit ?',
                (thread_id, 'done', limit),
            ).fetchall()
        return list(reversed(rows))

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
        columns = {row[1] for row in conn.execute('pragma table_info(message_turns)').fetchall()}
        if 'request_ref_json' not in columns:
            conn.execute("alter table message_turns add column request_ref_json text not null default ''")
        conn.execute(
            'create table if not exists message_projection('
            'thread_id text primary key, data_json text not null, updated_at real not null)',
        )
        return conn


def message_history(root: Path, thread_id: str, page_size: int, page_token: str) -> MessageHistoryResponse:
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
            pending_approval_ref=result.pending_approval_ref if result else None,
            action_receipt_ref=result.action_receipt_ref if result else None,
        ))
    return MessageHistoryResponse(thread_id=thread_id, items=items, next_page_token=next_token)


def _load_model(blobs: MessageBlobStore, ref_json: str, thread_id: str, model: type[Any]) -> Any:
    if not ref_json:
        return None
    ref = MessageContentRef.model_validate(json.loads(ref_json))
    return model.model_validate_json(blobs.load(ref, thread_id))


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
