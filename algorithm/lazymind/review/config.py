from lazymind.review.prompts import (
    COMBINED_REVIEW_PROMPT,
    MEMORY_REVIEW_PROMPT,
    SKILL_REVIEW_PROMPT,
)

REVIEW_TOOLS: dict[str, list[str]] = {
    'memory': ['memory_editor'],
    'skill': ['skill_editor'],
    'combined': ['memory_editor', 'skill_editor', 'vocab_learn'],
}

REVIEW_PROMPTS: dict[str, str] = {
    'memory': MEMORY_REVIEW_PROMPT,
    'skill': SKILL_REVIEW_PROMPT,
    'combined': COMBINED_REVIEW_PROMPT,
}

__all__ = ['REVIEW_TOOLS', 'REVIEW_PROMPTS']
