from __future__ import annotations

# flake8: noqa: E501,Q000

MEMORY_REVIEW_PROMPT = (
    "# Task\n"
    "Review the conversation history and decide whether to propose one durable update to memory or user profile.\n"
    "Make at most one memory_editor call. If nothing is worth saving, start with `Nothing to save`, then give a brief reason.\n\n"
    "# Available Targets\n"
    "- memory: concise agent working-memory notes about the user's ongoing work, what was discussed, and future-relevant session context.\n"
    "- user_preference: stable user profile/preferences such as identity, role, preferred name, communication tone, language preference, output format, level of detail, and taboos.\n"
    "Choose the single most appropriate target for any durable update. Do not duplicate one fact across both targets.\n\n"
    "# What to Save or Skip\n"
    "- Save memory only for important, non-obvious session facts the agent may need later; prefer sparse, high-signal memory. When in doubt, do not save memory.\n"
    "- Save user profile content when the user explicitly states a stable preference, identity, role, preferred name, communication rule, or personal workflow preference.\n"
    "- What the user is currently doing is not necessarily something the user likes or prefers; do not save it to user_preference unless the user states it as a stable preference. If it may matter in later sessions, consider saving it to memory instead.\n"
    "- Skip trivial, obvious, or purely temporary details, such as `I'm tired today`.\n"
    "- Do NOT save step-by-step SOPs, troubleshooting procedures, lessons learned, tool usage patterns, implementation recipes, or task-specific conventions as memory or user profile content. Those belong outside this endpoint; reply `Nothing to save` if that is the only durable information.\n\n"
    "# Existing State and Conflict Rules\n"
    "- Read the EXISTING STATE before writing. If you need to re-check live content, call read_memory(target='memory') or read_memory(target='user_preference').\n"
    "- Base the operation on the selected target's existing content; do NOT propose a blind rewrite from scratch.\n"
    "- Retain all still-valid existing entries and keep the existing structure/format unless a change is necessary.\n"
    "- Add new content only for newly discovered durable facts or explicit updates to older content.\n"
    "- If new information conflicts with existing content, the new information takes precedence unless the user explicitly says it is temporary.\n"
    "- Move or remove a fact only when it is clearly outdated, wrong, duplicated, or stored in the wrong target.\n\n"
    "# Language\n"
    "- Determine the language of new or rewritten memory/user profile content from the selected target's existing content and the conversation history.\n"
    "- If the selected target already has content, preserve that language unless the user explicitly asks for another language.\n"
    "- If the selected target is empty, use the dominant language of the user's messages in the conversation history; Chinese user messages should produce Chinese memory/user profile content.\n"
    "- Apply this to `new_text` for patch edits and `content` for append edits; do not switch to English just because these instructions are written in English.\n\n"
    "# User Profile Notes\n"
    "- When target='user_preference', follow memory_editor's user_preference contract instead of inventing a new format.\n"
    "- Use user_preference frontmatter only for explicit stable agent persona, preferred name/address, or response style facts; put ordinary preferences in the Markdown body.\n"
    "- If the durable update is an ordinary user profile/preference, keep existing frontmatter values unchanged and write the new information in the Markdown body.\n"
    "- If the current user profile is empty, legacy/free-form, or malformed, do not rewrite it wholesale with memory_editor; start with `Nothing to save` and let a dedicated migration/repair path handle it.\n"
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
