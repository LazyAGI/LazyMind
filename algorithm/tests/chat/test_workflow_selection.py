import base64
import inspect
from unittest.mock import MagicMock, patch

import lazyllm
import pytest

from lazymind.chat.workflow.workflow_manager import resolve_workflow_injection
from lazymind.workflow_sdk import WorkflowClientError


@pytest.fixture(autouse=True)
def workflow_enabled():
    previous = lazyllm.globals.get('agentic_config')
    lazyllm.globals['agentic_config'] = {'enable_workflow': True}
    yield
    if previous is None:
        lazyllm.globals.pop('agentic_config', None)
    else:
        lazyllm.globals['agentic_config'] = previous


def _tool(contribution, name):
    return next(tool for tool in contribution.tools if tool.__name__ == name)


def _tool_names(contribution):
    return {tool.__name__ for tool in contribution.tools}


def test_mentioned_workflow_is_injected_as_authoritative_selection():
    catalog = [{
        'workflow_ref': 'builtin:image-workflow',
        'workflow_id': 'image-workflow',
        'revision_id': 'revision-1',
        'name': 'AI image generation',
    }]
    contribution = resolve_workflow_injection(
        None,
        current_query='run it now',
        workflow_catalog=catalog,
        allowed_workflow_refs=['builtin:image-workflow'],
        workflow_activations=[{
            'workflow_ref': 'builtin:image-workflow',
            'workflow_id': 'image-workflow',
            'revision_id': 'revision-1',
            'tool_name': 'trigger_image_workflow',
            'tool_description': "Load the exact 'AI image generation' Workflow",
            'prompt': 'Call the bound trigger; do not call list_workflows.',
        }],
    )

    assert 'Explicit Workflow Selection [AUTHORITATIVE]' in contribution.runtime_context
    assert 'builtin:image-workflow' in contribution.runtime_context
    assert 'revision-1' in contribution.runtime_context
    assert '"current_query": "run it now"' in contribution.runtime_context
    assert 'do not ask for a second trigger message' in contribution.runtime_context
    assert _tool(contribution, 'trigger_image_workflow').__doc__.startswith(
        "Load the exact 'AI image generation' Workflow"
    )
    assert list(inspect.signature(
        _tool(contribution, 'trigger_image_workflow'),
    ).parameters) == ['input_bindings']
    assert 'prepare_workflow' not in _tool_names(contribution)
    assert 'list_workflow_attachments' not in _tool_names(contribution)
    assert 'bind_workflow_input' not in _tool_names(contribution)


def test_dynamic_trigger_loads_pinned_remote_package_without_listing():
    lazyllm.globals['agentic_config']['files'] = ['/safe/report.pdf']
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {
        'session_id': 'session-1', 'state_version': 1, 'ready_steps': ['prompt'],
    }
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ), patch('lazymind.chat.engine.subagent.tools._resolve_attachment', return_value=(
        '/safe/report.pdf', None,
    )), patch('lazymind.chat.workflow.workflow_manager._import_attachment', return_value={
        'resource_id': 'resource-1', 'revision': 1, 'content_hash': 'sha256:test',
    }):
        client_factory.return_value.get_workflow.return_value.result = {
            'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
        }
        client_factory.return_value.get_state.return_value = {
            'session_id': 'session-1', 'state_version': 1,
            'projection': {'reachable': ['prompt'], 'ready': ['prompt'], 'blocked': []},
        }
        contribution = resolve_workflow_injection(
            None,
            current_query='original workflow request',
            workflow_catalog=[{
                'workflow_ref': 'builtin:image-workflow',
                'workflow_id': 'image-workflow',
                'revision_id': 'revision-1',
                'name': 'AI image generation',
            }],
            allowed_workflow_refs=['builtin:image-workflow'],
            workflow_activations=[{
                'workflow_ref': 'builtin:image-workflow',
                'workflow_id': 'image-workflow',
                'revision_id': 'revision-1',
                'tool_name': 'trigger_image_workflow',
                'tool_description': 'Load selected workflow',
                'prompt': 'Call the bound trigger; do not call list_workflows.',
            }],
        )

        result = _tool(contribution, 'trigger_image_workflow')(
            {'source': 'report.pdf'},
        )

    client_factory.return_value.list_workflows.assert_not_called()
    client_factory.return_value.get_workflow.assert_called_once_with(
        'image-workflow', 'revision-1',
    )
    assert result['status'] == 'prepared'
    assert result['outcome'] == 'ready'
    assert result['request_context'] == 'original workflow request'
    assert result['revision_id'] == 'revision-1'
    assert result['reachable_steps'] == ['prompt']
    assert result['ready_steps'] == ['prompt']
    toolkit.prepare_workflow.assert_called_once_with(
        'image-workflow', input_bindings={
            'source': {'resource_id': 'resource-1', 'revision': 1,
                       'content_hash': 'sha256:test'},
        }, request_context='original workflow request',
    )
    toolkit.advance_step.assert_not_called()


