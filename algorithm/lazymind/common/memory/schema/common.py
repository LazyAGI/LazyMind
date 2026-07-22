from __future__ import annotations

import re
from typing import Any, Optional

_FRONTMATTER_RE = re.compile(r'^---\s*\n(.*?)\n---\s*(\n(.*))?$', re.DOTALL)


def parse_yaml_frontmatter(content: str) -> tuple[dict[str, Any], str]:
    match = _FRONTMATTER_RE.match(content or '')
    if not match:
        return {}, content or ''

    yaml_text, body = match.group(1), match.group(3) or ''
    try:
        import yaml  # type: ignore

        parsed = yaml.safe_load(yaml_text)
        if isinstance(parsed, dict):
            return parsed, body
    except Exception:
        pass
    return {}, body


def require_no_body(body: str, *, entity: str) -> Optional[str]:
    if body and body.strip():
        return f'{entity} must contain YAML frontmatter only; free-form body is not allowed.'
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


def optional_str_list(value: Any, *, field: str) -> Optional[str]:
    if value is None:
        return None
    if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
        return f"Field '{field}' must be a list of strings."
    return None
