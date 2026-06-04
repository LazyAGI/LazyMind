from __future__ import annotations

import json
from typing import Any, Dict


def truncate_text(text: Any, max_len: int) -> str:
    if text is None:
        return ''
    raw = text if isinstance(text, str) else str(text)
    return raw if len(raw) <= max_len else f'{raw[:max_len]}...'


def parse_json_dict(value: Any) -> Dict[str, Any]:
    if isinstance(value, dict):
        return value
    if isinstance(value, (str, bytes, bytearray)) and value:
        try:
            parsed = json.loads(value)
            return parsed if isinstance(parsed, dict) else {}
        except (TypeError, ValueError):
            return {}
    return {}


def parse_number_range(number: Any) -> tuple[int, int]:
    if isinstance(number, str):
        raw = number.strip()
        try:
            number = json.loads(raw)
        except (TypeError, ValueError):
            if ',' in raw:
                number = [part.strip() for part in raw.split(',', 1)]
            elif '-' in raw:
                number = [part.strip() for part in raw.split('-', 1)]
            else:
                number = raw

    if isinstance(number, (list, tuple)):
        if len(number) != 2:
            raise ValueError('number range must be [start, end]')
        start, end = int(number[0]), int(number[1])
    else:
        start = end = int(number)
    if start > end:
        start, end = end, start
    return start, end


def iter_lookup_ids(value: Any, *, field_name: str) -> list[Any]:
    if value is None:
        return [None]
    if isinstance(value, str):
        return [value]
    if isinstance(value, list):
        return value or [None]
    raise TypeError(f'{field_name} must be None, str, or list[str], got {type(value).__name__}')


def absolute_url(url: str) -> str:
    normalized = str(url or '').strip()
    if not normalized:
        return ''
    if normalized.startswith(('http://', 'https://')):
        return normalized
    return f'https://{normalized}'
