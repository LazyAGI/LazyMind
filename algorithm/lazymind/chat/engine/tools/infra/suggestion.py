from __future__ import annotations

from typing import Any, Dict, Optional

from pydantic import BaseModel


class Suggestion(BaseModel):
    """Shared natural-language edit suggestion for managed tool content."""

    title: str
    content: str
    reason: Optional[str] = None


def dump_suggestion(value: Suggestion | Dict[str, Any]) -> Dict[str, Any]:
    if isinstance(value, Suggestion):
        return value.model_dump(exclude_none=True)
    if isinstance(value, dict):
        payload = dict(value)
        if payload.get('reason') is None:
            payload.pop('reason', None)
        return payload
    raise TypeError(f'unsupported suggestion type: {type(value).__name__}')
