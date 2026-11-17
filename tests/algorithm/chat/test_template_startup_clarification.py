import json
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest
import yaml

from lazymind.chat.workflow.workflow_manager import (
    _merge_startup_clarification_context,
    resolve_workflow_injection,
    workflow_startup_clarification_already_asked,
)
from lazymind.workflow_sdk import WorkflowClientError


@pytest.mark.parametrize('binding,resolved,claimed,previous_mode,expected', [
    (None, None, 'upload', None, 'needs_input'),
    (None, None, 'upload', 'upload', 'needs_input'),
    ('missing.pptx', None, 'upload', None, 'ATTACHMENT_NOT_SELECTED'),
    ('notes.pdf', '/uploads/notes.pdf', 'upload', None, 'WORKFLOW_INPUT_INVALID'),
    ('template.pptx', '/uploads/template.pptx', 'none', 'none', 'prepared'),
])
def test_template_mode_requires_resolved_template_binding(
    monkeypatch, binding, resolved, claimed, previous_mode, expected,
):
    from lazymind.chat.workflow import workflow_manager as manager

    package_path = Path(__file__).resolve().parents[3] / 'workflows/ppt-workflow/workflow.yaml'
    runtime = yaml.safe_load(package_path.read_text(encoding='utf-8'))['runtime']
    item = {
        'workflow_id': 'ppt-workflow', 'workflow_ref': 'builtin:ppt-workflow',
        'revision_id': 'revision-1', 'runtime': runtime, 'tool_name': 'trigger_ppt_workflow',
        'compiled_graph': {
            'material_types': {'template_pptx': 'file'},
            'material_producers': {'template_pptx': {'kind': 'external'}},
        },
    }
    client, toolkit = MagicMock(), MagicMock()
    client.get_workflow.return_value.result = item
    toolkit.prepare_workflow.return_value = {
        'session_id': 'session-1', 'state_version': 1, 'ready_steps': ['analyze_requirements'],
    }
    resolve = MagicMock(return_value=(resolved, None if resolved else 'Attachment not found'))
    import_attachment = MagicMock(return_value='artifact:template')
    monkeypatch.setattr(manager, '_client', lambda: client)
    monkeypatch.setattr(manager, 'HostWorkflowToolkit', lambda *a, **kw: toolkit)
    monkeypatch.setattr(manager, '_resolve_workflow_attachment', resolve)
    monkeypatch.setattr(manager, '_import_attachment', import_attachment)
    monkeypatch.setattr(manager, '_conversation_has_attachments', lambda: bool(binding))
    monkeypatch.setattr('lazyllm.tools.agent.base._write_agent_data', lambda *a, **kw: None)
    contribution = resolve_workflow_injection(
        None, current_query='用模板制作关于气候变化的PPT，8页，输出PPTX',
        workflow_catalog=[item], allowed_workflow_refs=['builtin:ppt-workflow'], workflow_activations=[item],
        clarification=({'workflow_ref': 'builtin:ppt-workflow', 'values': {'template_mode': previous_mode}}
                       if previous_mode else None),
    )
    pending, registered = list(contribution.tools), {}
    while pending:
        tool = pending.pop()
        if isinstance(tool, dict):
            pending.extend(tool.get('tools') or [])
        else:
            registered[tool.__name__] = tool
    bindings = {'topic': '气候变化', 'template_mode': claimed, 'presentation_output_format': 'native_pptx'}
    if binding:
        bindings['template_pptx'] = binding
    if expected.startswith(('ATTACHMENT_', 'WORKFLOW_')):
        with pytest.raises(WorkflowClientError) as exc:
            registered['trigger_ppt_workflow'](
                input_bindings=bindings,
                clarification_extractions={'slide_count': [{'value': '8', 'evidence': '8页'}]},
            )
        assert exc.value.code == expected
    else:
        result = registered['trigger_ppt_workflow'](
            input_bindings=bindings,
            clarification_extractions={'slide_count': [{'value': '8', 'evidence': '8页'}]},
        )
        assert result['status'] == expected
        if expected == 'needs_input':
            assert result['missing_clarification_fields'] == ['generate_background_images']
        else:
            assert result['input_bindings'] == {'template_pptx': 'artifact:template'}
            assert '"template_mode": "upload"' in toolkit.prepare_workflow.call_args.kwargs['request_context']
    if binding:
        resolve.assert_called_once_with(binding)
    else:
        resolve.assert_not_called()
    if expected != 'prepared':
        toolkit.prepare_workflow.assert_not_called()
        import_attachment.assert_not_called()


