from __future__ import annotations

from typing import Any

from lazymind.chat.engine.tools.infra.core_api_client import post_core_api
from lazymind.review.skill_organize.schemas import SearchMetadata


def load_search_metadata(skill_keys: list[str]) -> dict[str, dict[str, Any]]:
    result = post_core_api('/internal/skills:metadata', {'skill_keys': skill_keys})
    body = result.get('response') or {}
    data = body.get('data') or {}
    metadata = {}
    for row in data.get('skills') or []:
        key = row.get('skill_key')
        if key in skill_keys:
            metadata[key] = SearchMetadata.model_validate({
                field: row[field] for field in ('field', 'tags', 'aliases', 'keywords') if field in row
            }).model_dump(exclude_none=True)
    if set(metadata) != set(skill_keys):
        raise ValueError('organizer could not read search metadata for all selected skills')
    return metadata


def update_search_metadata(updates: list[dict[str, Any]]) -> None:
    post_core_api('/internal/skills:metadata:update', {'updates': updates})
