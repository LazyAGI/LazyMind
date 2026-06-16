from __future__ import annotations

# flake8: noqa: E501,Q000

MEMORY_REVIEW_PROMPT = (
    "# Task\n"
    "Review the conversation history and decide whether to propose one durable update to memory or user profile.\n"
    "Make at most one memory_editor call. If nothing is worth saving, reply exactly `Nothing to save` with a brief reason.\n\n"
    "# Available Targets\n"
    "- memory: concise agent working-memory notes about the user's ongoing work, what was discussed, and future-relevant session context.\n"
    "- user: stable user profile/preferences such as identity, role, preferred name, communication tone, language preference, output format, level of detail, and taboos.\n"
    "Choose the single most appropriate target for any durable update. Do not duplicate one fact across both targets.\n\n"
    "# What to Save or Skip\n"
    "- Save memory only for important, non-obvious session facts the agent may need later; prefer sparse, high-signal memory. When in doubt, do not save memory.\n"
    "- Save user profile content when the user explicitly states a stable preference, identity, role, preferred name, or communication rule.\n"
    "- Skip trivial, obvious, or purely temporary details, such as `I'm tired today`.\n"
    "- Do NOT save multi-step reusable workflows, troubleshooting procedures, lessons learned, tool usage patterns, implementation recipes, SOPs, or task-specific conventions as memory or user profile content. Those belong outside this endpoint; reply `Nothing to save` if that is the only durable information.\n\n"
    "# Existing State and Conflict Rules\n"
    "- Read the EXISTING STATE before writing.\n"
    "- Base operations on the selected target's existing content; do NOT propose a blind rewrite from scratch.\n"
    "- Retain all still-valid existing entries and keep the existing structure/format unless a change is necessary.\n"
    "- Add new content only for newly discovered durable facts or explicit updates to older content.\n"
    "- If new information conflicts with existing content, the new information takes precedence unless the user explicitly says it is temporary.\n"
    "- Move or remove a fact only when it is clearly outdated, wrong, duplicated, or stored in the wrong target.\n\n"
    "# Language\n"
    "- Determine the language of new or rewritten memory/user profile content from the selected target's existing content and the conversation history.\n"
    "- If the selected target already has content, preserve that language unless the user explicitly asks for another language.\n"
    "- If the selected target is empty, use the dominant language of the user's messages in the conversation history; Chinese user messages should produce Chinese memory/user profile content.\n"
    "- Apply this to `replace_text.new` and `replace_all.content`; do not switch to English just because these instructions are written in English.\n\n"
    "# User Profile Format\n"
    "- When target='user', the edited full text must use YAML frontmatter delimited by `---`, followed by Markdown body content.\n"
    "- The YAML frontmatter must contain at least these fields: agent_persona, user_address, response_style.\n"
    "- Required opening shape:\n"
    "  ---\n"
    "  agent_persona: \"<role/persona>\"\n"
    "  user_address: \"<how to address the user>\"\n"
    "  response_style: \"<简洁|详细|幽默|正式|concise|detailed|humorous|formal|empty string>\"\n"
    "  ---\n"
    "- agent_persona means the role the user wants the agent to play, such as secretary, engineer, reviewer, or research assistant.\n"
    "- user_address means how the user wants the agent to address them, such as a preferred name, title, or pronoun.\n"
    "- When creating response_style or explicitly changing it, display/use exactly one of 简洁, 详细, 幽默, 正式 when the user language is Chinese; otherwise display/use exactly one of concise, detailed, humorous, formal.\n"
    "- Do not put language preferences, formatting rules, citation rules, workflow constraints, task procedures, verbs, or full instructions in response_style; write those details in the Markdown body.\n"
    "- Use \"\" when agent_persona, user_address, or response_style is unknown. If response_style is missing or invalid during format repair and the user did not specify one, use \"\".\n"
    "- Modify agent_persona, user_address, or response_style only when the user explicitly asks to change that specific field or clearly states the corresponding stable preference.\n"
    "- If the durable update is an ordinary user profile/preference, keep existing frontmatter values unchanged, including an existing valid response_style in either language, and write the new information in the Markdown body.\n"
    "- Write concrete user profile/preference body content after the closing `---`; never use generic acknowledgement text such as \"preference recorded\" as the body.\n"
    "- If the current user profile is empty, legacy/free-form, or missing this required frontmatter, use replace_all to rewrite the whole user profile into the frontmatter-plus-body format.\n"
    "- If using replace_text for target='user', ensure the final rendered text still has frontmatter and Markdown body.\n\n"
    "# Tool Contract\n"
    "- Use only memory_editor; do not call any other tool.\n"
    "- memory_editor requires exactly target and operations.\n"
    "- For agent working memory, call memory_editor(target='memory', operations=[...]).\n"
    "- For user profile/preferences, call memory_editor(target='user', operations=[...]).\n"
    "- Supported operation: replace_text: {\"op\": \"replace_text\", \"old\": \"...\", \"new\": \"...\"}; `old` MUST be an exact substring copied from the selected target's current content.\n"
    "- Supported operation: replace_all: {\"op\": \"replace_all\", \"content\": \"...\"}; `content` is the full replacement text for the selected target.\n"
    "- Prefer replace_text with exact old text copied from the selected target's existing content.\n"
    "- Use replace_all only when the selected target is empty, no exact substring can safely anchor the edit, or the content needs global deduplication/conflict resolution/reorganization.\n"
    "- For adding one item to non-empty content, replace the smallest exact existing section or block with the same block plus the new item.\n"
    "- The operations are applied to the selected target content below, and the edited full text is written to the memory_review table for human review.\n"
    "- If no durable update is warranted, do not call memory_editor; reply with `Nothing to save` and a brief reason."
)


def build_memory_review_prompt(
    *,
    memory: str,
    user: str,
) -> str:
    return (
        f'{MEMORY_REVIEW_PROMPT}\n\n'
        '--- EXISTING STATE ---\n'
        f'## Current agent working memory\n{memory or ""}\n\n'
        f'## Current user profile\n{user or ""}\n'
        '--- END EXISTING STATE ---\n\n'
        'Use the conversation history as the source of truth for this review.'
    )


__all__ = [
    'MEMORY_REVIEW_PROMPT',
    'build_memory_review_prompt',
]
