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


@pytest.fixture
def runtime():
    package = Path(__file__).resolve().parents[3] / 'workflows/ppt-workflow/workflow.yaml'
    policy = yaml.safe_load(package.read_text())['runtime']
    return {key: policy[key] for key in ('clarification_fields', 'attachment_clarification')}


def test_discovered_template_ask_emits_upload_control_without_model_metadata(runtime):
    from lazymind.chat.service.chat_service import _build_ask_user_tool

    field = next(f for f in runtime['clarification_fields'] if f['id'] == 'template_mode')
    question = {'text': field['question'], 'type': 'single', 'choices': field['choices']}
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

    question = {'text': '请选择文件处理方式', 'type': 'single', 'choices': ['摘要', '翻译']}
    catalog = [{'workflow_ref': 'builtin:ppt-workflow', 'runtime': runtime}]
    for discovery in (True, False):
        tool = _build_ask_user_tool({}, catalog, discovery_mode=discovery)[0]
        with patch('lazymind.chat.engine.tools.ask_user._write_agent_data') as emit:
            tool([question])
        assert 'attachment' not in emit.call_args.kwargs['questions'][0]


def test_paper_handoff_card_skips_defaulted_visual_fields_and_keeps_other_choices():
    from lazymind.chat.service.chat_service import _build_ask_user_tool
    package = Path(__file__).resolve().parents[3] / 'workflows/ppt-workflow/workflow.yaml'
    policy = yaml.safe_load(package.read_text())['runtime']
    toolkit = MagicMock()
    frozen = {'kind': 'ppt', 'handoff_id': 'owned',
              'input': {'papers': [{'paper_id': 'a'}, {'paper_id': 'b'}],
                        'user_instruction': '8页组会，详细讲解方法，15分钟'}}
    with patch('lazymind.chat.workflow.workflow_manager._client') as factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ):
        client = factory.return_value
        client.transport.request.return_value.status_code = 200
        client.transport.request.return_value.json.return_value = frozen
        contribution = resolve_workflow_injection(
            None, conversation_id='paper-chat', current_query='8页组会，详细讲解方法，15分钟',
            workflow_catalog=[{'workflow_ref': 'builtin:ppt-workflow', 'workflow_id': 'ppt-workflow',
                               'runtime': policy}],
            allowed_workflow_refs=['builtin:ppt-workflow'],
            workflow_activations=[{'workflow_ref': 'builtin:ppt-workflow', 'workflow_id': 'ppt-workflow',
                                   'tool_name': 'trigger_ppt_workflow'}],
        )
        questions = [{'id': f['id'], 'text': f['question'], 'type': f['type'],
                      'choices': f.get('choices', [])} for f in policy['clarification_fields']
                     if f['id'] in {'template_mode', 'visual_style', 'generate_background_images', 'generation_mode'}]
        tool = _build_ask_user_tool(contribution.runtime_policy)[0]
        with patch('lazymind.chat.engine.tools.ask_user._write_agent_data') as emit:
            tool(questions)
        emitted = emit.call_args.kwargs['questions']
        expected = {f['question'] for f in policy['clarification_fields']
                    if f['id'] in {'template_mode', 'generation_mode'}}
        assert {q['text'] for q in emitted} == expected
        assert sum('attachment' in q for q in emitted) == 1
        # Explicit style/background instructions stay in the original request;
        # suppressing questions must not rewrite them to defaults.
        explicit = '科研汇报用深色风格，启用 AI 底图'
        merged = _merge_startup_clarification_context(
            '不使用模板，完整生成',
            [{'role': 'user', 'content': explicit}, call('ask_user', {'questions': emitted})], policy,
        )
        assert explicit in merged


def test_conflicting_catalog_upload_contracts_require_workflow_selection(runtime):
    from copy import deepcopy
    from lazymind.chat.workflow.workflow_manager import enforce_startup_clarification_policy
    from lazymind.workflow_sdk import WorkflowClientError

    other = deepcopy(runtime)
    field = next(f for f in runtime['clarification_fields'] if f['id'] == 'template_mode')
    next(f for f in other['clarification_fields'] if f['id'] == 'template_mode')['attachment']['max_files'] = 2
    question = {'text': field['question'], 'type': 'single', 'choices': field['choices']}
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
        ask('template_detail_dates', '第 8 页汇报日期是哪天？试用期起止时间？'),
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
        ask('generation_mode', '请选择生成方式'),
    ]
    assert workflow_startup_clarification_already_asked(
        history, runtime, attachment_names=['template.pptx'], current_query='完整生成',
    )


def test_direct_upload_combined_card_preserves_all_questions_and_answers(runtime):
    history = [
        {'role': 'user', 'content': '用 template.pptx 和知识库做转正答辩，页数按内容安排'},
        call('pptx_analyzer', {'filename': 'template.pptx', 'page': 1}),
        {'role': 'tool', 'name': 'pptx_analyzer', 'content': '{"next_page":8}'},
        call('pptx_analyzer', {'filename': 'template.pptx', 'page': 8}),
        {'role': 'tool', 'name': 'pptx_analyzer', 'content': '{"next_page":null}'},
        call('ask_user', {'questions': [
            {'id': 'generation_mode', 'text': '请选择生成方式', 'type': 'single'},
            {'id': 'template_detail_date', 'text': '第八页的汇报日期？', 'type': 'text'},
            {'id': 'template_detail_name', 'text': '汇报人姓名？', 'type': 'text'},
        ]}),
    ]
    answer = '完整生成；11 月 10 日；小王'
    assert workflow_startup_clarification_already_asked(
        history, runtime, attachment_names=['template.pptx'], current_query=answer,
    )
    merged = _merge_startup_clarification_context(answer, history, runtime)
    for expected in ('页数按内容安排', '请选择生成方式', '第八页的汇报日期', '汇报人姓名', answer):
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
            None, current_query='汇报日期 11 月 10 日，试用期未知', conversation_history=second_round(),
            workflow_catalog=[item], allowed_workflow_refs=['builtin:ppt-workflow'],
            workflow_activations=[item],
        )
        pending = list(contribution.tools)
        registered = {}
        while pending:
            tool = pending.pop()
            if isinstance(tool, dict):
                pending.extend(tool.get('tools') or [])
            else:
                registered[tool.__name__] = tool
        result = registered['trigger_ppt_workflow'](
            input_bindings={'template_pptx': '转正模版.pptx'}, request_context='汇报日期 11 月 10 日',
        )
        registered['advance_step'](['analyze_requirements'])
    brief = result['request_context']
    assert all(value in brief for value in ['保留量化对比', '12 页', '15 分钟', '第 8 页', '试用期未知'])
    resolve.assert_called_once_with('转正模版.pptx')
    assert toolkit.prepare_workflow.call_args.kwargs['request_context'] == brief
    assert result['input_bindings'] == {'template_pptx': 'artifact:template'}
    assert toolkit.advance_step.call_args.args[2][0].user_input == brief
