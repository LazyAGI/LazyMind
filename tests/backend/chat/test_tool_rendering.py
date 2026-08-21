import json
import re
import sys
from pathlib import Path

import pytest


sys.path.insert(0, str(Path(__file__).resolve().parents[3] / 'algorithm'))

from lazymind.chat.service.component import tool_rendering  # noqa: E402
from lazymind.chat.service.component.tool_rendering import (  # noqa: E402
    _render_preview_template,
    _tool_call_frame_text,
    _tool_result_frame_text,
)
from lazymind.chat.engine.tools.system_query import list_data_sources  # noqa: E402


_SEARCH_PROVIDER_TOOL_NAMES = {
    'search_provider_search': 'ExampleSearch_search',
    'search_provider_get_content': 'ExampleSearch_get_content',
    'search_provider_get_contents': 'ExampleSearch_get_contents',
    'search_provider_meta_search': 'ExampleSearch_meta_search',
    'search_provider_meta_catalog': 'ExampleSearch_meta_catalog',
}

_REGEX_TOOL_NAMES = {
    'regex:get_(.+)_methods': 'get_Example_methods',
    'regex:trigger_(.+)_workflow': 'trigger_example_workflow',
    'regex:WriterCreateToolkit_(.+)': 'WriterCreateToolkit_generate_outline',
    'regex:WriterRevisionToolkit_(.+)': 'WriterRevisionToolkit_plan_revision',
}


def _concrete_template_tool_name(template_key):
    return (
        _SEARCH_PROVIDER_TOOL_NAMES.get(template_key)
        or _REGEX_TOOL_NAMES.get(template_key)
        or template_key
    )


def _template_cases():
    groups = (
        ('call-en', tool_rendering._TOOL_CALL_PREVIEW_TEMPLATES, {'ok': True, 'value': {}}),
        ('call-zh', tool_rendering._ZH_TOOL_CALL_PREVIEW_TEMPLATES, {'ok': True, 'value': {}}),
        (
            'success-en',
            tool_rendering._TOOL_RESULT_PREVIEW_TEMPLATES,
            {'ok': True, 'value': {'outcome': 'ready', 'reason': 'accepted'}},
        ),
        (
            'success-zh',
            tool_rendering._ZH_TOOL_RESULT_PREVIEW_TEMPLATES,
            {'ok': True, 'value': {'outcome': 'ready', 'reason': 'accepted'}},
        ),
        (
            'failure-en',
            tool_rendering._TOOL_RESULT_FAILURE_TEMPLATES,
            {'ok': False, 'value': 'sample failure'},
        ),
        (
            'failure-zh',
            tool_rendering._ZH_TOOL_RESULT_FAILURE_TEMPLATES,
            {'ok': False, 'value': 'sample failure'},
        ),
        (
            'approval-en',
            tool_rendering._TOOL_RESULT_APPROVAL_TEMPLATES,
            {'ok': False, 'value': 'sample approval', 'needs_approval': True},
        ),
        (
            'approval-zh',
            tool_rendering._ZH_TOOL_RESULT_APPROVAL_TEMPLATES,
            {'ok': False, 'value': 'sample approval', 'needs_approval': True},
        ),
    )
    for group_name, templates, result in groups:
        for template_key in templates:
            if group_name.startswith('success'):
                if template_key == 'search_provider_search':
                    result = {'ok': True, 'value': [{'title': 'sample result', 'url': 'https://example.test'}]}
                elif template_key == 'calculator':
                    result = {'ok': True, 'value': '42'}
                else:
                    result = {
                        'ok': True,
                        'value': {
                            'outcome': 'ready',
                            'reason': 'accepted',
                            'skill_key': 'sample-skill',
                            'total': 3,
                            'total_count': 3,
                        },
                    }
            yield pytest.param(
                templates,
                template_key,
                result,
                id=f'{group_name}:{template_key}',
            )


@pytest.mark.parametrize(('templates', 'template_key', 'result'), list(_template_cases()))
def test_every_configured_render_template_resolves(templates, template_key, result):
    rendered = _render_preview_template(
        _concrete_template_tool_name(template_key),
        'sample value',
        templates,
        'unexpected fallback',
        result,
    )

    assert rendered != 'unexpected fallback\n'
    assert rendered.endswith('\n')
    assert re.search(r'\{[^{}]+\}', rendered) is None


