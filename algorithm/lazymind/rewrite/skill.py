"""Skill content generation — prompt, edit operations, and registration."""

from __future__ import annotations

from typing import Any, Dict, Optional

from .base import (
    _COMMON_LANGUAGE_RULES,
    _EDIT_DISPATCH,
    _PROMPT_BUILDERS,
    _apply_replace_text_operation,
    _format_preservation_rules,
    _format_prompt_tail,
    _managed_content_governance_note,
    _normalize_numbered_lists,
    _parse_edit_operations,
)

_EDIT_OUTPUT_SPEC = (
    'Output requirements:\n'
    '1. Output only a JSON object; no markdown code blocks, no extra text.\n'
    '2. Preferred JSON structure is {"operations": [...]}.\n'
    '3. Supported operations are only replace_text and replace_all.\n'
    '4. Do not output any operation except replace_text or replace_all.\n'
    '5. Prefer {"op":"replace_text","old":"<exact old text>","new":"<new text>"} for exact local edits when old is a non-empty substring copied verbatim from current content.\n'  # noqa: E501
    '6. You may output multiple replace_text operations; they will be applied in order.\n'
    '7. Before final output, mentally apply operations in order to the current content using exact plain string search.\n'  # noqa: E501
    '8. For every replace_text, old must be found exactly in the content state at the moment that operation runs.\n'  # noqa: E501
    '9. Keep every replace_text old value as short as safely possible: use one exact line for line deletion/replacement, or one exact phrase/sentence for wording edits.\n'  # noqa: E501
    '10. Never use a whole section, a heading plus body, multiple bullets/list items, or unrelated paragraphs as one replace_text old. Split the change into several smaller replace_text operations instead.\n'  # noqa: E501
    '11. replace_text always replaces the first matching occurrence only.\n'
    '12. For delete/remove requests, the target text may appear in old but MUST NOT appear in new. Do not add, restore, or reword text that user_instruct asks to delete.\n'  # noqa: E501
    '13. Do not output replace_text operations where old and new are identical.\n'
    '14. If the exact old text is absent, outdated, ambiguous, not copied verbatim, or not enough to apply all requested changes safely, output full {"content": "..."} instead of operations.\n'  # noqa: E501
    '15. You may also use {"op":"replace_all","content":"<new full text>"} for full replacement.\n'
    '16. If you use replace_all, it MUST be the only operation in the operations array; do not output replace_all together with any other operation.\n'  # noqa: E501
    '17. The final generated content must reflect the requested change.\n'
)


