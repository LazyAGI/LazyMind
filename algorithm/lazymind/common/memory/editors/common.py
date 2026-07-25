from __future__ import annotations

import json
from typing import Any, Mapping

import yaml

from ..result import memory_err, memory_ok
from ..validation.common import parse_yaml_mapping


def render_yaml_document(document: dict[str, Any]) -> str:
    return yaml.safe_dump(
        document,
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    )


def field_path_parts(field: str) -> list[str]:
    parts = [part.strip() for part in str(field or '').split('.') if part.strip()]
    if not parts:
        raise ValueError('field is required.')
    return parts


def iter_leaf_fields(data: dict[str, Any], *, prefix: str = '') -> list[str]:
    """Return dot-paths for leaf values already present in ``data``.

    Nested mappings are traversed and only existing leaves are returned.
    """
    fields: list[str] = []
    for key, value in data.items():
        path = f'{prefix}.{key}' if prefix else str(key)
        if isinstance(value, dict):
            fields.extend(iter_leaf_fields(value, prefix=path))
        else:
            fields.append(path)
    return fields


def editable_fields_from_document(document: dict[str, Any]) -> frozenset[str]:
    return frozenset(iter_leaf_fields(document))


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


def set_existing_yaml_fields(
    content: str,
    changes: Mapping[str, str],
    *,
    entity: str,
    validate,
    require_non_empty_string: bool = False,
) -> dict[str, Any]:
    """Update existing YAML leaves; keys cannot be added or renamed.

    Editable fields are discovered from the loaded RemoteFS document. Returns a
    structured result: ``{'ok': True, 'content': ...}`` or
    ``{'ok': False, 'error': ..., 'type': ...}``.
    """
    if not isinstance(changes, Mapping) or not changes:
        return memory_err('changes must be a non-empty mapping.', type='validation')

    document = parse_yaml_mapping(content)
    if not document:
        return memory_err(
            f'{entity} must contain a non-empty YAML mapping.',
            type='validation',
        )

    editable = editable_fields_from_document(document)
    normalized_changes: dict[str, str] = {}
    for field, value in changes.items():
        normalized_field = str(field or '').strip()
        if not normalized_field:
            return memory_err('change fields must be non-empty.', type='validation')
        if normalized_field in normalized_changes:
            return memory_err(
                f'duplicate field {normalized_field!r} in changes.',
                type='validation',
            )
        normalized_changes[normalized_field] = value

    unsupported = sorted(set(normalized_changes) - editable)
    if unsupported:
        supported = ', '.join(sorted(editable)) or '(none)'
        subject = (
            f'field {unsupported[0]!r} does'
            if len(unsupported) == 1
            else f'fields {unsupported!r} do'
        )
        return memory_err(
            f'{subject} not exist in {entity}; '
            f'editable fields from the loaded document: {supported}.',
            type='validation',
        )

    for normalized_field, value in normalized_changes.items():
        try:
            existing = get_nested_field(document, normalized_field)
            parsed = coerce_value_to_existing_type(existing, value)
        except (TypeError, ValueError) as exc:
            return memory_err(str(exc), type='validation')

        if require_non_empty_string and (not isinstance(parsed, str) or not parsed.strip()):
            return memory_err(
                f'{entity} field {normalized_field!r} requires a non-empty string value.',
                type='validation',
            )
        try:
            set_existing_nested_field(document, normalized_field, parsed)
        except ValueError as exc:
            return memory_err(str(exc), type='validation')

    updated = render_yaml_document(document)

    error = validate(updated)
    if error:
        return memory_err(error, type='validation')
    return memory_ok(content=updated, fields=list(normalized_changes))
