from __future__ import annotations

from typing import Any

from ..validation.soul import validate_soul_content
from .common import set_existing_yaml_fields


def set_soul_fields(content: str, changes: dict[str, str]) -> dict[str, Any]:
    return set_existing_yaml_fields(
        content,
        changes,
        entity='soul',
        validate=validate_soul_content,
        require_non_empty_string=True,
    )
