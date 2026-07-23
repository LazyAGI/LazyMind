from __future__ import annotations

from typing import Any

from ..validation.profile import validate_profile_content
from .common import set_existing_frontmatter_field


def set_profile_field(content: str, field: str, value: str) -> dict[str, Any]:
    return set_existing_frontmatter_field(
        content,
        field,
        value,
        entity='profile',
        validate=validate_profile_content,
    )
