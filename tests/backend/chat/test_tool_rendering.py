import json
import sys
from pathlib import Path


sys.path.insert(0, str(Path(__file__).resolve().parents[3] / 'algorithm'))

from lazymind.chat.service.component.tool_rendering import (  # noqa: E402
    _render_preview_template,
    _tool_call_frame_text,
    _tool_result_frame_text,
)
from lazymind.chat.engine.tools.system_query import list_data_sources  # noqa: E402


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
                'success': True,
                'tool': 'list_data_sources',
                'result': {
                    'provider_groups': [
                        {'group_id': '593b933b257a492b9098eb771c6d9c06'}
                    ],
                },
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
            'result': json.dumps({'data': [{'text': 'matched'}]}),
        },
        'zh',
        preview_value,
    )

    assert '正在知识库中检索与 **LazyMind** 相关的知识。' in call_text
    assert '知识库检索' in result_text


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


def test_url_fetch_batch_preview_shows_count_and_sample_urls():
    tool_call = {
        'id': 'call-batch',
        'function': {
            'name': 'url_fetch',
            'arguments': json.dumps({
                'urls': [
                    'https://example.test/1',
                    'https://example.test/2',
                    'https://example.test/3',
                ],
            }),
        },
    }

    call_text, _ = _tool_call_frame_text(tool_call, 'zh')

    assert '正在并发读取 **3** 个网页' in call_text
    assert 'https://example.test/1' in call_text
    assert '另有 1 个' in call_text

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


def test_plugin_preflight_result_renders_outcome_and_reason_in_chinese():
    reason = 'The request can be answered directly without a multi-stage workflow.'
    result_text = _tool_result_frame_text(
        {
            'id': 'call-writer',
            'name': 'trigger_writer_plugin',
            'result': json.dumps({
                'outcome': 'not_applicable',
                'reason': reason,
            }),
        },
        'zh',
    )

    assert '插件启动检查已完成，结果是 **not_applicable**' in result_text
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
            'name': 'trigger_writer_plugin',
            'result': {
                'status': 'ready',
                'outcome': 'ready',
                'reason': 'The user explicitly requested this plugin.',
            },
        },
        'en',
    )

    assert 'Plugin preflight completed. Result: **ready**.' in result_text
    assert 'Reason: **The user explicitly requested this plugin.**' in result_text
