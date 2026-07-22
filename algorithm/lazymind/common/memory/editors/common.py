from __future__ import annotations

import json
from typing import Any

import yaml

from ..schema.common import parse_yaml_frontmatter


def render_yaml_frontmatter(frontmatter: dict[str, Any]) -> str:
    body = yaml.safe_dump(
        frontmatter,
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    ).strip()
    return f'---\n{body}\n---\n'


def field_path_parts(field: str) -> list[str]:
    parts = [part.strip() for part in str(field or '').split('.') if part.strip()]
    if not parts:
        raise ValueError('field is required.')
    return parts


def iter_leaf_fields(data: dict[str, Any], *, prefix: str = '') -> list[str]:
    """Return dot-paths for leaf values already present in ``data``.

    Nested mappings are traversed; ``schema_version`` at the document root is
    excluded because editors must not change schema identity.
    """
    fields: list[str] = []
    for key, value in data.items():
        if not prefix and key == 'schema_version':
            continue
        path = f'{prefix}.{key}' if prefix else str(key)
        if isinstance(value, dict):
            fields.extend(iter_leaf_fields(value, prefix=path))
        else:
            fields.append(path)
    return fields


def editable_fields_from_frontmatter(frontmatter: dict[str, Any]) -> frozenset[str]:
    return frozenset(iter_leaf_fields(frontmatter))


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


def coerce_value_to_existing_type(existing: Any, raw_value: str) -> Any:
    """Parse ``raw_value`` to match the type already stored at the leaf."""
    text = '' if raw_value is None else str(raw_value).strip()
    if isinstance(existing, list):
        if not text or text == '[]':
            return []
        if text.startswith('['):
            parsed = json.loads(text)
            if not isinstance(parsed, list) or not all(isinstance(item, str) for item in parsed):
                raise ValueError('list fields require a JSON string array.')
            return [str(item).strip() for item in parsed if str(item).strip()]
        return [part.strip() for part in text.split(',') if part.strip()]
    if existing is None:
        if not text or text.lower() == 'null':
            return None
        return text
    if isinstance(existing, str):
        if not text or text.lower() == 'null':
            return None
        return text
    raise ValueError(
        f'cannot update field with stored type {type(existing).__name__}; '
        'only string, null, or string-list leaves are editable.'
    )


def update_frontmatter_document(content: str, field: str, value: Any) -> str:
    frontmatter, body = parse_yaml_frontmatter(content)
    if not frontmatter:
        raise ValueError('document must contain YAML frontmatter.')
    set_existing_nested_field(frontmatter, field, value)
    rendered = render_yaml_frontmatter(frontmatter)
    if body and body.strip():
        return f'{rendered}{body}'
    return rendered
