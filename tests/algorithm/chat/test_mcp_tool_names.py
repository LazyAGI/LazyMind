from types import SimpleNamespace

import pytest

from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool

from lazymind.chat.service.chat_service import (
    _add_browser_visual_tools,
    _agent_max_retries_for_mcp_tools,
    _browser_tool_name,
    _mcp_model_tool_name,
    _normalize_mcp_tool_names,
)


def _tool(name: str):
    def invoke():
        return name

    invoke.__name__ = name
    return invoke


def test_mcp_model_tool_name_replaces_registry_separators():
    assert _mcp_model_tool_name('browser.open') == 'browser_open'
    assert _mcp_model_tool_name('browser capture-current') == 'browser_capture_current'
    assert _mcp_model_tool_name('12.browser.open') == 'mcp_12_browser_open'


def test_normalize_mcp_tool_names_keeps_wire_callable_and_avoids_collisions():
    dotted = _tool('browser.open')
    colliding = _tool('browser-open')

    tools = _normalize_mcp_tool_names([dotted, colliding], 'lazymind-browser')

    assert tools[0].__name__ == 'browser_open'
    assert tools[0]._lazymind_mcp_original_name == 'browser.open'
    assert tools[0]() == 'browser.open'
    assert tools[1].__name__.startswith('browser_open_')
    assert tools[1].__name__ != tools[0].__name__
    assert len(tools[1].__name__) <= 64


def test_mcp_model_tool_name_caps_model_function_limit():
    alias = _mcp_model_tool_name('browser.' + ('very-long-name-' * 10))

    assert len(alias) == 64
    assert '.' not in alias


def test_add_browser_visual_tools_only_when_vlm_and_browser_screenshot_exist():
    screenshot = _tool('browser.screenshot')
    normalized = _normalize_mcp_tool_names([screenshot], 'lazymind-browser')

    augmented = _add_browser_visual_tools(normalized, vlm_available=True)

    assert [tool.__name__ for tool in augmented] == [
        'browser_screenshot',
        'browser_visual_inspect',
    ]
    assert _add_browser_visual_tools(
        [_tool('other')], vlm_available=True,
    )[0].__name__ == 'other'
    assert _add_browser_visual_tools(normalized, vlm_available=False) == normalized


def test_browser_mcp_tools_raise_agent_round_limit_to_200():
    browser_open = _tool('browser.open')
    normalized = _normalize_mcp_tool_names([browser_open], 'lazymind-browser')

    # ReactAgent adds the initial attempt to max_retries when reporting round_limit.
    assert _agent_max_retries_for_mcp_tools(20, normalized) == 199


def test_non_browser_mcp_tools_keep_configured_agent_round_limit():
    other = _tool('filesystem.read')
    normalized = _normalize_mcp_tool_names([other], 'filesystem')

    assert _agent_max_retries_for_mcp_tools(20, normalized) == 20
    assert _agent_max_retries_for_mcp_tools(20, []) == 20


@pytest.mark.parametrize('wire_name', ['browser.open', 'browser.screenshot', 'browser.click_intersection'])
def test_browser_identity_survives_real_lazyllm_adapter(wire_name):
    tool = generate_lazyllm_tool(None, SimpleNamespace(
        name=wire_name, description='Browser regression test.',
        inputSchema={'type': 'object', 'properties': {}},
    ))
    assert '.' not in tool.__name__
    tools = _normalize_mcp_tool_names([tool], 'lazymind-browser')

    assert _browser_tool_name(tool) == wire_name
    assert _agent_max_retries_for_mcp_tools(20, tools) == 199
    assert _agent_max_retries_for_mcp_tools(299, tools) == 299
    augmented = _add_browser_visual_tools(tools, vlm_available=True)
    assert ('browser_visual_inspect' in [t.__name__ for t in augmented]) == (
        wire_name == 'browser.screenshot'
    )
    assert _add_browser_visual_tools(tools, vlm_available=False) == tools


def test_unrelated_mcp_browser_names_do_not_activate_browser_policy():
    tools = _normalize_mcp_tool_names([_tool('browser.screenshot')], 'other-server')

    assert _browser_tool_name(tools[0]) == ''
    assert _agent_max_retries_for_mcp_tools(20, tools) == 20
    assert _add_browser_visual_tools(tools, vlm_available=True) == tools
