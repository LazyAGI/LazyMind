from __future__ import annotations

import re
from typing import Any, Optional

_FRONTMATTER_RE = re.compile(r'^---\s*\n(.*?)\n---\s*(\n(.*))?$', re.DOTALL)
_FRONTMATTER_KEYS = ('agent_persona', 'preferred_name', 'response_style')
_FRONTMATTER_KEY_SET = set(_FRONTMATTER_KEYS)
_FRONTMATTER_FIELD_MAX_CHARS = 100


def parse_user_preference_frontmatter(content: str) -> tuple[dict[str, Any], str]:
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


def validate_user_preference_content(content: str) -> Optional[str]:
    if not content or not content.strip():
        return "user_preference requires a non-empty 'content'."

    frontmatter, _ = parse_user_preference_frontmatter(content)
    if not frontmatter:
        return 'user_preference must contain YAML frontmatter.'

    missing_keys = [key for key in _FRONTMATTER_KEYS if key not in frontmatter]
    extra_keys = sorted(str(key) for key in frontmatter if key not in _FRONTMATTER_KEY_SET)
    if missing_keys or extra_keys:
        details = []
        if missing_keys:
            details.append(f'missing: {", ".join(missing_keys)}')
        if extra_keys:
            details.append(f'extra: {", ".join(extra_keys)}')
        return (
            'Frontmatter keys must be exactly agent_persona, preferred_name, and response_style; '
            f'{("; ".join(details))}. Move other fields to the Markdown body.'
        )

    for key in _FRONTMATTER_KEYS:
        value = frontmatter.get(key)
        if not isinstance(value, str):
            return f"Frontmatter '{key}' must be a string."
        if len(value.strip()) > _FRONTMATTER_FIELD_MAX_CHARS:
            return f"Frontmatter '{key}' must be {_FRONTMATTER_FIELD_MAX_CHARS} characters or less."
    return None
