from __future__ import annotations

from typing import Any


def field_path_parts(field: str) -> list[str]:
    parts = [part.strip() for part in str(field or '').split('.') if part.strip()]
    if not parts:
        raise ValueError('field is required.')
    return parts


def get_nested_field(data: dict[str, Any], field: str) -> Any:
    node: Any = data
    for part in field_path_parts(field):
        if not isinstance(node, dict) or part not in node:
            raise ValueError(f'field {field!r} does not exist in document.')
        node = node[part]
    if isinstance(node, dict):
        raise ValueError(f'field {field!r} is a nested mapping; update a leaf value instead.')
    return node


def set_existing_nested_field(data: dict[str, Any], field: str, value: Any) -> None:
    """Update an existing leaf value only; never create or rename keys."""
    parts = field_path_parts(field)
    node = data
    for part in parts[:-1]:
        child = node.get(part) if isinstance(node, dict) else None
        if not isinstance(child, dict) or part not in node:
            raise ValueError(f'field {field!r} does not exist in document.')
        node = child
    leaf = parts[-1]
    if leaf not in node:
        raise ValueError(f'field {field!r} does not exist in document.')
    if isinstance(node[leaf], dict):
        raise ValueError(f'field {field!r} is a nested mapping; update a leaf value instead.')
    node[leaf] = value
