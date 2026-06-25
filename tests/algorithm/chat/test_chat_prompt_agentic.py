from lazymind.chat.engine.prompts import (
    ATTACHED_FILES_GUIDANCE,
    DEFAULT_SYSTEM_PROMPT,
    IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
    TOOL_CALL_STATUS_GUIDANCE,
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
        TOOL_CALL_STATUS_GUIDANCE,
        ATTACHED_FILES_GUIDANCE,
        IMAGE_REFERENCE_MARKDOWN_GUIDANCE,
    ]

    for prompt in prompts:
        assert isinstance(prompt, str)
        assert prompt.strip()
        assert_balanced_curly_braces(prompt)

    assert 'LAZYMIND' in DEFAULT_SYSTEM_PROMPT
    assert 'vision_extractor' not in ATTACHED_FILES_GUIDANCE
    assert 'kb_tmp_search' not in ATTACHED_FILES_GUIDANCE
    assert 'kb_*' not in ATTACHED_FILES_GUIDANCE


def test_search_guidance_lives_with_search_tools():
    from lazymind.chat.engine.tools.kb import KBToolGroup
    from lazyllm.tools.tools.search import (
        ArxivSearch,
        BingSearch,
        BochaSearch,
        GoogleSearch,
        GoogleBooksSearch,
        SciverseSearch,
        SearchBase,
        SemanticScholarSearch,
        StackOverflowSearch,
        TavilySearch,
        TencentSearch,
        WikipediaSearch,
    )
    import lazyllm.docs.tools.search  # noqa: F401

    assert 'highest retrieval priority' in KBToolGroup.__doc__
    assert 'before Wikipedia, web search, academic search' in KBToolGroup.__doc__
    assert 'core question' in KBToolGroup.kb_search.__doc__
    assert 'specific document' in KBToolGroup.kb_keyword_search.__doc__

    assert 'one search intent' in SearchBase.search.__doc__
    assert 'core question' in SearchBase.search.__doc__
    assert 'get_content(item) or get_contents(items)' in SearchBase.search.__doc__
    general_search_classes = (
        GoogleSearch,
        TencentSearch,
        BingSearch,
        BochaSearch,
        StackOverflowSearch,
        GoogleBooksSearch,
        TavilySearch,
        WikipediaSearch,
    )
    for search_cls in general_search_classes:
        assert 'one search intent' in search_cls.search.__doc__
        assert 'fabricate sources' in search_cls.search.__doc__

    for search_cls in (ArxivSearch, SciverseSearch, SemanticScholarSearch):
        assert 'academic evidence' in search_cls.search.__doc__
        assert 'one research intent' in search_cls.search.__doc__
        assert 'get_content' in search_cls.search.__doc__
    assert 'search_type selection' in SciverseSearch.search.__doc__


def test_image_guidance_uses_capability_descriptions():
    assert 'image_markdown' in IMAGE_REFERENCE_MARKDOWN_GUIDANCE
    assert '/static-files/' in IMAGE_REFERENCE_MARKDOWN_GUIDANCE
    assert 'other image-capable tools' not in IMAGE_REFERENCE_MARKDOWN_GUIDANCE
    for tool_name in ('KBToolGroup', 'image_generator', 'image_editor'):
        assert tool_name not in IMAGE_REFERENCE_MARKDOWN_GUIDANCE


