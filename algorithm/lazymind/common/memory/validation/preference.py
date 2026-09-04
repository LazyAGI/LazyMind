from __future__ import annotations

from dataclasses import asdict, dataclass
from datetime import datetime
from typing import Optional

import yaml

from ..paths import REFERENCE_ROOT, split_reference_ref
from .common import parse_yaml_mapping, reject_unknown_keys, validate_iso_datetime

_SUMMARY_MAX_CHARS = 100
_ROOT_KEYS = {'preferences'}
_ITEM_KEYS = {'name', 'summary', 'ref', 'created_at', 'updated_at'}


@dataclass(frozen=True)
class PreferenceItem:
    name: str
    summary: str
    ref: str
    created_at: str
    updated_at: str


def parse_preference_items(content: str) -> list[PreferenceItem]:
    document = parse_yaml_mapping(content)
    raw_items = document.get('preferences')
    if not isinstance(raw_items, list):
        return []
    items: list[PreferenceItem] = []
    for raw_item in raw_items:
        if not isinstance(raw_item, dict):
            continue
        if not _ITEM_KEYS.issubset(raw_item):
            continue
        values = {key: raw_item.get(key) for key in _ITEM_KEYS}
        if not all(isinstance(value, str) for value in values.values()):
            continue
        items.append(
            PreferenceItem(
                name=values['name'].strip(),
                summary=values['summary'].strip(),
                ref=values['ref'].strip(),
                created_at=values['created_at'].strip(),
                updated_at=values['updated_at'].strip(),
            )
        )
    return items


def validate_preference_index(content: str) -> Optional[str]:
    if not content or not str(content).strip():
        return 'preference requires a non-empty YAML mapping.'

    document = parse_yaml_mapping(content)
    if not document:
        return 'preference must be a valid non-empty YAML mapping.'
    root_error = reject_unknown_keys(document, _ROOT_KEYS, field='preference')
    if root_error:
        return root_error
    if 'preferences' not in document:
        return "preference requires a 'preferences' list."

    raw_items = document.get('preferences')
    if not isinstance(raw_items, list):
        return "preference 'preferences' must be a list."

    seen_names: set[str] = set()
    seen_refs: set[str] = set()
    for idx, raw_item in enumerate(raw_items):
        field = f'preferences[{idx}]'
        if not isinstance(raw_item, dict):
            return f"Field '{field}' must be a mapping."
        item_error = reject_unknown_keys(raw_item, _ITEM_KEYS, field=field)
        if item_error:
            return item_error
        missing = sorted(_ITEM_KEYS - set(raw_item))
        if missing:
            return f"Field '{field}' requires: {', '.join(missing)}."
        if not all(isinstance(raw_item.get(key), str) for key in _ITEM_KEYS):
            return f"Field '{field}' values must all be strings."

        item = PreferenceItem(
            name=str(raw_item['name']).strip(),
            summary=str(raw_item['summary']).strip(),
            ref=str(raw_item['ref']).strip(),
            created_at=str(raw_item['created_at']).strip(),
            updated_at=str(raw_item['updated_at']).strip(),
        )
        error = validate_preference_item(item)
        if error:
            return error
        if item.name in seen_names:
            return f'duplicate preference item name: {item.name!r}.'
        ref_path, _ = split_reference_ref(item.ref)
        if ref_path in seen_refs:
            return f'duplicate preference reference: {item.ref!r}.'
        seen_names.add(item.name)
        seen_refs.add(ref_path)
    return None


def validate_preference_item(item: PreferenceItem) -> Optional[str]:
    if not item.name:
        return 'preference item name is required.'
    if not item.summary:
        return f'preference item {item.name!r} requires a non-empty summary.'
    if len(item.summary) > _SUMMARY_MAX_CHARS:
        return f'preference item {item.name!r} summary must be {_SUMMARY_MAX_CHARS} characters or less.'
    try:
        path, _anchor = split_reference_ref(item.ref)
    except ValueError as exc:
        return f'preference item {item.name!r} has invalid ref: {exc}'
    if not path.startswith(f'{REFERENCE_ROOT}/'):
        return f'preference item {item.name!r} ref must be under {REFERENCE_ROOT}.'

    for key, value in (
        ('created_at', item.created_at),
        ('updated_at', item.updated_at),
    ):
        error = validate_iso_datetime(value, field=f'{item.name}.{key}')
        if error:
            return error
    created_at = _parse_datetime(item.created_at)
    updated_at = _parse_datetime(item.updated_at)
    if updated_at < created_at:
        return f'preference item {item.name!r} updated_at cannot precede created_at.'
    return None


def render_preference_index(content: str, items: list[PreferenceItem]) -> str:
    del content  # The fixed schema has no extensible root metadata to preserve.
    return yaml.safe_dump(
        {'preferences': [asdict(item) for item in items]},
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    )


def append_preference_item(content: str, item: PreferenceItem) -> str:
    error = validate_preference_item(item)
    if error:
        raise ValueError(error)
    existing = parse_preference_items(content)
    if any(entry.name == item.name for entry in existing):
        raise ValueError(f'preference item {item.name!r} already exists.')
    if any(entry.ref == item.ref for entry in existing):
        raise ValueError(f'preference reference {item.ref!r} already exists.')
    return render_preference_index(content, [*existing, item])


def remove_preference_item(content: str, name: str) -> str:
    normalized_name = str(name or '').strip()
    items = parse_preference_items(content)
    kept = [item for item in items if item.name != normalized_name]
    if len(kept) == len(items):
        raise ValueError(f'preference item {normalized_name!r} not found.')
    return render_preference_index(content, kept)


def _parse_datetime(value: str) -> datetime:
    return datetime.fromisoformat(value.strip().replace('Z', '+00:00'))
