from __future__ import annotations

from typing import Any, Dict

from lazymind.chat.engine.tools.infra import MemoryRemoteStore
from lazymind.chat.engine.tools.infra.memory_write_support import (
    map_memory_exception,
    memory_applied,
    memory_write_error,
)
from lazymind.review.memory_review.editors.profile import PROFILE_EDITABLE_FIELDS
from lazymind.review.memory_review.paths import PROFILE_PATH


def profile_editor(field: str, value: str) -> Dict[str, Any]:
    """Update one preset field in the user profile document.

    Use this for stable user facts such as preferred name, locale, role,
    organization, or accessibility needs. Do not use it for long-form
    behavioral preferences; those belong in ``preference_editor``. Only the
    preset profile fields listed below are supported.

    For list fields (``identity.aliases``, ``locale.languages``,
    ``professional.roles``, ``professional.expertise_domains``,
    ``accessibility.communication_needs``), pass a JSON string array such as
    ``["zh-CN","en-US"]`` or a comma-separated list. For optional string
    fields, pass an empty string or ``null`` to clear the value.

    Args:
        field: Dot-path of the profile field to update, for example
            ``identity.preferred_name`` or ``locale.languages``.
        value: Serialized value for the field.

    Returns:
        A unified tool payload with status, field, path, and value.
    """
    raw_field = str(field or '').strip()
    raw_value = '' if value is None else str(value)
    if raw_field not in PROFILE_EDITABLE_FIELDS:
        supported = ', '.join(sorted(PROFILE_EDITABLE_FIELDS))
        return map_memory_exception(
            'profile_editor',
            ValueError(f'unsupported profile field {raw_field!r}; expected one of: {supported}.'),
        )

    store = MemoryRemoteStore().store
    try:
        updated = store.apply_profile_field(raw_field, raw_value)
    except Exception as exc:
        if str(exc).strip().lower() == 'conflict':
            return memory_write_error('profile_editor', exc)
        return map_memory_exception('profile_editor', exc)

    return memory_applied(
        'profile_editor',
        field=raw_field,
        path=PROFILE_PATH,
        value=raw_value,
        content_length=len(updated),
    )
