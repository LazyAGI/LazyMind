from __future__ import annotations

import json
from typing import Any

from ..schema.profile import validate_profile_content
from .common import update_frontmatter_document

PROFILE_EDITABLE_FIELDS: frozenset[str] = frozenset({
    'identity.preferred_name',
    'identity.aliases',
    'identity.pronouns',
    'locale.languages',
    'locale.timezone',
    'locale.region',
    'professional.roles',
    'professional.organization',
    'professional.industry',
    'professional.expertise_domains',
    'accessibility.communication_needs',
})

_PROFILE_LIST_FIELDS = {
    'identity.aliases',
    'locale.languages',
    'professional.roles',
    'professional.expertise_domains',
    'accessibility.communication_needs',
}
_PROFILE_OPTIONAL_STRING_FIELDS = {
    'identity.preferred_name',
    'identity.pronouns',
    'locale.timezone',
    'locale.region',
    'professional.organization',
    'professional.industry',
}


def parse_profile_value(field: str, raw_value: str) -> Any:
    normalized_field = str(field or '').strip()
    text = '' if raw_value is None else str(raw_value).strip()
    if normalized_field in _PROFILE_LIST_FIELDS:
        if not text or text == '[]':
            return []
        if text.startswith('['):
            parsed = json.loads(text)
            if not isinstance(parsed, list) or not all(isinstance(item, str) for item in parsed):
                raise ValueError(f'profile field {normalized_field!r} requires a JSON string array.')
            return [str(item).strip() for item in parsed if str(item).strip()]
        return [part.strip() for part in text.split(',') if part.strip()]
    if normalized_field in _PROFILE_OPTIONAL_STRING_FIELDS:
        if not text or text.lower() == 'null':
            return None
        return text
    raise ValueError(f'unsupported profile field {normalized_field!r}.')


def set_profile_field(content: str, field: str, value: str) -> str:
    normalized_field = str(field or '').strip()
    if normalized_field not in PROFILE_EDITABLE_FIELDS:
        raise ValueError(
            f'unsupported profile field {normalized_field!r}; '
            f'expected one of: {", ".join(sorted(PROFILE_EDITABLE_FIELDS))}.'
        )
    parsed_value = parse_profile_value(normalized_field, value)
    updated = update_frontmatter_document(content, normalized_field, parsed_value)
    error = validate_profile_content(updated)
    if error:
        raise ValueError(error)
    return updated
