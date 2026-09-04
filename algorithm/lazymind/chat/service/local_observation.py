from __future__ import annotations

import json
import os
import threading
from pathlib import Path
from typing import Any, Mapping


_SENSITIVE_KEYS = frozenset({
    'prompt', 'input', 'output', 'content', 'messages', 'history', 'files',
    'api_key', 'authorization', 'token', 'headers', 'provider_raw_error',
})


def _redact(value: Any) -> Any:
    if isinstance(value, Mapping):
        return {str(key): _redact(item) for key, item in value.items()
                if str(key).lower() not in _SENSITIVE_KEYS}
    if isinstance(value, list):
        return [_redact(item) for item in value]
    if isinstance(value, tuple):
        return [_redact(item) for item in value]
    return value


class LocalObservationWriter:
    def __init__(self, directory: str | os.PathLike[str], *, max_bytes: int = 50 * 1024 * 1024):
        self.directory = Path(directory)
        self.max_bytes = max(1024, int(max_bytes))
        self._lock = threading.Lock()
        self.directory.mkdir(parents=True, exist_ok=True)

    def write_summary(self, record: Mapping[str, Any]) -> None:
        self._append('performance-summary.jsonl', record)

    def write_full(self, record: Mapping[str, Any]) -> None:
        self._append('performance-full.jsonl', record)

    def _append(self, filename: str, record: Mapping[str, Any]) -> None:
        payload = json.dumps(_redact(record), ensure_ascii=False, separators=(',', ':'), default=str)
        with self._lock:
            path = self.directory / filename
            if path.exists() and path.stat().st_size + len(payload.encode()) + 1 > self.max_bytes:
                rotated = path.with_name(f'{path.name}.1')
                try:
                    rotated.unlink(missing_ok=True)
                    path.replace(rotated)
                except OSError:
                    return
            with path.open('a', encoding='utf-8') as stream:
                stream.write(payload)
                stream.write('\n')
