from __future__ import annotations

from typing import Optional

from .common import optional_str, parse_yaml_frontmatter, reject_unknown_keys, require_mapping

_ROOT_KEYS = {'name', 'description', 'metadata'}
_METADATA_KEYS = {'node_type', 'type', 'originSessionId'}


def validate_reference_content(content: str) -> Optional[str]:
    if not content or not str(content).strip():
        return 'reference requires non-empty content.'

    frontmatter, body = parse_yaml_frontmatter(content)
    if not frontmatter:
        return 'reference must contain YAML frontmatter.'

    root_error = reject_unknown_keys(frontmatter, _ROOT_KEYS, field='reference')
    if root_error:
        return root_error

    for key in ('name', 'description'):
        if key not in frontmatter:
            return f"reference requires '{key}'."
        err = optional_str(frontmatter.get(key), field=key)
        if err:
            return err
        if not str(frontmatter.get(key) or '').strip():
            return f"reference '{key}' must be a non-empty string."

    metadata = frontmatter.get('metadata')
    if metadata is not None:
        err = require_mapping(metadata, field='metadata')
        if err:
            return err
        assert isinstance(metadata, dict)
        err = reject_unknown_keys(metadata, _METADATA_KEYS, field='metadata')
        if err:
            return err
        for key in ('node_type', 'type', 'originSessionId'):
            if key not in metadata:
                continue
            err = optional_str(metadata.get(key), field=f'metadata.{key}')
            if err:
                return err

    if not (body or '').strip():
        return 'reference requires a non-empty Markdown body.'
    return None
