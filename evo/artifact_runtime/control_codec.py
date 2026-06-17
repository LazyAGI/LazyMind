from __future__ import annotations

from collections.abc import Mapping
from types import MappingProxyType
from typing import Any

from .artifact import ArtifactKey, ArtifactPayload, ArtifactRef
from .utils import is_json_scalar, normalize_json_value, sorted_string_items

_SCHEMA_VERSION = 4
_SUPPORTED_SCHEMA_VERSIONS = frozenset({1, _SCHEMA_VERSION})


def encode_control_value(value: Any) -> Any:
    if isinstance(value, ArtifactKey):
        return _envelope('ArtifactKey', artifact_id=value.artifact_id, partition=value.partition)
    if isinstance(value, ArtifactRef):
        return _envelope('ArtifactRef', key=encode_control_value(value.key), version=value.version)
    if isinstance(value, ArtifactPayload):
        return _envelope(
            'ArtifactPayload',
            schema=value.schema,
            payload=_encode_json_compatible(value.payload),
            metadata=_encode_string_any_map(value.metadata),
            fragments=[_encode_json_compatible(item) for item in value.fragments],
            role=value.role,
        )
    if is_json_scalar(value):
        return value
    if isinstance(value, list):
        return [encode_control_value(item) for item in value]
    if isinstance(value, tuple):
        return [encode_control_value(item) for item in value]
    if type(value) is dict:
        _reject_reserved_envelope_shape(value)
        return {key: encode_control_value(item) for key, item in sorted_string_items(value)}
    if isinstance(value, Mapping):
        raise TypeError('plain control mappings must be dicts with string keys')
    raise TypeError(f'unsupported control value: {type(value).__name__}')


def decode_control_value(value: Any) -> Any:
    if is_json_scalar(value):
        return value
    if isinstance(value, list):
        return [decode_control_value(item) for item in value]
    if not isinstance(value, dict):
        raise TypeError(f'unsupported encoded value: {type(value).__name__}')
    if 'schema_version' not in value or 'type' not in value:
        return {key: decode_control_value(item) for key, item in sorted_string_items(value)}
    if value['schema_version'] not in _SUPPORTED_SCHEMA_VERSIONS:
        raise ValueError(f"unsupported schema_version: {value['schema_version']}")

    item_type = value['type']
    if item_type == 'ArtifactKey':
        return ArtifactKey(str(value['artifact_id']), str(value.get('partition') or ''))
    if item_type == 'ArtifactRef':
        return ArtifactRef(decode_control_value(value['key']), int(value['version']))
    if item_type == 'ArtifactPayload':
        return ArtifactPayload(
            str(value['schema']),
            _decode_json_compatible(value['payload']),
            _decode_string_any_map(value['metadata']),
            tuple(_decode_json_compatible(item) for item in value.get('fragments') or ()),
            str(value.get('role') or ''),
        )
    if item_type == 'StringAnyMap':
        return _decode_string_any_map(value)
    raise ValueError(f'unsupported control value type: {item_type}')


def is_basic_control_envelope(value: Any) -> bool:
    return (
        isinstance(value, Mapping)
        and value.get('schema_version') in _SUPPORTED_SCHEMA_VERSIONS
        and value.get('type') in {
            'ArtifactKey',
            'ArtifactRef',
            'ArtifactPayload',
            'StringAnyMap',
        }
    )


def _envelope(item_type: str, **fields: Any) -> dict[str, Any]:
    return {'schema_version': _SCHEMA_VERSION, 'type': item_type, **fields}


def _encode_string_any_map(values: Mapping[str, Any]) -> dict[str, Any]:
    return _envelope('StringAnyMap', items=[[key, _encode_json_compatible(item)]
                     for key, item in sorted_string_items(values)])


def _decode_string_any_map(value: Mapping[str, Any]) -> Mapping[str, Any]:
    if value.get('type') != 'StringAnyMap':
        raise ValueError('expected StringAnyMap')
    return MappingProxyType({str(key): _decode_json_compatible(item) for key, item in value['items']})


def _encode_json_compatible(value: Any) -> Any:
    return normalize_json_value(value, allow_tuple=False, reject_reserved_envelope=True)


def _decode_json_compatible(value: Any) -> Any:
    if is_json_scalar(value):
        return value
    if isinstance(value, list):
        return [_decode_json_compatible(item) for item in value]
    if isinstance(value, dict):
        return {key: _decode_json_compatible(item) for key, item in sorted_string_items(value)}
    raise TypeError(f'value is not JSON-compatible: {type(value).__name__}')


def _reject_reserved_envelope_shape(values: Mapping[Any, Any]) -> None:
    if 'schema_version' in values and 'type' in values:
        raise TypeError('plain JSON objects cannot use reserved schema_version/type envelope keys')
