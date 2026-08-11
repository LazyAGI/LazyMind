from pathlib import Path
from unittest.mock import MagicMock

import pytest

from lazymind.workflow_toolkit import HostWorkflowToolkit, WORKFLOW_SKILL_NAME, workflow_skills_dir
from lazymind.workflow_sdk import WorkflowClientError


def test_common_toolkit_exposes_complete_skill_capabilities():
    names = {tool.__name__ for tool in HostWorkflowToolkit(MagicMock()).tools()}
    assert {
        'workflow_connection_status', 'list_workflows', 'get_workflow', 'list_skills',
        'get_skill_conversion_context', 'create_workflow_draft',
        'validate_workflow_draft', 'publish_workflow',
        'prepare_workflow', 'start_workflow', 'get_workflow_state',
        'get_ready_steps', 'advance_step', 'stop_workflow', 'resume_workflow',
        'import_input_resource', 'read_input_resource', 'bind_workflow_input',
        'list_artifacts', 'read_artifact', 'patch_artifact', 'delete_artifact',
    } <= names


def test_advance_step_exposes_strict_step_command_schema_and_accepts_it():
    from lazyllm.tools.agent import ToolManager
    from lazymind.workflow_toolkit import StepCommandInput

    client = MagicMock()
    client.advance.return_value.result = {'accepted': True}
    toolkit = HostWorkflowToolkit(lambda: client)
    manager = ToolManager([toolkit.advance_step])
    schema = manager.tools_description[0]['function']['parameters']
    step_schema = schema['$defs']['StepCommandInput']
    assert set(step_schema['properties']) == {
        'step_id', 'task_id', 'objective', 'user_input',
        'runtime_instruction', 'partial_indices',
    }
    assert step_schema['additionalProperties'] is False
    assert step_schema['required'] == ['step_id']

    toolkit.advance_step('session-1', 1, [StepCommandInput(step_id='prompt')], 'command-1')
    request = client.advance.call_args.args[0]
    assert request.command_id == 'command-1'
    assert request.steps[0].step_id == 'prompt'


def test_generated_command_id_is_returned_for_later_reconciliation():
    client = MagicMock()
    client.stop_workflow.return_value.result = {'status': 'waiting'}
    toolkit = HostWorkflowToolkit(lambda: client)

    result = toolkit.stop_workflow('session-1')

    assert result['status'] == 'waiting'
    assert result['command_id']
    client.stop_workflow.assert_called_once_with('session-1', result['command_id'])


def test_prepare_workflow_binds_host_origin_reference():
    client = MagicMock()
    client.prepare_workflow.return_value.result = {'status': 'missing_inputs'}
    toolkit = HostWorkflowToolkit(lambda: client, origin_ref='conversation-1')
    assert toolkit.prepare_workflow('writer') == {'status': 'missing_inputs'}
    assert client.prepare_workflow.call_args.kwargs['fields'] == {
        'origin_ref': 'conversation-1',
    }


def test_prepare_workflow_persists_request_context_for_session_defaults():
    client = MagicMock()
    client.prepare_workflow.return_value.result = {'status': 'missing_inputs'}
    toolkit = HostWorkflowToolkit(lambda: client, origin_ref='conversation-1')

    toolkit.prepare_workflow('writer', request_context='run this workflow')

    assert client.prepare_workflow.call_args.kwargs['fields'] == {
        'origin_ref': 'conversation-1',
        'request_context': 'run this workflow',
    }


def test_advance_step_returns_failure_for_chat_agent_retry_decision():
    client = MagicMock()
    client.advance.return_value.result = {
        'accepted': True,
        'attempt_statuses': {'task-1': 'failed'},
        'attempt_results': [{'task_id': 'task-1', 'step_id': 'prompt', 'attempt': 1}],
        'step_id': 'prompt', 'attempt': 1, 'max_attempts': 3, 'retry_remaining': 2,
        'projection': {'retryable': ['prompt']},
    }
    toolkit = HostWorkflowToolkit(lambda: client)

    result = toolkit.advance_step('session-1', 1, [{'step_id': 'prompt'}])

    assert result['outcome'] == 'step_failed'
    assert result['retryable_steps'] == ['prompt']
    assert result['retry_remaining'] == 2
    assert result['next_action']['decision_owner'] == 'ChatAgent'


