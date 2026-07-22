from __future__ import annotations

from typing import Any, Optional


def memory_ok(**payload: Any) -> dict[str, Any]:
    return {'ok': True, **payload}


def memory_err(error: str, *, type: str = 'validation', **payload: Any) -> dict[str, Any]:
    return {'ok': False, 'error': str(error), 'type': type, **payload}


def is_memory_ok(result: Optional[dict[str, Any]]) -> bool:
    return isinstance(result, dict) and result.get('ok') is True
