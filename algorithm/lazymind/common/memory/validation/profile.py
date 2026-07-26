from __future__ import annotations

from typing import Any, Optional

import yaml

from .common import parse_yaml_mapping

CURRENT_PROFILE_SCHEMA_VERSION = 2

_TOP_LEVEL_V1 = {'identity', 'locale', 'professional', 'accessibility'}
_TOP_LEVEL_V2 = {'schema_version', 'identity', 'locale', 'professional'}
_SECTIONS_V1 = {
    'identity': {'preferred_name', 'aliases', 'pronouns'},
    'locale': {'languages', 'timezone', 'region'},
    'professional': {'roles', 'organization', 'industry', 'expertise_domains'},
    'accessibility': {'communication_needs'},
}
_SECTIONS_V2 = {
    'identity': {'preferred_name', 'aliases'},
    'locale': {'languages', 'residence'},
    'professional': {'occupations', 'organizations', 'industries', 'expertise_domains'},
}


def _exact_keys(data: dict[str, Any], expected: set[str], field: str) -> None:
    actual = set(data)
    extra = sorted(str(key) for key in actual - expected)
    missing = sorted(expected - actual)
    if extra:
        raise ValueError(f"{field} has unsupported keys: {', '.join(extra)}.")
    if missing:
        raise ValueError(f"{field} requires: {', '.join(missing)}.")


def _optional_string(value: Any, field: str) -> None:
    if value is not None and not isinstance(value, str):
        raise ValueError(f"Field '{field}' must be a string or null.")


def _string_list(value: Any, field: str) -> list[str]:
    if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
        raise ValueError(f"Field '{field}' must be a list of strings.")
    return list(value)


def _validate_sections(document: dict[str, Any], sections: dict[str, set[str]]) -> None:
    for section, fields in sections.items():
        value = document.get(section)
        if not isinstance(value, dict):
            raise ValueError(f"Field '{section}' must be a mapping.")
        _exact_keys(value, fields, section)


def _dump(document: dict[str, Any]) -> str:
    return yaml.safe_dump(
        document,
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    )


def migrate_profile_v1_to_v2(document: dict[str, Any]) -> dict[str, Any]:
    expected = set(_TOP_LEVEL_V1)
    if 'schema_version' in document:
        expected.add('schema_version')
    _exact_keys(document, expected, 'profile')
    _validate_sections(document, _SECTIONS_V1)
    identity = document['identity']
    locale = document['locale']
    professional = document['professional']
    _optional_string(identity['preferred_name'], 'identity.preferred_name')
    _optional_string(identity['pronouns'], 'identity.pronouns')
    aliases = _string_list(identity['aliases'], 'identity.aliases')
    languages = _string_list(locale['languages'], 'locale.languages')
    _optional_string(locale['timezone'], 'locale.timezone')
    _optional_string(locale['region'], 'locale.region')
    occupations = _string_list(professional['roles'], 'professional.roles')
    _optional_string(professional['organization'], 'professional.organization')
    _optional_string(professional['industry'], 'professional.industry')
    expertise = _string_list(
        professional['expertise_domains'],
        'professional.expertise_domains',
    )
    _string_list(
        document['accessibility']['communication_needs'],
        'accessibility.communication_needs',
    )
    organizations = (
        [professional['organization'].strip()]
        if isinstance(professional['organization'], str)
        and professional['organization'].strip()
        else []
    )
    industries = (
        [professional['industry'].strip()]
        if isinstance(professional['industry'], str)
        and professional['industry'].strip()
        else []
    )
    return {
        'schema_version': 2,
        'identity': {
            'preferred_name': identity['preferred_name'],
            'aliases': aliases,
        },
        'locale': {
            'languages': languages,
            'residence': locale['region'],
        },
        'professional': {
            'occupations': occupations,
            'organizations': organizations,
            'industries': industries,
            'expertise_domains': expertise,
        },
    }


_PROFILE_MIGRATION_CHAIN = {
    1: migrate_profile_v1_to_v2,
}


def _profile_version(document: dict[str, Any]) -> int:
    version = document.get('schema_version', 1)
    if isinstance(version, bool) or not isinstance(version, int) or version < 1:
        raise ValueError('profile schema_version must be a positive integer.')
    return version


def normalize_profile_content(content: str) -> tuple[str, str]:
    document = parse_yaml_mapping(content)
    if not document:
        raise ValueError('profile must be a valid non-empty YAML mapping.')

    version = _profile_version(document)
    while version < CURRENT_PROFILE_SCHEMA_VERSION:
        migrate = _PROFILE_MIGRATION_CHAIN.get(version)
        if migrate is None:
            raise ValueError(f'missing profile migration from schema_version {version}.')
        document = migrate(document)
        version += 1
    if version != CURRENT_PROFILE_SCHEMA_VERSION:
        raise ValueError(f'unsupported profile schema_version {version}.')

    _exact_keys(document, _TOP_LEVEL_V2, 'profile')
    _validate_sections(document, _SECTIONS_V2)
    identity = document['identity']
    locale = document['locale']
    professional = document['professional']
    _optional_string(identity['preferred_name'], 'identity.preferred_name')
    _optional_string(locale['residence'], 'locale.residence')
    _string_list(identity['aliases'], 'identity.aliases')
    _string_list(locale['languages'], 'locale.languages')
    for field in ('occupations', 'organizations', 'industries', 'expertise_domains'):
        _string_list(professional[field], f'professional.{field}')
    visible = {
        'identity': identity,
        'locale': locale,
        'professional': professional,
    }

    stored = {'schema_version': CURRENT_PROFILE_SCHEMA_VERSION, **visible}
    return _dump(stored), _dump(visible)


def validate_profile_content(content: str) -> Optional[str]:
    try:
        normalize_profile_content(content)
    except ValueError as exc:
        return str(exc)
    return None
