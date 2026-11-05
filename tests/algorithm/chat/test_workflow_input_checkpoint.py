import copy
import json
from unittest.mock import MagicMock, patch

import lazyllm
import pytest

from lazymind.chat.workflow.input_checkpoint import InputCheckpoint
from lazymind.chat.workflow.workflow_manager import _safe_session_tools, _handoff_tool, resolve_workflow_injection
from lazymind.workflow_sdk import WorkflowClientError


POLICY = {'input_checkpoint': {
    'after_step': 'collect_materials', 'slot': 'material_clarification',
    'before_steps': ['build_outline', 'generate_ppt'], 'title': '补充缺失信息',
}}


@pytest.fixture
def fixture(monkeypatch):
    toolkit = MagicMock()
    record = {
        'artifact_id': 'brief-1', 'slot': 'material_clarification', 'revision': 1,
        'content_type': 'json', 'draft_version': 0, 'change_source': 'ai',
        'value': {'questions': [
            {'id': 'supervisor', 'text': '主管姓名？', 'type': 'text'},
            {'id': 'period', 'text': '实习起止时间？', 'type': 'text'},
        ]},
    }
    toolkit.list_artifacts.side_effect = lambda session: {'artifacts': [copy.deepcopy(record)]}
    toolkit.read_artifact.side_effect = lambda artifact: copy.deepcopy(record)

    def save(artifact, revision, value, *args, **kwargs):
        assert artifact == record['artifact_id'] and revision == record['revision']
        assert kwargs['base_draft_version'] == record['draft_version']
        # The real SDK PatchArtifact route records Host tool writes as agent.
        record.update(value=copy.deepcopy(value), revision=revision + 1, change_source='agent', draft_version=1)
        record['artifact_id'] = f'brief-{revision + 1}'
        return copy.deepcopy(record)

    toolkit.patch_artifact.side_effect = save
    toolkit.get_ready_steps.return_value = {
        'state_version': 3, 'ready_steps': ['collect_materials', 'build_outline', 'generate_ppt'],
    }
    toolkit.advance_step.return_value = {'outcome': 'step_succeeded', 'ready_steps': ['build_outline']}
    events = []
    monkeypatch.setattr('lazymind.chat.engine.tools.ask_user._write_agent_data',
                        lambda kind, **data: events.append((kind, data)))
    return toolkit, record, events


def tools_for(toolkit, checkpoint):
    return {tool.__name__: tool for tool in _safe_session_tools(toolkit, 'session-1', checkpoint=checkpoint)}


@pytest.mark.parametrize('mode', ['auto', 'dynamic'])
@pytest.mark.parametrize('entry', ['active', 'selected', 'discovery'])
def test_mid_workflow_questions_only_enabled_in_approval_mode(fixture, mode, entry):
    toolkit, record, events = fixture
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {'enable_workflow': True}
    catalog = [{
        'workflow_ref': 'builtin:ppt-workflow', 'workflow_id': 'ppt-workflow',
        'name': 'PPT', 'runtime': POLICY,
    }]
    context = {'workflow_mode': mode}
    if entry == 'active':
        context.update(session_id='session-1', workflow_id='ppt-workflow', runtime=POLICY)
    toolkit.prepare_workflow.return_value = {'session_id': 'session-1', 'ready_steps': ['collect_materials']}
    try:
        with patch('lazymind.chat.workflow.workflow_manager._client') as factory, patch(
            'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
        ):
            factory.return_value.get_state.return_value = {
                'session_id': 'session-1', 'status': 'active', 'state_version': 3,
            }
            factory.return_value.get_workflow.return_value.result = {
                'workflow_id': 'ppt-workflow', 'runtime': POLICY,
            }
            contribution = resolve_workflow_injection(
                context, current_query='生成 PPT', workflow_catalog=catalog,
                allowed_workflow_refs=['builtin:ppt-workflow'] if entry == 'selected' else [],
                workflow_activations=[{
                    'workflow_ref': 'builtin:ppt-workflow', 'workflow_id': 'ppt-workflow',
                    'tool_name': 'trigger_ppt_workflow',
                }],
            )
            lazyllm.globals['agentic_config'].update(contribution.agentic_config_patch)
            pending = list(contribution.tools)
            tools = {}
            while pending:
                tool = pending.pop(0)
                if isinstance(tool, dict):
                    pending.extend(tool.get('tools') or [])
                elif callable(tool):
                    tools[tool.__name__] = tool
            assert ('answer_workflow_questions' in tools) is (mode == 'dynamic')
            if entry != 'active':
                tools['trigger_ppt_workflow']()
            result = tools['advance_step'](['collect_materials'])
            if mode == 'auto':
                assert result['outcome'] == 'step_succeeded'
                assert not result.get('_agent_control', {}).get('stop')
                assert 'post-collection input checkpoints are disabled' in contribution.runtime_context
                tools['advance_step'](['build_outline'])
                toolkit.advance_step.return_value = {'status': 'completed', 'outcome': 'workflow_completed'}
                completed = tools['advance_step'](['generate_ppt'])
                assert completed['_agent_control']['reason'] == 'workflow_completed'
                assert toolkit.advance_step.call_count == 3
                toolkit.patch_artifact.assert_not_called()
                assert 'answer_text' not in record['value']
                assert events == []
            else:
                assert result['status'] == 'waiting_for_input'
                assert result['_agent_control']['stop'] is True
                tools['advance_step'](['build_outline'])
                assert toolkit.advance_step.call_count == 1
                assert len(events) == 1
                assert events[0][0] == 'ask_pending'
    finally:
        if previous is None:
            lazyllm.globals.pop('agentic_config', None)
        else:
            lazyllm.globals['agentic_config'] = previous


