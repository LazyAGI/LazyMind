"""Prompt polishing — prompt building and registration (no edit operations needed)."""

from __future__ import annotations

from typing import Any, Dict, Optional

from lazyllm import AutoModel

from .base import (
    _COMMON_OUTPUT_SPEC,
    _PROMPT_BUILDERS,
    _format_prompt_tail,
)


def rewrite_editable_selection(
    full_content: str,
    selection_start: int,
    selection_end: int,
    user_instruct: str,
) -> Dict[str, Any]:
    """Rewrite one exact range while treating the complete editable block as read-only context."""
    selected = full_content[selection_start:selection_end]
    block_start = full_content.rfind('\n\n', 0, selection_start)
    block_start = 0 if block_start < 0 else block_start + 2
    block_end = full_content.find('\n\n', selection_end)
    block_end = len(full_content) if block_end < 0 else block_end
    containing_block = full_content[block_start:block_end]
    prompt = (
        'You edit one authorized Markdown block inside a complete document. '
        'The user quote is an attention anchor, not a modification boundary.\n'
        'Return only one JSON object with this shape:\n'
        '{"content":"complete replacement for the authorized Markdown block"}\n'
        'Focus first on the quoted text and the user instruction, then inspect the entire authorized block and modify '
        'as much or as little of that block as needed for a coherent result. '
        'You may change text before or after the quote '
        'inside the block. Do not change anything outside the authorized block.\n'
        'Preserve the block type, Markdown structure, facts, intent, and unaffected wording '
        'unless the instruction requires otherwise. Review the replacement in the context '
        'of the complete document and introduce no new language, logic, '
        'factual, structural, formatting, or boundary-coherence problems.\n\n'
        f'Complete Markdown document:\n<document>\n{full_content}\n</document>\n\n'
        f'User quote (attention anchor only):\n<quote>{selected}</quote>\n'
        f'Cited location (zero-based, end-exclusive): [{selection_start}, {selection_end})\n'
        'The location identifies the quote when the same text appears more than once; do not recalculate it.\n'
        f'Complete containing Markdown block:\n<containing_block>\n{containing_block}\n</containing_block>\n'
        f'Quote location inside containing block: [{selection_start - block_start}, {selection_end - block_start})\n'
        f'User instruction: {user_instruct}\n'
    )
    raw = AutoModel(model='llm')(prompt)
    # Keep parsing deliberately strict: callers must never apply prose as a patch.
    from .base import _extract_json_object  # local import keeps the public API small
    parsed = _extract_json_object(raw)
    content = parsed.get('content')
    if not isinstance(content, str):
        raise ValueError("Generated field 'content' must be a string.")
    return {
        'content': content,
        'target_start': block_start,
        'target_end': block_end,
    }


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