@pytest.fixture
def runtime():
    package = Path(__file__).resolve().parents[3] / 'workflows/ppt-workflow/workflow.yaml'
    policy = yaml.safe_load(package.read_text(encoding='utf-8'))['runtime']
    # Exercise the generic host's upload/follow-up contract independently of
    # PPT's current post-collection checkpoint and internally derived template_mode.
    template = next(f for f in policy['clarification_fields'] if f['id'] == 'template_mode')
    template.update(question='请选择并上传模板', choice_policy='fixed', allow_other=False)
    return {
        'clarification_fields': policy['clarification_fields'],
        'attachment_clarification': {
            'filename_suffixes': ['.pptx'],
            'question_id_prefix': 'template_detail_',
            'inspection_tools': ['pptx_analyzer', 'KBToolkit_kb_search'],
        },
    }


def test_discovered_template_ask_emits_upload_control_without_model_metadata(runtime):
    from lazymind.chat.service.chat_service import _build_ask_user_tool

    field = next(f for f in runtime['clarification_fields'] if f['id'] == 'template_mode')
    question = {'id': 'template_mode', 'text': field['question'], 'type': 'single',
                'choices': field['choices'], 'reason': '确认模板来源', 'required': True}
    tool = _build_ask_user_tool(
        {}, [{'workflow_ref': 'builtin:ppt-workflow', 'runtime': runtime}], discovery_mode=True,
    )[0]
    with patch('lazymind.chat.engine.tools.ask_user._write_agent_data') as emit:
        tool([question], title='转正答辩 PPT')
    payload = emit.call_args.kwargs
    assert emit.call_args.args == ('ask_pending',)
    emitted = payload['questions'][0]
    assert emitted['attachment'] == field['attachment']
    assert emitted['attachment']['when_choice'] in emitted['choices']
    assert emitted['attachment']['required'] is True
    assert emitted['allow_other'] is False
    assert 'attachment' not in question


def test_catalog_upload_rules_do_not_leak_into_bound_or_unrelated_asks(runtime):
    from lazymind.chat.service.chat_service import _build_ask_user_tool

    question = {'id': 'file_action', 'text': '请选择文件处理方式', 'type': 'single',
                'choices': ['摘要', '翻译'], 'reason': '确认文件处理目标', 'required': True}
    catalog = [{'workflow_ref': 'builtin:ppt-workflow', 'runtime': runtime}]
    for discovery in (True, False):
        tool = _build_ask_user_tool({}, catalog, discovery_mode=discovery)[0]
        with patch('lazymind.chat.engine.tools.ask_user._write_agent_data') as emit:
            tool([question])
        assert 'attachment' not in emit.call_args.kwargs['questions'][0]


def test_academic_prompt_defaults_preserve_explicit_visual_requirements():
    from lazymind.chat.workflow.clarification import resolve_fields
    package = Path(__file__).resolve().parents[3] / 'workflows/ppt-workflow/workflow.yaml'
    policy = yaml.safe_load(package.read_text(encoding='utf-8'))['runtime']
    request = '请根据附件生成科研汇报 PPT，主题：方法比较，8页。科研风格，不生成 AI 底图。'
    values, missing = resolve_fields(policy, request, {
        'topic': '方法比较', 'slide_count': '8', 'visual_style': '科研风格', 'generate_background_images': 'disabled',
    }, extractions={'slide_count': [{'value': '8', 'evidence': '8页'}]})
    assert values['visual_style'] == '科研风格'
    assert values['generate_background_images'] == 'disabled'
    assert not missing
    explicit = '请根据附件生成科研汇报 PPT，主题：方法比较，8页。使用深色风格，启用AI底图'
    values, missing = resolve_fields(policy, explicit, {
        'topic': '方法比较', 'slide_count': '8', 'visual_style': '深色风格', 'generate_background_images': 'enabled',
    }, extractions={'slide_count': [{'value': '8', 'evidence': '8页'}]})
    assert values['visual_style'] == '深色风格'
    assert values['generate_background_images'] == 'enabled'
    assert not missing


def test_conflicting_catalog_upload_contracts_require_workflow_selection(runtime):
    from copy import deepcopy
    from lazymind.chat.workflow.workflow_manager import enforce_startup_clarification_policy
    from lazymind.workflow_sdk import WorkflowClientError

    other = deepcopy(runtime)
    field = next(f for f in runtime['clarification_fields'] if f['id'] == 'template_mode')
    next(f for f in other['clarification_fields'] if f['id'] == 'template_mode')['attachment']['max_files'] = 2
    question = {'id': 'template_mode', 'text': field['question'], 'type': 'single',
                'choices': field['choices'], 'reason': '确认模板来源', 'required': True}
    with pytest.raises(WorkflowClientError, match='Multiple workflows'):
        enforce_startup_clarification_policy(
            [question], {}, [{'runtime': runtime}, {'runtime': other}], discovery_mode=True,
        )