def test_advance_step_success_directs_same_turn_continuation():
    client = MagicMock()
    client.advance.return_value.result = {
        'accepted': True,
        'attempt_statuses': {'task-1': 'succeeded'},
        'projection': {'ready': ['script'], 'completed': False},
    }
    toolkit = HostWorkflowToolkit(lambda: client)

    result = toolkit.advance_step('session-1', 1, [{'step_id': 'prompt'}])

    assert result['outcome'] == 'step_succeeded'
    assert result['ready_steps'] == ['script']
    assert result['next_action']['tool'] == 'advance_step'
    assert 'same ChatAgent turn' in result['next_action']['instruction']


def test_advance_step_completion_stops_continuation():
    client = MagicMock()
    client.advance.return_value.result = {
        'accepted': True,
        'attempt_statuses': {'task-1': 'succeeded'},
        'projection': {'ready': [], 'completed': True},
    }
    toolkit = HostWorkflowToolkit(lambda: client)

    result = toolkit.advance_step('session-1', 1, [{'step_id': 'verify'}])

    assert result['outcome'] == 'workflow_completed'
    assert result['next_action']['tool'] is None


def test_chat_prepare_starts_session_and_returns_authoritative_ready_frontier():
    client = MagicMock()
    client.prepare_workflow.return_value.result = {
        'id': 'preparation-1', 'status': 'ready', 'workflow_id': 'writer',
    }
    client.start_workflow.return_value.result = {
        'session_id': 'server-session', 'status': 'active', 'state_version': 1,
    }
    client.get_ready_steps.return_value = {
        'session_id': 'server-session', 'state_version': 1, 'ready_steps': ['prompt'],
    }
    toolkit = HostWorkflowToolkit(lambda: client, origin_ref='conversation-1')

    result = toolkit.prepare_workflow('writer')

    assert result['session_id'] == 'server-session'
    assert result['ready_steps'] == ['prompt']
    assert result['next_action']['tool'] == 'advance_step'
    client.start_workflow.assert_called_once_with(
        'preparation-1', '', command_id='',
    )
    client.advance.assert_not_called()


def test_non_chat_host_can_supply_session_id_without_auto_dispatch():
    client = MagicMock()
    client.start_workflow.return_value.result = {'session_id': 'host-session', 'status': 'active'}
    toolkit = HostWorkflowToolkit(lambda: client)

    assert toolkit.start_workflow('preparation-1', 'host-session')['session_id'] == 'host-session'
    client.start_workflow.assert_called_once_with(
        'preparation-1', 'host-session', command_id='',
    )
    client.get_ready_steps.assert_not_called()
    client.advance.assert_not_called()


def test_chat_toolset_hides_low_level_start_workflow():
    names = {tool.__name__ for tool in HostWorkflowToolkit(
        MagicMock(), origin_ref='conversation-1',
    ).tools()}
    assert 'prepare_workflow' in names
    assert 'advance_step' in names
    assert 'start_workflow' not in names


def test_common_toolkit_contains_no_model_dependency():
    source = (Path(__file__).parents[1] / 'lazymind/workflow_toolkit.py').read_text()
    assert 'AutoModel' not in source
    assert 'lazyllm' not in source
    assert 'llm_config' not in source


def test_explicit_workflow_selection_filters_discovery_and_guards_reads_and_prepare():
    client = MagicMock()
    client.list_workflows.return_value.result = {'workflows': [
        {'workflow_id': 'selected', 'workflow_ref': 'builtin:selected'},
        {'workflow_id': 'other', 'workflow_ref': 'builtin:other'},
    ]}
    toolkit = HostWorkflowToolkit(lambda: client, allowed_workflow_ids=['selected'])

    assert toolkit.list_workflows() == {'workflows': [
        {'workflow_id': 'selected', 'workflow_ref': 'builtin:selected'},
    ]}
    with pytest.raises(WorkflowClientError, match='not selected') as read_error:
        toolkit.get_workflow('other')
    assert read_error.value.code == 'WORKFLOW_NOT_SELECTED'
    with pytest.raises(WorkflowClientError, match='not selected'):
        toolkit.prepare_workflow('other')
    client.get_workflow.assert_not_called()
    client.prepare_workflow.assert_not_called()


def test_shared_skill_is_discoverable_by_in_process_hosts():
    root = Path(workflow_skills_dir())
    assert (root / WORKFLOW_SKILL_NAME / 'SKILL.md').is_file()


def test_lazyllm_skill_manager_discovers_shared_workflow_skill():
    from lazyllm.tools.agent.skill_manager import SkillManager
    prompt = SkillManager(
        dir=workflow_skills_dir(), skills=[WORKFLOW_SKILL_NAME],
    ).build_prompt()
    assert 'workflow-agent-kit:' in prompt
    assert 'Skill-to-Workflow conversion' in prompt
