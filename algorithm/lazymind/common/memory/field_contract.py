from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Literal

MemoryFieldType = Literal['string', 'null', 'string_list']

_ALLOWED_OPERATIONS: dict[MemoryFieldType, tuple[str, ...]] = {
    'string': ('set', 'clear'),
    'null': ('set', 'clear'),
    'string_list': ('add', 'remove', 'clear'),
}


@dataclass(frozen=True)
class MemoryField:
    path: str
    field_type: MemoryFieldType
    allowed_operations: tuple[str, ...]


def discover_memory_fields(
    document: dict[str, Any],
    *,
    label: str = 'memory',
) -> dict[str, MemoryField]:
    """Discover editable leaves without knowing any business field names."""
    fields: dict[str, MemoryField] = {}

    def visit(node: dict[str, Any], prefix: str = '') -> None:
        for key, value in node.items():
            if not isinstance(key, str) or not key.strip() or '.' in key:
                raise ValueError(
                    f'{label} mapping keys must be non-empty strings without dots.'
                )
            path = f'{prefix}.{key}' if prefix else key
            if isinstance(value, dict):
                visit(value, path)
                continue
            field_type = _field_type(value, path, label)
            fields[path] = MemoryField(
                path=path,
                field_type=field_type,
                allowed_operations=_ALLOWED_OPERATIONS[field_type],
            )

    visit(document)
    if not fields:
        raise ValueError(f'{label} must contain at least one editable leaf field.')
    return fields


def validate_memory_transition(
    before: dict[str, Any],
    after: dict[str, Any],
    *,
    label: str = 'memory',
) -> None:
    """Validate that an edit preserves mapping shape, paths, and leaf contracts."""
    before_fields = discover_memory_fields(before, label=label)
    after_fields = discover_memory_fields(after, label=label)
    if _mapping_paths(before) != _mapping_paths(after):
        raise ValueError(f'{label} mapping structure cannot be changed.')
    if set(before_fields) != set(after_fields):
        raise ValueError(f'{label} leaf fields cannot be added, removed, or renamed.')

    for path, field in before_fields.items():
        value = _nested_value(after, path)
        if field.field_type == 'string' and not isinstance(value, str):
            raise ValueError(f"{label} field '{path}' must remain a string.")
        if field.field_type == 'null' and value is not None and not isinstance(value, str):
            raise ValueError(
                f"{label} field '{path}' must remain null or become a string."
            )
        if field.field_type == 'string_list' and (
            not isinstance(value, list)
            or not all(isinstance(item, str) for item in value)
        ):
            raise ValueError(
                f"{label} field '{path}' must remain a list of strings."
            )


def memory_operation_rules() -> str:
    """Return the shared operation contract for prompt and tool descriptions."""
    return (
        'Soul and Profile field operations are determined by each current YAML leaf '
        'value:\n'
        f'- A YAML string supports {_format_operations("string")}; `clear` writes an '
        'empty string.\n'
        f'- A YAML `null` supports {_format_operations("null")}; `set` writes a '
        'non-empty string.\n'
        f'- A YAML list of strings supports {_format_operations("string_list")}; '
        'operate on one string item at a time and never replace the complete list '
        'with `set`.\n'
        '- Use only existing leaf dot paths. Do not add, remove, rename, or edit '
        'mappings.\n'
        '- `set`, `add`, and `remove` require a non-empty string `value`; `clear` '
        'must not carry a value.'
    )


def _field_type(value: Any, path: str, label: str) -> MemoryFieldType:
    if isinstance(value, str):
        return 'string'
    if value is None:
        return 'null'
    if isinstance(value, list) and all(isinstance(item, str) for item in value):
        return 'string_list'
    raise ValueError(
        f"{label} field '{path}' has unsupported type {type(value).__name__}; "
        'expected a string, null, or list of strings.'
    )


def _format_operations(field_type: MemoryFieldType) -> str:
    operations = [f'`{operation}`' for operation in _ALLOWED_OPERATIONS[field_type]]
    if len(operations) == 2:
        return ' and '.join(operations)
    return f'{", ".join(operations[:-1])}, and {operations[-1]}'


def _mapping_paths(document: dict[str, Any]) -> set[str]:
    paths = {''}

    def visit(node: dict[str, Any], prefix: str = '') -> None:
        for key, value in node.items():
            path = f'{prefix}.{key}' if prefix else key
            if isinstance(value, dict):
                paths.add(path)
                visit(value, path)

    visit(document)
    return paths


def _nested_value(document: dict[str, Any], path: str) -> Any:
    node: Any = document
    for part in path.split('.'):
        node = node[part]
    return node
