import pytest

import lazyllm
from lazymind.chat.service.component.tool_registry import (
    DEFAULT_TOOLS,
    ToolGroupConfig,
    build_agent_tools,
    filter_tools,
    get_all_tool_groups,
)


@pytest.fixture(autouse=True)
def reset_dynamic_tool_auth():
    old_auth = lazyllm.globals.config['dynamic_tool_auth']
    old_agentic_config = lazyllm.globals.get('agentic_config')
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    lazyllm.globals['agentic_config'] = {}
    try:
        yield
    finally:
        lazyllm.globals.config['dynamic_tool_auth'] = old_auth
        lazyllm.globals['agentic_config'] = old_agentic_config or {}


def _active_tool_names() -> set[str]:
    return {cfg.name for cfg in filter_tools(DEFAULT_TOOLS)}


def _tool_group(name: str) -> dict:
    return next(group for group in get_all_tool_groups() if group['name'] == name)


def test_web_search_requires_at_least_one_search_key():
    assert 'web_search' not in _active_tool_names()
    assert _tool_group('web_search')['active'] is False

    lazyllm.globals.config['dynamic_tool_auth'] = {'bing': 'bing-token'}

    assert 'web_search' in _active_tool_names()
    group = _tool_group('web_search')
    assert group['active'] is True
    assert any(method['name'] == 'BingSearch' and method['active'] for method in group['methods'])


def test_registry_key_source_activates_function_tool():
    from lazymind.chat.engine.tools import kb_tmp_search
    from lazyllm.tools.agent.toolsManager import ToolManager

    assert not hasattr(kb_tmp_search, '__key_source__')
    assert 'temp_kb' not in _active_tool_names()
    assert _tool_group('temp_kb')['active'] is False

    temp_kb_cfg = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'temp_kb')
    manager = ToolManager(build_agent_tools([temp_kb_cfg]))
    assert manager.tools_description == []

    lazyllm.globals['agentic_config'] = {'files': ['tmp-a.md']}

    configs = filter_tools(DEFAULT_TOOLS)
    assert 'temp_kb' in {cfg.name for cfg in configs}
    manager = ToolManager(build_agent_tools([temp_kb_cfg]))
    assert [d['function']['name'] for d in manager.tools_description] == ['kb_tmp_search']
    group = _tool_group('temp_kb')
    assert group['active'] is True
    assert group['methods'] == [
        {
            'name': 'kb_tmp_search',
            'summary': 'Search attached temporary uploaded files with the temporary document retriever.',
        }
    ]


def test_pick_first_valid_agent_tool_uses_group_config_description():
    lazyllm.globals.config['dynamic_tool_auth'] = {'bocha': 'bocha-token'}

    web_search_cfg = next(cfg for cfg in filter_tools(DEFAULT_TOOLS) if cfg.name == 'web_search')
    agent_tool = build_agent_tools([web_search_cfg])[0]

    assert agent_tool['name'] == 'web_search'
    assert agent_tool['desc'] == web_search_cfg.description
    assert agent_tool['pick_first_valid'] is True
    assert agent_tool['tools'] == web_search_cfg.instance


def test_pick_first_valid_requires_sequence_instance():
    with pytest.raises(TypeError, match='pick_first_valid'):
        ToolGroupConfig(
            name='broken',
            label='Broken',
            description='Broken pick-first-valid group',
            instance=None,
            pick_first_valid=True,
        )
