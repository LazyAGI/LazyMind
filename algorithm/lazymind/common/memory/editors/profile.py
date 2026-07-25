from __future__ import annotations

from typing import Any

from ..validation.profile import validate_profile_content
from .common import set_existing_yaml_fields


def set_profile_fields(content: str, changes: dict[str, str]) -> dict[str, Any]:
    return set_existing_yaml_fields(
        content,
        changes,
        entity='profile',
        validate=validate_profile_content,
    )
