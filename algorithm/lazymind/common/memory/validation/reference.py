from __future__ import annotations

import re
from datetime import datetime
from typing import Optional

from .common import (
    optional_str,
    parse_yaml_frontmatter,
    reject_unknown_keys,
    require_mapping,
    validate_iso_datetime,
)

_ROOT_KEYS = {'name', 'summary', 'created_at', 'updated_at', 'source'}
_SOURCE_KEYS = {'kind', 'conversation_id'}
_SOURCE_KINDS = {'memory_review', 'chat_explicit'}
_REQUIRED_SECTIONS = (
    'Application Scenarios',
    'Preference Details',
    'Reason',
)


def validate_reference_content(content: str) -> Optional[str]:
    if not content or not str(content).strip():
        return 'reference requires non-empty content.'

    frontmatter, body = parse_yaml_frontmatter(content)
    if not frontmatter:
        return 'reference must contain YAML frontmatter.'

    root_error = reject_unknown_keys(frontmatter, _ROOT_KEYS, field='reference')
    if root_error:
        return root_error

    for key in ('name', 'summary'):
        if key not in frontmatter:
            return f"reference requires '{key}'."
        err = optional_str(frontmatter.get(key), field=key)
        if err:
            return err
        if not str(frontmatter.get(key) or '').strip():
            return f"reference '{key}' must be a non-empty string."
    if len(str(frontmatter['summary'])) > 100:
        return "reference 'summary' must be 100 characters or less."

    for key in ('created_at', 'updated_at'):
        if key not in frontmatter:
            return f"reference requires '{key}'."
        err = validate_iso_datetime(frontmatter.get(key), field=key)
        if err:
            return err
    created_at = datetime.fromisoformat(
        str(frontmatter['created_at']).strip().replace('Z', '+00:00')
    )
    updated_at = datetime.fromisoformat(
        str(frontmatter['updated_at']).strip().replace('Z', '+00:00')
    )
    if updated_at < created_at:
        return "reference 'updated_at' cannot precede 'created_at'."

    source = frontmatter.get('source')
    if source is None:
        return "reference requires 'source'."
    err = require_mapping(source, field='source')
    if err:
        return err
    assert isinstance(source, dict)
    err = reject_unknown_keys(source, _SOURCE_KEYS, field='source')
    if err:
        return err
    missing = sorted(_SOURCE_KEYS - set(source))
    if missing:
        return f"reference source requires: {', '.join(missing)}."
    for key in _SOURCE_KEYS:
        err = optional_str(source.get(key), field=f'source.{key}')
        if err:
            return err
        if not str(source.get(key) or '').strip():
            return f"reference 'source.{key}' must be a non-empty string."
    if source.get('kind') not in _SOURCE_KINDS:
        return (
            "reference 'source.kind' must be either "
            "'memory_review' or 'chat_explicit'."
        )

    for section in _REQUIRED_SECTIONS:
        match = re.search(
            rf'(?ms)^## {re.escape(section)}\s*\n(?P<body>.*?)(?=^## |\Z)',
            body or '',
        )
        if not match or not match.group('body').strip():
            return f"reference requires a non-empty '## {section}' section."
    return None
