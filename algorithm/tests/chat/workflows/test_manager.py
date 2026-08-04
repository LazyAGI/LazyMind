"""Tests for workflow_manager — cold-start triggers and advance_step tool builder.

External dependencies (_write_agent_data, lazyllm.globals, httpx) are fully mocked
so these tests run without a real LLM or algorithm service.
"""
from __future__ import annotations

import asyncio
import json
from unittest.mock import MagicMock, patch

import pytest

# Re-use the fixture that builds a temporary workflow directory.
from tests.chat.workflows.test_loader import make_workflow_dir


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture()
def loaded_workflow(tmp_path):
    """Load the test-workflow into the registry and yield; restore afterwards."""
    from lazymind.chat.workflow import workflow_loader
    workflows_dir = make_workflow_dir(tmp_path)
    with patch.object(workflow_loader, '_WORKFLOWS_DIR', workflows_dir):
        workflow_loader.load_all()
    yield
    workflow_loader.load_all()   # restore original registry


@pytest.fixture()
def mock_write_agent_data():
    with patch('lazymind.chat.workflow.workflow_manager._write_agent_data') as m:
        yield m


@pytest.fixture()
def mock_agentic_config():
    """Provide an injectable agentic_config dict."""
    config: dict = {}
    with patch('lazymind.chat.workflow.workflow_manager._agentic_config', return_value=config):
        yield config


@pytest.fixture(autouse=True)
def mock_layer2_imports():
    """Stub out the two lazy imports inside _trigger_workflow_step so tests never
    touch the network or require a live lazymind.config.

    Both imports are inside the function body, so we intercept them via
    builtins.__import__ before they execute.
    """
    import builtins
    real_import = builtins.__import__

    fake_httpx = MagicMock()
    fake_httpx.get.side_effect = Exception('httpx stubbed')
    start_plan = MagicMock()
    start_plan.status_code = 200
    start_plan.json.return_value = {
        'data': {'accepted': True, 'projection': {'ready': ['step_a']}, 'task_id': 'task-a'}
    }
    fake_httpx.post.return_value = start_plan

    fake_config_obj = MagicMock()
    fake_config_obj.get = MagicMock(return_value='http://core:8000')
    fake_config_module = MagicMock()
    fake_config_module.config = fake_config_obj

    def patched_import(name, *args, **kwargs):
        if name == 'httpx':
            return fake_httpx
        if name == 'lazymind.config':
            return fake_config_module
        if name == 'regex':
            import re
            return re
        return real_import(name, *args, **kwargs)

    with patch('builtins.__import__', side_effect=patched_import):
        yield


# ---------------------------------------------------------------------------
# build_cold_start_tools
# ---------------------------------------------------------------------------

