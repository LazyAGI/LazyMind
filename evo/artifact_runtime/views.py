from __future__ import annotations

from collections.abc import Callable
import json
from typing import Any

from jsonpointer import JsonPointer, JsonPointerException

DEFAULT_VIEW_CHARS = 1200
MAX_VIEW_CHARS = 4000


class ArtifactViewService:
    def __init__(self, artifact_reader: Callable[[str], dict | None]) -> None:
        self._artifact_reader = artifact_reader

    def view(
        self,
        artifact_id: str,
        *,
        selector: str = '',
        cursor: str = '',
        max_chars: int = DEFAULT_VIEW_CHARS,
    ) -> dict[str, Any]:
        limit = clamp_view_chars(max_chars)
        row = self._artifact_reader(artifact_id)
        if row is None:
            return {
                'source_ref': artifact_id,
                'schema': '',
                'facts': {'exists': False},
                'excerpt': '',
                'max_chars': limit,
                'truncated': False,
                'next_cursor': '',
                'available_sections': [],
                'selector': '',
                'untrusted': True,
            }
        data = row.get('data')
        rendered = json.dumps(_select_value(data, selector), ensure_ascii=False, sort_keys=True, default=str)
        chunk = _chunk(rendered, cursor, limit)
        return {
            'source_ref': str(row.get('ref') or artifact_id),
            'schema': str(row.get('schema') or ''),
            'facts': _facts(data),
            'excerpt': chunk['text'],
            'max_chars': limit,
            'truncated': chunk['truncated'],
            'next_cursor': chunk['next_cursor'],
            'available_sections': _sections(data),
            'selector': selector,
            'untrusted': True,
        }


def clamp_view_chars(value: int) -> int:
    try:
        number = int(value)
    except (TypeError, ValueError):
        number = DEFAULT_VIEW_CHARS
    return max(200, min(MAX_VIEW_CHARS, number))


def _select_value(data: Any, selector: str) -> Any:
    text = str(selector or '').strip()
    if not text:
        return data
    if isinstance(data, dict) and text in data:
        return data[text]
    if text.startswith('/'):
        try:
            return JsonPointer(text).resolve(data)
        except (JsonPointerException, TypeError, ValueError, IndexError):
            return {'selector': text, 'error': 'selector_not_found'}
    return {'selector': text, 'error': 'selector_not_found'}


def _chunk(text: str, cursor: str, limit: int) -> dict[str, Any]:
    start = _cursor_offset(cursor)
    start = max(0, min(start, len(text)))
    end = min(len(text), start + limit)
    return {
        'text': text[start:end],
        'truncated': end < len(text),
        'next_cursor': '' if end >= len(text) else f'offset:{end}',
    }


def _cursor_offset(cursor: str) -> int:
    text = str(cursor or '').strip()
    if text.startswith('offset:'):
        text = text.split(':', 1)[1]
    try:
        return max(0, int(text))
    except ValueError:
        return 0


def _facts(data: Any) -> dict[str, Any]:
    if isinstance(data, dict):
        return {
            'exists': True,
            'keys': sorted(str(key) for key in data.keys())[:30],
            'status': str(data.get('status') or ''),
        }
    if isinstance(data, list):
        return {'exists': True, 'items': len(data)}
    return {'exists': True, 'type': type(data).__name__}


def _sections(data: Any) -> list[str]:
    if isinstance(data, dict):
        return sorted(str(key) for key in data.keys())[:30]
    if isinstance(data, list):
        return [f'/{index}' for index in range(min(len(data), 30))]
    return []
