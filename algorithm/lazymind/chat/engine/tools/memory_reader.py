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

    Use this tool when you are unsure about what is currently stored in
    memory, for example when the user asks about their preferences,
    references past discussions, or when you need to check existing content
    before making an informed decision. This tool returns the full current
    text and does not modify anything.

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