@pytest.mark.parametrize(
    ('templates', 'fallback'),
    [
        ({}, tool_rendering._TOOL_CALL_FALLBACK_TEMPLATE),
        ({}, tool_rendering._ZH_TOOL_CALL_FALLBACK_TEMPLATE),
        ({}, tool_rendering._TOOL_RESULT_FALLBACK_TEMPLATE),
        ({}, tool_rendering._ZH_TOOL_RESULT_FALLBACK_TEMPLATE),
        ({}, tool_rendering._TOOL_RESULT_FAILURE_FALLBACK_TEMPLATE),
        ({}, tool_rendering._ZH_TOOL_RESULT_FAILURE_FALLBACK_TEMPLATE),
        ({}, tool_rendering._TOOL_RESULT_APPROVAL_FALLBACK_TEMPLATE),
        ({}, tool_rendering._ZH_TOOL_RESULT_APPROVAL_FALLBACK_TEMPLATE),
    ],
)
def test_every_render_fallback_template_resolves(templates, fallback):
    rendered = _render_preview_template(
        'unknown_tool',
        'sample value',
        templates,
        fallback,
        {'ok': False, 'value': 'sample failure'},
    )

    assert rendered.endswith('\n')
    assert re.search(r'\{[^{}]+\}', rendered) is None


def test_render_template_maps_have_matching_english_and_chinese_keys():
    pairs = (
        (
            tool_rendering._TOOL_CALL_PREVIEW_TEMPLATES,
            tool_rendering._ZH_TOOL_CALL_PREVIEW_TEMPLATES,
        ),
        (
            tool_rendering._TOOL_RESULT_PREVIEW_TEMPLATES,
            tool_rendering._ZH_TOOL_RESULT_PREVIEW_TEMPLATES,
        ),
        (
            tool_rendering._TOOL_RESULT_FAILURE_TEMPLATES,
            tool_rendering._ZH_TOOL_RESULT_FAILURE_TEMPLATES,
        ),
        (
            tool_rendering._TOOL_RESULT_APPROVAL_TEMPLATES,
            tool_rendering._ZH_TOOL_RESULT_APPROVAL_TEMPLATES,
        ),
    )

    for english, chinese in pairs:
        assert english.keys() == chinese.keys()


def test_render_profiles_keep_localized_states_together():
    for profile in tool_rendering.TOOL_RENDER_PROFILES.values():
        for state in ('call', 'success', 'failure', 'approval'):
            localized = profile.get(state)
            if localized is not None:
                assert localized.keys() == {'en', 'zh'}
                assert localized['en'].strip()
                assert localized['zh'].strip()


@pytest.mark.parametrize(
    ('tool_name', 'arguments', 'expected_call', 'expected_result'),
    [
        (
            'MemoryTools_read_memory',
            {'target': 'preference'},
            '正在读取 **preference** 记忆文档。',
            '已成功读取 **preference** 记忆文档。',
        ),
        (
            'LocalFileToolkit_read',
            {'filepath': '/workspace/report.md'},
            '正在读取本地文件 **/workspace/report.md**。',
            '已成功读取本地文件 **/workspace/report.md**。',
        ),
        (
            'create_schedule',
            {'name': '每日摘要'},
            '正在创建定时任务 **每日摘要**。',
            '已成功创建定时任务 **每日摘要**。',
        ),
        (
            'read_user_attachment',
            {'filename': 'notes.txt'},
            '正在读取附件 **notes.txt**。',
            '已成功读取附件 **notes.txt**。',
        ),
        (
            'FeishuWikiFS_search',
            {'query': '项目计划'},
            '正在飞书文档中搜索 **项目计划**。',
            '已成功加载 **项目计划** 的飞书搜索结果。',
        ),
        (
            'GoogleDriveFS_read',
            {'path': '/project/spec'},
            '正在读取 Google Drive 文件 **/project/spec**。',
            '已成功读取 Google Drive 文件 **/project/spec**。',
        ),
        (
            'NotionFS_create_document',
            {'title': '方案', 'parent': '/项目'},
            '正在创建 Notion 页面 **方案**。',
            '已成功创建 Notion 页面 **方案**。',
        ),
        (
            'image_generator',
            {'prompt': 'a diagram'},
            '正在生成图片。',
            '已成功生成图片。',
        ),
        (
            'WriterCreateToolkit_generate_outline',
            {'writing_task_json': '{}', 'writing_context_json': '{}'},
            '正在执行文档创建步骤。',
            '文档创建步骤已完成。',
        ),
        (
            'WriterRevisionToolkit_apply_string_replace',
            {'markdown_document': '# Old', 'string_replace_set_json': '{}'},
            '正在执行文档修订步骤。',
            '文档修订步骤已完成。',
        ),
    ],
)
def test_user_visible_tool_families_do_not_fall_back_to_generic_copy(
    tool_name,
    arguments,
    expected_call,
    expected_result,
):
    tool_call = {
        'id': f'call-{tool_name}',
        'function': {'name': tool_name, 'arguments': json.dumps(arguments)},
    }
    call_text, preview_value = _tool_call_frame_text(tool_call, 'zh')
    result_text = _tool_result_frame_text(
        {
            'id': f'call-{tool_name}',
            'name': tool_name,
            'result': {'ok': True, 'value': {'status': 'ok'}},
        },
        'zh',
        preview_value,
    )

    assert expected_call in call_text
    assert expected_result in result_text
    assert '正在调用工具' not in call_text
    assert f'工具 **{tool_name}** 已调用完成' not in result_text


