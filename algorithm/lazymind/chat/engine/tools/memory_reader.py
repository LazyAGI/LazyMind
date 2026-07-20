from __future__ import annotations

from typing import Any, Dict

from lazymind.chat.engine.tools.infra import (
    MEMORY_TARGET_PATHS,
    MemoryRemoteStore,
    tool_error,
    tool_success,
)


def read_memory(target: str) -> Dict[str, Any]:
    """Read the agent's current working memory or user profile text.

    This reads optional persistent, cross-conversation notes. It does NOT read
    the current conversation history, which is already present in the model's
    messages. Never call this tool to recall earlier turns in the current chat,
    summarize the conversation, or resolve a follow-up question. Empty
    persistent memory does not mean that conversation history is unavailable.
    Use this tool only when the user explicitly asks about saved memory/profile
    content or when persistent cross-conversation notes are specifically needed.

    Args:
        target: Selects the document to read. Use 'memory' for agent
            working memory, or 'user_preference' for user profile and
            preference text.

    Returns:
        A unified tool payload whose result contains target, content, and
        content_length.
    """
    raw_target = str(target).strip()
    if raw_target not in MEMORY_TARGET_PATHS:
        return tool_error(
            'read_memory',
            f"Unknown target {raw_target!r}; expected one of 'memory', 'user_preference'.",
        )

    try:
        content = MemoryRemoteStore().read(raw_target)
    except Exception as exc:
        return tool_error('read_memory', f'Failed to read {raw_target} via RemoteFS: {exc}')

    return tool_success('read_memory', {
        'target': raw_target,
        'content': content,
        'content_length': len(content),
    })
