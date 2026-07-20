from __future__ import annotations

import re
from dataclasses import dataclass
from datetime import date
from typing import Optional

from ..paths import REFERENCE_ROOT, normalize_memory_path, split_reference_ref
from .common import parse_yaml_frontmatter, reject_unknown_keys

_SUMMARY_MAX_CHARS = 100
_ITEM_BLOCK_RE = re.compile(
    r'(?m)^- name:\s*(?P<name>\S+)\s*\n'
    r'[ \t]+summary:\s*(?P<summary>.+?)\s*\n'
    r'[ \t]+ref:\s*(?P<ref>\S+)\s*$'
)
_ROOT_KEYS = {'schema_version', 'updated_at'}


@dataclass(frozen=True)
class PreferenceItem:
    name: str
    summary: str
    ref: str


def parse_preference_items(content: str) -> list[PreferenceItem]:
    items: list[PreferenceItem] = []
    for match in _ITEM_BLOCK_RE.finditer(content or ''):
        items.append(
            PreferenceItem(
                name=match.group('name').strip(),
                summary=match.group('summary').strip(),
                ref=match.group('ref').strip(),
            )
        )
    return items


def validate_preference_index(content: str) -> Optional[str]:
    if not content or not str(content).strip():
        return 'preference requires non-empty content.'

    frontmatter, body = parse_yaml_frontmatter(content)
    if not frontmatter:
        return 'preference must contain YAML frontmatter.'

    root_error = reject_unknown_keys(frontmatter, _ROOT_KEYS, field='preference')
    if root_error:
        return root_error
    if frontmatter.get('schema_version') != 1:
        return "preference 'schema_version' must be 1."
    updated_at = frontmatter.get('updated_at')
    if updated_at is not None and not isinstance(updated_at, (str, date)):
        return "preference 'updated_at' must be a date string."

    items = parse_preference_items(content)
    # Detect malformed list markers that look like preference items but failed parse.
    list_markers = re.findall(r'(?m)^- name:\s*.+$', body or '')
    if len(list_markers) != len(items):
        return (
            'preference index items must use the form:\n'
            '- name: pref.xxx\n'
            '  summary: ...\n'
            '  ref: references/xxx.md#anchor'
        )

    seen_names: set[str] = set()
    for item in items:
        error = validate_preference_item(item)
        if error:
            return error
        if item.name in seen_names:
            return f'duplicate preference item name: {item.name!r}.'
        seen_names.add(item.name)
    return None


def validate_preference_item(item: PreferenceItem) -> Optional[str]:
    if not item.name:
        return 'preference item name is required.'
    if not item.summary:
        return f'preference item {item.name!r} requires a non-empty summary.'
    if len(item.summary) > _SUMMARY_MAX_CHARS:
        return (
            f'preference item {item.name!r} summary must be '
            f'{_SUMMARY_MAX_CHARS} characters or less.'
        )
    try:
        path, _anchor = split_reference_ref(item.ref)
    except ValueError as exc:
        return f'preference item {item.name!r} has invalid ref: {exc}'
    # Prefer relative refs in the index for readability.
    relative = path
    if relative.startswith(f'{REFERENCE_ROOT}/'):
        relative = f'references/{relative[len(REFERENCE_ROOT) + 1:]}'
    _ = relative
    return None


def render_preference_item(item: PreferenceItem) -> str:
    return (
        f'- name: {item.name}\n'
        f'  summary: {item.summary}\n'
        f'  ref: {item.ref}\n'
    )


def append_preference_item(content: str, item: PreferenceItem) -> str:
    error = validate_preference_item(item)
    if error:
        raise ValueError(error)
    existing = parse_preference_items(content)
    if any(entry.name == item.name for entry in existing):
        raise ValueError(f'preference item {item.name!r} already exists.')

    base = content if content.endswith('\n') else f'{content}\n'
    if not base.rstrip().endswith('# Preference Index'):
        # Keep caller content; only ensure trailing newline before append.
        pass
    return f'{base}{render_preference_item(item)}'


def preference_ref_path(ref: str) -> str:
    path, _ = split_reference_ref(ref)
    return normalize_memory_path(path)