def call(name, arguments):
    return {'role': 'assistant', 'tool_calls': [{
        'id': name, 'type': 'function',
        'function': {'name': name, 'arguments': json.dumps(arguments, ensure_ascii=False)},
    }]}


def ask(question_id, text):
    return call('ask_user', {'questions': [{'id': question_id, 'text': text, 'type': 'text'}]})


def first_round():
    return [
        {'role': 'user', 'content': '用知识库和转正模板做转正答辩，保留量化对比'},
        call('ask_user', {'questions': [
            {'id': 'slide_count', 'text': '需要多少页，面向谁？', 'type': 'text'},
            {'id': 'template_mode', 'text': '请选择并上传模板', 'type': 'single'},
        ]}),
        {'role': 'tool', 'name': 'ask_user', 'content': 'The user submitted the form.'},
    ]


def second_round():
    return [
        *first_round(),
        {'role': 'user', 'content': '12 页，直属领导，演讲 15 分钟'},
        call('pptx_analyzer', {'filename': '转正模版.pptx', 'page': 1}),
        {'role': 'tool', 'name': 'pptx_analyzer', 'content': '{"next_page":8}'},
        call('pptx_analyzer', {'filename': '转正模版.pptx', 'page': 8}),
        {'role': 'tool', 'name': 'pptx_analyzer',
         'content': '{"next_page":null,"sample_date":"2020-01-01"}'},
        call('KBToolkit_kb_search', {'query': '试用期起止日期'}),
        {'role': 'tool', 'name': 'KBToolkit_kb_search', 'content': '{"results":[]}'},
        call('ask_user', {'questions': [
            {'id': 'presentation_output_format', 'text': '请选择输出格式', 'type': 'single'},
            {'id': 'template_detail_dates', 'text': '第 8 页汇报日期是哪天？试用期起止时间？', 'type': 'text'},
        ]}),
        {'role': 'tool', 'name': 'ask_user', 'content': 'The user submitted the form.'},
    ]


@pytest.mark.parametrize('filenames,exhausted', [
    (['转正模版.pptx'], False), (['转正模版.PPTX'], False),
    ([], True), (['paper.pdf'], True),
])
def test_template_attachment_unlocks_only_supplemental_card(runtime, filenames, exhausted):
    assert workflow_startup_clarification_already_asked(
        first_round(), runtime, current_query='12 页，直属领导，演讲 15 分钟',
        attachment_names=filenames,
    ) is exhausted


def test_unrelated_workflow_keeps_single_card_contract(runtime):
    runtime.pop('attachment_clarification')
    assert workflow_startup_clarification_already_asked(
        first_round(), runtime, attachment_names=['转正模版.pptx'],
    )


def test_existing_attachment_does_not_unlock_a_second_basic_card(runtime):
    history = [
        {'role': 'user', 'content': '用已上传的 template.pptx 生成答辩'},
        ask('presentation_output_format', '请选择输出格式'),
    ]
    assert workflow_startup_clarification_already_asked(
        history, runtime, attachment_names=['template.pptx'], current_query='输出 PPTX',
    )


def test_direct_upload_combined_card_preserves_all_questions_and_answers(runtime):
    history = [
        {'role': 'user', 'content': '用 template.pptx 和知识库做转正答辩，页数按内容安排'},
        call('pptx_analyzer', {'filename': 'template.pptx', 'page': 1}),
        {'role': 'tool', 'name': 'pptx_analyzer', 'content': '{"next_page":8}'},
        call('pptx_analyzer', {'filename': 'template.pptx', 'page': 8}),
        {'role': 'tool', 'name': 'pptx_analyzer', 'content': '{"next_page":null}'},
        call('ask_user', {'questions': [
            {'id': 'presentation_output_format', 'text': '请选择输出格式', 'type': 'single'},
            {'id': 'template_detail_date', 'text': '第八页的汇报日期？', 'type': 'text'},
            {'id': 'template_detail_name', 'text': '汇报人姓名？', 'type': 'text'},
        ]}),
    ]
    answer = '输出 PPTX；11 月 10 日；小王'
    assert workflow_startup_clarification_already_asked(
        history, runtime, attachment_names=['template.pptx'], current_query=answer,
    )
    merged = _merge_startup_clarification_context(answer, history, runtime)
    for expected in ('页数按内容安排', '请选择输出格式', '第八页的汇报日期', '汇报人姓名', answer):
        assert expected in merged
    assert merged.count(answer) == 1


def test_discovery_uses_matching_package_contract(runtime):
    assert not workflow_startup_clarification_already_asked(
        first_round(), {}, [{'runtime': runtime}], discovery_mode=True,
        attachment_names=['转正模版.pptx'],
    )