def test_build_cold_start_tools_creates_one_trigger_per_workflow(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager
    tools = workflow_manager.build_cold_start_tools()
    assert len(tools) >= 1
    names = [t.__name__ for t in tools]
    assert 'trigger_test_workflow' in names


def test_build_cold_start_tools_honours_mentioned_workflow_allowlist(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager
    tools = workflow_manager.build_cold_start_tools(
        allowed_workflow_refs=['builtin:test-workflow'],
    )
    assert [tool.__name__ for tool in tools] == ['trigger_test_workflow']


def test_cold_start_prompt_treats_named_workflow_use_as_explicit_selection():
    from lazymind.chat.workflow import workflow_manager

    prompt = workflow_manager._COLD_START_PLUGIN_PROMPT
    assert '"Workflow" is a legacy internal synonym only' in prompt
    assert 'asks to use, run, start, launch, open, or enable it' in prompt
    assert '`explicit_workflow_request=true`' in prompt
    assert 'generic toolkit or same-domain tool' in prompt


def test_trigger_exposes_workflow_request_flag(loaded_workflow):
    import inspect
    from lazymind.chat.workflow import workflow_manager

    trigger = next(
        tool for tool in workflow_manager.build_cold_start_tools()
        if tool.__name__ == 'trigger_test_workflow'
    )
    assert 'explicit_workflow_request' in inspect.signature(trigger).parameters


def test_dynamic_launch_policy_defaults_to_hand_off():
    from lazymind.chat.workflow import workflow_manager

    policy = workflow_manager._build_cold_execution_policy('dynamic')
    assert 'Shared Workflow Decision Policy' in policy
    assert 'Ordinary Ready frontier' in policy
    assert 'LazyMind Workflow Launch Binding' in policy
    assert [tool.__name__ for tool in workflow_manager.build_cold_advance_tools()] == [
        'advance_step_and_hand_off', 'advance_step',
    ]


def test_cold_start_trigger_prepares_launch_without_creating_task(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    tools = workflow_manager.build_cold_start_tools()
    trigger = next(t for t in tools if t.__name__ == 'trigger_test_workflow')

    preflight = {
        'decision': 'ready',
        'reason': 'matches',
        'missing_information': [],
        'normalized_request': 'Draw a sunset',
        'first_step_id': 'step_a',
        'hand_off': True,
    }
    with patch.object(workflow_manager, '_evaluate_workflow_preflight', return_value=preflight):
        result = json.loads(trigger(request_context='Draw a sunset', explicit_workflow_request=False))

    assert result['status'] == 'ready'
    assert result['outcome'] == 'ready'
    assert result['must_advance'] is True
    assert result['launch_plan']['first_step_id'] == 'step_a'
    assert 'hand_off' not in result['launch_plan']
    assert 'advance_tool' not in result['launch_plan']
    assert 'step_a(Step A)' in result['step_name_index']
    assert 'step_d(Step D)' in result['step_name_index']
    assert result['first_step_default_approval'] == 'required'
    assert mock_agentic_config['prepared_workflow']['advance_committed'] is False
    mock_write_agent_data.assert_called_once()
    assert mock_write_agent_data.call_args.args[0] == 'workflow_preflight_updated'


def test_cold_start_trigger_hides_hand_off_choice_when_tool_is_static(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    mock_agentic_config['workflow_mode'] = 'auto'
    trigger = next(
        tool for tool in workflow_manager.build_cold_start_tools()
        if tool.__name__ == 'trigger_test_workflow'
    )
    preflight = {
        'decision': 'ready',
        'reason': 'matches',
        'missing_information': [],
        'normalized_request': 'Draw a sunset',
        'first_step_id': 'step_a',
    }

    with patch.object(workflow_manager, '_evaluate_workflow_preflight', return_value=preflight):
        result = json.loads(trigger(
            request_context='Draw a sunset',
            explicit_workflow_request=False,
        ))

    assert result['launch_plan']['advance_tool'] == 'advance_step_and_hand_off'
    assert 'hand_off' not in result['launch_plan']
    internal_plan = mock_agentic_config['prepared_workflow']['launch_plan']
    assert internal_plan['hand_off'] is True
    assert internal_plan['advance_tool'] == 'advance_step_and_hand_off'


def test_cold_start_trigger_rejects_empty_input(loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    tools = workflow_manager.build_cold_start_tools()
    trigger = next(t for t in tools if t.__name__ == 'trigger_test_workflow')

    result = json.loads(trigger(request_context='   ', explicit_workflow_request=False))
    assert result['status'] == 'preflight_failed'
    assert not mock_write_agent_data.called


def test_cold_start_trigger_need_information_does_not_prepare_launch(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    trigger = next(
        t for t in workflow_manager.build_cold_start_tools()
        if t.__name__ == 'trigger_test_workflow'
    )
    preflight = {
        'decision': 'need_information',
        'reason': 'size is required',
        'missing_information': [{'key': 'size', 'question': 'Which size?'}],
        'normalized_request': 'Draw a sunset',
        'first_step_id': '',
        'hand_off': True,
    }
    with patch.object(workflow_manager, '_evaluate_workflow_preflight', return_value=preflight):
        result = json.loads(trigger(request_context='Draw a sunset', explicit_workflow_request=False))

    assert result['status'] == 'need_information'
    assert 'prepared_workflow' not in mock_agentic_config
    assert mock_agentic_config['workflow_preflight_context']['original_intent'] == 'Draw a sunset'
    assert mock_write_agent_data.call_args.args[0] == 'workflow_preflight_updated'


def test_explicit_workflow_request_cannot_be_rejected_as_not_applicable(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    trigger = next(
        t for t in workflow_manager.build_cold_start_tools()
        if t.__name__ == 'trigger_test_workflow'
    )
    preflight = {
        'decision': 'not_applicable',
        'reason': 'The task is simple enough to answer directly.',
        'missing_information': [],
        'normalized_request': 'Use the test workflow to draw a sunset',
        'first_step_id': '',
        'hand_off': True,
    }

    with patch.object(workflow_manager, '_evaluate_workflow_preflight', return_value=preflight):
        result = json.loads(trigger(
            request_context='Use the test workflow to draw a sunset',
            explicit_workflow_request=True,
        ))

    assert result['status'] == 'ready'
    assert result['launch_plan']['first_step_id'] == 'step_a'
    assert mock_agentic_config['prepared_workflow']['must_advance'] is True


def test_implicit_workflow_request_can_still_be_not_applicable(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    trigger = next(
        t for t in workflow_manager.build_cold_start_tools()
        if t.__name__ == 'trigger_test_workflow'
    )
    preflight = {
        'decision': 'not_applicable',
        'reason': 'The request does not need this workflow.',
        'missing_information': [],
        'normalized_request': 'Say hello',
        'first_step_id': '',
        'hand_off': True,
    }

    with patch.object(workflow_manager, '_evaluate_workflow_preflight', return_value=preflight):
        result = json.loads(trigger(request_context='Say hello', explicit_workflow_request=False))

    assert result['status'] == 'not_applicable'
    assert result['outcome'] == 'not_applicable'
    assert 'prepared_workflow' not in mock_agentic_config


def test_explicit_workflow_choice_persists_across_clarification_turns(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    trigger = next(
        t for t in workflow_manager.build_cold_start_tools()
        if t.__name__ == 'trigger_test_workflow'
    )
    need_info = {
        'decision': 'need_information',
        'reason': 'A required value is missing.',
        'missing_information': [{'key': 'value', 'question': 'Which value?'}],
        'normalized_request': 'Use the test workflow',
        'first_step_id': '',
        'hand_off': True,
    }
    contradictory_follow_up = {
        'decision': 'not_applicable',
        'reason': 'This answer alone does not mention the workflow.',
        'missing_information': [],
        'normalized_request': 'Use the test workflow with value 42',
        'first_step_id': '',
        'hand_off': True,
    }

    with patch.object(
        workflow_manager,
        '_evaluate_workflow_preflight',
        side_effect=[need_info, contradictory_follow_up],
    ):
        first = json.loads(trigger(
            request_context='Use the test workflow',
            explicit_workflow_request=True,
        ))
        second = json.loads(trigger(
            request_context='Use value 42',
            explicit_workflow_request=False,
        ))

    assert first['status'] == 'need_information'
    assert second['status'] == 'ready'
    assert mock_agentic_config['prepared_workflow']['explicit_workflow_request'] is True


def test_retrigger_preserves_original_intent_and_accumulates_confirmations(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    trigger = next(
        t for t in workflow_manager.build_cold_start_tools()
        if t.__name__ == 'trigger_test_workflow'
    )
    need_info = {
        'decision': 'need_information',
        'reason': 'need style',
        'missing_information': [{'key': 'style', 'question': 'Which style?'}],
        'normalized_request': 'Draw a sunset',
        'first_step_id': '',
        'hand_off': True,
    }
    ready = {
        'decision': 'ready',
        'reason': 'complete',
        'missing_information': [],
        'normalized_request': 'Draw a watercolor sunset',
        'first_step_id': 'step_a',
        'hand_off': False,
    }
    with patch.object(
        workflow_manager, '_evaluate_workflow_preflight', side_effect=[need_info, ready]
    ):
        trigger(request_context='Draw a sunset', explicit_workflow_request=False)
        result = json.loads(trigger(
            request_context='Use watercolor style',
            explicit_workflow_request=False,
        ))

    prepared = mock_agentic_config['prepared_workflow']
    assert result['status'] == 'ready'
    assert prepared['original_intent'] == 'Draw a sunset'
    assert prepared['confirmation_answers'] == ['Use watercolor style']
    assert prepared['launch_plan']['normalized_request'] == 'Draw a watercolor sunset'


def test_cold_advance_commits_exact_prepared_plan(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    mock_agentic_config['prepared_workflow'] = {
        'workflow_id': 'test-workflow',
        'preflight_id': 'pf-1',
        'must_advance': True,
        'advance_committed': False,
        'launch_plan': {
            'first_step_id': 'step_a',
            'normalized_request': 'Draw a sunset after all confirmations',
            'hand_off': True,
        },
    }
    handoff = next(
        t for t in workflow_manager.build_cold_advance_tools()
        if t.__name__ == 'advance_step_and_hand_off'
    )

    result = handoff(step_id='step_a')

    assert 'acceptance is pending' in result.lower()
    params = mock_write_agent_data.call_args.kwargs['params']
    assert params['is_cold_start'] is True
    assert params['hand_off'] is True
    assert params['preflight_id'] == 'pf-1'
    assert params['user_input'] == 'Draw a sunset after all confirmations'
    assert mock_agentic_config['prepared_workflow']['advance_committed'] is True


def test_cold_advance_allows_chat_agent_choice_when_launch_has_no_hand_off(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    mock_agentic_config['prepared_workflow'] = {
        'workflow_id': 'test-workflow',
        'preflight_id': 'pf-choice',
        'must_advance': True,
        'advance_committed': False,
        'fallback_hand_off': True,
        'launch_plan': {
            'first_step_id': 'step_a',
            'normalized_request': 'Continue to Step D, then ask for confirmation',
        },
    }

    result = workflow_manager._commit_prepared_workflow(
        'step_a', hand_off=False, wait_for_result=False
    )

    assert 'acceptance is pending' in result
    params = mock_write_agent_data.call_args.kwargs['params']
    assert params['hand_off'] is False
    assert mock_agentic_config['prepared_workflow']['advance_committed'] is True


def test_cold_advance_rejects_tool_that_disagrees_with_launch_plan(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    mock_agentic_config['prepared_workflow'] = {
        'workflow_id': 'test-workflow',
        'preflight_id': 'pf-1',
        'must_advance': True,
        'advance_committed': False,
        'launch_plan': {
            'first_step_id': 'step_a',
            'normalized_request': 'Draw a sunset',
            'hand_off': False,
        },
    }
    handoff = next(
        t for t in workflow_manager.build_cold_advance_tools()
        if t.__name__ == 'advance_step_and_hand_off'
    )

    with pytest.raises(ValueError, match='requires advance_step'):
        handoff(step_id='step_a')
    assert not mock_write_agent_data.called


def test_deterministic_fallback_executes_only_the_validated_plan(
        loaded_workflow, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    mock_agentic_config['prepared_workflow'] = {
        'workflow_id': 'test-workflow',
        'preflight_id': 'pf-fallback',
        'must_advance': True,
        'advance_committed': False,
        'launch_plan': {
            'first_step_id': 'step_a',
            'normalized_request': 'Run continuously without interruption',
            'hand_off': False,
        },
    }

    result = workflow_manager.commit_prepared_workflow_fallback()

    assert 'acceptance is pending' in result
    params = mock_write_agent_data.call_args.kwargs['params']
    assert params['step_id'] == 'step_a'
    assert params['hand_off'] is False
    assert params['preflight_id'] == 'pf-fallback'
    assert mock_agentic_config['prepared_workflow']['advance_committed'] is True


def test_preflight_model_uses_llm_role_json_mode_and_timeout():
    from lazymind.chat.workflow import workflow_manager
    llm = MagicMock(return_value=json.dumps({
        'decision': 'ready',
        'reason': 'matches',
        'missing_information': [],
        'normalized_request': 'Draw a sunset',
        'first_step_id': 'step_a',
        'hand_off': True,
    }))
    with (
        patch.object(workflow_manager, 'is_model_role_available', return_value=True),
        patch.object(workflow_manager.lazyllm, 'AutoModel', return_value=llm) as auto_model,
    ):
        result = workflow_manager._evaluate_workflow_preflight(
            workflow_id='test-workflow',
            workflow_name='Test Workflow',
            description='Test',
            when_to_use='Use for tests',
            scenario='Scenario',
            request_context='Draw a sunset',
            previous=None,
            first_steps=['step_a'],
            workflow_mode='dynamic',
        )

    assert result['decision'] == 'ready'
    auto_model.assert_called_once_with(model='llm')
    assert llm.call_args.kwargs['response_format'] == {'type': 'json_object'}
    assert llm.call_args.kwargs['stream_output'] is False
    assert llm.call_args.kwargs['timeout'] == workflow_manager._PREFLIGHT_TIMEOUT_SECONDS
    assert 'hand_off' not in llm.call_args.args[0]
    assert 'Default approval' not in llm.call_args.args[0]


def test_preflight_without_approval_choice_hides_mode_and_hand_off_policy():
    from lazymind.chat.workflow import workflow_manager
    llm = MagicMock(return_value=json.dumps({
        'decision': 'ready',
        'reason': 'matches',
        'missing_information': [],
        'normalized_request': 'Draw a sunset',
        'first_step_id': 'step_a',
    }))
    with (
        patch.object(workflow_manager, 'is_model_role_available', return_value=True),
        patch.object(workflow_manager.lazyllm, 'AutoModel', return_value=llm),
    ):
        result = workflow_manager._evaluate_workflow_preflight(
            workflow_id='test-workflow',
            workflow_name='Test Workflow',
            description='Test',
            when_to_use='Use for tests',
            scenario='Scenario',
            request_context='Draw a sunset',
            previous=None,
            first_steps=['step_a'],
            workflow_mode='auto',
        )

    prompt = llm.call_args.args[0]
    assert result['hand_off'] is True
    assert 'hand_off' not in prompt
    assert 'Default approval' not in prompt
    assert 'Workflow mode' not in prompt
    assert 'dynamic mode' not in prompt.lower()
    assert 'auto mode' not in prompt.lower()


def test_preflight_json_repair_is_also_hidden_from_user_stream():
    from lazymind.chat.workflow import workflow_manager
    llm = MagicMock(side_effect=[
        'not valid json',
        json.dumps({
            'decision': 'ready',
            'reason': 'matches',
            'missing_information': [],
            'normalized_request': 'Draw a sunset',
            'first_step_id': 'step_a',
            'hand_off': True,
        }),
    ])
    with (
        patch.object(workflow_manager, 'is_model_role_available', return_value=True),
        patch.object(workflow_manager.lazyllm, 'AutoModel', return_value=llm),
    ):
        result = workflow_manager._evaluate_workflow_preflight(
            workflow_id='test-workflow',
            workflow_name='Test Workflow',
            description='Test',
            when_to_use='Use for tests',
            scenario='Scenario',
            request_context='Draw a sunset',
            previous=None,
            first_steps=['step_a'],
            workflow_mode='dynamic',
        )

    assert result['decision'] == 'ready'
    assert llm.call_count == 2
    assert all(call.kwargs['stream_output'] is False for call in llm.call_args_list)


def test_cold_injection_without_approval_choice_registers_only_hand_off_tool(
        loaded_workflow, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    mock_agentic_config['enable_workflow'] = True

    contribution = workflow_manager.resolve_workflow_injection({
        'workflow_mode': 'auto',
        'workflow_preflight': {
            'preflight_id': 'pf-old',
            'workflow_id': 'test-workflow',
            'status': 'collecting',
            'original_intent': 'Original request ten turns ago',
            'normalized_request': 'Original request plus answers',
        },
    })
    tools = contribution.tools
    stop_tools = contribution.stop_tools
    patch_config = contribution.agentic_config_patch
    context = contribution.runtime_context

    names = {tool.__name__ for tool in tools}
    assert 'trigger_test_workflow' in names
    assert 'advance_step' not in names
    assert 'advance_step_and_hand_off' in names
    assert stop_tools == ['advance_step_and_hand_off']
    assert 'trigger_test_workflow' not in stop_tools
    assert patch_config['workflow_mode'] == 'auto'
    assert patch_config['workflow_preflight_context']['preflight_id'] == 'pf-old'
    assert 'Original request ten turns ago' in context
    assert 'Shared Workflow Decision Policy' in context
    assert 'cannot choose a Runtime operation' in context


def test_compact_step_name_index_has_names_but_no_graph_details(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager

    index = workflow_manager._build_step_name_index('test-workflow')

    assert 'step_a(Step A)' in index
    assert 'step_b(Step B)' in index
    assert 'step_c(Step C)' in index
    assert 'step_d(Step D)' in index
    assert 'default approval' not in index.lower()
    assert 'condition' not in index.lower()
    assert 'route:' not in index.lower()


def test_active_injection_switches_tools_and_request_local_policy_per_turn(
        loaded_workflow, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    mock_agentic_config['enable_workflow'] = True
    workflow_context = {
        'session_id': 'session-1',
        'workflow_id': 'test-workflow',
        'current_step': 'step_a',
    }

    with (
        patch.object(workflow_manager, '_fetch_go_projection', return_value={'past': [], 'ready': ['step_b']}),
        patch.object(workflow_manager, '_build_session_artifact_section', return_value='artifacts'),
        patch.object(workflow_manager, '_build_intent_section', return_value=''),
        patch.object(workflow_manager, '_build_step_status_section', return_value='step status'),
    ):
        auto_result = workflow_manager.resolve_workflow_injection({
            **workflow_context,
            'workflow_mode': 'auto',
        })
        dynamic_result = workflow_manager.resolve_workflow_injection({
            **workflow_context,
            'workflow_mode': 'dynamic',
        })

    auto_tools = auto_result.tools
    auto_system_prompt = auto_result.system_prompt
    auto_stop_tools = auto_result.stop_tools
    auto_context = auto_result.runtime_context
    dynamic_tools = dynamic_result.tools
    dynamic_system_prompt = dynamic_result.system_prompt
    dynamic_stop_tools = dynamic_result.stop_tools
    dynamic_context = dynamic_result.runtime_context
    auto_names = {tool.__name__ for tool in auto_tools}
    dynamic_names = {tool.__name__ for tool in dynamic_tools}

    assert 'advance_step_and_hand_off' in auto_names
    assert 'advance_step' not in auto_names
    assert {'advance_step', 'advance_step_and_hand_off'} <= dynamic_names
    assert 'advance_steps' not in dynamic_names
    assert 'advance_steps_and_hand_off' not in dynamic_names
    assert set(auto_stop_tools) == {'advance_step_and_hand_off'}
    assert set(dynamic_stop_tools) == {'advance_step_and_hand_off'}
    assert 'Current Workflow Execution Policy' not in auto_system_prompt
    assert 'Current Workflow Execution Policy' not in dynamic_system_prompt
    assert 'Shared Workflow Decision Policy' in auto_context
    assert 'Shared Workflow Decision Policy' in dynamic_context
    assert 'Workflow Step Name Index' in auto_context
    assert 'step_a(Step A)' in auto_context
    assert 'step_d(Step D)' in dynamic_context
    assert 'default approval' not in auto_context.lower()
    assert 'approval' in dynamic_context.lower()
    assert 'auto mode' not in auto_context.lower()
    assert 'dynamic mode' not in dynamic_context.lower()

    auto_advance = next(
        tool for tool in auto_tools if tool.__name__ == 'advance_step_and_hand_off'
    )
    dynamic_advance = next(
        tool for tool in dynamic_tools if tool.__name__ == 'advance_step_and_hand_off'
    )
    assert 'default approval' not in (auto_advance.__doc__ or '').lower()
    assert 'default approval' in (dynamic_advance.__doc__ or '').lower()


def test_workflow_stream_guard_is_noop_without_ready_preflight(mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager

    async def initial_stream():
        yield 'event', {'tag': 'text', 'delta': 'ordinary answer'}
        yield 'final', 'ordinary answer'

    async def collect():
        return [item async for item in workflow_manager.guard_workflow_agent_stream(
            initial_stream(),
            all_tools=[],
            query='hello',
            runtime_prompt='prompt',
            agent=MagicMock(),
            runtime_config=MagicMock(),
            fs=MagicMock(),
            stop_tools=[],
            history=[],
        )]

    assert asyncio.run(collect()) == [
        ('event', {'tag': 'text', 'delta': 'ordinary answer'}),
        ('final', 'ordinary answer'),
    ]


def test_workflow_stream_guard_suppresses_prose_while_advance_is_pending(mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager
    mock_agentic_config['prepared_workflow'] = {
        'must_advance': True,
        'advance_committed': False,
    }

    assert workflow_manager._should_suppress_prepared_workflow_text({
        'tag': 'text', 'delta': 'I will explain instead',
    }) is True
    assert workflow_manager._should_suppress_prepared_workflow_text({
        'tag': 'tool_calls', 'tool_calls': [],
    }) is False


# ---------------------------------------------------------------------------
# build_advance_step_tool
# ---------------------------------------------------------------------------

def test_render_step_objective_replaces_user_input():
    from lazymind.chat.workflow.workflow_manager import _render_step_objective
    cfg = {'prompt': 'Analyze {{user_input}} carefully.'}
    rendered = _render_step_objective(cfg, 'a sunset over the ocean')
    assert 'a sunset over the ocean' in rendered
    assert '{{user_input}}' not in rendered


def test_render_step_objective_leaves_other_placeholders():
    from lazymind.chat.workflow.workflow_manager import _render_step_objective
    cfg = {'prompt': 'Enhance {{image_url}} based on {{user_input}}.'}
    rendered = _render_step_objective(cfg, 'high contrast')
    assert '{{image_url}}' in rendered       # Python runner injects this via _enrich_objective_with_artifacts
    assert '{{user_input}}' not in rendered
    assert 'high contrast' in rendered


def test_render_step_objective_empty_prompt():
    from lazymind.chat.workflow.workflow_manager import _render_step_objective
    rendered = _render_step_objective({}, 'anything')
    assert rendered == ''


def test_export_parent_agentic_config_preserves_runtime_context_without_credentials():
    from lazymind.chat.workflow.workflow_manager import _export_parent_agentic_config

    exported = _export_parent_agentic_config({
        'databases': [{'id': 'db-1'}],
        'dataset': 'default',
        'local_fs_sources': [{'path': '/tmp/source'}],
        'priority': 3,
        'memory': 'preference',
        'llm_config': {'llm': {'api_key': 'secret'}},
        'tool_config': {'search': {'api_key': 'secret'}},
        'ocr_config': {'api_key': 'secret'},
        'citation_state': object(),
    })

    assert exported['databases'] == [{'id': 'db-1'}]
    assert exported['dataset'] == 'default'
    assert exported['local_fs_sources'] == [{'path': '/tmp/source'}]
    assert exported['priority'] == 3
    assert exported['memory'] == 'preference'
    assert 'llm_config' not in exported
    assert 'tool_config' not in exported
    assert 'ocr_config' not in exported
    assert 'citation_state' not in exported


def test_trigger_workflow_step_rejects_missing_step_config(mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager

    mock_agentic_config.update({'workflow_step': 'step_a', 'workflow_session_id': 'ps-missing'})
    with (
        patch.object(workflow_manager.workflow_loader, 'get_workflow', return_value=object()),
        patch.object(workflow_manager.workflow_loader, 'get_step_config', return_value={}),
    ):
        with pytest.raises(ValueError, match='not defined'):
            workflow_manager._trigger_workflow_step(
                'test-workflow',
                'missing_step',
                'go',
                is_cold_start=False,
            )


def test_trigger_workflow_step_uses_unified_advance_operation(
        loaded_workflow, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager

    mock_agentic_config.update({
        'workflow_step': 'step_a', 'workflow_session_id': 'session-advance',
    })
    accepted = workflow_manager._TransitionSubmission(True, 'accepted', task_id='task-b')
    with patch.object(workflow_manager, '_submit_transition_to_core', return_value=accepted) as submit:
        workflow_manager._trigger_workflow_step('test-workflow', 'step_b', 'continue')

    assert submit.call_args.kwargs['operation'] == 'advance'


def test_trigger_workflow_steps_submits_one_atomic_batch(
        loaded_workflow, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager

    mock_agentic_config.update({
        'workflow_session_id': 'session-batch',
        'query': 'continue workflow',
    })
    accepted = workflow_manager._TransitionSubmission(
        accepted=True,
        message='accepted',
        command_id='command-batch',
        task_id='task-b',
        tasks=[
            {'step_id': 'step_b', 'task_id': 'task-b', 'step_state': 'pending'},
            {'step_id': 'step_c', 'task_id': 'task-c', 'step_state': 'pending'},
        ],
    )
    with patch.object(workflow_manager, '_submit_transition_to_core', return_value=accepted) as submit:
        result = workflow_manager._trigger_workflow_steps('test-workflow', [
            {'step_id': 'step_b', 'user_input': 'run B', 'runtime_instruction': 'instruction B'},
            {'step_id': 'step_c', 'user_input': 'run C', 'runtime_instruction': 'instruction C'},
        ])

    assert result.accepted is True
    kwargs = submit.call_args.kwargs
    assert kwargs['operation'] == 'execute_batch'
    assert [target['target_step_id'] for target in kwargs['targets']] == ['step_b', 'step_c']
    assert kwargs['targets'][0]['runtime_instruction'] == 'instruction B'
    assert kwargs['targets'][1]['runtime_instruction'] == 'instruction C'
    assert mock_agentic_config['_last_workflow_tasks'][1]['task_id'] == 'task-c'


def test_trigger_workflow_steps_rejects_duplicate_step_locally(
        loaded_workflow, mock_agentic_config):
    from lazymind.chat.workflow import workflow_manager

    mock_agentic_config['workflow_session_id'] = 'session-batch'
    with pytest.raises(ValueError, match='duplicate batch step_id'):
        workflow_manager._trigger_workflow_steps('test-workflow', [
            {'step_id': 'step_b', 'user_input': 'first'},
            {'step_id': 'step_b', 'user_input': 'second'},
        ])


# ---------------------------------------------------------------------------
# _trigger_workflow_step — layer 1 format validation (no DB / HTTP needed)
# ---------------------------------------------------------------------------

def test_framework_tools_always_present_even_when_step_declares_none(
        loaded_workflow, mock_agentic_config, mock_write_agent_data):
    """step_a declares no tools in state.yml; framework tools must still be injected."""
    from lazymind.chat.workflow.workflow_manager import _trigger_workflow_step, _FRAMEWORK_TOOLS
    mock_agentic_config['workflow_step'] = '__start__'

    _trigger_workflow_step('test-workflow', 'step_a', 'hello', is_cold_start=True)

    assert mock_write_agent_data.called
    tools = mock_write_agent_data.call_args.kwargs['tools']
    for fw_tool in _FRAMEWORK_TOOLS:
        assert fw_tool in tools, f'framework tool {fw_tool!r} missing from tools list'


def test_framework_tools_prepended_before_workflow_tools(
        loaded_workflow, mock_agentic_config, mock_write_agent_data):
    """Framework tools are first in the merged list; workflow-declared tools come after."""
    from lazymind.chat.workflow.workflow_manager import _trigger_workflow_step, _FRAMEWORK_TOOLS
    mock_agentic_config['workflow_step'] = 'step_c'

    _trigger_workflow_step('test-workflow', 'step_d', 'enhance it', is_cold_start=False)

    tools = mock_write_agent_data.call_args.kwargs['tools']
    for i, fw_tool in enumerate(_FRAMEWORK_TOOLS):
        assert tools[i] == fw_tool, (
            f'expected framework tool at position {i}: {fw_tool!r}, got {tools[i]!r}'
        )
    # Workflow-declared tool must also be present.
    assert 'enhance_tool' in tools


def test_framework_tools_no_duplicates(
        loaded_workflow, mock_agentic_config, mock_write_agent_data):
    """If a workflow explicitly declares a framework tool, there should be no duplicate."""
    from lazymind.chat.workflow.workflow_manager import _merge_tools
    merged = _merge_tools(['save_artifacts', 'my_custom_tool', 'load_artifact'])
    assert merged.count('save_artifacts') == 1
    assert merged.count('load_artifact') == 1
    assert 'my_custom_tool' in merged


# ---------------------------------------------------------------------------
# runtime_instruction
# ---------------------------------------------------------------------------

def test_render_step_objective_replaces_runtime_instruction():
    from lazymind.chat.workflow.workflow_manager import _render_step_objective
    cfg = {'prompt': 'Do {{user_input}}. {{runtime_instruction}}'}
    rendered = _render_step_objective(cfg, 'draw a cat', 'Only draw the left eye.')
    assert 'draw a cat' in rendered
    assert 'Only draw the left eye.' in rendered
    assert '{{runtime_instruction}}' not in rendered
    assert '{{user_input}}' not in rendered


def test_render_step_objective_empty_runtime_instruction_removed():
    from lazymind.chat.workflow.workflow_manager import _render_step_objective
    cfg = {'prompt': 'Do {{user_input}}. {{runtime_instruction}} Done.'}
    rendered = _render_step_objective(cfg, 'draw a cat')
    assert '{{runtime_instruction}}' not in rendered
    # Placeholder replaced with empty string, surrounding text intact.
    assert 'Done.' in rendered


def test_enrich_objective_no_placeholders():
    """Objective without {{ }} is returned as-is without hitting the DB."""
    from lazymind.chat.engine.subagent.runner import _enrich_objective_with_artifacts
    from unittest.mock import MagicMock

    db = MagicMock()
    result = _enrich_objective_with_artifacts('Analyze the image.', {'session_id': 'ps-1'}, db)
    assert result == 'Analyze the image.'
    db.load_workflow_session_steps.assert_not_called()


def test_enrich_objective_no_session_id():
    """Missing session_id falls back to original objective."""
    from lazymind.chat.engine.subagent.runner import _enrich_objective_with_artifacts
    from unittest.mock import MagicMock

    db = MagicMock()
    result = _enrich_objective_with_artifacts('Do {{something}}.', {}, db)
    assert result == 'Do {{something}}.'
    db.load_workflow_session_steps.assert_not_called()


def test_enrich_objective_replaces_placeholders():
    """Artifacts from succeeded steps are substituted into the objective."""
    from lazymind.chat.engine.subagent.runner import _enrich_objective_with_artifacts
    from unittest.mock import MagicMock

    db = MagicMock()
    db.load_workflow_session_steps.return_value = [
        {'step_id': 'step_a', 'task_id': 'task-001', 'status': 'succeeded'},
    ]
    db.load_artifacts_for_tasks.return_value = [
        {'task_id': 'task-001', 'slot': 'prompt_used', 'content_type': 'text',
         'value': {'text': 'a beautiful sunset'}, 'seq': 1},
    ]

    objective = 'Generate image from: {{prompt_used}}.'
    result = _enrich_objective_with_artifacts(objective, {'session_id': 'ps-1'}, db)
    assert 'a beautiful sunset' in result
    assert '{{prompt_used}}' not in result


def test_enrich_objective_skips_non_succeeded_steps():
    """Only artifacts from succeeded steps are used."""
    from lazymind.chat.engine.subagent.runner import _enrich_objective_with_artifacts
    from unittest.mock import MagicMock

    db = MagicMock()
    db.load_workflow_session_steps.return_value = [
        {'step_id': 'step_a', 'task_id': 'task-running', 'status': 'running'},
    ]
    objective = 'Generate from: {{analysis}}.'
    result = _enrich_objective_with_artifacts(objective, {'session_id': 'ps-1'}, db)
    # No succeeded steps → placeholder stays.
    assert '{{analysis}}' in result
    db.load_artifacts_for_tasks.assert_not_called()


def test_enrich_objective_db_error_falls_back():
    """Any DB error falls back gracefully to original objective."""
    from lazymind.chat.engine.subagent.runner import _enrich_objective_with_artifacts
    from unittest.mock import MagicMock

    db = MagicMock()
    db.load_workflow_session_steps.side_effect = Exception('DB unavailable')
    objective = 'Enhance: {{image_url}}.'
    result = _enrich_objective_with_artifacts(objective, {'session_id': 'ps-err'}, db)
    assert result == objective


# ---------------------------------------------------------------------------
# _resolve_workflow_step_tools (runner-side tools resolution)
# ---------------------------------------------------------------------------

def test_resolve_workflow_step_tools_returns_merged_list(loaded_workflow):
    """Tools for a known step_id are resolved from workflow_loader."""
    from lazymind.chat.engine.subagent.runner import _resolve_workflow_step_tools

    # step_d declares enhance_tool in state.yml; framework tools must be prepended.
    tools = _resolve_workflow_step_tools({'workflow_id': 'test-workflow', 'step_id': 'step_d'})
    assert tools is not None
    assert 'save_artifacts' in tools
    assert 'enhance_tool' in tools
    # Framework tools come first.
    assert tools.index('save_artifacts') < tools.index('enhance_tool')


def test_resolve_workflow_step_tools_no_declared_tools_returns_only_framework(loaded_workflow):
    """step_a declares no tools; only framework tools are returned."""
    from lazymind.chat.engine.subagent.runner import _resolve_workflow_step_tools

    tools = _resolve_workflow_step_tools({'workflow_id': 'test-workflow', 'step_id': 'step_a'})
    assert tools is not None
    assert 'save_artifacts' in tools
    assert 'get_artifact' in tools


def test_resolve_workflow_step_tools_unknown_workflow_returns_none(loaded_workflow):
    """Unknown workflow_id returns None so caller can fall back."""
    from lazymind.chat.engine.subagent.runner import _resolve_workflow_step_tools

    result = _resolve_workflow_step_tools({'workflow_id': 'nonexistent-workflow', 'step_id': 'step_a'})
    assert result is None


def test_resolve_workflow_step_tools_missing_params_returns_none(loaded_workflow):
    """Empty params returns None."""
    from lazymind.chat.engine.subagent.runner import _resolve_workflow_step_tools

    assert _resolve_workflow_step_tools({}) is None


def test_build_advance_step_tool_docstring_contains_forward_steps(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager
    advance = workflow_manager.build_advance_step_tool(
        'test-workflow', 'step_a',
        rewind_steps=[],
        step_labels={'step_b': 'Optimize'},
    )
    doc = advance.__doc__ or ''
    assert 'step_b' in doc
    assert 'Forward' in doc
    assert 'Optimize' in doc
    assert 'default approval: required' in doc


def test_hand_off_tool_doc_is_mode_neutral(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager

    hand_off = workflow_manager.build_advance_step_and_hand_off_tool(
        'test-workflow', 'step_a', rewind_steps=[]
    )
    doc = hand_off.__doc__ or ''

    assert 'Start one or more Ready workflow steps asynchronously' in doc
    assert 'dynamic' not in doc
    assert 'auto' not in doc


def test_step_choice_doc_uses_configured_default_approval(loaded_workflow):
    from lazymind.chat.workflow import workflow_loader, workflow_manager

    spec = workflow_loader.get_workflow('test-workflow')
    assert spec is not None
    spec._steps['step_b']['mode'] = 'auto'
    advance = workflow_manager.build_advance_step_tool(
        'test-workflow', 'step_a',
        rewind_steps=[],
        step_labels={'step_b': 'Optimize'},
    )

    assert 'step_b' in (advance.__doc__ or '')
    assert 'default approval: not required' in (advance.__doc__ or '')


def test_build_advance_step_tool_docstring_contains_rerunnable_steps(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager
    advance = workflow_manager.build_advance_step_tool(
        'test-workflow', 'step_b',
        rewind_steps=['step_a'],
        step_labels={'step_a': 'Analyze Subject', 'step_c': 'Generate Image'},
    )
    doc = advance.__doc__ or ''
    assert 'step_a' in doc
    assert 'Previously attempted steps that may be run again' in doc
    assert 'Analyze Subject' in doc
    assert 'previously completed' in doc


def test_build_advance_step_tool_docstring_no_rerun_when_empty(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager
    advance = workflow_manager.build_advance_step_tool(
        'test-workflow', 'step_a',
        rewind_steps=[],
    )
    doc = advance.__doc__ or ''
    assert 'Previously attempted steps that may be run again' not in doc


def test_live_projection_does_not_offer_succeeded_current_step_as_retry(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager

    config = {'workflow_session_id': 'writer-session', 'workflow_step': 'step_a'}
    projection = {
        'past': ['step_a'],
        'ready': ['step_b'],
        'nodes': {'step_a': {'execution': 'succeeded'}},
    }
    with (
        patch.object(workflow_manager, '_agentic_config', return_value=config),
        patch.object(workflow_manager, '_fetch_go_projection', return_value=projection),
    ):
        advance = workflow_manager.build_advance_step_tool('test-workflow', 'step_a')

    doc = advance.__doc__ or ''
    assert 'Retry (re-run current step):' not in doc
    assert 'step_b' in doc
    assert 'step_a' in doc
    assert 'Previously attempted steps that may be run again' in doc


def test_dynamic_guidance_respects_explicit_target_boundary(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager

    guidance = workflow_manager._build_mode_guidance('dynamic')

    assert 'Explicit continuous scope/boundary' in guidance
    assert 'requested/final boundary' in guidance
    assert 'Runtime projection is authoritative' in guidance


def test_guidance_without_approval_choice_assigns_continuation_to_backend(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager

    guidance = workflow_manager._build_mode_guidance('auto')

    assert 'Driver is enabled' in guidance
    assert 'changes only turn orchestration' in guidance
    assert 'never change Runtime projection' in guidance


def test_batch_guidance_requires_one_atomic_call_for_ready_frontier(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager

    guidance = workflow_manager._build_mode_guidance('dynamic')

    assert 'one atomic batch' in guidance
    assert 'profile permits parallel execution' in guidance
    assert 'Ready step' in guidance


def test_step_status_exposes_multi_ready_batch_hint(loaded_workflow):
    from lazymind.chat.workflow import workflow_manager

    with patch.object(workflow_manager, '_fetch_go_projection', return_value={
        'past': ['step_a'], 'ready': ['step_b', 'step_c'],
    }):
        section = workflow_manager._build_step_status_section(
            'test-workflow', 'session-batch', '', [],
            step_labels={'step_b': 'B', 'step_c': 'C'},
        )

    assert 'step_b (B), step_c (C)' in section
    assert 'parallel frontier' in section
    assert 'one plural advancement tool call' in section


# ---------------------------------------------------------------------------
# 必修D — _build_intent_section no longer injects step-level intent
# ---------------------------------------------------------------------------

def test_build_intent_section_no_step_intent(loaded_workflow):
    """Step-level intent must NOT appear in ChatAgent's prompt context."""
    from lazymind.chat.workflow import workflow_manager

    mock_db_instance = MagicMock()
    mock_db_instance.get_session_intent.return_value = 'Global constraint A'
    mock_db_instance.get_step_intent.return_value = 'Step constraint X'
    mock_db_class = MagicMock(return_value=mock_db_instance)

    with patch('lazymind.chat.engine.subagent.db.TaskQueryDB', mock_db_class):
        result = workflow_manager._build_intent_section('sess-1', step_id='step_a')

    # Global intent should be present.
    assert 'Global constraint A' in result
    # Step intent must NOT be injected by this function.
    assert 'Step constraint X' not in result


def test_build_intent_section_global_only(loaded_workflow):
    """When only session intent exists, it is still injected."""
    from lazymind.chat.workflow import workflow_manager

    mock_db_instance = MagicMock()
    mock_db_instance.get_session_intent.return_value = 'Only global rule'
    mock_db_class = MagicMock(return_value=mock_db_instance)

    with patch('lazymind.chat.engine.subagent.db.TaskQueryDB', mock_db_class):
        result = workflow_manager._build_intent_section('sess-2')

    assert 'Only global rule' in result
