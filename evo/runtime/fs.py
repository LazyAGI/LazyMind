from __future__ import annotations
import json
import os
from pathlib import Path
from typing import Any


def atomic_write(path: Path, text: str, *, encoding: str = 'utf-8') -> None:
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.parent / (path.name + '.tmp')
    tmp.write_text(text, encoding=encoding)
    os.replace(tmp, path)


def atomic_write_json(path: Path, obj: Any, *, indent: int | None = 2) -> None:
    separators = (',', ':') if indent is None else None
    text = json.dumps(obj, ensure_ascii=False, indent=indent, separators=separators, default=str)
    atomic_write(Path(path), text)


def load_json(path: Path) -> Any:
    return json.loads(Path(path).read_text(encoding='utf-8'))