def test_default_registered_tools_have_explicit_rendering_and_valid_argument_selectors():
    from lazyllm.tools.agent.toolsManager import ToolManager
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS

    template_maps = (
        tool_rendering._TOOL_CALL_PREVIEW_TEMPLATES,
        tool_rendering._TOOL_RESULT_PREVIEW_TEMPLATES,
        tool_rendering._TOOL_RESULT_FAILURE_TEMPLATES,
    )
    missing_templates = []
    invalid_selectors = []

    for config in DEFAULT_TOOLS:
        ensure_lazyllm_tool_docs([config.tool])
        manager = ToolManager([config.tool])
        expanded_gateways = set()
        while True:
            gateways = [
                name for name in manager._tool_call
                if name.startswith('get_')
                and name.endswith('_methods')
                and name not in expanded_gateways
            ]
            if not gateways:
                break
            for gateway in gateways:
                expanded_gateways.add(gateway)
                manager._tool_call[gateway]({})

        for tool_name, tool in manager._tool_call.items():
            render_name, _ = tool_rendering._render_tool_context(tool_name)
            for templates in template_maps:
                exact = tool_rendering._resolve_tool_key(render_name, templates)
                regex, _ = tool_rendering._resolve_tool_key_regex(render_name, templates)
                if exact is None and regex is None:
                    missing_templates.append((config.name, tool_name))
                    break

            selector = tool_rendering._resolve_tool_key(
                render_name,
                tool_rendering._REPRESENTATIVE_TOOL_ARGUMENTS,
            )
            if not selector or tool_name in expanded_gateways:
                continue
            roots = {
                part.split('.', 1)[0]
                for part in re.split(r'\s*(?:/|<->)\s*', selector)
            }
            argument_names = set((getattr(tool, 'args', {}) or {}).keys())
            if roots.isdisjoint(argument_names):
                invalid_selectors.append((config.name, tool_name, selector, argument_names))

    assert missing_templates == []
    assert invalid_selectors == []


def test_lazy_tool_group_gateway_uses_group_expansion_preview_in_chinese():
    tool_call = {
        'id': 'call_1',
        'function': {
            'name': 'get_KBToolkit_methods',
            'arguments': json.dumps({}),
        },
    }

    call_text, preview_value = _tool_call_frame_text(tool_call, 'zh')
    result_text = _tool_result_frame_text(
        {
            'id': 'call_1',
            'name': 'get_KBToolkit_methods',
            'result': 'Activated Toolkit "KBToolkit". Available tools: kb_search',
        },
        'zh',
        preview_value,
    )

    assert '正在展开**KBToolkit**工具箱。' in call_text
    assert '已经展开**KBToolkit**工具箱。' in result_text


def test_list_data_sources_preview_hides_empty_keyword_and_internal_ids():
    tool_call = {
        'id': 'call-data-sources',
        'function': {'name': 'list_data_sources', 'arguments': '{}'},
    }
    call_text, preview_value = _tool_call_frame_text(tool_call, 'zh')
    result_text = _tool_result_frame_text(
        {
            'id': 'call-data-sources',
            'name': 'list_data_sources',
            'result': {
                'provider_groups': [
                    {'group_id': '593b933b257a492b9098eb771c6d9c06'}
                ],
            },
        },
        'zh',
        preview_value,
    )

    assert '正在检查已配置的数据源服务。' in call_text
    assert '已成功加载数据源服务列表。' in result_text
    call_preview = call_text.split('</tp>', 1)[0]
    result_preview = result_text.split('</trp>', 1)[0]
    assert 'the current item' not in call_preview
    assert '593b933b257a492b9098eb771c6d9c06' not in result_preview


