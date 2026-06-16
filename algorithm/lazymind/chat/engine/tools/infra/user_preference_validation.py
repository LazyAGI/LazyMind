from __future__ import annotations

import re
from typing import Any, Optional

_FRONTMATTER_RE = re.compile(r'^---\s*\n(.*?)\n---\s*\n(.*)$', re.DOTALL)
_RESPONSE_STYLES_ZH = ('简洁', '详细', '幽默', '正式')
_RESPONSE_STYLES_EN = ('concise', 'detailed', 'humorous', 'formal')
_RESPONSE_STYLES = (
    *_RESPONSE_STYLES_ZH,
    *_RESPONSE_STYLES_EN,
    '',
)


def parse_user_preference_frontmatter(content: str) -> tuple[dict[str, Any], str]:
    match = _FRONTMATTER_RE.match(content or '')
    if not match:
        return {}, content or ''

    yaml_text, body = match.group(1), match.group(2)
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

    frontmatter, body = parse_user_preference_frontmatter(content)
    if not frontmatter:
        return 'user_preference must contain YAML frontmatter.'
    if 'agent_persona' not in frontmatter:
        return "Frontmatter must include 'agent_persona'."
    if 'user_address' not in frontmatter:
        return "Frontmatter must include 'user_address'."
    if 'response_style' not in frontmatter:
        return "Frontmatter must include 'response_style'."
    if frontmatter.get('response_style') not in _RESPONSE_STYLES:
        return (
            "Frontmatter 'response_style' must be one of: "
            '简洁/详细/幽默/正式 or concise/detailed/humorous/formal or "".'
        )
    if not body.strip():
        return 'user_preference must have markdown content after frontmatter.'
    return None
