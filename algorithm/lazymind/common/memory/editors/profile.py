from __future__ import annotations

from typing import Any

import yaml

from ..result import memory_err, memory_ok
from ..validation.common import parse_yaml_mapping
from ..validation.profile import (
    CURRENT_PROFILE_SCHEMA_VERSION,
    normalize_profile_content,
)
from .common import get_nested_field, set_existing_nested_field

_SCALAR_PATHS = {
    'identity.preferred_name',
    'locale.residence',
}
_LIST_PATHS = {
    'identity.aliases',
    'locale.languages',
    'professional.occupations',
    'professional.organizations',
    'professional.industries',
    'professional.expertise_domains',
}


def apply_profile_operations(
    content: str,
    operations: list[dict[str, Any]],
) -> dict[str, Any]:
    document = parse_yaml_mapping(content)
    if not document:
        return memory_err('profile must contain a non-empty YAML mapping.', type='validation')
    if not isinstance(operations, list) or not operations:
        return memory_err('operations must be a non-empty list.', type='validation')

    applied: list[dict[str, str]] = []
    for operation in operations:
        if not isinstance(operation, dict):
            return memory_err('each operation must be a mapping.', type='validation')
        if set(operation) - {'op', 'path', 'value'}:
            return memory_err('profile operations contain unsupported fields.', type='validation')
        raw_op = operation.get('op')
        raw_path = operation.get('path')
        op = raw_op.strip() if isinstance(raw_op, str) else ''
        path = raw_path.strip() if isinstance(raw_path, str) else ''
        value = operation.get('value')
        if path in _SCALAR_PATHS:
            if op == 'set':
                normalized_value = value.strip() if isinstance(value, str) else ''
                if not normalized_value:
                    return memory_err(
                        f'profile set operation on {path!r} requires a non-empty value.',
                        type='validation',
                    )
                set_existing_nested_field(document, path, normalized_value)
                applied.append({'op': op, 'path': path, 'value': normalized_value})
            elif op == 'clear' and value is None:
                set_existing_nested_field(document, path, None)
                applied.append({'op': op, 'path': path})
            else:
                return memory_err(
                    f'profile scalar path {path!r} only supports set or clear.',
                    type='validation',
                )
            continue

        if path not in _LIST_PATHS:
            return memory_err(f'unsupported profile operation path {path!r}.', type='validation')
        try:
            current = get_nested_field(document, path)
        except ValueError as exc:
            return memory_err(str(exc), type='validation')
        if not isinstance(current, list):
            return memory_err(f'profile path {path!r} is not a list.', type='validation')
        if op == 'clear' and value is None:
            next_value: list[str] = []
            applied.append({'op': op, 'path': path})
        elif op in {'add', 'remove'}:
            normalized_value = value.strip() if isinstance(value, str) else ''
            if not normalized_value:
                return memory_err(
                    f'profile {op} operation on {path!r} requires a non-empty value.',
                    type='validation',
                )
            if op == 'add':
                next_value = list(current)
                if normalized_value not in next_value:
                    next_value.append(normalized_value)
            else:
                next_value = [item for item in current if item != normalized_value]
            applied.append({'op': op, 'path': path, 'value': normalized_value})
        else:
            return memory_err(
                f'profile list path {path!r} only supports add, remove, or clear.',
                type='validation',
            )
        set_existing_nested_field(document, path, next_value)

    stored_input = yaml.safe_dump(
        {'schema_version': CURRENT_PROFILE_SCHEMA_VERSION, **document},
        allow_unicode=True,
        sort_keys=False,
    )
    try:
        stored, _ = normalize_profile_content(stored_input)
    except ValueError as exc:
        return memory_err(str(exc), type='validation')
    return memory_ok(content=stored, operations=applied)
