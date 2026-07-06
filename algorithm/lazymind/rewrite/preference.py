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
    '4. The final rendered user_preference text must be a frontmatter-style document: YAML frontmatter delimited by --- followed by Markdown body content.\n'  # noqa: E501
    '5. replace_text always replaces the first matching occurrence only. If that is not safe enough, use replace_all instead of trying to target a later match.\n'  # noqa: E501
    '6. You may output multiple replace_text operations and they will be applied in order.\n'
    '7. Use {"op":"replace_all","content":"<new full user_preference text>"} only when the current text is truly empty (""), free-form without any YAML frontmatter, or the frontmatter structure is so broken that individual replace_text cannot repair it. A valid YAML frontmatter skeleton with empty field values (agent_persona: "", etc.) is NOT a reason to use replace_all — fill in fields individually with replace_text targeting exact empty-value lines.\n'  # noqa: E501
    '8. When using replace_all, you MUST copy ALL existing frontmatter field names verbatim from the current content; only change their values as instructed by user_instruct. Never drop or rename an existing frontmatter key, and never invent new ones.\n'  # noqa: E501
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
        '[Format requirements]\n'
        '- Must start with YAML frontmatter delimited by `---`, containing at least agent_persona, preferred_name, and response_style fields, followed by a blank line and Markdown body content.\n'  # noqa: E501
        '- agent_persona（智能体身份、职责和边界）describes the identity, responsibilities, and boundaries the agent should maintain when replying.\n'  # noqa: E501
        '- preferred_name（对用户的称呼方式）means how replies should address the user.\n'
        '- response_style（表达习惯、篇幅和结构偏好）is a short text describing expression habits, length preference, and structure preference.\n'  # noqa: E501
        '- Each YAML frontmatter value must be a string of 100 characters or less. Use "" when agent_persona, preferred_name, or response_style is unknown.\n'  # noqa: E501
        '- The YAML frontmatter field names (keys) are FIXED. You may ONLY change their values; NEVER add, remove, or rename a frontmatter key. When user_instruct asks to record new information that does not fit an existing frontmatter field, write it in the Markdown body, not as a new frontmatter field.\n'  # noqa: E501
        '- If response_style is missing or invalid during format repair and the user did not specify one, use "".\n'  # noqa: E501
        '- Modify agent_persona, preferred_name, or response_style only when user_instruct explicitly asks to change that specific field or clearly states the corresponding stable preference.\n'  # noqa: E501
        '- If user_instruct only adds ordinary profile/preferences, keep existing frontmatter values unchanged and write the new information in the Markdown body.\n'  # noqa: E501
        '- Write concrete user profile/preference entries in the Markdown body after the closing `---`.\n'
        '- The Markdown body must NOT repeat information already captured in the frontmatter fields (agent_persona, preferred_name, response_style). For example, do not write "智能体身份：技术助理", "称呼方式：老师", or "表达结构：先结论后解释" in the body when those values are already in the frontmatter.\n'  # noqa: E501
        '\n'
        '[Writing and merging rules]\n'
        '- You do NOT output final user_preference text directly unless you must use replace_all. Normally you output edit operations that will be applied to the existing user_preference inside the generate endpoint.\n'  # noqa: E501
        '- If the current text is empty, free-form, paragraph-based, or missing the required YAML frontmatter, use `replace_all` to convert the whole content to the frontmatter-plus-body format.\n'  # noqa: E501
        '- `replace_text` always replaces the first matching occurrence only. If first-match replacement is unsafe or not enough, use `replace_all` instead.\n'  # noqa: E501
        '- Prefer small, local `replace_text` edits when the existing content already has the YAML frontmatter skeleton (delimited by `---`). This includes when field values are empty strings or body is empty — you can fill in individual fields by targeting the exact empty-value lines (e.g. replace `agent_persona: ""` with `agent_persona: "技术助理"`), and add body content by targeting the closing `---` line.\n'  # noqa: E501
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
