from __future__ import annotations

import json
import math
import os
import re
import time
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from filelock import FileLock

PAYLOAD_LIMIT = 8192
COLLECTION_LIMIT = 50
DEPTH_LIMIT = 4
TAIL_BYTES = 1024 * 1024
THREAD_ID = re.compile(r'[A-Za-z0-9][A-Za-z0-9_.-]{0,127}')
SECRET_KEY = re.compile(r'(api[_-]?key|token|secret|password|authorization|llm_config)', re.I)
SECRET_VALUE = re.compile(
    r'(?i)\b(authorization|api[_-]?key|token|secret|password)\b\s*[:=]\s*(?:bearer\s+)?[^\s,;)\]}]+'
)
BEARER = re.compile(r'(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}')
URL_SECRET = re.compile(r'(?i)([?&](?:api[_-]?key|token|secret|password)=)[^&#\s]+')
URL = re.compile(r'(?i)\bhttps?://[^\s,;)\]}]+')
ABS_PATH = re.compile(r'(?<![:/\w])/(?!/)(?:[^\s,;)\]}\"\']*/)+[^\s,;)\]}\"\']*')
EVENT_TYPES = {
    'repair.attempt_started',
    'repair.attempt_completed',
    'repair.base_selected',
    'repair.loop_completed',
    'repair.patch_verified',
    'opencode.setup',
    'opencode.process_start',
    'opencode.tool_use.search',
    'opencode.tool_use.read_file',
    'opencode.tool_use.edit_file',
    'opencode.tool_use.run_command',
    'opencode.code',
    'opencode.message',
    'opencode.error',
    'opencode.process_exit',
    'verify.pre_validation_started',
    'verify.diff_scope_completed',
    'verify.hardcode_check_completed',
    'verify.behaviorful_diff_completed',
    'verify.patch_policy_completed',
    'verify.command_started',
    'verify.command_completed',
    'verify.pre_validation_completed',
    'candidate.service_started',
    'candidate.service_ready',
    'candidate.service_failed',
    'candidate.service_stopped',
    'candidate.case_started',
    'candidate.case_completed',
    'candidate.eval_summary_completed',
    'analysis.candidate_started',
    'analysis.candidate_completed',
    'analysis.delta_completed',
}
STATUSES = {'started', 'running', 'completed', 'failed', 'skipped'}


