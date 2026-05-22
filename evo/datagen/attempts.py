from __future__ import annotations
import json
import time
from pathlib import Path
from typing import Any
from evo.runtime.fs import atomic_write_json


def start_attempt(base: Path, group: str, attempt_id: str | None) -> tuple[Path, dict[str, Any]]:
    root = Path(base) / group
    root.mkdir(parents=True, exist_ok=True)
    current = root / (attempt_id or str(int(time.time() * 1000)))
    current.mkdir(parents=True, exist_ok=True)
    return current, _read(current / 'partial.json') or _read(root / 'latest.json')


def save_attempt(current: Path, data: dict[str, Any], *, indent: int | None = 2) -> None:
    payload = {**data, '_attempt_id': current.name, '_updated_at': time.time()}
    atomic_write_json(current / 'partial.json', payload, indent=indent)
    atomic_write_json(current.parent / 'latest.json', payload, indent=indent)


def _read(path: Path) -> dict[str, Any]:
    if not path.is_file():
        return {}
    try:
        return json.loads(path.read_text(encoding='utf-8'))
    except Exception:
        return {}
