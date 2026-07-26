from __future__ import annotations

from typing import Any

import yaml

from ..result import memory_err, memory_ok
from ..validation.common import parse_yaml_mapping
from ..validation.soul import (
    CURRENT_SOUL_SCHEMA_VERSION,
    normalize_soul_content,
)
from .common import set_existing_nested_field


def apply_soul_operations(
    content: str,
    operations: list[dict[str, Any]],
) -> dict[str, Any]:
    document = parse_yaml_mapping(content)
    if not document:
        return memory_err('soul must contain a non-empty YAML mapping.', type='validation')
    if not isinstance(operations, list) or not operations:
        return memory_err('operations must be a non-empty list.', type='validation')

    applied: list[dict[str, str]] = []
    for operation in operations:
        if not isinstance(operation, dict):
            return memory_err('each operation must be a mapping.', type='validation')
        if set(operation) - {'op', 'path', 'value'}:
            return memory_err('soul operations contain unsupported fields.', type='validation')
        raw_op = operation.get('op')
        raw_path = operation.get('path')
        op = raw_op.strip() if isinstance(raw_op, str) else ''
        path = raw_path.strip() if isinstance(raw_path, str) else ''
        value = operation.get('value')
        if op != 'set':
            return memory_err(
                f"soul operation {path!r} only supports op 'set'.",
                type='validation',
            )
        normalized_value = value.strip() if isinstance(value, str) else ''
        if not path or not normalized_value:
            return memory_err(
                'soul set operations require a path and non-empty string value.',
                type='validation',
            )
        try:
            set_existing_nested_field(document, path, normalized_value)
        except ValueError as exc:
            return memory_err(str(exc), type='validation')
        applied.append({'op': op, 'path': path, 'value': normalized_value})

    stored_input = yaml.safe_dump(
        {'schema_version': CURRENT_SOUL_SCHEMA_VERSION, **document},
        allow_unicode=True,
        sort_keys=False,
    )
    try:
        stored, _ = normalize_soul_content(stored_input)
    except ValueError as exc:
        return memory_err(str(exc), type='validation')
    return memory_ok(content=stored, operations=applied)
