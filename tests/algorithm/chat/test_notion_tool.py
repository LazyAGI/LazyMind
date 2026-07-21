from lazymind.chat.service.component.tool_registry import (
    CLOUD_DOCUMENT_TOOL_POLICY_APPENDIX,
    DEFAULT_TOOLS,
    _extract_methods,
)


def test_cloud_file_toolkit_exposes_stable_notion_read_contract():
    group = next(item for item in DEFAULT_TOOLS if item.name == 'cloud_files')
    notion = next(
        item for item in group.tool['tools'] if item.__class__.__name__ == 'NotionFS'
    )
    methods = [method['name'] for method in _extract_methods(notion)]

    assert {'ls', 'search', 'info', 'exists', 'read', 'read_file'} <= set(methods)
    assert {'resolve_link', 'read_with_references'} <= set(methods)


def test_cloud_document_guidance_distinguishes_notion_list_and_search():
    guidance = '\n'.join(CLOUD_DOCUMENT_TOOL_POLICY_APPENDIX['tool_policy'])

    assert 'Notion URL' in guidance
    assert 'Notion file-system tools first' in guidance
    assert 'reading with references' in guidance
    assert 'Do not fall back to generic URL fetching' in guidance
