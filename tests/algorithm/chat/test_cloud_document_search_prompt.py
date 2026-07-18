import importlib.util
import sys
import types
from pathlib import Path


def _load_system_prompt_module():
    fake_guidance = types.ModuleType('lazymind.chat.engine.prompts.guidance')
    fake_guidance.ATTACHED_FILES_GUIDANCE = 'attached files'
    fake_guidance.DEFAULT_SYSTEM_PROMPT = 'default prompt'
    fake_guidance.IMAGE_REFERENCE_MARKDOWN_GUIDANCE = 'image guidance'
    fake_guidance.KNOWLEDGE_EVIDENCE_CITATION_GUIDANCE = 'knowledge guidance'
    fake_guidance.SEARCH_GUIDANCE = 'knowledge search guidance'
    fake_guidance.TOOL_CALL_STATUS_GUIDANCE = 'tool guidance'
    fake_guidance.WEB_SEARCH_GUIDANCE = 'web search guidance'
    old_guidance = sys.modules.get(fake_guidance.__name__)
    sys.modules[fake_guidance.__name__] = fake_guidance
    try:
        path = Path('algorithm/lazymind/chat/engine/prompts/system_prompt.py')
        spec = importlib.util.spec_from_file_location(
            'lazymind.chat.engine.prompts.system_prompt_test', path,
        )
        module = importlib.util.module_from_spec(spec)
        assert spec.loader is not None
        spec.loader.exec_module(module)
        return module
    finally:
        if old_guidance is None:
            sys.modules.pop(fake_guidance.__name__, None)
        else:
            sys.modules[fake_guidance.__name__] = old_guidance


def test_cloud_document_search_guidance_is_added_for_feishu_group():
    prompt = _load_system_prompt_module().build_system_prompt(
        {'feishu'}, use_memory=False,
    )

    assert '`FeishuWikiFS_search`' in prompt
    assert '`FeishuWikiFS_find`' in prompt
    assert 'do not ask the user to provide a space id, node id, document' in prompt
    assert 'searches all Wiki documents accessible to the authenticated' in prompt
    assert 'not the local knowledge base' in prompt


def test_cloud_document_search_guidance_is_added_for_notion_group():
    prompt = _load_system_prompt_module().build_system_prompt(
        {'notion'}, use_memory=False,
    )

    assert '`NotionFS_search`' in prompt
    assert '`NotionFS_find`' in prompt


def test_cloud_document_search_guidance_is_not_added_for_other_groups():
    prompt = _load_system_prompt_module().build_system_prompt({'kb'}, use_memory=False)

    assert '`FeishuWikiFS_search`' not in prompt
    assert '`NotionFS_search`' not in prompt


def test_cloud_document_and_web_search_guidance_can_be_enabled_together():
    prompt = _load_system_prompt_module().build_system_prompt(
        {'feishu', 'web_search'}, use_memory=False,
    )

    assert '`FeishuWikiFS_search`' in prompt
    assert 'web search guidance' in prompt
