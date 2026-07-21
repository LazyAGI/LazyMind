from __future__ import annotations

import pytest

from lazymind.common.skill_storage_key import (
    EXTERNAL_SKILL_CATEGORY,
    INTERNAL_SKILL_CATEGORY,
    SkillStorageCategory,
    parse_skill_key,
    parse_skill_storage_key,
    require_skill_storage_category,
)


def test_skill_storage_key_parses_path_authoritative_identity():
    assert parse_skill_storage_key('internal/example-skill') == (
        INTERNAL_SKILL_CATEGORY,
        'example-skill',
    )
    assert parse_skill_storage_key('external/example-skill') == (
        EXTERNAL_SKILL_CATEGORY,
        'example-skill',
    )
    assert require_skill_storage_category(SkillStorageCategory.INTERNAL) == 'internal'


def test_skill_key_allows_any_safe_category():
    assert parse_skill_key('research3/web-research') == ('research3', 'web-research')


@pytest.mark.parametrize(
    'value',
    ['', 'example-skill', 'research3/nested/example', '../example', 'research3/invalid name'],
)
def test_skill_key_rejects_invalid_keys(value):
    with pytest.raises(ValueError):
        parse_skill_key(value)


@pytest.mark.parametrize(
    'value',
    [
        '',
        'example-skill',
        'writing/example-skill',
        'internal/nested/example-skill',
        'internal/invalid name',
    ],
)
def test_skill_storage_key_rejects_invalid_keys(value):
    with pytest.raises(ValueError):
        parse_skill_storage_key(value)