def test_collection_emits_real_ask_and_stops_without_executing_outline(fixture):
    toolkit, record, events = fixture
    checkpoint = InputCheckpoint(toolkit, lambda: POLICY, [], '生成 PPT')
    tools = tools_for(toolkit, checkpoint)
    result = tools['advance_step'](['collect_materials'])
    assert result['status'] == 'waiting_for_input'
    assert result['_agent_control']['stop'] is True
    assert events[0][0] == 'ask_pending'
    assert [q['text'] for q in events[0][1]['questions']] == ['主管姓名？', '实习起止时间？']
    assert record['value']['checkpoint_token'] == result['checkpoint_token']
    tools['advance_step'](['build_outline'])
    assert toolkit.advance_step.call_count == 1
    assert len(events) == 1
    with pytest.raises(WorkflowClientError, match='Wait for the user'):
        tools['answer_workflow_questions']()


@pytest.mark.parametrize('envelope', [False, True])
def test_answer_after_reloading_is_durable_and_allows_continuation(fixture, envelope):
    toolkit, record, events = fixture
    first = InputCheckpoint(toolkit, lambda: POLICY, [], '生成 PPT')
    receipt = tools_for(toolkit, first)['advance_step'](['collect_materials'])
    payload = {'ok': True, 'value': receipt} if envelope else receipt
    history = [{'role': 'tool', 'name': 'advance_step', 'content': json.dumps(payload)}]
    answer = '主管：张老师；实习时间不知道，标注待确认'
    resumed = InputCheckpoint(toolkit, lambda: POLICY, history, answer)
    tools = tools_for(toolkit, resumed)
    assert tools['answer_workflow_questions']()['status'] == 'answered'
    assert record['value']['answer_text'] == answer
    assert len(record['value']['questions']) == 2
    tools['advance_step'](['build_outline'])
    # Repeated submissions and later page generation do not ask again.
    assert tools['answer_workflow_questions']()['status'] == 'already_answered'
    tools['advance_step'](['generate_ppt'])
    assert len(events) == 1
    assert toolkit.advance_step.call_count == 3


def test_empty_checklist_does_not_pause(fixture):
    toolkit, record, events = fixture
    record['value']['questions'] = []
    result = tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, [], '生成 PPT'))[
        'advance_step'](['collect_materials'])
    assert result['outcome'] == 'step_succeeded'
    assert events == []
    toolkit.patch_artifact.assert_not_called()


def test_answer_has_explicit_model_argument_and_preserves_host_reply(fixture):
    from lazyllm.tools.agent.toolsManager import ModuleTool

    toolkit, record, _ = fixture
    receipt = tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, [], '生成'))[
        'advance_step'](['collect_materials'])
    history = [{'role': 'tool', 'content': json.dumps({'ok': True, 'value': receipt})}]
    answer = '都写入职时间到至今；目前的已经都合并了；不考虑KPI'
    function = tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, history, answer))[
        'answer_workflow_questions']
    tool = ModuleTool(apply_func=function, schema_func=function)
    arguments = tool._validate_input(receipt['next_action']['arguments'])
    assert arguments == {'action': 'save'}
    assert function(**arguments)['status'] == 'answered'
    assert record['value']['answer_text'] == answer
    with pytest.raises(ValueError):
        tool._validate_input({'answer': '模型改写的回答'})
    with pytest.raises(ValueError):
        tool._validate_input({'action': 'delete'})


