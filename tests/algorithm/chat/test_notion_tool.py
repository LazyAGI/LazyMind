from lazymind.chat.engine.prompts.guidance import CLOUD_DOCUMENT_GUIDANCE
from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS, _extract_methods


def test_notion_tool_group_exposes_stable_read_only_contract():
    group = next(item for item in DEFAULT_TOOLS if item.name == 'notion')

    assert group.instance.__class__.__name__ == 'NotionFS'
    assert [method['name'] for method in _extract_methods(group.instance)] == [
        'ls',
        'search',
        'info',
        'exists',
        'read',
        'read_file',
        'resolve_link',
        'read_with_references',
    ]


def test_cloud_document_guidance_distinguishes_notion_list_and_search():
    assert '`ls` with `path="/"`' in CLOUD_DOCUMENT_GUIDANCE
    assert 'never use `*` as a wildcard' in CLOUD_DOCUMENT_GUIDANCE
    assert 'empty result does not mean authentication failed' in CLOUD_DOCUMENT_GUIDANCE
