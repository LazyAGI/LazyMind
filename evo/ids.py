from __future__ import annotations

import re


_ID_PATTERN = re.compile(r'^[A-Za-z0-9][A-Za-z0-9_.:#-]*$')


def validate_id(value: str, kind: str = 'id') -> str:
    if not isinstance(value, str) or not value:
        raise ValueError(f'invalid {kind}: {value!r}')
    if value in {'.', '..'} or '..' in value:
        raise ValueError(f'invalid {kind}: {value!r}')
    if not _ID_PATTERN.fullmatch(value):
        raise ValueError(f'invalid {kind}: {value!r}')
    return value
