from __future__ import annotations

from typing import Optional

from .common import parse_yaml_mapping


def validate_profile_content(content: str) -> Optional[str]:
    if not content or not str(content).strip():
        return 'profile requires a non-empty YAML mapping.'

    document = parse_yaml_mapping(content)
    if not document:
        return 'profile must be a valid non-empty YAML mapping.'
    return None
