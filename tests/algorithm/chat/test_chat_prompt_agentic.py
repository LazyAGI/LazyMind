from lazymind.chat.engine.prompts import (
    ATTACHED_FILES_GUIDANCE,
    DEFAULT_SYSTEM_PROMPT,
    IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
    MEMORY_GUIDANCE,
    SKILLS_GUIDANCE,
    TOOL_AVAILABILITY_GUIDANCE,
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
        TOOL_CALL_STATUS_GUIDANCE,
        TOOL_AVAILABILITY_GUIDANCE,
        ATTACHED_FILES_GUIDANCE,
        IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
        VISION_EXTRACTOR_GUIDANCE,
    ]

    for prompt in prompts:
        assert isinstance(prompt, str)
        assert prompt.strip()
        assert_balanced_curly_braces(prompt)

    assert 'LAZYMIND' in DEFAULT_SYSTEM_PROMPT
    assert 'get_<ToolGroup>_methods' in TOOL_AVAILABILITY_GUIDANCE
    assert 'memory_editor' in MEMORY_GUIDANCE
    assert 'skill_editor' in SKILLS_GUIDANCE
    assert 'vocab_learn' in VOCAB_GUIDANCE


def test_search_guidance_lives_with_search_tools():
    from lazymind.chat.engine.tools.kb import KBToolGroup
    from lazymind.chat.service.component.tool_registry import _PICK_FIRST_VALID_GROUPS
    from lazyllm.tools.tools.search import (
        ArxivSearch,
        BingSearch,
        BochaSearch,
        GoogleSearch,
        SciverseSearch,
        TavilySearch,
    )

    assert 'highest retrieval priority' in KBToolGroup.__doc__
    assert 'before Wikipedia, web search, academic search' in KBToolGroup.__doc__
    assert 'core question' in KBToolGroup.kb_search.__doc__
    assert 'specific document' in KBToolGroup.kb_keyword_search.__doc__

    web_desc, _ = _PICK_FIRST_VALID_GROUPS['web_search']
    assert 'one search intent' in web_desc
    for search_cls in (GoogleSearch, BingSearch, BochaSearch, TavilySearch):
        assert 'one search intent' in search_cls.search.__doc__

    academic_desc, _ = _PICK_FIRST_VALID_GROUPS['academic_search']
    assert 'academic evidence' in academic_desc
    for search_cls in (SciverseSearch, ArxivSearch):
        assert 'academic evidence' in search_cls.search.__doc__


def test_image_guidance_uses_capability_descriptions():
    assert 'generation' in IMAGE_REFERENCE_MARKDOWN_GUIDANCE
    assert 'editing' in IMAGE_REFERENCE_MARKDOWN_GUIDANCE
    assert 'other image-capable tools' not in IMAGE_REFERENCE_MARKDOWN_GUIDANCE
    for tool_name in ('KBToolGroup', 'image_generator', 'image_editor'):
        assert tool_name not in IMAGE_REFERENCE_MARKDOWN_GUIDANCE
    for tool_name in ('KBToolGroup', 'vision_extractor'):
        assert tool_name not in VISION_EXTRACTOR_GUIDANCE


def test_build_system_prompt_includes_image_guidance_for_generation_tools():
    from lazymind.chat.engine.prompts import build_system_prompt

    with_tools = build_system_prompt({'image_generator', 'llm'})
    without_tools = build_system_prompt({'llm'})
    assert IMAGE_REFERENCE_MARKDOWN_GUIDANCE in with_tools
    assert IMAGE_REFERENCE_MARKDOWN_GUIDANCE not in without_tools
