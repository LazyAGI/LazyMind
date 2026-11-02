"""Convert imported resource descriptions to Workflow input references."""
from collections.abc import Mapping
from dataclasses import asdict, is_dataclass
from typing import Any


def normalize_input_reference(value: Any) -> Any:
    if isinstance(value, list):
        return [normalize_input_reference(item) for item in value]
    if is_dataclass(value) and not isinstance(value, type):
        value = asdict(value)
    if isinstance(value, Mapping):
        # Only discard known resource-description fields. Preserve unknown or
        # invalid fields so Core can reject them instead of hiding caller bugs.
        return {key: item for key, item in value.items()
                if key not in {'name', 'mime_type', 'size'}}
    return value


def normalize_input_bindings(bindings: Any) -> Any:
    if bindings is None:
        return {}
    if isinstance(bindings, Mapping):
        return {key: normalize_input_reference(value) for key, value in bindings.items()}
    return bindings
