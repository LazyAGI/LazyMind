from __future__ import annotations

from typing import Any, Dict, Optional

import yaml  # type: ignore

from .skill_validation import (
    normalize_skill_category,
    parse_skill_frontmatter,
    validate_skill_name,
)


def skill_identity_from_content(content: str) -> tuple[str, str]:
    frontmatter, _ = parse_skill_frontmatter(content)
    category = str(frontmatter.get('category') or '').strip()
    name = str(frontmatter.get('name') or '').strip()
    return category, name


def resolve_skill_editor_identity(
    name: str,
    category: Optional[str],
    action: str,
) -> Dict[str, Any]:
    raw_name = str(name or '').strip()
    raw_category = str(category or '').strip()
    if '/' in raw_name:
        parts = [part for part in raw_name.split('/') if part]
        if len(parts) != 2 or raw_name.startswith('/') or raw_name.endswith('/') or '//' in raw_name:
            return {'error': f'Skill key {raw_name!r} is invalid; expected category/name.'}
        key_category, raw_name = parts
        normalized_key_category = normalize_skill_category(key_category)
        if not normalized_key_category:
            return {
                'error': (
                    f'Category {key_category!r} is invalid; it must be a single '
                    "ASCII-safe path segment (only letters, digits, '-', '_' "
                    "and '.'; no spaces, no Chinese, no '/')."
                )
            }
        if raw_category:
            normalized_category = normalize_skill_category(raw_category)
            if not normalized_category:
                return {
                    'error': (
                        f'Category {raw_category!r} is invalid; it must be a single '
                        "ASCII-safe path segment (only letters, digits, '-', '_' "
                        "and '.'; no spaces, no Chinese, no '/')."
                    )
                }
            if normalized_category != normalized_key_category:
                return {
                    'error': (
                        f'Skill key {name!r} conflicts with category {category!r}; '
                        'they must refer to the same category.'
                    )
                }
            raw_category = normalized_category
        else:
            raw_category = normalized_key_category

    if raw_category:
        name_error = validate_skill_name(raw_name)
        if name_error:
            return {'error': name_error}
        normalized_category = normalize_skill_category(raw_category)
        if not normalized_category:
            return {
                'error': (
                    f'Category {raw_category!r} is invalid; it must be a single '
                    "ASCII-safe path segment (only letters, digits, '-', '_' "
                    "and '.'; no spaces, no Chinese, no '/')."
                )
            }
        return {'category': normalized_category, 'name': raw_name}

    name_error = validate_skill_name(raw_name)
    if name_error:
        return {'error': name_error}
    return {'error': f"action='{action}' requires category, or name formatted as category/name."}


def rewrite_skill_identity(content: str, category: str, name: str) -> str:
    frontmatter, body = parse_skill_frontmatter(content)
    if not frontmatter:
        raise ValueError('SKILL.md must contain YAML frontmatter.')
    frontmatter = dict(frontmatter)
    frontmatter['category'] = category
    frontmatter['name'] = name

    yaml_text = yaml.safe_dump(frontmatter, allow_unicode=True, sort_keys=False).strip()
    return f'---\n{yaml_text}\n---\n{body}'
