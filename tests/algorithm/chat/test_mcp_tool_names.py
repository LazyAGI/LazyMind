import pytest

from lazyllm.tools import get_tool_runtime_metadata
from lazyllm.tools.agent.toolsManager import ToolManager
from lazymind.chat.service import chat_service


def _tools(names, *, server_id='lazymind-browser', server_name='lazymind-browser'):
    server = {'id': server_id, 'name': server_name, 'url': 'https://mcp.example/tools',
              'transport': 'streamable-http'}
    capability = {'status': 'ready', 'tools': [
        {'tool_name': name, 'description': 'Test tool.', 'input_schema': {'type': 'object', 'properties': {}}}
        for name in names
    ]}
    return chat_service._mcp_tools_for_preview(server, capability, 'system')


@pytest.mark.parametrize(('server_id', 'server_name', 'wire', 'expected'), [
    ('lazymind-knowledge', 'lazymind-knowledge', 'knowledge.list', 'mcp_kbmanager_list'),
    ('lazymind-knowledge', 'changed display label', 'knowledge.document.list', 'mcp_kbmanager_document_list'),
    ('lazymind-browser', 'lazymind-browser', 'browser.open', 'mcp_browser_open'),
    ('notion-id', 'Notion', 'search', 'mcp_notion_search'),
])
def test_preview_uses_readable_names_and_keeps_wire_identity(server_id, server_name, wire, expected):
    tool, = _tools([wire], server_id=server_id, server_name=server_name)
    assert tool.__name__ == expected
    assert tool.__mcp_tool_name__ == wire
    assert get_tool_runtime_metadata(tool).tool_origin == server_id
    assert list(ToolManager([tool]).atomic_tool_catalog()) == [expected]


def test_normalized_tool_collisions_are_rejected():
    with pytest.raises(ValueError, match='MCP tool name|Duplicate tool name'):
        ToolManager(_tools(['browser.foo.bar', 'browser.foo-bar']))


@pytest.mark.parametrize('name', ['', '---', 'browser.' + 'very-long-name-' * 10])
def test_preview_rejects_invalid_names_instead_of_hashing(name):
    with pytest.raises(ValueError, match='MCP tool name'):
        _tools([name])


def test_browser_visual_tools_use_original_method_and_server_identity():
    tools = _tools(['browser.screenshot'])
    augmented = chat_service._add_browser_visual_tools(tools, vlm_available=True)
    assert [tool.__name__ for tool in augmented] == ['mcp_browser_screenshot', 'browser_visual_inspect']
    assert chat_service._add_browser_visual_tools(tools, vlm_available=False) == tools
    assert chat_service._add_browser_visual_tools([], vlm_available=True) == []


@pytest.mark.parametrize('wire', ['browser.open', 'browser.screenshot', 'browser.click_intersection'])
def test_browser_policy_survives_readable_names(wire):
    tools = _tools([wire])
    assert chat_service._browser_tool_name(tools[0]) == wire
    assert chat_service._agent_max_retries_for_mcp_tools(20, tools) == 199
    assert chat_service._agent_max_retries_for_mcp_tools(299, tools) == 299


def test_display_name_cannot_turn_another_server_into_builtin_browser():
    tools = _tools(['browser.screenshot'], server_id='external-browser', server_name='lazymind-browser')
    assert chat_service._browser_tool_name(tools[0]) == ''
    assert chat_service._agent_max_retries_for_mcp_tools(20, tools) == 20
    assert chat_service._add_browser_visual_tools(tools, vlm_available=True) == tools