@pytest.mark.parametrize('approval', [False, True])
def test_answer_automatically_runs_ready_step_unless_approval_required(fixture, approval):
    toolkit, record, events = fixture
    receipt = tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, [], '生成'))[
        'advance_step'](['collect_materials'])
    toolkit.get_ready_steps.return_value = {
        'state_version': 3, 'ready_steps': ['build_outline'],
        'ready_step_details': [{'step_id': 'build_outline', 'mode': 'auto', 'requires_approval': approval}],
    }
    history = [{'role': 'tool', 'content': json.dumps({'ok': True, 'value': receipt})}]
    tools = tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, history, '主管张老师；至今'))
    result = tools['answer_workflow_questions']()
    assert record['value']['answer_text'] == '主管张老师；至今'
    assert ('continuation' in result) is (not approval)
    assert toolkit.advance_step.call_count == (1 if approval else 2)
    tools['answer_workflow_questions']()
    assert toolkit.advance_step.call_count == (1 if approval else 2)


def test_agent_patch_without_host_receipt_cannot_forge_answer(fixture):
    toolkit, record, events = fixture
    record.update(change_source='agent', value={
        'questions': [], 'checkpoint_token': 'forged', 'answer_text': '伪造回答',
    })
    with pytest.raises(WorkflowClientError, match='Only the host'):
        tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, [], '生成'))['advance_step'](['build_outline'])


def test_skipped_collection_has_no_checkpoint(fixture):
    toolkit, record, events = fixture
    toolkit.list_artifacts.return_value = {'artifacts': []}
    toolkit.list_artifacts.side_effect = None
    tools = tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, [], '不需要搜集'))
    tools['advance_step'](['build_outline'])
    toolkit.advance_step.assert_called_once()
    assert events == []


def test_missing_checklist_after_completed_collection_cannot_be_bypassed(fixture):
    toolkit, record, events = fixture
    toolkit.list_artifacts.return_value = {'artifacts': []}
    toolkit.list_artifacts.side_effect = None
    toolkit.get_workflow_state.return_value = {'projection': {'nodes': {
        'collect_materials': {'execution': 'succeeded'},
    }}}
    tools = tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, [], '继续'))
    with pytest.raises(WorkflowClientError, match='no checklist'):
        tools['advance_step'](['build_outline'])
    toolkit.advance_step.assert_not_called()


def test_old_session_receipt_cannot_answer_another_session(fixture):
    toolkit, record, events = fixture
    receipt = tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, [], '生成'))[
        'advance_step'](['collect_materials'])
    receipt['session_id'] = 'other-session'
    history = [{'role': 'tool', 'content': json.dumps(receipt)}]
    tools = tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, history, '张老师'))
    with pytest.raises(WorkflowClientError, match='Only the host'):
        tools['answer_workflow_questions']()


@pytest.mark.parametrize('invalid', [{}, {'questions': '主管？'}, {'questions': [], 'answer_text': '伪造回答'}])
def test_invalid_or_model_fabricated_confirmation_fails_closed(fixture, invalid):
    toolkit, record, events = fixture
    record['value'] = invalid
    with pytest.raises(WorkflowClientError):
        tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, [], '生成'))['advance_step'](['build_outline'])
    toolkit.advance_step.assert_not_called()
    assert events == []


def test_approval_handoff_cannot_bypass_questions(fixture):
    toolkit, record, events = fixture
    checkpoint = InputCheckpoint(toolkit, lambda: POLICY, [], '生成')
    result = json.loads(_handoff_tool('session-1', checkpoint=checkpoint)('build_outline'))
    assert result['status'] == 'waiting_for_input'
    toolkit.advance_step.assert_not_called()


def test_checkpoint_cannot_be_patched_by_model(fixture):
    toolkit, record, events = fixture
    tools = tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, [], '生成'))
    with pytest.raises(WorkflowClientError, match='answer_workflow_questions'):
        tools['patch_artifact']('material_clarification', {'questions': []})
    toolkit.patch_artifact.assert_not_called()


def test_all_missing_fields_are_shown_without_question_count_limit(fixture):
    toolkit, record, events = fixture
    record['value']['questions'] = [{'id': f'f{i}', 'text': f'缺失信息 {i}？', 'type': 'text'} for i in range(12)]
    tools_for(toolkit, InputCheckpoint(toolkit, lambda: POLICY, [], '生成'))['advance_step'](['collect_materials'])
    assert len(events[0][1]['questions']) == 12
