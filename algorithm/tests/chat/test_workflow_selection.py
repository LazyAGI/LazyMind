import inspect
import json
from unittest.mock import MagicMock, patch

import lazyllm
import pytest

from lazymind.chat.workflow.workflow_manager import (
    resolve_workflow_injection,
    workflow_startup_clarification_already_asked,
)
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
    pending = list(contribution.tools)
    while pending:
        tool = pending.pop(0)
        if isinstance(tool, dict):
            pending.extend(tool.get('tools') or [])
        elif tool.__name__ == name:
            return tool
    raise StopIteration(name)


def _tool_names(contribution):
    names = set()
    pending = list(contribution.tools)
    while pending:
        tool = pending.pop(0)
        if isinstance(tool, dict):
            names.add(str(tool.get('name') or ''))
            pending.extend(tool.get('tools') or [])
        else:
            names.add(tool.__name__)
    return names


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
    ).parameters) == ['request_context']
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
    ), patch('lazymind.chat.workflow.workflow_manager._resolve_workflow_attachment', return_value=(
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
            'compiled_graph': {
                'material_types': {'target_length': 'text'},
                'material_producers': {'target_length': {'kind': 'external'}},
            },
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


def test_selected_workflow_declares_missing_only_startup_clarification():
    runtime = {
        'clarification_fields': [
            {'id': 'topic', 'label': '主题', 'question': '主题是什么？', 'type': 'text'},
            {'id': 'slide_count', 'label': '页数', 'question': '生成多少页？',
             'type': 'single', 'choices': ['3 页', '5 页']},
        ],
    }
    contribution = resolve_workflow_injection(
        None,
        current_query='给协和医院做一份科技风 PPT',
        workflow_catalog=[{
            'workflow_ref': 'builtin:ppt-workflow',
            'workflow_id': 'ppt-workflow',
            'revision_id': 'revision-1',
            'runtime': runtime,
        }],
        allowed_workflow_refs=['builtin:ppt-workflow'],
        workflow_activations=[{
            'workflow_ref': 'builtin:ppt-workflow',
            'workflow_id': 'ppt-workflow',
            'revision_id': 'revision-1',
            'tool_name': 'trigger_ppt_workflow',
        }],
    )

    assert contribution.runtime_policy == runtime
    assert 'startup_clarification_fields' in contribution.runtime_context
    assert 'only those missing fields' in contribution.runtime_context
    assert '主题是什么？' in contribution.runtime_context
    assert '生成多少页？' in contribution.runtime_context
    assert 'exactly once TOTAL' in contribution.runtime_context
    assert 'context-specific suggested answers' in contribution.runtime_context


def test_startup_clarification_is_single_shot_and_default_trigger_merges_context():
    runtime = {
        'clarification_fields': [
            {'id': 'topic', 'question': '主题是什么？', 'type': 'text'},
            {'id': 'target_customer', 'question': '面向哪类客户？', 'type': 'text'},
        ],
    }
    history = [
        {'role': 'user', 'content': '根据知识库生成两页企业招标准则 PPT'},
        {
            'role': 'assistant',
            'content': '',
            'tool_calls': [{
                'id': 'ask-1',
                'type': 'function',
                'function': {
                    'name': 'ask_user',
                    'arguments': '{"questions":[{"id":"target_customer",'
                                 '"text":"面向哪类客户？","type":"single",'
                                 '"choices":["公司负责人","采购部门"]}]}',
                },
            }],
        },
        {'role': 'tool', 'name': 'ask_user', 'tool_call_id': 'ask-1',
         'content': 'The user submitted the form.'},
    ]
    assert workflow_startup_clarification_already_asked(history, runtime)

    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {
        'session_id': 'session-1', 'state_version': 1,
        'ready_steps': ['analyze_requirements'],
    }
    toolkit.get_ready_steps.return_value = {
        'session_id': 'session-1', 'state_version': 1,
        'ready_steps': ['analyze_requirements'],
        'retryable_steps': [], 'rewindable_steps': [],
    }
    toolkit.advance_step.return_value = {'status': 'succeeded'}
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ):
        client_factory.return_value.get_workflow.return_value.result = {
            'workflow_id': 'ppt-workflow', 'revision_id': 'revision-1',
        }
        client_factory.return_value.get_state.return_value = {
            'session_id': 'session-1', 'state_version': 1,
            'projection': {'ready': ['analyze_requirements']},
        }
        contribution = resolve_workflow_injection(
            None,
            current_query='面向哪类客户？: 公司负责人',
            conversation_history=history,
            workflow_catalog=[{
                'workflow_ref': 'builtin:ppt-workflow',
                'workflow_id': 'ppt-workflow',
                'revision_id': 'revision-1',
                'runtime': runtime,
            }],
            allowed_workflow_refs=['builtin:ppt-workflow'],
            workflow_activations=[{
                'workflow_ref': 'builtin:ppt-workflow',
                'workflow_id': 'ppt-workflow',
                'revision_id': 'revision-1',
                'tool_name': 'trigger_ppt_workflow',
            }],
        )
        # Even if the model supplies only this turn's answer, Host preserves
        # the original request and all of its already-known fields.
        result = _tool(contribution, 'trigger_ppt_workflow')(
            request_context='面向哪类客户？: 公司负责人',
        )
        _tool(contribution, 'advance_step')(['analyze_requirements'])

    assert '根据知识库生成两页企业招标准则 PPT' in result['request_context']
    assert '面向哪类客户？: 公司负责人' in result['request_context']
    toolkit.prepare_workflow.assert_called_once_with(
        'ppt-workflow', input_bindings={}, request_context=result['request_context'],
    )
    command = toolkit.advance_step.call_args.args[2][0]
    assert command.user_input == result['request_context']


def test_completed_historical_clarification_does_not_block_a_new_workflow_request():
    runtime = {'clarification_fields': [{
        'id': 'topic', 'question': '这份 PPT 的主题是什么？', 'type': 'text',
    }]}
    history = [
        {'role': 'user', 'content': '做一个 PPT'},
        {
            'role': 'assistant',
            'tool_calls': [{
                'id': 'ask-old',
                'type': 'function',
                'function': {
                    'name': 'ask_user',
                    'arguments': json.dumps({'questions': [{
                        'id': 'topic', 'text': '这份 PPT 的主题是什么？', 'type': 'text',
                    }]}, ensure_ascii=False),
                },
            }],
        },
        {'role': 'tool', 'name': 'ask_user', 'tool_call_id': 'ask-old', 'content': '产品介绍'},
        {'role': 'assistant', 'content': 'PPT 已生成完成。'},
        {'role': 'user', 'content': '再做一份 PPT'},
    ]

    assert not workflow_startup_clarification_already_asked(history, runtime)


def test_clarification_answer_turn_can_pass_merged_request_context_to_trigger():
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {
        'session_id': 'session-1', 'state_version': 1,
        'ready_steps': ['analyze_requirements'],
    }
    merged = (
        '原始需求：制作医院智算应用技术方案。'
        '补充：8页；客户是协和医院；科技未来风格。'
    )
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ):
        client_factory.return_value.get_workflow.return_value.result = {
            'workflow_id': 'ppt-workflow', 'revision_id': 'revision-1',
        }
        client_factory.return_value.get_state.return_value = {
            'session_id': 'session-1', 'state_version': 1,
            'projection': {'ready': ['analyze_requirements']},
        }
        contribution = resolve_workflow_injection(
            None,
            current_query='8页，协和医院，科技未来风格',
            workflow_catalog=[{
                'workflow_ref': 'builtin:ppt-workflow',
                'workflow_id': 'ppt-workflow',
                'revision_id': 'revision-1',
            }],
            allowed_workflow_refs=['builtin:ppt-workflow'],
            workflow_activations=[{
                'workflow_ref': 'builtin:ppt-workflow',
                'workflow_id': 'ppt-workflow',
                'revision_id': 'revision-1',
                'tool_name': 'trigger_ppt_workflow',
            }],
        )

        result = _tool(contribution, 'trigger_ppt_workflow')(request_context=merged)

    assert result['request_context'] == merged
    toolkit.prepare_workflow.assert_called_once_with(
        'ppt-workflow', input_bindings={}, request_context=merged,
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


def test_selected_workflow_session_tool_auto_initializes_without_attachments():
    toolkit = MagicMock()
    toolkit.prepare_workflow.return_value = {
        'session_id': 'session-1', 'state_version': 1,
        'ready_steps': ['analyze_requirements'],
    }
    toolkit.get_ready_steps.return_value = {
        'session_id': 'session-1', 'state_version': 1,
        'ready_steps': ['analyze_requirements'],
        'retryable_steps': [], 'rewindable_steps': [],
    }
    with patch('lazymind.chat.workflow.workflow_manager._client') as client_factory, patch(
        'lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit', return_value=toolkit,
    ):
        client_factory.return_value.get_workflow.return_value.result = {
            'workflow_id': 'ppt-workflow', 'revision_id': 'revision-1',
        }
        client_factory.return_value.get_state.return_value = {
            'session_id': 'session-1', 'state_version': 1,
            'projection': {
                'reachable': ['analyze_requirements'],
                'ready': ['analyze_requirements'],
            },
        }
        contribution = resolve_workflow_injection(
            None,
            current_query='生成三页关于加减法数学题的PPT',
            conversation_id='conversation-1',
            workflow_catalog=[{
                'workflow_ref': 'builtin:ppt-workflow',
                'workflow_id': 'ppt-workflow',
                'revision_id': 'revision-1',
            }],
            allowed_workflow_refs=['builtin:ppt-workflow'],
            workflow_activations=[{
                'workflow_ref': 'builtin:ppt-workflow',
                'workflow_id': 'ppt-workflow',
                'revision_id': 'revision-1',
                'tool_name': 'trigger_ppt_workflow',
            }],
        )

        result = _tool(contribution, 'get_ready_steps')()
        repeated_trigger = _tool(contribution, 'trigger_ppt_workflow')()

    assert result['ready_steps'] == ['analyze_requirements']
    assert repeated_trigger['outcome'] == 'already_initialized'
    assert repeated_trigger['session_id'] == 'session-1'
    toolkit.prepare_workflow.assert_called_once_with(
        'ppt-workflow', input_bindings={},
        request_context='生成三页关于加减法数学题的PPT',
    )
    assert 'upload is required' in contribution.runtime_context


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
    assert request.steps[0].user_input == 'run it'


def test_active_workflow_forwards_current_edit_request_and_focus_to_step():
    toolkit = MagicMock()
    toolkit.get_ready_steps.return_value = {
        'session_id': 'session-1', 'state_version': 7,
        'ready_steps': [], 'retryable_steps': [], 'rewindable_steps': ['generate_ppt'],
    }
    toolkit.advance_step.return_value = {'status': 'succeeded'}
    with patch('lazymind.chat.workflow.workflow_manager.HostWorkflowToolkit',
               return_value=toolkit), patch(
        'lazymind.chat.workflow.workflow_manager._client',
    ) as client_factory:
        client_factory.return_value.get_state.return_value = {
            'status': 'completed', 'state_version': 7,
            'projection': {
                'completed': True,
                'rewindable': ['generate_ppt'],
            },
        }
        contribution = resolve_workflow_injection(
            {
                'session_id': 'session-1',
                'workflow_id': 'deck-workflow',
                'runtime': {'completed_edit_step': 'generate_ppt'},
                'focused_tab': 'composite_preview',
                'focused_sort_order': 2,
            },
            conversation_id='conversation-1',
            current_query='把这一页标题改成期末练习',
        )
        # resolve_workflow_injection's patch is applied to agentic_config by ChatService.
        lazyllm.globals['agentic_config'].update(contribution.agentic_config_patch)
        result = _tool(contribution, 'advance_step')(['generate_ppt'])

    assert result == {'status': 'succeeded'}
    assert 'A completed Session is not immutable' in contribution.runtime_context
    assert "advance_step(step_ids=['generate_ppt'])" in contribution.runtime_context
    assert 'never paste replacement content into chat' in contribution.runtime_context
    command = toolkit.advance_step.call_args.args[2][0]
    assert command.user_input == '把这一页标题改成期末练习'
    assert 'sort order 2' in command.runtime_instruction


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


def test_enabled_workflow_without_catalog_exposes_only_lazy_authoring_group():
    contribution = resolve_workflow_injection(None, workflow_catalog=[])

    assert contribution.runtime_context == ''
    assert _tool_names(contribution) >= {'workflow_authoring', 'create_workflow_draft'}
    assert 'get_workflow' not in _tool_names(contribution)


def test_model_tool_projection_hides_controller_lifecycle_tools_without_session():
    contribution = resolve_workflow_injection(None, workflow_catalog=[])

    names = _tool_names(contribution)
    assert 'prepare_workflow' not in names
    assert 'start_workflow' not in names
    assert 'stop_workflow' not in names
    assert 'resume_workflow' not in names


@pytest.mark.parametrize('status', ['active', 'waiting', 'failed', 'completed', 'stopped'])
def test_existing_session_hides_controller_lifecycle_tools(status):
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
    assert 'resume_workflow' not in names


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
