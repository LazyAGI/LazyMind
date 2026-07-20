from __future__ import annotations

import re
from typing import Any, Literal, Optional

import yaml  # type: ignore

_PATH_SEGMENT_RE = re.compile(r'^[A-Za-z0-9._-]+$')
_FRONTMATTER_RE = re.compile(r'^---\s*\n(.*?)\n---\s*\n(.*)$', re.DOTALL)
_MAX_DESCRIPTION_LENGTH = 1024

INTERNAL_SKILL_CATEGORY = 'internal'
EXTERNAL_SKILL_CATEGORY = 'external'
SkillStorageCategory = Literal['internal', 'external']
SKILL_STORAGE_CATEGORIES: frozenset[SkillStorageCategory] = frozenset({
    INTERNAL_SKILL_CATEGORY,
    EXTERNAL_SKILL_CATEGORY,
})
_SKILL_STORAGE_CATEGORY_ERROR = (
    f'Skill storage category must be {INTERNAL_SKILL_CATEGORY!r} or '
    f'{EXTERNAL_SKILL_CATEGORY!r}.'
)


def validate_skill_name(name: str) -> Optional[str]:
    raw = str(name or '')
    cleaned = raw.strip()
    if not cleaned:
        return "'name' must be a non-empty skill name."
    if raw != cleaned or cleaned in {'.', '..'} or not _PATH_SEGMENT_RE.match(cleaned):
        return (
            f'Skill name {name!r} is invalid; only ASCII letters, digits, '
            "'-', '_' and '.' are allowed."
        )
    return None


def require_skill_storage_category(category: str) -> str:
    normalized = str(category or '').strip()
    if normalized not in SKILL_STORAGE_CATEGORIES:
        raise ValueError(_SKILL_STORAGE_CATEGORY_ERROR)
    return normalized


def parse_skill_storage_key(value: str) -> tuple[str, str]:
    raw = str(value or '').strip()
    parts = raw.split('/')
    if len(parts) != 2 or not all(parts):
        raise ValueError(f"Skill key {raw!r} must be in 'category/name' form.")
    category = require_skill_storage_category(parts[0])
    name = parts[1]
    name_error = validate_skill_name(name)
    if name_error:
        raise ValueError(f'Skill key {raw!r} has invalid name: {name_error}')
    return category, name


def parse_skill_frontmatter(content: str) -> tuple[dict[str, Any], str]:
    match = _FRONTMATTER_RE.match(content or '')
    if not match:
        return {}, content or ''

    yaml_text, body = match.group(1), match.group(2)
    try:
        parsed = yaml.safe_load(yaml_text)
        if isinstance(parsed, dict):
            return parsed, body
    except Exception:
        pass

    return {}, body


def skill_name_from_content(content: str) -> str:
    frontmatter, _ = parse_skill_frontmatter(content)
    return str(frontmatter.get('name') or '').strip()


def rewrite_skill_name(content: str, name: str) -> str:
    frontmatter, body = parse_skill_frontmatter(content)
    if not frontmatter:
        raise ValueError('SKILL.md must contain YAML frontmatter.')
    frontmatter = dict(frontmatter)
    frontmatter['name'] = name

    yaml_text = yaml.safe_dump(frontmatter, allow_unicode=True, sort_keys=False).strip()
    return f'---\n{yaml_text}\n---\n{body}'


def validate_skill_document(content: str) -> Optional[str]:
    """Validate SKILL.md structure without interpreting frontmatter category."""
    if not content or not content.strip():
        return "action='create' requires a non-empty 'content' (full SKILL.md body)."

    frontmatter, body = parse_skill_frontmatter(content)
    if not frontmatter:
        return 'SKILL.md must contain YAML frontmatter.'
    name = str(frontmatter.get('name') or '').strip()
    description = str(frontmatter.get('description') or '').strip()
    if not name:
        return "Frontmatter must include non-empty 'name'."
    if not description:
        return "Frontmatter must include non-empty 'description'."
    name_error = validate_skill_name(name)
    if name_error:
        return name_error
    if len(description) > _MAX_DESCRIPTION_LENGTH:
        return f'Description exceeds {_MAX_DESCRIPTION_LENGTH} characters.'
    if not body.strip():
        return 'SKILL.md must have markdown content after frontmatter.'
    return None