class RepairTraceStore:
    def __init__(self, root: Path) -> None:
        self.root = root / 'repair-traces'
        self.root.mkdir(parents=True, exist_ok=True)

    def append(self, thread_id: str, event: Mapping[str, Any], *, terminal: bool = False) -> dict[str, Any]:
        path = self._path(thread_id)
        path.parent.mkdir(parents=True, exist_ok=True)
        with self._lock(thread_id):
            row = dict(event, seq=self._last_seq(path, repair=True) + 1,
                       created_at=event.get('created_at') or time.time())
            clean = _clean(row)
            line = _line(clean)
            saved = json.loads(line)
            with path.open('a', encoding='utf-8') as handle:
                handle.write(line + '\n')
                handle.flush()
                if terminal:
                    os.fsync(handle.fileno())
            return saved

    def read_since(self, thread_id: str, cursor: int = 0, *, until: int | None = None) -> list[dict[str, Any]]:
        path = self._path(thread_id)
        if not path.exists():
            return []
        with self._lock(thread_id):
            rows = self._read_valid(path, repair=False)
        return [
            row for row in rows
            if int(row.get('seq') or 0) > cursor and (until is None or int(row.get('seq') or 0) <= until)
        ]

    def last_seq(self, thread_id: str) -> int:
        path = self._path(thread_id)
        if not path.exists():
            return 0
        with self._lock(thread_id):
            return self._last_seq(path, repair=False)

    def fallback(self, thread_id: str, event_type: str, error: Exception) -> None:
        try:
            path = self.root / f'{self._safe_thread_id(thread_id)}.fallback.log'
            path.parent.mkdir(parents=True, exist_ok=True)
            with path.open('a', encoding='utf-8') as handle:
                handle.write(json.dumps({
                    'created_at': time.time(),
                    'type': 'trace_emit_failed',
                    'event_type': event_type,
                    'error_type': type(error).__name__,
                    'message': _clean(str(error))[:500],
                }, ensure_ascii=False) + '\n')
        except Exception:
            pass

    def _path(self, thread_id: str) -> Path:
        return self.root / f'{self._safe_thread_id(thread_id)}.jsonl'

    def _lock(self, thread_id: str) -> FileLock:
        return FileLock(str(self.root / f'{self._safe_thread_id(thread_id)}.lock'))

    def _last_seq(self, path: Path, *, repair: bool) -> int:
        if not path.exists():
            return 0
        row = self._tail_row(path, repair=repair)
        if row is None:
            rows = self._read_valid(path, repair=repair)
            row = rows[-1] if rows else {}
        return int(row.get('seq') or 0) if row else 0

    @staticmethod
    def _tail_row(path: Path, *, repair: bool) -> dict[str, Any] | None:
        if not path.exists() or not path.stat().st_size:
            return {}
        mode = 'rb+' if repair else 'rb'
        with path.open(mode) as handle:
            size = path.stat().st_size
            window = min(TAIL_BYTES, size)
            handle.seek(size - window)
            data = handle.read(window)
            if repair and data and not data.endswith(b'\n'):
                cut = data.rfind(b'\n')
                handle.truncate(size - window + cut + 1 if cut >= 0 else 0)
                size = handle.seek(0, os.SEEK_END)
                if not size:
                    return {}
                window = min(TAIL_BYTES, size)
                handle.seek(size - window)
                data = handle.read(window)
        lines = [line for line in data.splitlines() if line.strip()]
        if not lines:
            return {}
        try:
            row = json.loads(lines[-1].decode('utf-8'))
        except (UnicodeDecodeError, json.JSONDecodeError):
            return None
        return row if isinstance(row, dict) and _seq(row) > 0 else None

    @staticmethod
    def _read_valid(path: Path, *, repair: bool) -> list[dict[str, Any]]:
        if not path.exists():
            return []
        rows, good_offset = [], 0
        with path.open('rb') as handle:
            while line := handle.readline():
                offset = handle.tell()
                try:
                    row = json.loads(line.decode('utf-8'))
                except (UnicodeDecodeError, json.JSONDecodeError):
                    break
                if not isinstance(row, dict) or _seq(row) <= 0:
                    break
                rows.append(row)
                good_offset = offset
        if repair and path.exists() and good_offset < path.stat().st_size:
            with path.open('ab') as handle:
                handle.truncate(good_offset)
        return rows

    @staticmethod
    def _safe_thread_id(thread_id: str) -> str:
        value = str(thread_id or '')
        if not THREAD_ID.fullmatch(value):
            raise ValueError('invalid thread_id')
        return value


class RepairTraceSink:
    def __init__(self, store: RepairTraceStore, thread_id: str, trace_id: str, materialization_key: str) -> None:
        self.store = store
        self.thread_id = thread_id
        self.trace_id = trace_id
        self.materialization_key = materialization_key
        self.seq_start = 0
        self.seq_end = 0
        self.failures = 0

    def emit(
        self,
        event_type: str,
        *,
        status: str = 'running',
        message: str = '',
        source: str = 'repair',
        attempt: int | None = None,
        payload: Mapping[str, Any] | None = None,
        terminal: bool = False,
    ) -> None:
        if event_type not in EVENT_TYPES:
            raise ValueError(f'unknown repair trace event type: {event_type}')
        event = {
            'thread_id': self.thread_id,
            'trace_id': self.trace_id,
            'materialization_key': self.materialization_key,
            'step': 'repair',
            'attempt': attempt,
            'source': source,
            'type': event_type,
            'status': status if status in STATUSES else 'running',
            'message': message[:500],
            'payload': payload or {},
        }
        try:
            row = self.store.append(self.thread_id, event, terminal=terminal)
            self.seq_start = self.seq_start or int(row['seq'])
            self.seq_end = int(row['seq'])
        except Exception as exc:
            self.failures += 1
            self.store.fallback(self.thread_id, event_type, exc)

    def cursor(self) -> dict[str, Any]:
        return {
            'trace_id': self.trace_id,
            'seq_start': self.seq_start,
            'seq_end': self.seq_end,
            'status': 'partial' if self.failures else 'ok',
        }