def test_list_data_sources_description_excludes_tool_catalog_questions():
    description = list_data_sources.__doc__ or ''

    assert 'Do not call it to answer which tools' in description
    assert 'does not provide a tool catalog' in description


def test_instance_toolkit_method_with_class_prefix_uses_kb_template():
    tool_call = {
        'id': 'call-kb',
        'function': {
            'name': 'KBToolkit_kb_search',
            'arguments': json.dumps({'query': 'LazyMind'}),
        },
    }

    call_text, preview_value = _tool_call_frame_text(tool_call, 'zh')
    result_text = _tool_result_frame_text(
        {
            'id': 'call-kb',
            'name': 'KBToolkit_kb_search',
            'result': json.dumps({'total': 1, 'data': [{'text': 'matched'}]}),
        },
        'zh',
        preview_value,
    )

    assert '正在知识库中检索与 **LazyMind** 相关的知识。' in call_text
    assert '知识库检索完成，共找到 **1** 条相关内容。' in result_text


def test_nested_cloud_supplier_method_with_class_prefix_uses_supplier_template():
    tool_call = {
        'id': 'call-notion',
        'function': {
            'name': 'NotionFS_read',
            'arguments': json.dumps({'path': '/project/spec'}),
        },
    }

    call_text, _ = _tool_call_frame_text(tool_call, 'en')

    assert 'Reading Notion content from **/project/spec**.' in call_text


def test_url_fetch_preview_uses_single_url():
    tool_call = {
        'id': 'call-url',
        'function': {
            'name': 'url_fetch',
            'arguments': json.dumps({'url': 'https://example.test/page'}),
        },
    }

    call_text, _ = _tool_call_frame_text(tool_call, 'zh')

    assert '正在读取网页 **https://example.test/page**' in call_text


def test_google_drive_search_uses_provider_specific_preview():
    tool_call = {
        'id': 'call_drive',
        'function': {
            'name': 'GoogleDriveFS_search',
            'arguments': json.dumps({'keywords': ['release', 'owner']}),
        },
    }

    call_text, preview_value = _tool_call_frame_text(tool_call, 'zh')
    result_text = _tool_result_frame_text(
        {
            'id': 'call_drive',
            'name': 'GoogleDriveFS_search',
            'result': [{'title': 'Release Plan'}],
        },
        'zh',
        preview_value,
    )

    assert '正在 Google Drive 中搜索' in call_text
    assert '已查询到' in result_text
    assert 'Google Drive 搜索结果' in result_text


@pytest.mark.parametrize(
    ('tool_name', 'brand'),
    [
        ('GoogleSearch_search', 'Google'),
        ('BingSearch_search', 'Bing'),
        ('BochaSearch_search', 'Bocha'),
        ('TavilySearch_search', 'Tavily'),
        ('ArxivSearch_search', 'Arxiv'),
        ('SciverseSearch_search', 'Sciverse'),
        ('WikipediaToolkit_search', 'Wikipedia'),
    ],
)
def test_search_provider_preview_uses_query_and_business_result_count(tool_name, brand):
    tool_call = {
        'id': f'call-{brand}',
        'function': {
            'name': tool_name,
            'arguments': json.dumps({'query': 'DeepSeek'}),
        },
    }

    call_text, preview_value = _tool_call_frame_text(tool_call, 'zh')
    result_text = _tool_result_frame_text(
        {
            'id': f'call-{brand}',
            'name': tool_name,
            'result': {
                'ok': True,
                'value': [
                    {'title': 'DeepSeek', 'url': 'https://example.test/deepseek'},
                    {'title': 'DeepSeek-V2', 'url': 'https://example.test/deepseek-v2'},
                ],
            },
        },
        'zh',
        preview_value,
    )

    assert f'正在使用 **{brand}** 搜索 **DeepSeek**。' in call_text
    assert f'已找到 **DeepSeek** 的 **2** 条 **{brand}** 搜索结果。' in result_text


def test_search_result_count_excludes_tavily_summary_item():
    result_text = _tool_result_frame_text(
        {
            'id': 'call-tavily-summary',
            'name': 'TavilySearch_search',
            'result': {
                'ok': True,
                'value': [
                    {'title': 'Result', 'url': 'https://example.test/result'},
                    {'title': 'summary', 'url': '', 'snippet': 'Synthesized answer'},
                ],
            },
        },
        'en',
        'DeepSeek',
    )

    assert 'returned **1** results' in result_text


