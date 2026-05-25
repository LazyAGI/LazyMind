from chat.prompts.agentic import (
    DEFAULT_SYSTEM_PROMPT,
    IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
    MEMORY_GUIDANCE,
    SEARCH_GUIDANCE,
    SKILLS_GUIDANCE,
    TOOL_CALL_STATUS_GUIDANCE,
    VOCAB_GUIDANCE,
    VISION_EXTRACTOR_GUIDANCE,
)
from chat.components.agentic.config import _build_runtime_system_prompt


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
        IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
        VISION_EXTRACTOR_GUIDANCE,
    ]

    for prompt in prompts:
        assert isinstance(prompt, str)
        assert prompt.strip()
        assert_balanced_curly_braces(prompt)

    assert 'LAZYMIND' in DEFAULT_SYSTEM_PROMPT
    assert 'kb_search' in SEARCH_GUIDANCE
    assert 'memory tool' in MEMORY_GUIDANCE
    assert 'skill_manage' in SKILLS_GUIDANCE
    assert 'vocab_manage' in VOCAB_GUIDANCE


def test_runtime_system_prompt_includes_relevant_guidance_blocks():
    config = {
        'use_memory': True,
        'user_preference': 'Respond in Chinese.',
        'memory': '2026-05-25: User is debugging tests.',
        'image_files': ['/tmp/example.png'],
        'environment_context': {
            'time': {
                'now': '2026-05-25 10:30',
                'timezone': 'Asia/Shanghai',
            }
        },
    }

    rendered = _build_runtime_system_prompt(
        config,
        ['kb_search', 'memory', 'skill_manage', 'vocab_manage', 'vision_extractor'],
    )

    assert 'LAZYMIND' in rendered
    assert '## User Profile / Preferences' in rendered
    assert '## Agent Working Memory' in rendered
    assert 'Current user time: 2026-05-25 10:30' in rendered
    assert 'User timezone: Asia/Shanghai' in rendered
    assert 'kb_search' in rendered
    assert 'skill_manage' in rendered
    assert 'vocab_manage' in rendered
    assert 'vision_extractor' in rendered