def safe_emit(trace: Any | None, event_type: str, **kwargs: Any) -> None:
    if trace is None:
        return
    try:
        trace.emit(event_type, **kwargs)
    except Exception:
        pass


def trace_cursor(trace: Any | None) -> dict[str, Any]:
    if trace is None:
        return {}
    try:
        cursor = trace.cursor()
    except Exception:
        return {}
    return cursor if isinstance(cursor, dict) else {}


def _clean(value: Any, depth: int = 0) -> Any:
    if depth > DEPTH_LIMIT:
        return '<truncated>'
    if isinstance(value, Mapping):
        return {
            str(key): '<redacted>' if SECRET_KEY.search(str(key)) else _clean(item, depth + 1)
            for key, item in list(value.items())[:COLLECTION_LIMIT]
        }
    if isinstance(value, list):
        return [_clean(item, depth + 1) for item in value[:COLLECTION_LIMIT]]
    if isinstance(value, tuple):
        return [_clean(item, depth + 1) for item in value[:COLLECTION_LIMIT]]
    if isinstance(value, str):
        text = URL_SECRET.sub(r'\1<redacted>', BEARER.sub('bearer <redacted>', SECRET_VALUE.sub(
            lambda match: f'{match.group(1)}=<redacted>', value,
        )))
        return _fit(ABS_PATH.sub('<redacted-path>', URL.sub('<redacted-url>', text)))
    if isinstance(value, bool) or value is None:
        return value
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        return value if math.isfinite(value) else None
    return _fit(repr(value))


def _fit(text: str) -> str:
    data = text.encode('utf-8')
    return text if len(data) <= PAYLOAD_LIMIT else data[:PAYLOAD_LIMIT].decode('utf-8', 'ignore') + '...<truncated>'


def _line(row: Mapping[str, Any]) -> str:
    line = json.dumps(row, ensure_ascii=False, sort_keys=True, allow_nan=False)
    if len(line.encode('utf-8')) <= PAYLOAD_LIMIT:
        return line
    compact = dict(row)
    compact['payload'] = {
        'truncated': True,
        'summary': _fit_bytes(json.dumps(row.get('payload', {}), ensure_ascii=False), 2048),
    }
    compact['message'] = _fit_bytes(str(row.get('message') or ''), 500)
    line = json.dumps(compact, ensure_ascii=False, sort_keys=True, allow_nan=False)
    if len(line.encode('utf-8')) <= PAYLOAD_LIMIT:
        return line
    compact['payload'] = {'truncated': True}
    compact['message'] = _fit_bytes(str(row.get('message') or ''), 120)
    return json.dumps(compact, ensure_ascii=False, sort_keys=True, allow_nan=False)


def _fit_bytes(text: str, limit: int) -> str:
    data = text.encode('utf-8')
    return text if len(data) <= limit else data[:limit].decode('utf-8', 'ignore') + '...<truncated>'


def _seq(row: Mapping[str, Any]) -> int:
    try:
        seq = int(row.get('seq') or 0)
    except (TypeError, ValueError):
        return 0
    return seq if seq > 0 and row.get('thread_id') and row.get('trace_id') and row.get('type') else 0