def test_dynamic_trigger_imports_literal_scalar_binding():
    lazyllm.globals['agentic_config']['files'] = ['/safe/report.pdf']
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {
        'status': 'needs_input', 'missing_inputs': ['source_document'],
    }
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ), patch('lazymind.chat.engine.subagent.tools._resolve_attachment', return_value=(
        None, 'not an attachment',
    )), patch('lazymind.chat.workflow.workflow_manager._import_text_binding', return_value={
        'resource_id': 'text-resource', 'revision': 1, 'content_hash': 'sha256:text',
    }) as import_text:
        client_factory.return_value.get_workflow.return_value.result = {
            'workflow_id': 'report', 'revision_id': 'revision-1',
        }
        contribution = resolve_workflow_injection(
            None, current_query='write about 3000 words',
            workflow_catalog=[{
                'workflow_ref': 'user:user-1:report', 'workflow_id': 'report',
                'revision_id': 'revision-1',
            }],
            allowed_workflow_refs=['user:user-1:report'],
            workflow_activations=[{
                'workflow_ref': 'user:user-1:report', 'workflow_id': 'report',
                'revision_id': 'revision-1', 'tool_name': 'trigger_report_workflow',
            }],
        )

        result = _tool(contribution, 'trigger_report_workflow')({
            'target_length': '3000',
        })

    import_text.assert_called_once_with('target_length', '3000')
    toolkit.prepare_workflow.assert_called_once_with(
        'report', input_bindings={
            'target_length': {
                'resource_id': 'text-resource', 'revision': 1,
                'content_hash': 'sha256:text',
            },
        }, request_context='write about 3000 words',
    )
    assert result['status'] == 'waiting'
    assert result['outcome'] == 'waiting_for_input'
    assert 'literal values' in _tool(contribution, 'trigger_report_workflow').__doc__


def test_dynamic_trigger_imports_scalar_binding_without_conversation_attachments():
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {
        'session_id': 'session-1', 'state_version': 1, 'ready_steps': ['draft'],
    }
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ), patch('lazymind.chat.workflow.workflow_manager._import_text_binding', return_value={
        'resource_id': 'text-resource', 'revision': 1, 'content_hash': 'sha256:text',
    }) as import_text:
        client_factory.return_value.get_workflow.return_value.result = {
            'workflow_id': 'report', 'revision_id': 'revision-1',
            'files': {'workflow.yaml': base64.b64encode(b'''\
slots:
  - id: target_length
    type: text
    external: true
''').decode()},
        }
        client_factory.return_value.get_state.return_value = {
            'session_id': 'session-1', 'state_version': 1,
            'projection': {'reachable': ['draft'], 'ready': ['draft'], 'blocked': []},
        }
        contribution = resolve_workflow_injection(
            None, current_query='write about 3000 words',
            workflow_catalog=[{
                'workflow_ref': 'builtin:report', 'workflow_id': 'report',
                'revision_id': 'revision-1',
            }],
            allowed_workflow_refs=['builtin:report'],
            workflow_activations=[{
                'workflow_ref': 'builtin:report', 'workflow_id': 'report',
                'revision_id': 'revision-1', 'tool_name': 'trigger_report_workflow',
            }],
        )

        result = _tool(contribution, 'trigger_report_workflow')({
            'target_length': '3000',
        })

    import_text.assert_called_once_with('target_length', '3000')
    assert result['session_id'] == 'session-1'
    assert 'target_length (text)' in _tool(
        contribution, 'trigger_report_workflow',
    ).__doc__
    toolkit.prepare_workflow.assert_called_once_with(
        'report', input_bindings={
            'target_length': {
                'resource_id': 'text-resource', 'revision': 1,
                'content_hash': 'sha256:text',
            },
        }, request_context='write about 3000 words',
    )


