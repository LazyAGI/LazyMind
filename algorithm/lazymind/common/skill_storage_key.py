from __future__ import annotations

from enum import StrEnum

from lazymind.common.skill_document import require_skill_name


class SkillStorageCategory(StrEnum):
    INTERNAL = 'internal'
    EXTERNAL = 'external'


INTERNAL_SKILL_CATEGORY = SkillStorageCategory.INTERNAL.value
EXTERNAL_SKILL_CATEGORY = SkillStorageCategory.EXTERNAL.value
SKILL_STORAGE_CATEGORIES = frozenset(category.value for category in SkillStorageCategory)


def require_skill_storage_category(category: str) -> str:
    normalized = str(category or '').strip()
    try:
        return SkillStorageCategory(normalized).value
    except ValueError:
        allowed = ' or '.join(repr(item.value) for item in SkillStorageCategory)
        raise ValueError(f'Skill storage category must be {allowed}.') from None


def parse_skill_key(value: str) -> tuple[str, str]:
    raw = str(value or '').strip()
    parts = raw.split('/')
    if len(parts) != 2 or not all(parts):
        raise ValueError(f"Skill key {raw!r} must be in 'category/name' form.")
    try:
        category = require_skill_name(parts[0])
    except ValueError as exc:
        raise ValueError(f'Skill key {raw!r} has invalid category: {exc}') from exc
    try:
        name = require_skill_name(parts[1])
    except ValueError as exc:
        raise ValueError(f'Skill key {raw!r} has invalid name: {exc}') from exc
    return category, name


def parse_skill_storage_key(value: str) -> tuple[str, str]:
    category, name = parse_skill_key(value)
    return require_skill_storage_category(category), name
