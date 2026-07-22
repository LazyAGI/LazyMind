from __future__ import annotations

import re
from typing import Any

from ..paths import reference_filename, split_reference_ref
from ..result import memory_err, memory_ok
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


def validate_preference_name(name: str) -> dict[str, Any]:
    normalized = str(name or '').strip()
    if not _PREFERENCE_NAME_RE.fullmatch(normalized):
        return memory_err(
            "preference name must match 'pref.<slug>' using letters, numbers, '.', '_', or '-'.",
            type='validation',
        )
    slug = normalized[len('pref.'):].replace('.', '-').replace('_', '-')
    if not _REFERENCE_SLUG_RE.fullmatch(slug):
        return memory_err(
            f'preference name {normalized!r} cannot be mapped to a valid reference file.',
            type='validation',
        )
    return memory_ok(name=normalized)


def preference_name_to_reference_name(name: str) -> str:
    result = validate_preference_name(name)
    if not result.get('ok'):
        raise ValueError(result.get('error') or 'invalid preference name')
    normalized = str(result['name'])
    return normalized[len('pref.'):].replace('.', '-').replace('_', '-')


def build_preference_reference_content(
    *,
    reference_name: str,
    summary: str,
    scenario: str,
    reason: str,
) -> dict[str, Any]:
    scenario_text = str(scenario or '').strip()
    reason_text = str(reason or '').strip()
    summary_text = str(summary or '').strip()
    if not scenario_text:
        return memory_err('scenario is required.', type='validation')
    if not reason_text:
        return memory_err('reason is required.', type='validation')
    if not summary_text:
        return memory_err('summary is required.', type='validation')

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
        return memory_err(error, type='validation')
    return memory_ok(content=content)


def build_add_preference_item(
    *,
    name: str,
    summary: str,
    scenario: str,
    reason: str,
) -> dict[str, Any]:
    name_result = validate_preference_name(name)
    if not name_result.get('ok'):
        return name_result
    normalized_name = str(name_result['name'])
    reference_name = normalized_name[len('pref.'):].replace('.', '-').replace('_', '-')
    reference_result = build_preference_reference_content(
        reference_name=reference_name,
        summary=summary,
        scenario=scenario,
        reason=reason,
    )
    if not reference_result.get('ok'):
        return reference_result
    item = PreferenceItem(
        name=normalized_name,
        summary=str(summary).strip(),
        ref=f'references/{reference_name}.md',
    )
    return memory_ok(
        item=item,
        reference_name=reference_name,
        reference_content=reference_result['content'],
    )


def add_preference_entry(
    preference_content: str,
    *,
    name: str,
    summary: str,
    scenario: str,
    reason: str,
) -> dict[str, Any]:
    built = build_add_preference_item(
        name=name,
        summary=summary,
        scenario=scenario,
        reason=reason,
    )
    if not built.get('ok'):
        return built
    item = built['item']
    try:
        updated = append_preference_item(preference_content, item)
    except ValueError as exc:
        return memory_err(str(exc), type='validation')
    error = validate_preference_index(updated)
    if error:
        return memory_err(error, type='validation')
    return memory_ok(
        content=updated,
        item=item,
        reference_content=built['reference_content'],
        reference_name=built['reference_name'],
    )


def delete_preference_entry(preference_content: str, *, name: str) -> dict[str, Any]:
    name_result = validate_preference_name(name)
    if not name_result.get('ok'):
        return name_result
    normalized_name = str(name_result['name'])
    items = parse_preference_items(preference_content)
    target = next((item for item in items if item.name == normalized_name), None)
    if target is None:
        return memory_err(f'preference item {normalized_name!r} not found.', type='not_found')
    try:
        updated = remove_preference_item(preference_content, normalized_name)
    except ValueError as exc:
        return memory_err(str(exc), type='validation')
    error = validate_preference_index(updated)
    if error:
        return memory_err(error, type='validation')
    return memory_ok(content=updated, item=target)


def reference_name_from_item(item: PreferenceItem) -> str:
    path, _anchor = split_reference_ref(item.ref)
    filename = reference_filename(path)
    return filename[:-3] if filename.endswith('.md') else filename
