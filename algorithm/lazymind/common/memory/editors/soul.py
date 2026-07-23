from __future__ import annotations

from typing import Any

from ..validation.soul import validate_soul_content
from .common import set_existing_yaml_field


def set_soul_field(content: str, field: str, value: str) -> dict[str, Any]:
    return set_existing_yaml_field(
        content,
        field,
        value,
        entity='soul',
        validate=validate_soul_content,
        require_non_empty_string=True,
    )
