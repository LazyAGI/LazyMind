from typing import Any, Dict, Literal

from lazymind.chat.engine.tools.infra import (
    MEMORY_TARGET_PATHS,
    MemoryRemoteStore,
    tool_error,
    tool_success,
)
from lazymind.chat.engine.tools.infra.memory_operations import apply_memory_tool_operation
from lazymind.rewrite.base import (
    UnprocessableContentError,
    _validate_generated_content,
)


MemoryEditorTarget = Literal['memory', 'user_preference']


def _memory_editor_write_error(target: str, exc: Exception) -> Dict[str, Any]:
    message = str(exc).strip()
    if message.lower() == 'conflict':
        return tool_error(
            'memory_editor',
            'There are pending changes. Please ask the user to handle them before modifying.',
        )
    return tool_error('memory_editor', f'Failed to write {target} via RemoteFS: {message}')


def memory_editor(
    target: MemoryEditorTarget,
    op: str,
    old_text: str = '',
    new_text: str = '',
    replace_all_matches: bool = False,
    content: str = '',
) -> Dict[str, Any]:
    """Apply one edit operation to memory or user_preference.

    Use this tool for durable cross-session knowledge only, and only after
    comparing the conversation with the current full target text. Save
    user-stated identity, preferred names or nicknames, communication tone,
    language preference, output format, and stable habits to
    target='user_preference'. Save agent working memory to target='memory':
    timestamped notes about what the user and agent
    discussed, what the user was working on, active context that may matter in
    later sessions, and other concise session-history facts from the agent's
    perspective.

    Save stable personal preferences and habits to target='user_preference',
    including stable workflow preferences. Never save step-by-step SOPs,
    troubleshooting procedures, lessons learned, tool usage patterns,
    implementation recipes, or task-specific conventions to memory or user
    profile; those belong in reusable skills. Do not save obvious facts
    derivable from the codebase or raw transcript dumps. Do not use memory for
    explicit user-specific vocabulary or terminology mappings; use the
    vocabulary learning tool instead when it is available.

    Only claim to have saved, remembered, or recorded something when this tool
    or another durable-write tool was actually called in the same response. If
    no write tool was called, do not say things like "I've saved this", or
    "I'll remember that".

    For target='user_preference', the edited full text must start with YAML
    frontmatter delimited by --- containing agent_persona, preferred_name, and
    response_style, followed by Markdown body
    content. These are the only supported frontmatter fields; put any other
    user profile data, such as email addresses, account names, roles, habits,
    or preferences, in the Markdown body. agent_persona describes the identity,
    responsibilities, and boundaries the agent should maintain when replying.
    preferred_name is how replies should address the user. response_style is a
    short text describing expression habits, length
    preference, and structure preference. Each frontmatter value must be 100
    characters or less. The Markdown body must NOT repeat information already
    captured in the frontmatter fields.

    Args:
        target: Selects the document to edit. Use 'memory' for the agent's
            working memory about the user's ongoing context and prior
            discussions. Use 'user_preference' for the user profile and
            preference text.
        op: Edit operation to apply. Use 'patch' to replace existing text, or
            'append' to add content to the end of the selected document.
        old_text: Exact non-empty substring to replace when op='patch'.
        new_text: Replacement text to use when op='patch'.
        replace_all_matches: When op='patch', replace every matching occurrence
            instead of requiring old_text to match exactly once.
        content: Non-empty content to append when op='append'. Do not use
            append to rewrite or repair existing content.
    """
    raw_target = str(target).strip()
    if raw_target not in MEMORY_TARGET_PATHS:
        return tool_error(
            'memory_editor',
            f"Unknown target {raw_target!r}; expected one of 'memory', 'user_preference'.",
        )

    store = MemoryRemoteStore()
    try:
        current_content = store.read(raw_target)
    except Exception as exc:
        return tool_error('memory_editor', f'Failed to read {raw_target} via RemoteFS: {exc}')

    try:
        edited_content = apply_memory_tool_operation(
            current_content,
            op=op,
            old_text=old_text,
            new_text=new_text,
            replace_all_matches=replace_all_matches,
            content=content,
        )
        if edited_content.strip() == current_content.strip():
            raise UnprocessableContentError(
                f'Generated {raw_target} content is unchanged from current content. '
                'A RemoteFS write must contain at least one real content change.'
            )
        edited_content = _validate_generated_content(raw_target, edited_content)
    except UnprocessableContentError as exc:
        return tool_error('memory_editor', str(exc))

    try:
        store.write(raw_target, edited_content)
    except Exception as exc:
        return _memory_editor_write_error(raw_target, exc)

    return tool_success(
        'memory_editor',
        {
            'target': raw_target,
            'status': 'pending_review',
            'message': 'Memory changes were written to draft and are pending review.',
            'operation_count': 1,
        },
    )
