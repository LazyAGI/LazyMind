from __future__ import annotations

import re
from collections import Counter

from lazymind.chat.engine.tools.infra.skill_validation import validate_skill_content
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
    _ensure_unique([item.path for item in skills], 'source skill path')
    _ensure_unique([item.name for item in skills], 'source skill name')
    for skill in skills:
        if not skill.name.strip():
            raise ValueError(f'source skill at {skill.path!r} has empty name')
        if not skill.path.strip():
            raise ValueError(f'source skill {skill.name!r} has empty path')
        if skill.path.endswith('/SKILL.md'):
            raise ValueError(f'source skill path must be package root, got {skill.path!r}')
        if not skill.content.strip():
            raise ValueError(f'source skill {skill.path!r} has empty content')


def validate_plan(plan: SkillOrganizePlan, source_skills: list[SourceSkill]) -> None:
    if not plan.plans:
        raise ValueError('organize plan must contain at least one plan item')
    source_paths = {item.path for item in source_skills}
    source_names = {item.name for item in source_skills}
    covered_paths: set[str] = set()
    path_plan_index: dict[str, int] = {}
    for index, item in enumerate(plan.plans):
        label = f'plans[{index}]'
        if not item.reason.strip():
            raise ValueError(f'{label}.reason is required')
        if not item.source_paths:
            raise ValueError(f'{label}.source_paths is required')
        _ensure_unique(item.source_paths, f'{label}.source_paths')
        unknown_paths = sorted(set(item.source_paths) - source_paths)
        if unknown_paths:
            raise ValueError(f'{label}.source_paths contains unknown paths: {unknown_paths}')
        unknown_names = sorted(set(item.source_names) - source_names)
        if unknown_names:
            raise ValueError(f'{label}.source_names contains unknown names: {unknown_names}')
        for path in item.source_paths:
            if path in path_plan_index:
                raise ValueError(
                    f'source path {path!r} appears in both plans[{path_plan_index[path]}] and {label}; '
                    'each source skill must be handled by exactly one plan item'
                )
            path_plan_index[path] = index
        covered_paths.update(item.source_paths)

        if item.type in {'refactor', 'merge'}:
            if not item.target_name:
                raise ValueError(f'{label}.target_name is required for {item.type}')
            if not _KEBAB_CASE_RE.match(item.target_name):
                raise ValueError(f'{label}.target_name must be kebab-case English')
            if not item.target_path:
                raise ValueError(f'{label}.target_path is required for {item.type}')
            if item.target_path.endswith('/SKILL.md'):
                raise ValueError(f'{label}.target_path must be package root, got {item.target_path!r}')
        if item.type == 'merge' and len(item.source_paths) < 2:
            raise ValueError(f'{label}.source_paths must contain at least two paths for merge')
        if item.type == 'refactor' and item.step_handling_policy not in {'keep_steps', 'minimally_adjust_steps'}:
            raise ValueError(f'{label}.step_handling_policy is invalid for refactor')
        if item.type == 'merge' and item.step_handling_policy != 'merge_and_deduplicate_existing_steps':
            raise ValueError(f'{label}.step_handling_policy must be merge_and_deduplicate_existing_steps for merge')
        if item.type == 'keep' and item.step_handling_policy not in {'keep_steps', 'none'}:
            raise ValueError(f'{label}.step_handling_policy is invalid for keep')
        if item.type == 'delete_duplicate' and len(item.source_paths) != 1:
            raise ValueError(f'{label}.source_paths must contain exactly one path for delete_duplicate')

    missing = sorted(source_paths - covered_paths)
    if missing:
        raise ValueError(f'every input skill path must be covered by plan; missing={missing}')


def validate_fs_draft(draft: SkillFsDraft, source_skills: list[SourceSkill]) -> None:
    source_paths = {item.path for item in source_skills}
    _ensure_unique(draft.delete_paths, 'delete path')
    _ensure_unique([item.path for item in draft.upsert_skills], 'upsert path')
    unknown_delete_paths = sorted(set(draft.delete_paths) - source_paths)
    if unknown_delete_paths:
        raise ValueError(f'delete_paths contains paths outside source skills: {unknown_delete_paths}')
    for item in draft.upsert_skills:
        if not item.path.strip():
            raise ValueError('upsert skill path is required')
        if item.path.endswith('/SKILL.md'):
            raise ValueError(f'upsert skill path must be package root, got {item.path!r}')
        content_error = validate_skill_content(item.content)
        if content_error:
            raise ValueError(f'upsert skill {item.path!r} is invalid: {content_error}')


def _ensure_unique(values: list[str], label: str) -> None:
    counts = Counter(values)
    duplicates = sorted(value for value, count in counts.items() if count > 1)
    if duplicates:
        raise ValueError(f'duplicate {label}: {duplicates}')