def test_dynamic_trigger_activates_advance_step_in_the_same_agent_turn():
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {
        'session_id': 'session-1', 'state_version': 1, 'ready_steps': ['prompt'],
    }
    toolkit.get_ready_steps.return_value = {
        'session_id': 'session-1', 'state_version': 1,
        'ready_steps': ['prompt'], 'retryable_steps': [], 'rewindable_steps': [],
    }
    toolkit.advance_step.return_value = {'status': 'succeeded'}
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ):
        client_factory.return_value.get_workflow.return_value.result = {
            'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
        }
        client_factory.return_value.get_state.return_value = {
            'session_id': 'session-1', 'state_version': 1,
            'projection': {'reachable': ['prompt'], 'ready': ['prompt']},
        }
        contribution = resolve_workflow_injection(
            {'workflow_mode': 'dynamic'},
            current_query='run it', conversation_id='conversation-1',
            workflow_catalog=[{
                'workflow_ref': 'builtin:image-workflow',
                'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
            }],
            allowed_workflow_refs=['builtin:image-workflow'],
            workflow_activations=[{
                'workflow_ref': 'builtin:image-workflow',
                'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
                'tool_name': 'trigger_image_workflow',
            }],
        )

        assert 'advance_step' in _tool_names(contribution)
        assert 'advance_step_and_hand_off' in _tool_names(contribution)
        assert contribution.stop_tools == []
        _tool(contribution, 'trigger_image_workflow')()
        result = _tool(contribution, 'advance_step')(['prompt'])

    assert result == {'status': 'succeeded'}
    assert toolkit.advance_step.call_args.args[0] == 'session-1'
    assert toolkit.advance_step.call_args.args[1] == 1


def test_dynamic_trigger_exposes_handoff_after_session_is_created_in_same_turn():
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {
        'session_id': 'session-1', 'state_version': 1, 'ready_steps': ['prompt'],
    }
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ):
        client = client_factory.return_value
        client.get_workflow.return_value.result = {
            'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
        }
        client.get_state.return_value = {
            'session_id': 'session-1', 'state_version': 1,
            'projection': {'reachable': ['prompt'], 'ready': ['prompt']},
        }
        client.get_ready_steps.return_value = {
            'session_id': 'session-1', 'state_version': 1,
            'ready_steps': ['review'], 'retryable_steps': [], 'rewindable_steps': [],
        }
        client.advance.return_value.result = {'status': 'queued'}
        contribution = resolve_workflow_injection(
            {'workflow_mode': 'dynamic'},
            current_query='run it', conversation_id='conversation-1',
            workflow_catalog=[{
                'workflow_ref': 'builtin:image-workflow',
                'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
            }],
            allowed_workflow_refs=['builtin:image-workflow'],
            workflow_activations=[{
                'workflow_ref': 'builtin:image-workflow',
                'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
                'tool_name': 'trigger_image_workflow',
            }],
        )

        _tool(contribution, 'trigger_image_workflow')()
        result = _tool(contribution, 'advance_step_and_hand_off')('review')

    assert result == '{"status": "queued"}'
    request = client.advance.call_args.args[0]
    assert request.session_id == 'session-1'
    assert request.handoff is True
    assert request.steps[0].step_id == 'review'


