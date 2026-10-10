"""Pending organize approvals stay attached to the plan item that produced them."""

from __future__ import annotations

from typing import Any

from lazymind.review.skill_organize.schemas import SkillFsDraft, SkillOrganizePlan


def build_approval_items(plan: SkillOrganizePlan, partials: list[SkillFsDraft]) -> list[dict[str, Any]]:
    """Build one approval item per non-keep plan entry.

    Associations come from the plan item itself (`type`, `source_keys`,
    `target_source_key`, `target_name`). Partials are aligned by index with
    `plan.plans`; they are not recovered by matching flattened delete and
    upsert lists or by reading `reason`.
    """
    if len(partials) != len(plan.plans):
        raise ValueError('materialized plan items do not match the organize plan')
    items: list[dict[str, Any]] = []
    for index, (item, partial) in enumerate(zip(plan.plans, partials)):
        if item.type == 'keep':
            if partial.delete_keys or partial.upsert_skills:
                raise ValueError(f'plans[{index}] keep item must not change skills')
            continue
        content = ''
        metadata: dict[str, Any] = {}
        if item.type in {'refactor', 'merge'}:
            if len(partial.upsert_skills) != 1:
                raise ValueError(f'plans[{index}] {item.type} must materialize one skill')
            upsert = partial.upsert_skills[0]
            expected_source = item.target_source_key if item.type == 'merge' else item.source_keys[0]
            if upsert.source_key != expected_source:
                raise ValueError(f'plans[{index}] upsert source does not match the plan item')
            content = upsert.content
            metadata = upsert.search_metadata.model_dump(exclude_none=True)
        elif partial.upsert_skills:
            raise ValueError(f'plans[{index}] delete_duplicate must not upsert a skill')
        if item.type == 'merge':
            delete_keys = [key for key in item.source_keys if key != item.target_source_key]
        elif item.type == 'delete_duplicate':
            delete_keys = list(item.source_keys)
        else:
            delete_keys = []
        if sorted(partial.delete_keys) != sorted(delete_keys):
            raise ValueError(f'plans[{index}] deletes do not match the plan item')
        items.append({
            'id': str(index),
            'type': item.type,
            'source_keys': list(item.source_keys),
            'target_source_key': item.target_source_key,
            'target_name': item.target_name,
            'content': content,
            'search_metadata': metadata,
            'delete_keys': delete_keys,
            # Current plans put each source in exactly one item, so there is no
            # cross-item dependency. Record that explicitly instead of inferring
            # one from reason text.
            'depends_on': [],
        })
    return items
