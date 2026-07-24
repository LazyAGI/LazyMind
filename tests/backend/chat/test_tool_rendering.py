import json
import sys
from pathlib import Path


sys.path.insert(0, str(Path(__file__).resolve().parents[3] / 'algorithm'))

from lazymind.chat.service.component.tool_rendering import (  # noqa: E402
    _render_preview_template,
    _tool_call_frame_text,
    _tool_result_frame_text,
)


def test_lazy_tool_group_gateway_uses_group_expansion_preview_in_chinese():
    tool_call = {
        'id': 'call_1',
        'function': {
            'name': 'get_KBToolGroup_methods',
            'arguments': json.dumps({}),
        },
    }

    call_text, preview_value = _tool_call_frame_text(tool_call, 'zh')
    result_text = _tool_result_frame_text(
        {
            'id': 'call_1',
            'name': 'get_KBToolGroup_methods',
            'result': 'Activated tool group "KBToolGroup". Available tools: kb_search',
        },
        'zh',
        preview_value,
    )

    assert '正在展开**KBToolGroup**工具组。' in call_text
    assert '已经展开**KBToolGroup**工具组。' in result_text


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
