from lazymind.chat.engine.prompts import (
    ATTACHED_FILES_GUIDANCE,
    DEFAULT_SYSTEM_PROMPT,
    IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
    MEMORY_GUIDANCE,
    SEARCH_GUIDANCE,
    SKILLS_GUIDANCE,
    TOOL_CALL_STATUS_GUIDANCE,
    VOCAB_GUIDANCE,
    VISION_EXTRACTOR_GUIDANCE,
)


def assert_balanced_curly_braces(text):
    depth = 0
    for char in text:
        if char == '{':
            depth += 1
        elif char == '}':
            depth -= 1
        assert depth >= 0
    assert depth == 0


def test_agentic_guidance_strings_are_non_empty_and_balanced():
    prompts = [
        DEFAULT_SYSTEM_PROMPT,
        MEMORY_GUIDANCE,
        VOCAB_GUIDANCE,
        SKILLS_GUIDANCE,
        SEARCH_GUIDANCE,
        TOOL_CALL_STATUS_GUIDANCE,
        ATTACHED_FILES_GUIDANCE,
        IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
        VISION_EXTRACTOR_GUIDANCE,
    ]

    for prompt in prompts:
        assert isinstance(prompt, str)
        assert prompt.strip()
        assert_balanced_curly_braces(prompt)

    assert 'LAZYMIND' in DEFAULT_SYSTEM_PROMPT
    assert 'kb_search' in SEARCH_GUIDANCE
    assert 'memory_editor' in MEMORY_GUIDANCE
    assert 'skill_editor' in SKILLS_GUIDANCE
    assert 'vocab_learn' in VOCAB_GUIDANCE