def _build_skill_prompt(
    content: str,
    user_instruct: str,
    previous_error: Optional[str] = None,
) -> str:
    return (
        'You are a SKILL.md editor. Generate a JSON draft update based on the input; no explanations or summaries.\n'  # noqa: E501
        'memory type: skill\n'
        'SKILL.md is an abstract SOP (Standard Operating Procedure) that guides the agent to complete tasks '
        'using a unified methodology when the description scope is satisfied.\n'
        '\n'
        '[Format requirements]\n'
        '1. Must start with YAML frontmatter containing non-empty name, category, and description fields, '
        'followed by a blank line, then the markdown body.\n'
        '2. Keep the existing name value; do not rename unless user_instruct explicitly requests it.\n'
        '3. description should describe the applicable scope and trigger conditions in one sentence; '
        'this is the sole basis for routing/recalling this skill.\n'
        '\n'
        '[Scope and description linkage (important)]\n'
        '- When user_instruct involves expanding/narrowing/adjusting the skill scope, trigger scenarios, or coverage, '  # noqa: E501
        'update the frontmatter description accordingly to accurately reflect the new scope.\n'
        '- When changes only affect methodology details in the body without changing the scope, keep description unchanged.\n'  # noqa: E501
        '- When the requested change is only deleting or editing one body line, do NOT update frontmatter description, title, tags, version, author, created, or updated.\n'  # noqa: E501
        '\n'
        '[Body content rules]\n'
        '- replace_text is the primary edit path for skill drafts. Prefer multiple small replace_text operations over full replacement whenever the exact targets exist in current content.\n'  # noqa: E501
        '- Apply only the exact target explicitly requested by user_instruct. Do not infer related cleanup in other sections.\n'  # noqa: E501
        '- When user_instruct quotes a line, phrase, or word to remove/edit, modify only that quoted target and any necessary numbered-list renumbering.\n'  # noqa: E501
        '- Do not rewrite Usage, Examples, or neighboring sections unless a suggestion explicitly targets text in those sections.\n'  # noqa: E501
        '- Use replace_text only for exact local edits whose old text is copied verbatim from current content.\n'
        '- Keep every replace_text old value as short as safely possible: use one exact full line for line deletion/replacement, or one exact phrase/sentence for wording edits.\n'  # noqa: E501
        '- Hard limit for replace_text old: prefer 1 line; never include more than 1 newline; never exceed 200 characters unless the exact single line itself is longer.\n'  # noqa: E501
        '- Never use a whole section, a heading plus body, or multiple bullets/list items as one replace_text old. Edit each affected line or phrase separately.\n'  # noqa: E501
        '- Do not make a replace_text old value span multiple markdown sections, headings, or unrelated paragraphs. Split the change into several smaller replace_text operations instead.\n'  # noqa: E501
        '- For numbered-list deletion or insertion, use one replace_text to delete/insert the target line and separate replace_text operations to renumber each affected line; after deleting item N, renumber N+1 to N, N+2 to N+1, and so on. Never leave numbering gaps.\n'  # noqa: E501
        '- For delete/remove requests, the quoted target text may appear in old but MUST NOT appear in new. Do not add, restore, or reword text that user_instruct asks to delete.\n'  # noqa: E501
        '- Only when the user explicitly asks to delete, clear, or remove all skill content, output an empty draft via full {"content": ""} or a single replace_all operation with empty content.\n'  # noqa: E501
        '- For a request like "delete/remove this line", use replace_text only if you can copy the exact line from current content as old; otherwise use replace_all instead of fabricating old text.\n'  # noqa: E501
        '- The body must be an abstract SOP: steps, decision criteria, checklists, general rules, output format requirements, etc.\n'  # noqa: E501
        '- Do not include specific cases, project names, specific data, conversation snippets, or one-time examples in the SKILL.md body; '  # noqa: E501
        'if examples are needed, use only highly abstract placeholder illustrations.\n'
        '- If user_instruct contains specific cases, abstract the reusable experience into general rules '
        'before writing to the body; do not copy cases verbatim.\n'
        '- Recommended body structure: Applicable conditions / Steps / Judgment & validation / Common pitfalls / Output spec (trim as needed).\n'  # noqa: E501
        '\n'
        f'{_COMMON_LANGUAGE_RULES}'
        '\n'
        f'{_format_preservation_rules("body content")}'
        '\n'
        '[Length control]\n'
        '- Total length of SKILL.md (including frontmatter) must be within 2000 characters; keep it concise.\n'
        f'{_managed_content_governance_note(content, 2000)}'
        '\n'
        f'{_format_prompt_tail(content, user_instruct, _EDIT_OUTPUT_SPEC, previous_error)}'
    )


def _apply_skill_edit_operations(current_content: str, payload: Dict[str, Any]) -> str:
    operations = _parse_edit_operations(payload, entity_name='skill')
    if operations[0]['op'] == 'replace_all':
        return operations[0]['content']

    current = current_content
    applied_delete = False
    for op in operations:
        if op['old'] == op['new']:
            continue
        current = _apply_replace_text_operation(
            current, op['old'], op['new'], entity_name='skill',
        )
        if not op['new'].strip():
            applied_delete = True
    if applied_delete:
        current = _normalize_numbered_lists(current)
    return current.strip()


# Register
_PROMPT_BUILDERS['skill'] = _build_skill_prompt
_EDIT_DISPATCH['skill'] = _apply_skill_edit_operations
