from __future__ import annotations

from ..schema.common import parse_yaml_frontmatter
from ..schema.soul import validate_soul_content
from .common import (
    coerce_value_to_existing_type,
    editable_fields_from_frontmatter,
    get_nested_field,
    update_frontmatter_document,
)


def set_soul_field(content: str, field: str, value: str) -> str:
    normalized_field = str(field or '').strip()
    frontmatter, _body = parse_yaml_frontmatter(content)
    if not frontmatter:
        raise ValueError('soul must contain YAML frontmatter.')

    editable = editable_fields_from_frontmatter(frontmatter)
    if normalized_field not in editable:
        supported = ', '.join(sorted(editable)) or '(none)'
        raise ValueError(
            f'field {normalized_field!r} does not exist in soul; '
            f'editable fields from the loaded document: {supported}.'
        )

    existing = get_nested_field(frontmatter, normalized_field)
    parsed = coerce_value_to_existing_type(existing, value)
    if not isinstance(parsed, str) or not parsed.strip():
        raise ValueError(f'soul field {normalized_field!r} requires a non-empty string value.')

    updated = update_frontmatter_document(content, normalized_field, parsed)
    error = validate_soul_content(updated)
    if error:
        raise ValueError(error)
    return updated
