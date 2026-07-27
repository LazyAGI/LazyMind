from __future__ import annotations

from typing import Any, Optional

import yaml

from ..field_contract import discover_memory_fields
from .common import parse_yaml_mapping

_INTERNAL_VERSION_KEY = 'schema_version'


def split_stored_memory_content(
    content: str,
    *,
    label: str,
) -> tuple[str, str]:
    """Return complete storage YAML and a model-visible YAML projection."""
    document = parse_yaml_mapping(content)
    if not document:
        raise ValueError(f'{label} must be a valid non-empty YAML mapping.')
    if _INTERNAL_VERSION_KEY not in document:
        raise ValueError(f'{label} internal version metadata is missing.')

    visible = {
        key: value
        for key, value in document.items()
        if key != _INTERNAL_VERSION_KEY
    }
    if not visible:
        raise ValueError(f'{label} must contain at least one business field.')
    discover_memory_fields(visible, label=label)
    return _dump(document), _dump(visible)


def build_stored_memory_content(
    stored_document: dict[str, Any],
    visible_document: dict[str, Any],
    *,
    label: str,
) -> tuple[str, str]:
    if _INTERNAL_VERSION_KEY not in stored_document:
        raise ValueError(f'{label} internal version metadata is missing.')
    discover_memory_fields(visible_document, label=label)
    combined = {
        _INTERNAL_VERSION_KEY: stored_document[_INTERNAL_VERSION_KEY],
        **visible_document,
    }
    return _dump(combined), _dump(visible_document)


def validate_stored_memory_content(content: str, *, label: str) -> Optional[str]:
    try:
        split_stored_memory_content(content, label=label)
    except ValueError as exc:
        return str(exc)
    return None


def is_internal_memory_path(path: str) -> bool:
    return path == _INTERNAL_VERSION_KEY


def _dump(document: dict[str, Any]) -> str:
    return yaml.safe_dump(
        document,
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    )
