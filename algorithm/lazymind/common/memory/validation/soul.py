from __future__ import annotations

from typing import Any, Optional

from .common import (
    optional_str,
    parse_yaml_mapping,
    reject_unknown_keys,
    require_mapping,
)

_ROOT_KEYS = {'identity', 'mission', 'interaction', 'epistemic'}
_IDENTITY_KEYS = {'name', 'role', 'description'}
_MISSION_KEYS = {'primary_goal', 'success_definition'}
_INTERACTION_KEYS = {
    'relationship_mode',
    'default_tone',
    'initiative_level',
    'challenge_level',
    'decision_mode',
}
_EPISTEMIC_KEYS = {'uncertainty_style', 'verification_mode'}


def validate_soul_content(content: str) -> Optional[str]:
    if not content or not str(content).strip():
        return 'soul requires a non-empty YAML mapping.'

    document = parse_yaml_mapping(content)
    if not document:
        return 'soul must be a valid non-empty YAML mapping.'

    root_error = reject_unknown_keys(document, _ROOT_KEYS, field='soul')
    if root_error:
        return root_error

    for section, allowed, required_strings in (
        ('identity', _IDENTITY_KEYS, ('name', 'role', 'description')),
        ('mission', _MISSION_KEYS, ('primary_goal', 'success_definition')),
        (
            'interaction',
            _INTERACTION_KEYS,
            (
                'relationship_mode',
                'default_tone',
                'initiative_level',
                'challenge_level',
                'decision_mode',
            ),
        ),
        ('epistemic', _EPISTEMIC_KEYS, ('uncertainty_style', 'verification_mode')),
    ):
        error = _validate_required_string_section(
            document.get(section),
            section=section,
            allowed=allowed,
            required_strings=required_strings,
        )
        if error:
            return error
    return None


def _validate_required_string_section(
    value: Any,
    *,
    section: str,
    allowed: set[str],
    required_strings: tuple[str, ...],
) -> Optional[str]:
    if value is None:
        return f"soul requires '{section}'."
    mapping_error = require_mapping(value, field=section)
    if mapping_error:
        return mapping_error
    assert isinstance(value, dict)
    unknown = reject_unknown_keys(value, allowed, field=section)
    if unknown:
        return unknown
    for key in required_strings:
        if key not in value:
            return f"soul '{section}' requires '{key}'."
        str_error = optional_str(value.get(key), field=f'{section}.{key}')
        if str_error:
            return str_error
        if not str(value.get(key) or '').strip():
            return f"soul '{section}.{key}' must be a non-empty string."
    return None
