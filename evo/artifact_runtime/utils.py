import hashlib
import json
import math
from collections.abc import Mapping
from typing import Any


def validate_nonempty(value: str, name: str) -> None:
    if not value or not value.strip():
        raise ValueError(f'{name} must be non-empty')


def unique_ordered(values: tuple[str, ...] | list[str]) -> tuple[str, ...]:
    seen: set[str] = set()
    out: list[str] = []
    for value in values:
        if value in seen:
            continue
        seen.add(value)
        out.append(value)
    return tuple(out)


def json_mapping_fingerprint(
    values: Mapping[str, Any],
    *,
    allow_tuple: bool = True,
    reject_reserved_envelope: bool = True,
) -> str:
    return hashlib.sha256(
        canonical_json(
            normalize_json_mapping(
                values,
                allow_tuple=allow_tuple,
                reject_reserved_envelope=reject_reserved_envelope,
            )
        ).encode()
    ).hexdigest()


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False)


def normalize_json_mapping(
    values: Mapping[Any, Any],
    *,
    allow_tuple: bool = True,
    reject_reserved_envelope: bool = True,
) -> dict[str, Any]:
    if reject_reserved_envelope and 'schema_version' in values and 'type' in values:
        raise TypeError('plain JSON objects cannot use reserved schema_version/type envelope keys')
    return {
        key: normalize_json_value(item, allow_tuple=allow_tuple, reject_reserved_envelope=reject_reserved_envelope)
        for key, item in sorted_string_items(values)
    }


def normalize_json_value(
    value: Any,
    *,
    allow_tuple: bool = True,
    reject_reserved_envelope: bool = True,
) -> Any:
    if is_json_scalar(value):
        return value
    if isinstance(value, list):
        return [
            normalize_json_value(
                item,
                allow_tuple=allow_tuple,
                reject_reserved_envelope=reject_reserved_envelope) for item in value]
    if allow_tuple and isinstance(value, tuple):
        return [
            normalize_json_value(
                item,
                allow_tuple=allow_tuple,
                reject_reserved_envelope=reject_reserved_envelope) for item in value]
    if type(value) is dict:
        return normalize_json_mapping(value, allow_tuple=allow_tuple, reject_reserved_envelope=reject_reserved_envelope)
    raise TypeError(f'value is not JSON-compatible: {type(value).__name__}')


def sorted_string_items(values: Mapping[Any, Any]) -> list[tuple[str, Any]]:
    out: list[tuple[str, Any]] = []
    for key, value in values.items():
        if not isinstance(key, str):
            raise TypeError('mapping keys must be strings')
        out.append((key, value))
    return sorted(out)


def is_json_scalar(value: Any) -> bool:
    return value is None or isinstance(value, (str, int, bool)) or (isinstance(value, float) and math.isfinite(value))
