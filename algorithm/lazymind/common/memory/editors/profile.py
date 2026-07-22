from __future__ import annotations

from ..schema.common import parse_yaml_frontmatter
from ..schema.profile import validate_profile_content
from .common import (
    coerce_value_to_existing_type,
    editable_fields_from_frontmatter,
    get_nested_field,
    update_frontmatter_document,
)


def set_profile_field(content: str, field: str, value: str) -> str:
    normalized_field = str(field or '').strip()
    frontmatter, _body = parse_yaml_frontmatter(content)
    if not frontmatter:
        raise ValueError('profile must contain YAML frontmatter.')

    editable = editable_fields_from_frontmatter(frontmatter)
    if normalized_field not in editable:
        supported = ', '.join(sorted(editable)) or '(none)'
        raise ValueError(
            f'field {normalized_field!r} does not exist in profile; '
            f'editable fields from the loaded document: {supported}.'
        )

    existing = get_nested_field(frontmatter, normalized_field)
    parsed_value = coerce_value_to_existing_type(existing, value)
    updated = update_frontmatter_document(content, normalized_field, parsed_value)
    error = validate_profile_content(updated)
    if error:
        raise ValueError(error)
    return updated
