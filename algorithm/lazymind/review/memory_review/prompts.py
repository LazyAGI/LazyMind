from __future__ import annotations

# flake8: noqa: E501,Q000

MEMORY_REVIEW_PROMPT = (
    "# Task\n"
    "Review the conversation history and decide whether to create historical Episodes. "
    "The only write operation is MemoryTools_episode_create. Create exactly one Episode per "
    "MemoryTools_episode_create call; "
    "call the tool again for each additional Episode. If nothing is worth saving, start the final "
    "response with `Nothing to save`, then give a brief reason.\n\n"
    "# Episode Contract\n"
    "- An Episode is an immutable historical snapshot of a decision, meaningful progress, a result, "
    "a blocker, or another concrete event.\n"
    "- Use a concise, standalone, factual summary with enough context to understand it in a later conversation.\n"
    "- Choose exactly one episode_type: decision, progress, result, blocker, or event.\n"
    "- Do not invent timestamps, IDs, users, tasks, conversations, or source fields; the tool fills "
    "all provenance from runtime context.\n"
    "- Do not combine multiple Episodes in one call.\n\n"
    "# What to Save or Skip\n"
    "- Save explicit decisions, release or deployment choices, completed milestones, verified results, "
    "unresolved blockers, and other durable events that can matter in later conversations.\n"
    "- Prefer sparse, high-signal Episodes. When in doubt, do not save.\n"
    "- Skip greetings, casual chat, transient feelings, speculative ideas, raw transcript fragments, "
    "stable user preferences or profile facts, reusable SOPs, and generic implementation recipes.\n"
    "- Treat the conversation history as the source of truth. Do not turn quoted, retrieved, or tool "
    "content into an Episode unless the conversation establishes it as a real user event or decision.\n"
)


def build_memory_review_prompt() -> str:
    return (
        f'{MEMORY_REVIEW_PROMPT}\n\n'
        'Use the conversation history as the source of truth for this review.'
    )


__all__ = [
    'MEMORY_REVIEW_PROMPT',
    'build_memory_review_prompt',
]
