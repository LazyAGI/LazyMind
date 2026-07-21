from __future__ import annotations

from typing import Any, Dict

from lazymind.chat.engine.tools.infra import MemoryRemoteStore
from lazymind.chat.engine.tools.infra.memory_write_support import (
    map_memory_exception,
    memory_applied,
    memory_write_error,
)
from lazymind.review.memory_review.editors.soul import SOUL_EDITABLE_FIELDS
from lazymind.review.memory_review.paths import SOUL_PATH


def soul_editor(field: str, value: str) -> Dict[str, Any]:
    """Update one preset field in the agent soul document.

    Use this only when the user explicitly asks to change the assistant's
    default identity, mission, interaction style, or epistemic behavior.
    Do not use it for user-specific facts; those belong in profile or
    preference editors. Only the preset soul fields listed below are
    supported; other fields are rejected.

    Args:
        field: Dot-path of the soul field to update, for example
            ``identity.description`` or ``interaction.default_tone``.
        value: New non-empty string value for the field.

    Returns:
        A unified tool payload with status, field, path, and value.
    """
    raw_field = str(field or '').strip()
    raw_value = str(value if value is not None else '')
    if raw_field not in SOUL_EDITABLE_FIELDS:
        supported = ', '.join(sorted(SOUL_EDITABLE_FIELDS))
        return map_memory_exception(
            'soul_editor',
            ValueError(f'unsupported soul field {raw_field!r}; expected one of: {supported}.'),
        )

    store = MemoryRemoteStore().store
    try:
        updated = store.apply_soul_field(raw_field, raw_value)
    except Exception as exc:
        if str(exc).strip().lower() == 'conflict':
            return memory_write_error('soul_editor', exc)
        return map_memory_exception('soul_editor', exc)

    return memory_applied(
        'soul_editor',
        field=raw_field,
        path=SOUL_PATH,
        value=raw_value.strip(),
        content_length=len(updated),
    )
