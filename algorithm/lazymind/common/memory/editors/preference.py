from __future__ import annotations

import re
from datetime import datetime, timezone
from typing import Any

import yaml

from ..paths import reference_filename, split_reference_ref
from ..validation.preference import (
    PreferenceItem,
    append_preference_item,
    parse_preference_items,
    remove_preference_item,
    validate_preference_index,
)
from ..validation.reference import validate_reference_content

_PREFERENCE_NAME_RE = re.compile(r'^pref\.[A-Za-z0-9][A-Za-z0-9_.-]{0,126}$')
_REFERENCE_SLUG_RE = re.compile(r'^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$')


def validate_preference_name(name: str) -> str:
    normalized = str(name or '').strip()
    if not _PREFERENCE_NAME_RE.fullmatch(normalized):
        raise ValueError(
            "preference name must match 'pref.<slug>' using letters, numbers, '.', '_', or '-'.",
        )
    slug = normalized[len('pref.'):].replace('.', '-').replace('_', '-')
    if not _REFERENCE_SLUG_RE.fullmatch(slug):
        raise ValueError(
            f'preference name {normalized!r} cannot be mapped to a valid reference file.',
        )
    return normalized


def preference_name_to_reference_name(name: str) -> str:
    normalized = validate_preference_name(name)
    return normalized[len('pref.'):].replace('.', '-').replace('_', '-')


def build_preference_reference_content(
    *,
    preference_name: str,
    summary: str,
    scenario: str,
    details: str,
    reason: str,
    created_at: str,
    updated_at: str,
    source_kind: str,
    conversation_id: str,
) -> str:
    scenario_text = str(scenario or '').strip()
    details_text = str(details or '').strip()
    reason_text = str(reason or '').strip()
    summary_text = str(summary or '').strip()
    if not scenario_text:
        raise ValueError('scenario is required.')
    if not details_text:
        raise ValueError('details is required.')
    if not reason_text:
        raise ValueError('reason is required.')
    if not summary_text:
        raise ValueError('summary is required.')
    if len(summary_text) > 100:
        raise ValueError('summary must be 100 characters or less.')

    body = (
        '## Application Scenarios\n'
        f'{scenario_text}\n\n'
        '## Preference Details\n'
        f'{details_text}\n\n'
        '## Reason\n'
        f'{reason_text}\n'
    )
    frontmatter = yaml.safe_dump(
        {
            'name': preference_name,
            'summary': summary_text,
            'created_at': created_at,
            'updated_at': updated_at,
            'source': {
                'kind': source_kind,
                'conversation_id': conversation_id,
            },
        },
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    )
    content = f'---\n{frontmatter}---\n{body}'
    error = validate_reference_content(content)
    if error:
        raise ValueError(error)
    return content


def build_add_preference_item(
    *,
    name: str,
    summary: str,
    scenario: str,
    details: str,
    reason: str,
    source_kind: str,
    conversation_id: str,
    timestamp: str | None = None,
) -> dict[str, Any]:
    normalized_name = validate_preference_name(name)
    reference_name = normalized_name[len('pref.'):].replace('.', '-').replace('_', '-')
    created_at = str(timestamp or _utc_now()).strip()
    reference_content = build_preference_reference_content(
        preference_name=normalized_name,
        summary=summary,
        scenario=scenario,
        details=details,
        reason=reason,
        created_at=created_at,
        updated_at=created_at,
        source_kind=source_kind,
        conversation_id=conversation_id,
    )
    item = PreferenceItem(
        name=normalized_name,
        summary=str(summary).strip(),
        ref=f'references/{reference_name}.md',
        created_at=created_at,
        updated_at=created_at,
    )
    return {
        'item': item,
        'reference_name': reference_name,
        'reference_content': reference_content,
    }


def add_preference_entry(
    preference_content: str,
    *,
    name: str,
    summary: str,
    scenario: str,
    details: str,
    reason: str,
    source_kind: str,
    conversation_id: str,
    timestamp: str | None = None,
) -> dict[str, Any]:
    built = build_add_preference_item(
        name=name,
        summary=summary,
        scenario=scenario,
        details=details,
        reason=reason,
        source_kind=source_kind,
        conversation_id=conversation_id,
        timestamp=timestamp,
    )
    item = built['item']
    updated = append_preference_item(preference_content, item)
    error = validate_preference_index(updated)
    if error:
        raise ValueError(error)
    return {
        'content': updated,
        'item': item,
        'reference_content': built['reference_content'],
        'reference_name': built['reference_name'],
    }


def delete_preference_entry(preference_content: str, *, name: str) -> dict[str, Any]:
    normalized_name = validate_preference_name(name)
    items = parse_preference_items(preference_content)
    target = next((item for item in items if item.name == normalized_name), None)
    if target is None:
        raise FileNotFoundError(f'preference item {normalized_name!r} not found.')
    updated = remove_preference_item(preference_content, normalized_name)
    error = validate_preference_index(updated)
    if error:
        raise ValueError(error)
    return {'content': updated, 'item': target}


def reference_name_from_item(item: PreferenceItem) -> str:
    path, _anchor = split_reference_ref(item.ref)
    filename = reference_filename(path)
    return filename[:-3] if filename.endswith('.md') else filename


def _utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec='seconds')
