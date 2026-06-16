from __future__ import annotations

from typing import Any, Dict, List

from typing_extensions import TypedDict


class SkillEditOperation(TypedDict, total=False):
    """JSON edit operation applied to current SKILL.md content."""

    op: str
    old: str
    new: str
    content: str


def apply_skill_edit_operations(
    current_content: str,
    operations: List[SkillEditOperation],
) -> tuple[str, list[Dict[str, Any]]]:
    from lazymind.rewrite.base import UnprocessableContentError
    from lazymind.rewrite.skill import _apply_skill_edit_operations

    operation_payload = [dict(op) for op in operations]
    edited_content = _apply_skill_edit_operations(current_content, {'operations': operation_payload})
    if edited_content.strip() == current_content.strip():
        raise UnprocessableContentError(
            'Edited SKILL.md content is unchanged from current content. '
            'A review row must contain at least one real content change.'
        )
    return edited_content, operation_payload
