from __future__ import annotations

from typing import Any, Dict

import lazyllm

from lazymind.chat.engine.tools.infra import tool_error, tool_success


def read_memory(target: str) -> Dict[str, Any]:
    """Read the agent's current working memory or user profile text.

    Use this tool when you are unsure about what is currently stored in
    memory — for example, when the user asks about their preferences,
    references past discussions, or when you need to check existing content
    before making an informed decision. This tool returns the full current
    text and does not modify anything.

    Args:
        target: Which buffer to read. ``'memory'`` returns the agent's own
            working memory about the user's ongoing context and prior
            discussions. ``'user_preference'`` returns the user profile / preference text
            (YAML frontmatter with ``agent_persona``, ``preferred_name``,
            ``response_style``, followed by Markdown body).

    Returns:
        A unified tool payload whose ``result`` contains ``target``,
        ``content``, and ``content_length``.
    """
    raw_target = str(target).strip()
    if raw_target not in ('memory', 'user_preference'):
        return tool_error(
            'read_memory',
            f"Unknown target {raw_target!r}; expected 'memory' or 'user_preference'."
        )

    agentic_config = lazyllm.globals.get('agentic_config') or {}
    content = str(agentic_config.get(raw_target) or '')

    return tool_success('read_memory', {
        'target': raw_target,
        'content': content,
        'content_length': len(content),
    })
