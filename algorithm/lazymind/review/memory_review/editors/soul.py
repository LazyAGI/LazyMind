from __future__ import annotations

from ..schema.soul import validate_soul_content
from .common import update_frontmatter_document

SOUL_EDITABLE_FIELDS: frozenset[str] = frozenset({
    'identity.name',
    'identity.role',
    'identity.description',
    'mission.primary_goal',
    'mission.success_definition',
    'interaction.relationship_mode',
    'interaction.default_tone',
    'interaction.initiative_level',
    'interaction.challenge_level',
    'interaction.decision_mode',
    'epistemic.uncertainty_style',
    'epistemic.verification_mode',
})


def set_soul_field(content: str, field: str, value: str) -> str:
    normalized_field = str(field or '').strip()
    if normalized_field not in SOUL_EDITABLE_FIELDS:
        raise ValueError(
            f'unsupported soul field {normalized_field!r}; '
            f'expected one of: {", ".join(sorted(SOUL_EDITABLE_FIELDS))}.'
        )
    text = str(value if value is not None else '').strip()
    if not text:
        raise ValueError(f'soul field {normalized_field!r} requires a non-empty value.')
    updated = update_frontmatter_document(content, normalized_field, text)
    error = validate_soul_content(updated)
    if error:
        raise ValueError(error)
    return updated
