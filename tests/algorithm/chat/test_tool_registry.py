import pytest

import lazyllm
from lazymind.chat.service.component.tool_registry import (
    DEFAULT_TOOLS,
    IMAGE_MARKDOWN_OUTPUT_APPENDIX,
    SKILL_TOOL_CONFIG,
    ToolConfig,
    _capability_is_denied,
    collect_system_prompt_appendices,
    filter_tools,
    get_all_tool_groups,
)


@pytest.mark.parametrize(
    'query',
    [
        '不要使用知识库', '别用知识库', '我不想用知识库', '无需查询知识库',
        '不能使用知识库', '禁止调用知识库', '忽略知识库', 'do not use knowledge base',
    ],
)
def test_capability_denial_recognizes_common_wording(query):
    assert _capability_is_denied(query, ('知识库', 'knowledge base')) is True


def test_capability_denial_does_not_leak_across_positive_clause():
    assert _capability_is_denied(
        '不用知识库A，可以使用知识库B', ('知识库',),
    ) is False


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


def test_wikipedia_and_web_search_remain_visible_when_tavily_is_configured():
    lazyllm.globals.config['dynamic_tool_auth'] = {'tavily': 'tavily-token'}
    lazyllm.globals['agentic_config'] = {'query': '怎么制作 AI 视频'}

    assert 'web_search' in _active_tool_names()
    assert 'wikipedia' in _active_tool_names()


def test_wikipedia_remains_available_without_web_provider():
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    lazyllm.globals['agentic_config'] = {'query': 'AI 视频是什么'}

    assert 'web_search' not in _active_tool_names()
    assert 'wikipedia' in _active_tool_names()


def test_registry_key_source_activates_function_tool():
    from lazymind.chat.engine.tools import kb_tmp_search
    from lazyllm.tools.agent.toolsManager import ToolManager

    assert not hasattr(kb_tmp_search, '__key_source__')
    assert 'temp_kb' not in _active_tool_names()
    assert _tool_group('temp_kb')['active'] is False

    temp_kb_cfg = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'temp_kb')
    manager = ToolManager([temp_kb_cfg.tool])
    assert manager.tools_description == []

    lazyllm.globals['agentic_config'] = {'files': ['tmp-a.md']}

    configs = filter_tools(DEFAULT_TOOLS)
    assert 'temp_kb' in {cfg.name for cfg in configs}
    manager = ToolManager([temp_kb_cfg.tool])
    assert [d['function']['name'] for d in manager.tools_description] == ['kb_tmp_search']
    group = _tool_group('temp_kb')
    assert group['active'] is True
    assert group['methods'] == [
        {
            'name': 'kb_tmp_search',
            'summary': 'Search attached temporary uploaded files with the temporary document retriever.',
        }
    ]


def test_catalog_exposes_modules_without_registering_module_gateways():
    from lazyllm.tools.agent.toolsManager import ToolManager

    groups = get_all_tool_groups()
    assert all(group['module'] for group in groups)
    calculator = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'calculator')
    manager = ToolManager([calculator.tool])
    names = {item['function']['name'] for item in manager.tools_description}
    assert names == {'calculator'}
    assert not any('utility' in name for name in names)


def test_shared_prompt_appendix_is_reused_and_deduplicated():
    configs = [
        cfg for cfg in DEFAULT_TOOLS
        if cfg.name in {'image_generator', 'image_editor', 'video_to_gif'}
    ]

    assert len(configs) == 3
    assert all(cfg.appendix_system_prompt is IMAGE_MARKDOWN_OUTPUT_APPENDIX for cfg in configs)
    collected = collect_system_prompt_appendices(configs)
    assert collected['output_contract'] == list(
        IMAGE_MARKDOWN_OUTPUT_APPENDIX['output_contract']
    )

    with_dynamic_attachment = collect_system_prompt_appendices(
        configs,
        extra_appendices=(IMAGE_MARKDOWN_OUTPUT_APPENDIX,),
    )
    assert with_dynamic_attachment == collected


