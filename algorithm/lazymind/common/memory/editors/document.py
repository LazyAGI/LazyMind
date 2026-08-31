from __future__ import annotations

from copy import deepcopy
from typing import Any

from ..field_contract import discover_memory_fields, validate_memory_transition
from ..validation.common import parse_yaml_mapping
from ..validation.document import (
    build_stored_memory_content,
    is_internal_memory_path,
)
from .common import get_nested_field, set_existing_nested_field


def apply_memory_operations(
    content: str,
    operations: list[dict[str, Any]],
    *,
    label: str,
) -> dict[str, Any]:
    stored_document = parse_yaml_mapping(content)
    if not stored_document:
        raise ValueError(f'{label} must contain a non-empty YAML mapping.')
    visible_document = {
        key: value
        for key, value in stored_document.items()
        if not is_internal_memory_path(key)
    }
    if not isinstance(operations, list) or not operations:
        raise ValueError('operations must be a non-empty list.')

    fields = discover_memory_fields(visible_document, label=label)

    before = deepcopy(visible_document)
    document = deepcopy(visible_document)
    applied: list[dict[str, str]] = []
    for operation in operations:
        if not isinstance(operation, dict):
            raise ValueError('each operation must be a mapping.')
        if set(operation) - {'op', 'path', 'value'}:
            raise ValueError(f'{label} operations contain unsupported fields.')
        raw_op = operation.get('op')
        raw_path = operation.get('path')
        op = raw_op.strip() if isinstance(raw_op, str) else ''
        path = raw_path.strip() if isinstance(raw_path, str) else ''
        value = operation.get('value')
        if is_internal_memory_path(path):
            raise ValueError('internal memory metadata cannot be edited.')
        field = fields.get(path)
        if field is None:
            raise ValueError(f'unsupported {label} operation path {path!r}.')
        if op not in field.allowed_operations:
            allowed = ', '.join(field.allowed_operations)
            raise ValueError(
                f'{label} {field.field_type} path {path!r} only supports {allowed}.',
            )

        if op == 'set':
            normalized_value = _required_value(value)
            if normalized_value is None:
                raise ValueError(
                    f'{label} set operation on {path!r} requires a non-empty value.',
                )
            set_existing_nested_field(document, path, normalized_value)
            applied.append({'op': op, 'path': path, 'value': normalized_value})
            continue

        if op == 'clear':
            if value is not None:
                raise ValueError(
                    f'{label} clear operation on {path!r} must not carry a value.',
                )
            if field.field_type == 'string_list':
                cleared_value: Any = []
            elif field.field_type == 'string':
                cleared_value = ''
            else:
                cleared_value = None
            set_existing_nested_field(document, path, cleared_value)
            applied.append({'op': op, 'path': path})
            continue

        current = get_nested_field(document, path)
        normalized_value = _required_value(value)
        if normalized_value is None:
            raise ValueError(
                f'{label} {op} operation on {path!r} requires a non-empty value.',
            )
        if op == 'add':
            next_value = list(current)
            if normalized_value not in next_value:
                next_value.append(normalized_value)
        else:
            next_value = [item for item in current if item != normalized_value]
        set_existing_nested_field(document, path, next_value)
        applied.append({'op': op, 'path': path, 'value': normalized_value})

    validate_memory_transition(before, document, label=label)
    stored, visible = build_stored_memory_content(
        stored_document,
        document,
        label=label,
    )
    return {
        'content': visible,
        'stored_content': stored,
        'operations': applied,
    }


def _required_value(value: Any) -> str | None:
    normalized = value.strip() if isinstance(value, str) else ''
    return normalized or None
