from __future__ import annotations

import re

from ..paths import reference_filename, split_reference_ref
from ..schema.preference import (
    PreferenceItem,
    append_preference_item,
    parse_preference_items,
    remove_preference_item,
    validate_preference_index,
)
from ..schema.reference import validate_reference_content

_PREFERENCE_NAME_RE = re.compile(r'^pref\.[A-Za-z0-9][A-Za-z0-9_.-]{0,126}$')
_REFERENCE_SLUG_RE = re.compile(r'^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$')


def validate_preference_name(name: str) -> str:
    normalized = str(name or '').strip()
    if not _PREFERENCE_NAME_RE.fullmatch(normalized):
        raise ValueError(
            "preference name must match 'pref.<slug>' using letters, numbers, '.', '_', or '-'."
        )
    slug = normalized[len('pref.'):].replace('.', '-').replace('_', '-')
    if not _REFERENCE_SLUG_RE.fullmatch(slug):
        raise ValueError(f'preference name {normalized!r} cannot be mapped to a valid reference file.')
    return normalized


def preference_name_to_reference_name(name: str) -> str:
    normalized = validate_preference_name(name)
    return normalized[len('pref.'):].replace('.', '-').replace('_', '-')


def build_preference_reference_content(
    *,
    reference_name: str,
    summary: str,
    scenario: str,
    reason: str,
) -> str:
    scenario_text = str(scenario or '').strip()
    reason_text = str(reason or '').strip()
    summary_text = str(summary or '').strip()
    if not scenario_text:
        raise ValueError('scenario is required.')
    if not reason_text:
        raise ValueError('reason is required.')
    if not summary_text:
        raise ValueError('summary is required.')

    description = summary_text.replace('"', '\\"')
    body = (
        '## Application Scenarios\n'
        f'{scenario_text}\n\n'
        '## Reasons\n'
        f'{reason_text}\n'
    )
    content = (
        '---\n'
        f'name: {reference_name}\n'
        f'description: "{description}"\n'
        'metadata:\n'
        '  node_type: memory\n'
        '  type: user_preference\n'
        '---\n'
        f'{body}'
    )
    error = validate_reference_content(content)
    if error:
        raise ValueError(error)
    return content


def build_add_preference_item(
    *,
    name: str,
    summary: str,
    scenario: str,
    reason: str,
) -> tuple[PreferenceItem, str, str]:
    normalized_name = validate_preference_name(name)
    reference_name = preference_name_to_reference_name(normalized_name)
    reference_content = build_preference_reference_content(
        reference_name=reference_name,
        summary=summary,
        scenario=scenario,
        reason=reason,
    )
    item = PreferenceItem(
        name=normalized_name,
        summary=str(summary).strip(),
        ref=f'references/{reference_name}.md',
    )
    return item, reference_name, reference_content


def add_preference_entry(
    preference_content: str,
    *,
    name: str,
    summary: str,
    scenario: str,
    reason: str,
) -> tuple[str, PreferenceItem, str]:
    item, reference_name, reference_content = build_add_preference_item(
        name=name,
        summary=summary,
        scenario=scenario,
        reason=reason,
    )
    updated = append_preference_item(preference_content, item)
    error = validate_preference_index(updated)
    if error:
        raise ValueError(error)
    return updated, item, reference_content


def delete_preference_entry(preference_content: str, *, name: str) -> tuple[str, PreferenceItem]:
    normalized_name = validate_preference_name(name)
    items = parse_preference_items(preference_content)
    target = next((item for item in items if item.name == normalized_name), None)
    if target is None:
        raise ValueError(f'preference item {normalized_name!r} not found.')
    updated = remove_preference_item(preference_content, normalized_name)
    error = validate_preference_index(updated)
    if error:
        raise ValueError(error)
    return updated, target


def reference_name_from_item(item: PreferenceItem) -> str:
    path, _anchor = split_reference_ref(item.ref)
    filename = reference_filename(path)
    return filename[:-3] if filename.endswith('.md') else filename