def test_knowledge_base_priority_policy_is_not_globally_attached():
    kb_config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'kb')
    lazyllm.globals['agentic_config'] = {'filters': {}}
    default_appendices = collect_system_prompt_appendices([kb_config])
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'selected-kb'}}
    selected_appendices = collect_system_prompt_appendices([kb_config])

    assert not any(
        'Selected Knowledge Base Rules' in item
        for item in default_appendices.get('tool_policy', [])
    )
    assert any(
        'Selected Knowledge Base Rules' in item
        for item in selected_appendices['tool_policy']
    )


def test_conditional_prompt_appendix_provider_can_disable_itself():
    enabled = False
    config = ToolConfig(
        name='conditional', label='conditional', description='conditional',
        tool=lambda: None, module='utility',
        appendix_system_prompt=lambda: {'tool_policy': 'Enabled policy.'} if enabled else None,
    )

    assert collect_system_prompt_appendices([config]) == {}
    enabled = True
    assert collect_system_prompt_appendices([config]) == {'tool_policy': ['Enabled policy.']}


def test_search_tool_descriptions_distinguish_open_web_from_encyclopedic_lookup():
    web_config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'web_search')
    wikipedia_config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'wikipedia')
    policy = '\n'.join(collect_system_prompt_appendices([web_config])['tool_policy'])

    assert 'Wikipedia' not in policy
    assert 'current information' in web_config.tool['desc']
    assert 'stable encyclopedic background' in wikipedia_config.description_en
    assert 'not for news' in wikipedia_config.description_en


def test_prompt_appendix_deduplication_normalizes_whitespace():
    first = ToolConfig(
        name='first', label='first', description='first', tool=lambda: None, module='utility',
        appendix_system_prompt={'safety': 'Confirm before writing external data.'},
    )
    second = ToolConfig(
        name='second', label='second', description='second', tool=lambda: None, module='utility',
        appendix_system_prompt={'safety': ' Confirm  before\nwriting external data. '},
    )

    assert collect_system_prompt_appendices([first, second]) == {
        'safety': ['Confirm before writing external data.'],
    }


def test_cloud_files_use_nested_supplier_toolkits():
    from lazyllm.tools.agent.toolsManager import ToolManager

    config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'cloud_files')
    manager = ToolManager([config.tool])
    names = {item['function']['name'] for item in manager.tools_description}
    assert names == {'get_CloudFileToolkit_methods'}
    manager._tool_call['get_CloudFileToolkit_methods']({})
    names = {item['function']['name'] for item in manager.tools_description}
    assert not any(name.endswith('_read') for name in names)


def test_pick_first_valid_agent_tool_uses_group_config_description():
    lazyllm.globals.config['dynamic_tool_auth'] = {'bocha': 'bocha-token'}

    web_search_cfg = next(cfg for cfg in filter_tools(DEFAULT_TOOLS) if cfg.name == 'web_search')
    agent_tool = web_search_cfg.tool

    assert agent_tool['name'] == 'WebSearchToolkit'
    assert agent_tool['pick_first_valid'] is True
    assert agent_tool['tools']


def test_tool_catalog_localizes_display_fields_without_changing_runtime_description():
    zh_group = next(group for group in get_all_tool_groups('zh-CN') if group['name'] == 'web_search')
    en_group = next(group for group in get_all_tool_groups('en-US') if group['name'] == 'web_search')
    unsupported_group = next(group for group in get_all_tool_groups('fr-FR') if group['name'] == 'web_search')

    assert zh_group['label'] == '网页搜索'
    assert en_group['label'] == 'Web Search'
    assert en_group['description'] == (
        'Search the open internet for current information and broad research using the first '
        'available search provider.'
    )
    assert unsupported_group['label'] == zh_group['label']
    assert unsupported_group['description'] == zh_group['description']
    assert en_group['name'] == zh_group['name']
    assert en_group['methods'] == zh_group['methods']

    config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'web_search')
    agent_tool = config.tool
    assert agent_tool['desc']

    for group_config in [*DEFAULT_TOOLS, SKILL_TOOL_CONFIG]:
        assert group_config.label_en.strip()
        assert group_config.description_en.strip()
