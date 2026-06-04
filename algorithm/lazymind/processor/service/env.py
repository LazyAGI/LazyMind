"""Helpers for reading typed values from environment variables."""
from __future__ import annotations

import os
from typing import List, Optional


def env_int(name: str, default: int) -> int:
    raw = os.environ.get(name, '').strip()
    if not raw:
        return default
    return int(raw)


def env_float(name: str, default: float) -> float:
    raw = os.environ.get(name, '').strip()
    if not raw:
        return default
    return float(raw)


def env_bool(name: str, default: bool) -> bool:
    if name not in os.environ:
        return default
    raw = os.environ[name].strip().lower()
    return raw in ('1', 'true', 'yes', 'on')


def env_list(name: str, sep: str = ',') -> Optional[List[str]]:
    raw = os.environ.get(name, '').strip()
    if not raw:
        return None
    return [item.strip() for item in raw.split(sep) if item.strip()]
