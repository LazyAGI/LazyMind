import json
import re

import pytest

from lazymind.chat.service.component.tool_rendering import _tool_result_frame_text


@pytest.mark.parametrize('language', ['zh', 'en'])
@pytest.mark.parametrize('encoding', ['direct', 'json', 'nested'])
@pytest.mark.parametrize('outcome,chinese,english', [
    ('waiting_for_input', '请补充必要信息后继续。', 'Please provide the required information to continue.'),
    ('ready', '工作流已准备就绪，即将开始执行。', 'The workflow is ready and will start shortly.'),
    ('waiting', '工作流已创建，正在等待后续步骤具备执行条件。',
     'The workflow has been created and is waiting for the next step to become available.'),
    ('already_initialized', '工作流已创建，无需重复创建。',
     'The workflow has already been created; there is no need to create it again.'),
    ('not_applicable', '当前请求可以直接处理，无需启动工作流。',
     'This request can be handled directly without starting a workflow.'),
    ('queued', '工作流已加入队列，等待执行。', 'The workflow is queued and waiting to run.'),
    ('custom_outcome', '已检查工作流的准备情况。', 'Workflow preparation has been checked.'),
])
def test_initialization_preview_localizes_outcomes_and_preserves_result(language, encoding, outcome, chinese, english):
    result = {'outcome': outcome, 'reason': 'Internal instruction: call advance_step.', 'session_id': 'session-1'}
    if encoding == 'json':
        result = json.dumps(result)
    elif encoding == 'nested':
        result = {'ok': True, 'value': {'result': json.dumps(result)}}
    frame = _tool_result_frame_text({'id': 'init', 'name': 'trigger_ppt_workflow', 'result': result}, language)
    preview = re.search(r'<trp id="init">(.*?)</trp>', frame, re.S).group(1).strip()
    payload = json.loads(re.search(r'<tool_result>(.*?)</tool_result>', frame, re.S).group(1))
    assert preview == (chinese if language == 'zh' else english)
    assert payload['result'] == result


@pytest.mark.parametrize('language', ['zh', 'en'])
def test_initialization_failure_keeps_diagnostics_without_exposing_internal_reason(language):
    result = {'ok': False, 'value': 'workflow or revision was not found'}
    frame = _tool_result_frame_text({'id': 'init', 'name': 'trigger_ppt_workflow', 'result': result}, language)
    preview = re.search(r'<trp id="init">(.*?)</trp>', frame, re.S).group(1).strip()
    assert preview == ('工作流未能启动，请查看错误详情。' if language == 'zh'
                       else 'The workflow could not start. Please review the error details.')
    payload = json.loads(re.search(r'<tool_result>(.*?)</tool_result>', frame, re.S).group(1))
    assert payload['result'] == result