def test_search_provider_meta_search_uses_structured_total_count():
    result_text = _tool_result_frame_text(
        {
            'id': 'call-meta-search',
            'name': 'SciverseSearch_meta_search',
            'result': {'ok': True, 'value': {'total_count': 7, 'items': []}},
        },
        'zh',
        'machine learning',
    )

    assert '已找到 **7** 条 **Sciverse** 元数据结果。' in result_text


def test_calculator_preview_uses_wrapped_business_result_instead_of_expression():
    tool_call = {
        'id': 'call-calculator',
        'function': {
            'name': 'calculator',
            'arguments': json.dumps({'expression': '17*23'}),
        },
    }
    _, preview_value = _tool_call_frame_text(tool_call, 'zh')
    result_text = _tool_result_frame_text(
        {
            'id': 'call-calculator',
            'name': 'calculator',
            'result': {'ok': True, 'value': '391'},
        },
        'zh',
        preview_value,
    )
    result_preview = result_text.split('</trp>', 1)[0]

    assert '已计算完成，结果为 **391**' in result_preview
    assert '17*23' not in result_preview


def test_install_skill_preview_uses_wrapped_skill_key_instead_of_source_url():
    result_text = _tool_result_frame_text(
        {
            'id': 'call-install',
            'name': 'SkillManagementToolkit_install_skill',
            'result': {
                'ok': True,
                'value': {'skill_key': 'example-skill'},
            },
        },
        'zh',
        'https://github.com/example/skill',
    )
    result_preview = result_text.split('</trp>', 1)[0]

    assert '已成功安装 **example-skill** 技能' in result_preview
    assert 'github.com' not in result_preview


def test_kb_empty_preview_uses_wrapped_business_result():
    result_text = _tool_result_frame_text(
        {
            'id': 'call-kb-empty',
            'name': 'KBToolkit_kb_search',
            'result': {
                'ok': True,
                'value': {'total': 0, 'data': []},
            },
        },
        'zh',
        '没有匹配内容的问题',
    )

    assert '知识库搜索已完成，但没有找到匹配结果' in result_text
    assert '已查询到' not in result_text


@pytest.mark.parametrize(
    ('tool_name', 'arguments', 'expected_call', 'expected_result'),
    [
        (
            'KBToolkit_kb_search',
            {'query': 'LazyMind'},
            '正在知识库中检索与 **LazyMind** 相关的知识。',
            '知识库检索完成，共找到 **3** 条相关内容。',
        ),
        (
            'KBToolkit_kb_tmp_search',
            {'query': '附件内容'},
            '正在附件中检索与 **附件内容** 相关的内容。',
            '附件检索完成，共找到 **3** 条相关内容。',
        ),
        (
            'KBToolkit_kb_get_parent_node',
            {'node_id': 'node-parent'},
            '正在加载 **node-parent** 的相关上下文。',
            '已加载 **3** 条上级上下文。',
        ),
        (
            'KBToolkit_kb_get_window_nodes',
            {'node_id': 'node-window', 'before': 2, 'after': 2},
            '正在扩展 **node-window** 附近的相关片段。',
            '已加载 **3** 条邻近知识库片段。',
        ),
        (
            'KBToolkit_kb_keyword_search',
            {'keyword': '工具调用'},
            '正在目标文档中搜索关键词 **工具调用**。',
            '关键词 **工具调用** 检索完成，共找到 **3** 条文档片段。',
        ),
    ],
)
def test_kb_tools_use_input_for_calls_and_structured_total_for_success(
    tool_name,
    arguments,
    expected_call,
    expected_result,
):
    tool_call = {
        'id': f'call-{tool_name}',
        'function': {'name': tool_name, 'arguments': json.dumps(arguments)},
    }
    call_text, preview_value = _tool_call_frame_text(tool_call, 'zh')
    result_text = _tool_result_frame_text(
        {
            'id': f'call-{tool_name}',
            'name': tool_name,
            'result': {'ok': True, 'value': {'total': 3, 'data': []}},
        },
        'zh',
        preview_value,
    )

    assert expected_call in call_text
    assert expected_result in result_text


