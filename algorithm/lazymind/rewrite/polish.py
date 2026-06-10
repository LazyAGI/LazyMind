"""Prompt polishing — prompt building and registration (no edit operations needed)."""

from __future__ import annotations

from typing import Optional

from .base import (
    _COMMON_OUTPUT_SPEC,
    _PROMPT_BUILDERS,
    _format_prompt_tail,
)


def _build_polish_prompt(
    content: str,
    user_instruct: str,
    previous_error: Optional[str] = None,
) -> str:
    return (
        'You are a prompt polishing assistant. Rewrite the input prompt according to user_instruct.\n'
        'task type: polish\n'
        '\n'
        '[Rules]\n'
        '- Do not answer the prompt.\n'
        '- Preserve the original intent and constraints.\n'
        '- Do not add unsupported facts, requirements, tools, data sources, or user preferences.\n'
        '- Improve clarity, structure, specificity, and wording only as requested by user_instruct.\n'
        '- Keep the output directly usable as a prompt.\n'
        '- Determine the output language from current content and user_instruct, and keep it consistent.\n'
        '\n'
        f'{_format_prompt_tail(content, user_instruct, _COMMON_OUTPUT_SPEC, previous_error)}'
    )


# Register (no edit dispatch — polish uses default parsed.get('content'))
_PROMPT_BUILDERS['polish'] = _build_polish_prompt
