from __future__ import annotations

import re
from collections import Counter

from lazymind.common.skill_document import require_valid_skill_document
from lazymind.common.skill_storage_key import parse_skill_storage_key
from lazymind.review.skill_organize.config import MAX_SKILL_ORGANIZE_LIMIT
from lazymind.review.skill_organize.schemas import (
    SkillFsDraft,
    SkillOrganizePlan,
    SourceSkill,
)

_KEBAB_CASE_RE = re.compile(r'^[a-z0-9]+(?:-[a-z0-9]+)*$')


def validate_source_skills(skills: list[SourceSkill]) -> None:
    if not skills:
        raise ValueError('at least one source skill is required')
    if len(skills) > MAX_SKILL_ORGANIZE_LIMIT:
        raise ValueError(f'at most {MAX_SKILL_ORGANIZE_LIMIT} skills can be organized at once')
    _ensure_unique([item.key for item in skills], 'source skill key')
    for skill in skills:
        category, name = parse_skill_storage_key(skill.key)
        if (skill.category, skill.name) != (category, name):
            raise ValueError(f'source skill identity does not match key {skill.key!r}')
        if not skill.name.strip():
            raise ValueError('source skill has empty name')
        if not skill.content.strip():
            raise ValueError(f'source skill {skill.name!r} has empty content')


def validate_plan(plan: SkillOrganizePlan, source_skills: list[SourceSkill]) -> None:
    if not plan.plans:
        raise ValueError('organize plan must contain at least one plan item')
    source_keys = {item.key for item in source_skills}
    covered_keys: set[str] = set()
    key_plan_index: dict[str, int] = {}
    for index, item in enumerate(plan.plans):
        label = f'plans[{index}]'
        if not item.reason.strip():
            raise ValueError(f'{label}.reason is required')
        if not item.source_keys:
            raise ValueError(f'{label}.source_keys is required')
        _ensure_unique(item.source_keys, f'{label}.source_keys')
        unknown_keys = sorted(set(item.source_keys) - source_keys)
        if unknown_keys:
            raise ValueError(f'{label}.source_keys contains unknown keys: {unknown_keys}')
        for key in item.source_keys:
            if key in key_plan_index:
                raise ValueError(
                    f'source key {key!r} appears in both plans[{key_plan_index[key]}] and {label}; '
                    'each source skill must be handled by exactly one plan item'
                )
            key_plan_index[key] = index
        covered_keys.update(item.source_keys)

        if item.type == 'merge':
            if item.target_source_key not in item.source_keys:
                raise ValueError(f'{label}.target_source_key must be one of source_keys')
        elif item.target_source_key:
            raise ValueError(f'{label}.target_source_key is only valid for merge')

        if item.type in {'keep', 'refactor'} and len(item.source_keys) != 1:
            raise ValueError(f'{label}.source_keys must contain exactly one key for {item.type}')

        if item.type in {'refactor', 'merge'}:
            if not item.target_name:
                raise ValueError(f'{label}.target_name is required for {item.type}')
            if not _KEBAB_CASE_RE.match(item.target_name):
                raise ValueError(f'{label}.target_name must be kebab-case English')
        if item.type == 'merge' and len(item.source_keys) < 2:
            raise ValueError(f'{label}.source_keys must contain at least two keys for merge')
        if item.type == 'refactor' and item.step_handling_policy not in {'keep_steps', 'minimally_adjust_steps'}:
            raise ValueError(f'{label}.step_handling_policy is invalid for refactor')
        if item.type == 'merge' and item.step_handling_policy != 'merge_and_deduplicate_existing_steps':
            raise ValueError(f'{label}.step_handling_policy must be merge_and_deduplicate_existing_steps for merge')
        if item.type == 'keep' and item.step_handling_policy not in {'keep_steps', 'none'}:
            raise ValueError(f'{label}.step_handling_policy is invalid for keep')
        if item.type == 'delete_duplicate' and len(item.source_keys) != 1:
            raise ValueError(f'{label}.source_keys must contain exactly one key for delete_duplicate')

    missing = sorted(source_keys - covered_keys)
    if missing:
        raise ValueError(f'every input skill key must be covered by plan; missing={missing}')


def validate_fs_draft(draft: SkillFsDraft, source_skills: list[SourceSkill]) -> None:
    source_keys = {item.key for item in source_skills}
    _ensure_unique(draft.delete_keys, 'delete key')
    _ensure_unique([item.source_key for item in draft.upsert_skills], 'upsert source key')
    _ensure_unique([item.target_key for item in draft.upsert_skills], 'upsert target key')
    unknown_delete_keys = sorted(set(draft.delete_keys) - source_keys)
    if unknown_delete_keys:
        raise ValueError(f'delete_keys contains keys outside source skills: {unknown_delete_keys}')
    upsert_source_keys = {item.source_key for item in draft.upsert_skills}
    upsert_target_keys = {item.target_key for item in draft.upsert_skills}
    if conflicts := sorted(set(draft.delete_keys) & upsert_source_keys):
        raise ValueError(f'delete_keys conflicts with upsert source keys: {conflicts}')
    if conflicts := sorted(set(draft.delete_keys) & upsert_target_keys):
        raise ValueError(f'delete_keys conflicts with upsert target keys: {conflicts}')
    for item in draft.upsert_skills:
        if item.source_key not in source_keys:
            raise ValueError(f'upsert source_key is outside source skills: {item.source_key!r}')
        source_category, _ = parse_skill_storage_key(item.source_key)
        target_storage_category, target_name = parse_skill_storage_key(item.target_key)
        if source_category != target_storage_category:
            raise ValueError('upsert source_key and target_key must use the same storage category')
        try:
            require_valid_skill_document(item.content, expected_name=target_name)
        except ValueError as exc:
            raise ValueError(f'upsert skill {item.target_key!r} is invalid: {exc}') from exc


def _ensure_unique(values: list[str], label: str) -> None:
    counts = Counter(values)
    duplicates = sorted(value for value, count in counts.items() if count > 1)
    if duplicates:
        raise ValueError(f'duplicate {label}: {duplicates}')
