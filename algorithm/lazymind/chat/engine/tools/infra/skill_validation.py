from __future__ import annotations

import re
from typing import Any, Optional

_PATH_SEGMENT_RE = re.compile(r'^[^\s/\\]+$')
_FRONTMATTER_RE = re.compile(r'^---\s*\n(.*?)\n---\s*\n(.*)$', re.DOTALL)
_MAX_DESCRIPTION_LENGTH = 1024


def validate_skill_name(name: str) -> Optional[str]:
    if not name or not name.strip():
        return "'name' must be a non-empty skill name."
    if name in {'.', '..'} or not _PATH_SEGMENT_RE.match(name):
        return (
            f'Skill name {name!r} is invalid; whitespace and slashes '
            'are not allowed.'
        )
    return None


def normalize_skill_category(category: Optional[str]) -> Optional[str]:
    if category is None:
        return ''
    cleaned = str(category).strip().strip('/')
    if not cleaned:
        return ''
    if cleaned in {'.', '..'} or not _PATH_SEGMENT_RE.match(cleaned):
        return None
    return cleaned


def parse_skill_frontmatter(content: str) -> tuple[dict[str, Any], str]:
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


def validate_skill_content(content: str) -> Optional[str]:
    if not content or not content.strip():
        return "action='create' requires a non-empty 'content' (full SKILL.md body)."

    frontmatter, body = parse_skill_frontmatter(content)
    if not frontmatter:
        return 'SKILL.md must contain YAML frontmatter.'
    if 'name' not in frontmatter:
        return "Frontmatter must include 'name'."
    if 'description' not in frontmatter:
        return "Frontmatter must include 'description'."
    if len(str(frontmatter.get('description', ''))) > _MAX_DESCRIPTION_LENGTH:
        return f'Description exceeds {_MAX_DESCRIPTION_LENGTH} characters.'
    if not body.strip():
        return 'SKILL.md must have markdown content after frontmatter.'
    return None