def test_dynamic_trigger_defaults_request_context_to_current_query():
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {
        'session_id': 'session-1', 'state_version': 1, 'ready_steps': ['prompt'],
    }
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ):
        client_factory.return_value.get_workflow.return_value.result = {
            'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
        }
        client_factory.return_value.get_state.return_value = {
            'session_id': 'session-1', 'state_version': 1,
            'projection': {'reachable': ['prompt'], 'ready': ['prompt']},
        }
        contribution = resolve_workflow_injection(
            None,
            current_query='run the selected workflow',
            workflow_catalog=[{
                'workflow_ref': 'builtin:image-workflow',
                'workflow_id': 'image-workflow',
                'revision_id': 'revision-1',
            }],
            allowed_workflow_refs=['builtin:image-workflow'],
            workflow_activations=[{
                'workflow_ref': 'builtin:image-workflow',
                'workflow_id': 'image-workflow',
                'revision_id': 'revision-1',
                'tool_name': 'trigger_image_workflow',
            }],
        )

        result = _tool(contribution, 'trigger_image_workflow')()

    assert result['request_context'] == 'run the selected workflow'
    toolkit.advance_step.assert_not_called()


def test_dynamic_trigger_returns_waiting_without_advancing_when_no_step_is_ready():
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {'status': 'missing_inputs'}
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ):
        client_factory.return_value.get_workflow.return_value.result = {
            'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
        }
        contribution = resolve_workflow_injection(
            None, current_query='run it', conversation_id='conversation-1',
            workflow_catalog=[{'workflow_ref': 'builtin:image-workflow',
                               'workflow_id': 'image-workflow', 'revision_id': 'revision-1'}],
            allowed_workflow_refs=['builtin:image-workflow'],
            workflow_activations=[{'workflow_ref': 'builtin:image-workflow',
                                   'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
                                   'tool_name': 'trigger_image_workflow'}],
        )

        result = _tool(contribution, 'trigger_image_workflow')()

    assert result['status'] == 'waiting'
    assert result['outcome'] == 'waiting_for_input'
    toolkit.advance_step.assert_not_called()


def test_enabled_workflow_without_mention_keeps_generic_discovery_tools():
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory:
        client_factory.return_value.get_workflow.return_value.result = {'workflow_id': 'any'}
        contribution = resolve_workflow_injection(None, workflow_catalog=[])

        assert contribution.runtime_context == ''
        assert _tool(contribution, 'get_workflow')('any') == {'workflow_id': 'any'}


def test_model_tool_projection_hides_controller_lifecycle_tools_without_session():
    contribution = resolve_workflow_injection(None, workflow_catalog=[])

    names = _tool_names(contribution)
    assert 'prepare_workflow' not in names
    assert 'start_workflow' not in names
    assert 'stop_workflow' not in names
    assert 'resume_workflow' not in names


@pytest.mark.parametrize(
    ('status', 'expects_resume'),
    [('active', False), ('waiting', False), ('failed', False),
     ('completed', False), ('stopped', True)],
)
def test_existing_session_hides_creation_and_only_stopped_session_exposes_resume(
        status, expects_resume):
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory:
        client_factory.return_value.get_state.return_value = {
            'session_id': 'session-1', 'status': status, 'state_version': 3,
        }
        contribution = resolve_workflow_injection(
            {'session_id': 'session-1', 'workflow_id': 'image-workflow'},
            conversation_id='conversation-1',
            workflow_catalog=[{
                'workflow_ref': 'builtin:image-workflow',
                'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
            }],
            allowed_workflow_refs=['builtin:image-workflow'],
            workflow_activations=[{
                'workflow_ref': 'builtin:image-workflow',
                'workflow_id': 'image-workflow', 'revision_id': 'revision-1',
                'tool_name': 'trigger_image_workflow',
                'tool_description': 'Load selected workflow',
            }],
        )

    names = _tool_names(contribution)
    assert 'trigger_image_workflow' not in names
    assert 'prepare_workflow' not in names
    assert 'start_workflow' not in names
    assert 'stop_workflow' not in names
    assert ('resume_workflow' in names) is expects_resume


