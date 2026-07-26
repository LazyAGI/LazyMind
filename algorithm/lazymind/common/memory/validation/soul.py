from __future__ import annotations

from typing import Any, Optional

import yaml

from .common import parse_yaml_mapping

CURRENT_SOUL_SCHEMA_VERSION = 2

_TOP_LEVEL_V1 = {'identity', 'mission', 'interaction', 'epistemic'}
_TOP_LEVEL_V2 = {'schema_version', *_TOP_LEVEL_V1}
_SECTIONS_V1 = {
    'identity': {'name', 'role', 'description'},
    'mission': {'primary_goal', 'success_definition'},
    'interaction': {
        'relationship_mode',
        'default_tone',
        'initiative_level',
        'challenge_level',
        'decision_mode',
    },
    'epistemic': {'uncertainty_style', 'verification_mode'},
}
_SECTIONS_V2 = {
    **_SECTIONS_V1,
    'interaction': {
        'default_relationship_mode',
        'default_tone',
        'default_initiative_level',
        'default_challenge_level',
        'default_decision_mode',
    },
}


def _exact_keys(data: dict[str, Any], expected: set[str], field: str) -> None:
    actual = set(data)
    extra = sorted(str(key) for key in actual - expected)
    missing = sorted(expected - actual)
    if extra:
        raise ValueError(f"{field} has unsupported keys: {', '.join(extra)}.")
    if missing:
        raise ValueError(f"{field} requires: {', '.join(missing)}.")


def _validate_sections(document: dict[str, Any], sections: dict[str, set[str]]) -> None:
    for section, fields in sections.items():
        value = document.get(section)
        if not isinstance(value, dict):
            raise ValueError(f"Field '{section}' must be a mapping.")
        _exact_keys(value, fields, section)
        for name in fields:
            item = value.get(name)
            if not isinstance(item, str) or not item.strip():
                raise ValueError(f"Soul field '{section}.{name}' must be a non-empty string.")


def _dump(document: dict[str, Any]) -> str:
    return yaml.safe_dump(
        document,
        allow_unicode=True,
        sort_keys=False,
        default_flow_style=False,
    )


def migrate_soul_v1_to_v2(document: dict[str, Any]) -> dict[str, Any]:
    expected = set(_TOP_LEVEL_V1)
    if 'schema_version' in document:
        expected.add('schema_version')
    _exact_keys(document, expected, 'soul')
    _validate_sections(document, _SECTIONS_V1)
    interaction = document['interaction']
    return {
        'schema_version': 2,
        'identity': document['identity'],
        'mission': document['mission'],
        'interaction': {
            'default_relationship_mode': interaction['relationship_mode'],
            'default_tone': interaction['default_tone'],
            'default_initiative_level': interaction['initiative_level'],
            'default_challenge_level': interaction['challenge_level'],
            'default_decision_mode': interaction['decision_mode'],
        },
        'epistemic': document['epistemic'],
    }


_SOUL_MIGRATION_CHAIN = {
    1: migrate_soul_v1_to_v2,
}


def _soul_version(document: dict[str, Any]) -> int:
    version = document.get('schema_version', 1)
    if isinstance(version, bool) or not isinstance(version, int) or version < 1:
        raise ValueError('soul schema_version must be a positive integer.')
    return version


def normalize_soul_content(content: str) -> tuple[str, str]:
    document = parse_yaml_mapping(content)
    if not document:
        raise ValueError('soul must be a valid non-empty YAML mapping.')

    version = _soul_version(document)
    while version < CURRENT_SOUL_SCHEMA_VERSION:
        migrate = _SOUL_MIGRATION_CHAIN.get(version)
        if migrate is None:
            raise ValueError(f'missing soul migration from schema_version {version}.')
        document = migrate(document)
        version += 1
    if version != CURRENT_SOUL_SCHEMA_VERSION:
        raise ValueError(f'unsupported soul schema_version {version}.')

    _exact_keys(document, _TOP_LEVEL_V2, 'soul')
    _validate_sections(document, _SECTIONS_V2)
    visible = {
        'identity': document['identity'],
        'mission': document['mission'],
        'interaction': document['interaction'],
        'epistemic': document['epistemic'],
    }

    stored = {'schema_version': CURRENT_SOUL_SCHEMA_VERSION, **visible}
    return _dump(stored), _dump(visible)


def validate_soul_content(content: str) -> Optional[str]:
    try:
        normalize_soul_content(content)
    except ValueError as exc:
        return str(exc)
    return None
