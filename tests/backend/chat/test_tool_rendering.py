import json
import sys
from pathlib import Path


sys.path.insert(0, str(Path(__file__).resolve().parents[3] / 'algorithm'))

from lazymind.chat.service.component.tool_rendering import (  # noqa: E402
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
