from __future__ import annotations

from typing import Any, Dict, Literal

from lazymind.chat.engine.tools.infra import MemoryRemoteStore
from lazymind.chat.engine.tools.infra.memory_write_support import (
    map_memory_exception,
    memory_applied,
    memory_write_error,
)
from lazymind.review.memory_review.editors.preference import preference_name_to_reference_name
from lazymind.review.memory_review.paths import PREFERENCE_PATH


def preference_editor(
    op: Literal['add', 'delete'],
    name: str,
    summary: str = '',
    scenario: str = '',
    reason: str = '',
) -> Dict[str, Any]:
    """Add or delete a user preference index entry.

    Use this for stable long-term preferences that should appear in the
    injected preference index. Each added entry creates or updates
    ``preference.md`` and writes a matching reference file under
    ``memory/users/references/``. Updating an existing entry is not supported
    yet; delete and re-add if a preference must change.

    For ``op='add'``, provide ``name``, ``summary``, ``scenario``, and
    ``reason``. The ``name`` must start with ``pref.`` and remain unique in
    the index. The program writes the index summary and stores the scenario
    and reason in the reference file body.

    For ``op='delete'``, provide only ``name``. The matching index entry and
    its reference file are removed.

    Args:
        op: ``add`` to create a new preference entry, or ``delete`` to remove
            an existing one.
        name: Preference identifier such as ``pref.response.concise``.
        summary: Short executable summary for the index. Required for ``add``.
        scenario: When the preference should apply. Required for ``add``.
        reason: Why the preference should be saved. Required for ``add``.

    Returns:
        A unified tool payload with status, operation, name, and ref details.
    """
    raw_op = str(op or '').strip().lower()
    raw_name = str(name or '').strip()
    if raw_op not in {'add', 'delete'}:
        return map_memory_exception(
            'preference_editor',
            ValueError("op must be 'add' or 'delete'."),
        )
    if not raw_name:
        return map_memory_exception('preference_editor', ValueError('name is required.'))

    store = MemoryRemoteStore().store
    try:
        if raw_op == 'add':
            item = store.add_preference_with_reference(
                name=raw_name,
                summary=summary,
                scenario=scenario,
                reason=reason,
            )
            return memory_applied(
                'preference_editor',
                op='add',
                name=item.name,
                summary=item.summary,
                ref=item.ref,
                path=PREFERENCE_PATH,
                reference_name=preference_name_to_reference_name(item.name),
            )

        item = store.remove_preference_with_reference(raw_name)
        return memory_applied(
            'preference_editor',
            op='delete',
            name=item.name,
            ref=item.ref,
            path=PREFERENCE_PATH,
            reference_name=preference_name_to_reference_name(item.name),
        )
    except Exception as exc:
        if str(exc).strip().lower() == 'conflict':
            return memory_write_error('preference_editor', exc)
        return map_memory_exception('preference_editor', exc)
