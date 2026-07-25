from __future__ import annotations

import re
from datetime import datetime
from typing import Any, Optional

import yaml

_FRONTMATTER_RE = re.compile(r'^---\s*\n(.*?)\n---\s*(\n(.*))?$', re.DOTALL)


def parse_yaml_frontmatter(content: str) -> tuple[dict[str, Any], str]:
    match = _FRONTMATTER_RE.match(content or '')
    if not match:
        return {}, content or ''

    yaml_text, body = match.group(1), match.group(3) or ''
    try:
        parsed = yaml.safe_load(yaml_text)
        if isinstance(parsed, dict):
            return parsed, body
    except Exception:
        pass
    return {}, body


def parse_yaml_mapping(content: str) -> dict[str, Any]:
    """Parse one plain YAML mapping, returning an empty mapping on invalid input."""
    try:
        parsed = yaml.safe_load(content or '')
    except Exception:
        return {}
    return parsed if isinstance(parsed, dict) else {}


def validate_iso_datetime(value: Any, *, field: str) -> Optional[str]:
    if not isinstance(value, str) or not value.strip():
        return f"Field '{field}' must be a non-empty ISO 8601 datetime string."
    normalized = value.strip()
    try:
        parsed = datetime.fromisoformat(normalized.replace('Z', '+00:00'))
    except ValueError:
        return f"Field '{field}' must be an ISO 8601 datetime string."
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        return f"Field '{field}' must include a timezone offset."
    return None


def require_mapping(value: Any, *, field: str) -> Optional[str]:
    if value is None:
        return None
    if not isinstance(value, dict):
        return f"Field '{field}' must be a mapping."
    return None


def reject_unknown_keys(data: dict[str, Any], allowed: set[str], *, field: str) -> Optional[str]:
    extra = sorted(str(key) for key in data if key not in allowed)
    if extra:
        return f"Field '{field}' has unsupported keys: {', '.join(extra)}."
    return None


def optional_str(value: Any, *, field: str) -> Optional[str]:
    if value is None:
        return None
    if not isinstance(value, str):
        return f"Field '{field}' must be a string or null."
    return None