def test_tool_specific_guidance_lives_with_tool_docstrings():
    from lazymind.chat.engine.tools.kb import kb_tmp_search
    from lazymind.chat.engine.tools.memory_editor import memory_editor
    from lazymind.chat.engine.tools.multimodal import vision_extractor
    from lazymind.chat.engine.tools.skill_editor import skill_editor
    from lazymind.chat.engine.tools.vocab_learn import vocab_learn

    assert 'durable cross-session knowledge only' in memory_editor.__doc__
    assert 'Never save workflows' in memory_editor.__doc__
    assert 'vocabulary learning tool' in memory_editor.__doc__
    assert "I've saved this" in memory_editor.__doc__

    assert 'Prefer this tool over memory' in vocab_learn.__doc__
    assert 'vocabulary' in vocab_learn.__doc__
    assert '`word`' in vocab_learn.__doc__
    assert '`synonym`' in vocab_learn.__doc__
    assert '`description`' in vocab_learn.__doc__
    assert '`reason`' in vocab_learn.__doc__

    skill_doc = ' '.join(skill_editor.__doc__.split())
    assert 'complex task (5+ tool calls)' in skill_editor.__doc__
    assert 'name, category, and description' in skill_doc
    assert 'single path segment' in skill_doc
    assert 'source=remote' in skill_editor.__doc__
    assert 'replace_text' in skill_editor.__doc__

    assert 'attached' in kb_tmp_search.__doc__
    assert 'temporary uploaded files' in kb_tmp_search.__doc__
    assert 'exactly one search intent' in kb_tmp_search.__doc__

    vision_doc = ' '.join(vision_extractor.__doc__.split())
    assert 'Attached Files' in vision_doc
    assert 'local_path' in vision_doc
    assert '/static-files/' in vision_doc


def test_vision_default_instruction_lives_with_vision_tool():
    import lazymind.chat.engine.prompts as prompts
    import lazymind.chat.engine.tools.multimodal as multimodal

    assert not hasattr(prompts, 'VISION_EXTRACT_DEFAULT_INSTRUCTION')
    assert 'Describe the image in plain text' in multimodal._VISION_EXTRACT_DEFAULT_INSTRUCTION


def test_cloud_document_link_guidance_lives_with_filesystem_docs():
    from lazymind.chat.engine.prompts import build_system_prompt
    from lazyllm.tools.fs import FeishuFS, FeishuWikiFS, LinkDocumentFSBase, NotionFS
    import lazyllm.docs.tools.tool_fs  # noqa: F401

    prompt = build_system_prompt({'feishu', 'notion'})
    assert 'Cloud document link rules' not in prompt
    assert 'Feishu/Lark document URL' not in prompt
    assert 'Notion URL' not in prompt
    assert 'generic URL fetching for private document' not in prompt

    resolve_doc = ' '.join(LinkDocumentFSBase.resolve_link.__doc__.split())
    references_doc = ' '.join(LinkDocumentFSBase.read_with_references.__doc__.split())
    for doc in (resolve_doc, references_doc):
        assert 'Document link usage' in doc
        assert 'generic web URL' in doc
        assert 'pretending the document was read' in doc

    feishu_docs = [
        ' '.join(obj.__doc__.split())
        for obj in (
            FeishuFS,
            FeishuWikiFS,
            FeishuWikiFS.fetch_url,
            FeishuWikiFS.read_bytes,
            FeishuWikiFS.read,
        )
    ]
    for doc in feishu_docs:
        assert 'Feishu/Lark document link usage' in doc
        assert 'generic URL fetching first' in doc
        assert 'missing authorization or access' in doc

    notion_docs = [
        ' '.join(obj.__doc__.split())
        for obj in (NotionFS, NotionFS.resolve_notion_ref)
    ]
    for doc in notion_docs:
        assert 'Notion document link usage' in doc
        assert 'generic URL fetching first' in doc
        assert 'integration is not connected' in doc


def test_build_system_prompt_includes_image_guidance_for_generation_tools():
    from lazymind.chat.engine.prompts import build_system_prompt

    with_tools = build_system_prompt({'image_generator', 'llm'})
    without_tools = build_system_prompt({'llm'})
    assert IMAGE_REFERENCE_MARKDOWN_GUIDANCE in with_tools
    assert IMAGE_REFERENCE_MARKDOWN_GUIDANCE not in without_tools


def test_build_system_prompt_avoids_unregistered_attachment_tool_names():
    from lazymind.chat.engine.prompts import build_system_prompt

    prompt = build_system_prompt({'llm'}, files=['report.pdf', 'diagram.png'])

    assert ATTACHED_FILES_GUIDANCE in prompt
    assert 'vision_extractor' not in prompt
    assert 'kb_tmp_search' not in prompt
    assert 'kb_*' not in prompt
    assert 'memory_editor' not in prompt
    assert 'vocab_learn' not in prompt
    assert 'skill_editor' not in prompt
