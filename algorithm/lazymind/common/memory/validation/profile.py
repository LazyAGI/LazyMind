from __future__ import annotations

from typing import Optional

from .common import (
    optional_str,
    parse_yaml_mapping,
    reject_unknown_keys,
    require_mapping,
    str_list,
)

_ROOT_KEYS = {'identity', 'locale', 'professional', 'accessibility'}
_IDENTITY_KEYS = {'preferred_name', 'aliases', 'pronouns'}
_LOCALE_KEYS = {'languages', 'timezone', 'region'}
_PROFESSIONAL_KEYS = {'roles', 'organization', 'industry', 'expertise_domains'}
_ACCESSIBILITY_KEYS = {'communication_needs'}


def validate_profile_content(content: str) -> Optional[str]:
    if not content or not str(content).strip():
        return 'profile requires a non-empty YAML mapping.'

    document = parse_yaml_mapping(content)
    if not document:
        return 'profile must be a valid non-empty YAML mapping.'

    root_error = reject_unknown_keys(document, _ROOT_KEYS, field='profile')
    if root_error:
        return root_error
    missing_sections = sorted(_ROOT_KEYS - set(document))
    if missing_sections:
        return f"profile requires sections: {', '.join(missing_sections)}."

    identity = document.get('identity')
    err = _validate_section(identity, section='identity', allowed=_IDENTITY_KEYS)
    if err:
        return err
    assert isinstance(identity, dict)
    for key in ('preferred_name', 'pronouns'):
        err = optional_str(identity.get(key), field=f'identity.{key}')
        if err:
            return err
    err = str_list(identity.get('aliases'), field='identity.aliases')
    if err:
        return err

    locale = document.get('locale')
    err = _validate_section(locale, section='locale', allowed=_LOCALE_KEYS)
    if err:
        return err
    assert isinstance(locale, dict)
    err = str_list(locale.get('languages'), field='locale.languages')
    if err:
        return err
    for key in ('timezone', 'region'):
        err = optional_str(locale.get(key), field=f'locale.{key}')
        if err:
            return err

    professional = document.get('professional')
    err = _validate_section(
        professional,
        section='professional',
        allowed=_PROFESSIONAL_KEYS,
    )
    if err:
        return err
    assert isinstance(professional, dict)
    for key in ('roles', 'expertise_domains'):
        err = str_list(professional.get(key), field=f'professional.{key}')
        if err:
            return err
    for key in ('organization', 'industry'):
        err = optional_str(professional.get(key), field=f'professional.{key}')
        if err:
            return err

    accessibility = document.get('accessibility')
    err = _validate_section(
        accessibility,
        section='accessibility',
        allowed=_ACCESSIBILITY_KEYS,
    )
    if err:
        return err
    assert isinstance(accessibility, dict)
    err = str_list(
        accessibility.get('communication_needs'),
        field='accessibility.communication_needs',
    )
    if err:
        return err
    return None


def _validate_section(
    value,
    *,
    section: str,
    allowed: set[str],
) -> Optional[str]:
    err = require_mapping(value, field=section)
    if err:
        return err
    if not isinstance(value, dict):
        return f"profile requires '{section}'."
    err = reject_unknown_keys(value, allowed, field=section)
    if err:
        return err
    missing = sorted(allowed - set(value))
    if missing:
        return f"profile section '{section}' requires fields: {', '.join(missing)}."
    return None
