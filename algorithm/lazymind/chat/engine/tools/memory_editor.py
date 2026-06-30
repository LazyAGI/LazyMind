from typing import Any, Dict, List, Literal

import lazyllm
from typing_extensions import TypedDict

from lazymind.chat.engine.tools.infra import (
    handle_tool_errors,
    tool_error,
    tool_success,
)
from lazymind.rewrite.base import (
    UnprocessableContentError,
    _validate_generated_content,
)
from lazymind.rewrite.memory import _apply_memory_edit_operations
from lazymind.rewrite.preference import _apply_user_preference_edit_operations
from lazymind.review.memory_review.db import insert_memory_review_record


class EditOperation(TypedDict, total=False):
    """JSON edit operation applied to current memory or user profile text.

    Fields:
        op (str, required): either ``replace_text`` or ``replace_all``.
        old (str, required for replace_text): exact substring to replace.
        new (str, required for replace_text): replacement text.
        content (str, required for replace_all): full replacement content.
    """

    op: str
    old: str
    new: str
    content: str


MemoryEditorTarget = Literal['memory', 'user_preference']


@handle_tool_errors
def memory_editor(
    target: MemoryEditorTarget,
    operations: List[EditOperation],
) -> Dict[str, Any]:
    """Apply edit operations to memory or user profile and submit a review row.

    Use this tool for durable cross-session knowledge only, and only after
    comparing the conversation with the current full target text. Save
    user-stated identity, preferred names or nicknames, communication tone,
    language preference, output format, and stable habits to
    ``target='user_preference'``. Save agent working memory to
    ``target='memory'``: timestamped notes about what the user and agent
    discussed, what the user was working on, active context that may matter in
    later sessions, and other concise session-history facts from the agent's
    perspective.

    Never save workflows, procedures, lessons learned, tool usage patterns,
    implementation recipes, SOPs, or general task conventions to memory or user
    profile; those belong in reusable skills. Do not save obvious facts
    derivable from the codebase or raw transcript dumps. Do not use memory for
    explicit user-specific vocabulary or terminology mappings; use the
    vocabulary learning tool instead when it is available.

    Only claim to have saved, remembered, or recorded something when this tool
    or another durable-write tool was actually called in the same response. If
    no write tool was called, do not say things like "已保存到记忆",
    "我会记住你的偏好", "I've saved this", or "I'll remember that".

    The tool applies the supplied JSON edit operations to the original text,
    validates the edited full text, and writes one pending row to the
    algorithm-side ``memory_review`` table. It returns status metadata only; it
    does not return the edited content.

    Args:
        target: Which buffer the edit operations belong to. ``'memory'`` is the
            agent's own working memory about the user's ongoing context and
            prior discussions; ``'user_preference'`` is the user profile / preference text.
            For ``'user_preference'``, the edited full text must start with YAML
            frontmatter delimited by ``---`` containing ``agent_persona``,
            ``preferred_name``, and ``response_style``, followed by Markdown body
            content. ``response_style`` must be empty or exactly one of
            ``简洁``, ``详细``, ``幽默``, ``正式``, ``concise``, ``detailed``,
            ``humorous``, or ``formal``; use the Chinese values for Chinese
            user language and the English values otherwise. Write language,
            formatting, and workflow preferences in the Markdown body.
            The Markdown body must NOT repeat information already captured
            in the frontmatter fields (agent_persona, preferred_name,
            response_style).
        operations: Ordered JSON edit operations. Supported operations:

            - ``{"op": "replace_text", "old": "...", "new": "..."}``:
              replace the first exact ``old`` substring with ``new``. Prefer
              this whenever the current content is non-empty, including when
              adding a new entry to an existing section.
            - ``{"op": "replace_all", "content": "..."}``: replace the
              full original target text with ``content``. Use this only when
              the current content is empty, no exact substring can safely
              anchor the edit, or the update needs global deduplication,
              conflict resolution, or broader reorganization.
    """
    raw_target = str(target).strip()
    if raw_target not in {'memory', 'user_preference'}:
        return tool_error(
            'memory_editor',
            f"Unknown target {raw_target!r}; expected one of 'memory', 'user_preference'."
        )

    agentic_config = lazyllm.globals['agentic_config']
    user_id = str(agentic_config.get('user_id') or '').strip()
    session_id = str(agentic_config.get('session_id') or '').strip()
    current_content = agentic_config.get(raw_target) or ''
    operation_payload = [dict(op) for op in operations]
    try:
        apply_operations = (
            _apply_user_preference_edit_operations
            if raw_target == 'user_preference'
            else _apply_memory_edit_operations
        )
        edited_content = apply_operations(current_content, {'operations': operation_payload})
        if edited_content.strip() == current_content.strip():
            raise UnprocessableContentError(
                f'Generated {raw_target} content is unchanged from current content. '
                'A review row must contain at least one real content change.'
            )
        edited_content = _validate_generated_content(raw_target, edited_content)
    except UnprocessableContentError as exc:
        return tool_error('memory_editor', str(exc))

    insert_memory_review_record(
        target=raw_target,
        user_id=user_id,
        session_id=session_id,
        source_content=current_content,
        content=edited_content,
        operations=operation_payload,
    )
    return tool_success('memory_editor', {
        'target': raw_target,
        'status': 'pending_review',
        'message': '记忆修改已提交，等待审核',
        'operation_count': len(operation_payload),
    })
