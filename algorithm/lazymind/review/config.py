from lazymind.review.prompts import (
    COMBINED_REVIEW_PROMPT,
    MEMORY_REVIEW_PROMPT,
    SKILL_REVIEW_PROMPT,
)

REVIEW_TOOLS: dict[str, list[str]] = {
    'memory': ['memory'],
    'skill': ['skill_manage'],
    'combined': ['memory', 'skill_manage', 'vocab_manage'],
}

REVIEW_PROMPTS: dict[str, str] = {
    'memory': MEMORY_REVIEW_PROMPT,
    'skill': SKILL_REVIEW_PROMPT,
    'combined': COMBINED_REVIEW_PROMPT,
}

__all__ = ['REVIEW_TOOLS', 'REVIEW_PROMPTS']