@pytest.mark.parametrize(
    ('tool_name', 'expected'),
    [
        ('KBToolkit_kb_search', '知识库搜索已完成，但没有找到匹配结果'),
        ('KBToolkit_kb_tmp_search', '附件检索已完成，但没有找到匹配结果'),
        ('KBToolkit_kb_get_parent_node', '未找到请求节点的上级上下文'),
        ('KBToolkit_kb_get_window_nodes', '未找到附近的知识库片段'),
        ('KBToolkit_kb_keyword_search', '关键词搜索已完成，但没有找到匹配的文档片段'),
    ],
)
def test_kb_empty_results_take_precedence_over_success_templates(tool_name, expected):
    result_text = _tool_result_frame_text(
        {
            'id': f'call-empty-{tool_name}',
            'name': tool_name,
            'result': {'ok': True, 'value': {'total': 0, 'data': []}},
        },
        'zh',
        'sample input',
    )

    assert expected in result_text
    assert '共找到 **0** 条' not in result_text


def test_kb_total_normalizes_json_string_and_nested_result_value():
    result_text = _tool_result_frame_text(
        {
            'id': 'call-kb-nested',
            'name': 'KBToolkit_kb_search',
            'result': {
                'ok': True,
                'value': json.dumps({'result': {'total': 4, 'data': []}}),
            },
        },
        'en',
        'LazyMind',
    )

    assert 'completed with **4** relevant items' in result_text


def test_plugin_preflight_result_renders_outcome_and_reason_in_chinese():
    reason = 'The request can be answered directly without a multi-stage workflow.'
    result_text = _tool_result_frame_text(
        {
            'id': 'call-writer',
            'name': 'trigger_writer_workflow',
            'result': json.dumps({
                'outcome': 'not_applicable',
                'reason': reason,
            }),
        },
        'zh',
    )

    assert '工作流初始化已完成，结果是 **not_applicable**' in result_text
    assert f'原因是 **{reason}**' in result_text


def test_result_template_supports_generic_dotted_paths_for_nested_json_fields():
    result = json.dumps({
        'result': json.dumps({
            'outcome': 'custom_outcome',
            'reason': 'custom explanation',
            'details': {'count': 3},
        }),
    })

    rendered = _render_preview_template(
        'custom_tool',
        '',
        {
            'custom_tool': (
                'Outcome {result.outcome}; reason {result.reason}; '
                'count {result.details.count}.'
            ),
        },
        'fallback',
        result,
    )

    assert rendered == (
        'Outcome **custom_outcome**; reason **custom explanation**; count **3**.\n'
    )


def test_plugin_preflight_result_supports_ready_status_payload():
    result_text = _tool_result_frame_text(
        {
            'id': 'call-writer',
            'name': 'trigger_writer_workflow',
            'result': {
                'status': 'ready',
                'outcome': 'ready',
                'reason': 'The user explicitly requested this plugin.',
            },
        },
        'en',
    )

    assert 'Workflow initialization completed. Result: **ready**.' in result_text
    assert 'Reason: **The user explicitly requested this plugin.**' in result_text


def test_workflow_failure_template_handles_canonical_failure():
    result_text = _tool_result_frame_text(
        {
            'id': 'call-workflow',
            'name': 'trigger_test_workflow',
            'result': {
                'ok': False,
                'value': 'workflow or revision was not found',
            },
        },
        'zh',
    )

    assert '工作流初始化失败，结果是 **failed**' in result_text
    assert 'workflow or revision was not found' in result_text


def test_workflow_success_template_normalizes_json_string_value():
    result_text = _tool_result_frame_text(
        {
            'id': 'call-workflow-json',
            'name': 'trigger_writer_workflow',
            'result': {
                'ok': True,
                'value': json.dumps({'outcome': 'queued', 'reason': 'accepted'}),
            },
        },
        'en',
    )

    assert 'Result: **queued**. Reason: **accepted**.' in result_text


def test_workflow_success_template_normalizes_nested_result_value():
    result_text = _tool_result_frame_text(
        {
            'id': 'call-workflow-nested',
            'name': 'trigger_writer_workflow',
            'result': {
                'ok': True,
                'value': {
                    'result': {'outcome': 'queued', 'reason': 'accepted'},
                },
            },
        },
        'en',
    )

    assert 'Result: **queued**. Reason: **accepted**.' in result_text


def test_permission_failure_uses_canonical_failure_rendering():
    result_text = _tool_result_frame_text(
        {
            'id': 'call-delete',
            'name': 'delete_file',
            'result': {
                'ok': False,
                'value': 'Deleting /workspace/a.txt requires approval.',
                'needs_approval': True,
            },
        },
        'en',
    )

    assert 'Please review the confirmation note' in result_text