@pytest.mark.parametrize('include_current', [False, True])
def test_second_answer_preserves_questions_and_all_answers_without_sample_values(runtime, include_current):
    history = second_round()
    answer = '11 月 10 日，试用期不确定，标注待确认'
    if include_current:
        history.append({'role': 'user', 'content': answer})
    assert workflow_startup_clarification_already_asked(
        history, runtime, current_query=answer, attachment_names=['转正模版.pptx'],
    )
    merged = _merge_startup_clarification_context(answer, history, runtime)
    for expected in ('保留量化对比', '需要多少页', '12 页', '15 分钟', '第 8 页', '试用期', answer):
        assert expected in merged
    assert merged.count(answer) == 1
    assert '2020-01-01' not in merged


def test_new_task_does_not_inherit_completed_template_answers(runtime):
    history = [*second_round(), {'role': 'user', 'content': '11 月 10 日'},
               {'role': 'assistant', 'content': '已生成答辩 PPT。'}]
    assert not workflow_startup_clarification_already_asked(
        history, runtime, current_query='重新做一份产品介绍', attachment_names=['转正模版.pptx'],
    )
    assert _merge_startup_clarification_context('重新做一份产品介绍', history, runtime) == '重新做一份产品介绍'


def test_supplemental_only_card_does_not_merge_an_older_completed_task(runtime):
    history = [
        *first_round(), {'role': 'user', 'content': '之前的回答'},
        {'role': 'assistant', 'content': '旧任务已完成'},
        {'role': 'user', 'content': '新任务：模板填 6 页，其他要求已说明'},
        call('pptx_analyzer', {'filename': 'new.pptx', 'page': 1}),
        ask('template_detail_date', '演讲日期？'),
    ]
    merged = _merge_startup_clarification_context('明天', history, runtime)
    assert '新任务' in merged and '演讲日期' in merged and '明天' in merged
    assert '之前的回答' not in merged and '保留量化对比' not in merged
    assert workflow_startup_clarification_already_asked(history, runtime, attachment_names=['new.pptx'])


def test_trigger_and_first_step_receive_both_rounds_and_original_template_binding(runtime):
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {
        'session_id': 'session-1', 'state_version': 1, 'ready_steps': ['analyze_requirements'],
    }
    toolkit.get_ready_steps.return_value = {
        'session_id': 'session-1', 'state_version': 1, 'ready_steps': ['analyze_requirements'],
        'retryable_steps': [], 'rewindable_steps': [],
    }
    toolkit.advance_step.return_value = {'status': 'succeeded'}
    item = {
        'workflow_ref': 'builtin:ppt-workflow', 'workflow_id': 'ppt-workflow',
        'revision_id': 'revision-1', 'runtime': runtime, 'tool_name': 'trigger_ppt_workflow',
    }
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ), patch('lazymind.chat.workflow.workflow_manager._resolve_workflow_attachment') as resolve, patch(
        'lazymind.chat.workflow.workflow_manager._import_attachment', return_value='artifact:template',
    ), patch(
        'lazymind.chat.workflow.workflow_manager._conversation_has_attachments', return_value=True,
    ):
        resolve.return_value = ('/uploads/转正模版.pptx', None)
        client_factory.return_value.get_workflow.return_value.result = {
            **item, 'slots': [{'id': 'template_pptx', 'type': 'file', 'external': True}],
        }
        client_factory.return_value.get_state.return_value = {
            'session_id': 'session-1', 'state_version': 1, 'projection': {'ready': ['analyze_requirements']},
        }
        contribution = resolve_workflow_injection(
            None, current_query='输出 PPTX，汇报日期 11 月 10 日，试用期未知', conversation_history=second_round(),
            workflow_catalog=[item], allowed_workflow_refs=['builtin:ppt-workflow'],
            workflow_activations=[item],
            clarification={'workflow_ref': 'builtin:ppt-workflow',
                           'values': {'topic': '转正答辩', 'slide_count': '12'}},
        )
        assert contribution.pending_ask is None
        pending = list(contribution.tools)
        registered = {}
        while pending:
            tool = pending.pop()
            if isinstance(tool, dict):
                pending.extend(tool.get('tools') or [])
            else:
                registered[tool.__name__] = tool
        result = registered['trigger_ppt_workflow'](
            input_bindings={'template_pptx': '转正模版.pptx', 'presentation_output_format': 'native_pptx'},
            request_context='汇报日期 11 月 10 日',
        )
        assert result['status'] == 'prepared', result
        registered['advance_step'](['analyze_requirements'])
    brief = result['request_context']
    assert all(value in brief for value in ['保留量化对比', '12 页', '15 分钟', '第 8 页', '试用期未知'])
    resolve.assert_called_once_with('转正模版.pptx')
    assert toolkit.prepare_workflow.call_args.kwargs['request_context'] == brief
    assert result['input_bindings'] == {'template_pptx': 'artifact:template'}
    assert toolkit.advance_step.call_args.args[2][0].user_input == brief