def test_existing_session_tools_inject_protocol_and_concurrency_fields():
    toolkit = MagicMock()
    toolkit.get_ready_steps.return_value = {
        'state_version': 7, 'ready_steps': ['draft'],
        'retryable_steps': [], 'rewindable_steps': [],
    }
    toolkit.advance_step.return_value = {'status': 'succeeded'}
    with patch('lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit',
               return_value=toolkit), patch(
        'lazymind.chat.workflow.workflow_manager._client',
    ) as client_factory:
        client_factory.return_value.get_state.return_value = {
            'status': 'active', 'state_version': 7,
        }
        contribution = resolve_workflow_injection(
            {'session_id': 'session-1', 'workflow_id': 'writer'},
            conversation_id='conversation-1',
        )

    advance = _tool(contribution, 'advance_step')
    assert list(inspect.signature(advance).parameters) == ['step_ids']
    assert list(inspect.signature(_tool(contribution, 'get_workflow_state')).parameters) == []
    assert advance(['draft']) == {'status': 'succeeded'}
    args = toolkit.advance_step.call_args.args
    assert args[0] == 'session-1'
    assert args[1] == 7
    assert args[2][0].step_id == 'draft'
    assert args[2][0].objective == ''
    assert args[2][0].task_id == ''


def test_advance_step_refreshes_state_version_once_on_conflict():
    toolkit = MagicMock()
    toolkit.get_ready_steps.side_effect = [
        {'state_version': 7, 'ready_steps': ['draft']},
        {'state_version': 8, 'ready_steps': ['draft']},
    ]
    toolkit.advance_step.side_effect = [
        WorkflowClientError('STATE_VERSION_CONFLICT', 'stale'),
        {'status': 'succeeded'},
    ]
    with patch('lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit',
               return_value=toolkit), patch(
        'lazymind.chat.workflow.workflow_manager._client',
    ) as client_factory:
        client_factory.return_value.get_state.return_value = {
            'status': 'active', 'state_version': 7,
        }
        contribution = resolve_workflow_injection(
            {'session_id': 'session-1', 'workflow_id': 'writer'},
            conversation_id='conversation-1',
        )

    result = _tool(contribution, 'advance_step')(['draft'])

    assert result['status'] == 'succeeded'
    assert result['state_version_refreshed'] is True
    assert '无需提供' in result['user_notice']
    assert [call.args[1] for call in toolkit.advance_step.call_args_list] == [7, 8]


def test_only_handoff_advance_stops_an_active_workflow_turn():
    toolkit = MagicMock()
    with patch('lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit',
               return_value=toolkit), patch(
        'lazymind.chat.workflow.workflow_manager._client',
    ) as client_factory:
        client_factory.return_value.get_state.return_value = {
            'status': 'active', 'state_version': 7,
        }
        contribution = resolve_workflow_injection(
            {'session_id': 'session-1', 'workflow_id': 'writer'},
            conversation_id='conversation-1',
        )

    assert contribution.stop_tools == ['advance_step_and_hand_off']


def test_advance_step_returns_user_notice_when_target_changes_after_conflict():
    toolkit = MagicMock()
    toolkit.get_ready_steps.side_effect = [
        {'state_version': 7, 'ready_steps': ['draft']},
        {'state_version': 8, 'ready_steps': ['review']},
    ]
    toolkit.advance_step.side_effect = WorkflowClientError(
        'STATE_VERSION_CONFLICT', 'stale',
    )
    with patch('lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit',
               return_value=toolkit), patch(
        'lazymind.chat.workflow.workflow_manager._client',
    ) as client_factory:
        client_factory.return_value.get_state.return_value = {
            'status': 'active', 'state_version': 7,
        }
        contribution = resolve_workflow_injection(
            {'session_id': 'session-1', 'workflow_id': 'writer'},
            conversation_id='conversation-1',
        )

    result = _tool(contribution, 'advance_step')(['draft'])

    assert result['outcome'] == 'workflow_state_changed'
    assert result['ready_steps'] == ['review']
    assert '重新确认' in result['user_notice']
    assert toolkit.advance_step.call_count == 1
