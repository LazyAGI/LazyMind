"""User preference content generation — prompt, edit operations, and registration."""

from __future__ import annotations

from typing import Any, Dict, Optional

from .base import (
    _COMMON_LANGUAGE_RULES,
    _EDIT_DISPATCH,
    _PROMPT_BUILDERS,
    _apply_replace_text_operation,
    _format_preservation_rules,
    _format_prompt_tail,
    _parse_edit_operations,
)

_USER_PREFERENCE_EDIT_OUTPUT_SPEC = (
    'Output requirements:\n'
    '1. Output only a JSON object; no markdown code blocks, no extra text.\n'
    '2. JSON structure must be {"operations": [...]}.\n'
    '3. operations is a list of edit commands that will be applied inside the generate endpoint and then rendered back to full user_preference text.\n'  # noqa: E501
    '4. When the current text is free-form or not clearly section-structured, prefer {"op":"replace_text","old":"<exact old text>","new":"<new text>"} for local edits.\n'  # noqa: E501
    '5. replace_text always replaces the first matching occurrence only. If that is not safe enough, use replace_all instead of trying to target a later match.\n'  # noqa: E501
    '6. You may output multiple replace_text operations and they will be applied in order.\n'
    '7. Use {"op":"replace_all","content":"<new full user_preference text>"} only as a last resort when the current text is too malformed or legacy to edit safely with local text replacement operations.\n'  # noqa: E501
)


def _build_user_preference_prompt(
    content: str,
    user_instruct: str,
    previous_error: Optional[str] = None,
) -> str:
    return (
        'You are a user_preference editor. Generate the complete new user_preference content based on the input; no explanations or summaries.\n'  # noqa: E501
        'memory type: user_preference\n'
        'user_preference stores long-term stable user profile information, such as: user identity / role / domain, '
        'long-term preferences (communication tone, output format, language, level of detail), taboos, common workflow preferences, default context assumptions, etc.\n'  # noqa: E501
        '\n'
        '[Content boundaries]\n'
        '- Only record long-term stable profile information that can be reused in every future interaction.\n'
        '- Do not record specific experiences, specific project knowledge, or one-time events here; those belong to memory.\n'  # noqa: E501
        '- Do not write as chat logs or journals; organize as itemized profile entries that the agent can quickly read.\n'  # noqa: E501
        '\n'
        '[Writing and merging rules]\n'
        '- You do NOT output final user_preference text directly unless you must use replace_all. Normally you output edit operations that will be applied to the existing user_preference inside the generate endpoint.\n'  # noqa: E501
        "- If the current text is free-form, paragraph-based, or otherwise not clearly section-structured, prefer `replace_text` with an exact old substring and the desired new substring so the user's own writing structure is preserved.\n"  # noqa: E501
        '- `replace_text` always replaces the first matching occurrence only. If first-match replacement is unsafe or not enough, use `replace_all` instead.\n'  # noqa: E501
        '- Prefer small, local `replace_text` edits over rewriting the whole text. Keep untouched user-authored wording and structure exactly as-is whenever possible.\n'  # noqa: E501
        '- When preferences conflict, the new preference should replace the old text directly, and user_instruct takes precedence.\n'  # noqa: E501
        '- Keep language concise and neutral; no anthropomorphic comments; only state factual user profile entries.\n'
        '- Use `replace_all` only when the current content cannot be edited safely with local text replacement operations.\n'  # noqa: E501
        '\n'
        f'{_COMMON_LANGUAGE_RULES}'
        '\n'
        f'{_format_preservation_rules("profile entries")}'
        '\n'
        f'{_format_prompt_tail(content, user_instruct, _USER_PREFERENCE_EDIT_OUTPUT_SPEC, previous_error)}'
    )


def _apply_user_preference_edit_operations(current_content: str, payload: Dict[str, Any]) -> str:
    operations = _parse_edit_operations(payload, entity_name='user_preference', allow_empty_old=True)
    if operations[0]['op'] == 'replace_all':
        return operations[0]['content']

    current = current_content
    for op in operations:
        if op['op'] == 'replace_text':
            if op['old'] == op['new']:
                continue
            current = _apply_replace_text_operation(
                current, op['old'], op['new'], entity_name='user_preference',
            )
    return current.strip()


# Register
_PROMPT_BUILDERS['user_preference'] = _build_user_preference_prompt
_EDIT_DISPATCH['user_preference'] = _apply_user_preference_edit_operations
