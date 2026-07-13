"""Tests for plugin_manager — cold-start triggers and advance_step tool builder.

External dependencies (_write_agent_data, lazyllm.globals, httpx) are fully mocked
so these tests run without a real LLM or algorithm service.
"""
from __future__ import annotations

import asyncio
import json
from unittest.mock import MagicMock, patch

import pytest

# Re-use the fixture that builds a temporary plugin directory.
from tests.chat.plugins.test_loader import make_plugin_dir


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture()
def loaded_plugin(tmp_path):
    """Load the test-plugin into the registry and yield; restore afterwards."""
    from lazymind.chat.plugin import plugin_loader
    plugins_dir = make_plugin_dir(tmp_path)
    with patch.object(plugin_loader, '_PLUGINS_DIR', plugins_dir):
        plugin_loader.load_all()
    yield
    plugin_loader.load_all()   # restore original registry


@pytest.fixture()
def mock_write_agent_data():
    with patch('lazymind.chat.plugin.plugin_manager._write_agent_data') as m:
        yield m


@pytest.fixture()
def mock_agentic_config():
    """Provide an injectable agentic_config dict."""
    config: dict = {}
    with patch('lazymind.chat.plugin.plugin_manager._agentic_config', return_value=config):
        yield config


@pytest.fixture(autouse=True)
def mock_layer2_imports():
    """Stub out the two lazy imports inside _trigger_plugin_step so tests never
    touch the network or require a live lazymind.config.

    Both imports are inside the function body, so we intercept them via
    builtins.__import__ before they execute.
    """
    import builtins
    real_import = builtins.__import__

    fake_httpx = MagicMock()
    fake_httpx.get.side_effect = Exception('httpx stubbed')

    fake_config_obj = MagicMock()
    fake_config_obj.get = MagicMock(return_value='http://core:8000')
    fake_config_module = MagicMock()
    fake_config_module.config = fake_config_obj

    def patched_import(name, *args, **kwargs):
        if name == 'httpx':
            return fake_httpx
        if name == 'lazymind.config':
            return fake_config_module
        return real_import(name, *args, **kwargs)

    with patch('builtins.__import__', side_effect=patched_import):
        yield


@pytest.fixture()
def mock_dynamic_step_waits():
    """advance_step tests focus on trigger semantics unless they override waits."""
    with (
        patch('lazymind.chat.plugin.plugin_manager._wait_for_step_started', return_value='task-ack') as started,
        patch('lazymind.chat.plugin.plugin_manager._wait_for_step_done', return_value='step done') as done,
    ):
        yield started, done


# ---------------------------------------------------------------------------
# build_cold_start_tools
# ---------------------------------------------------------------------------

def test_build_cold_start_tools_creates_one_trigger_per_plugin(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager
    tools = plugin_manager.build_cold_start_tools()
    assert len(tools) >= 1
    names = [t.__name__ for t in tools]
    assert 'trigger_test_plugin' in names


def test_cold_start_trigger_prepares_launch_without_creating_task(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    tools = plugin_manager.build_cold_start_tools()
    trigger = next(t for t in tools if t.__name__ == 'trigger_test_plugin')

    preflight = {
        'decision': 'ready',
        'reason': 'matches',
        'missing_information': [],
        'normalized_request': 'Draw a sunset',
        'first_step_id': 'step_a',
        'hand_off': True,
    }
    with patch.object(plugin_manager, '_evaluate_plugin_preflight', return_value=preflight):
        result = json.loads(trigger(request_context='Draw a sunset', explicit_plugin_request=False))

    assert result['status'] == 'ready'
    assert result['outcome'] == 'ready'
    assert result['must_advance'] is True
    assert result['launch_plan']['first_step_id'] == 'step_a'
    assert 'hand_off' not in result['launch_plan']
    assert 'advance_tool' not in result['launch_plan']
    assert 'step_a(Step A)' in result['step_name_index']
    assert 'step_d(Step D)' in result['step_name_index']
    assert result['first_step_default_approval'] == 'required'
    assert mock_agentic_config['prepared_plugin']['advance_committed'] is False
    mock_write_agent_data.assert_called_once()
    assert mock_write_agent_data.call_args.args[0] == 'plugin_preflight_updated'


def test_cold_start_trigger_hides_hand_off_choice_when_tool_is_static(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config['plugin_mode'] = 'auto'
    trigger = next(
        tool for tool in plugin_manager.build_cold_start_tools()
        if tool.__name__ == 'trigger_test_plugin'
    )
    preflight = {
        'decision': 'ready',
        'reason': 'matches',
        'missing_information': [],
        'normalized_request': 'Draw a sunset',
        'first_step_id': 'step_a',
    }

    with patch.object(plugin_manager, '_evaluate_plugin_preflight', return_value=preflight):
        result = json.loads(trigger(
            request_context='Draw a sunset',
            explicit_plugin_request=False,
        ))

    assert result['launch_plan']['advance_tool'] == 'advance_step_and_hand_off'
    assert 'hand_off' not in result['launch_plan']
    internal_plan = mock_agentic_config['prepared_plugin']['launch_plan']
    assert internal_plan['hand_off'] is True
    assert internal_plan['advance_tool'] == 'advance_step_and_hand_off'


def test_cold_start_trigger_rejects_empty_input(loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    tools = plugin_manager.build_cold_start_tools()
    trigger = next(t for t in tools if t.__name__ == 'trigger_test_plugin')

    result = json.loads(trigger(request_context='   ', explicit_plugin_request=False))
    assert result['status'] == 'preflight_failed'
    assert not mock_write_agent_data.called


def test_cold_start_trigger_need_information_does_not_prepare_launch(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    trigger = next(
        t for t in plugin_manager.build_cold_start_tools()
        if t.__name__ == 'trigger_test_plugin'
    )
    preflight = {
        'decision': 'need_information',
        'reason': 'size is required',
        'missing_information': [{'key': 'size', 'question': 'Which size?'}],
        'normalized_request': 'Draw a sunset',
        'first_step_id': '',
        'hand_off': True,
    }
    with patch.object(plugin_manager, '_evaluate_plugin_preflight', return_value=preflight):
        result = json.loads(trigger(request_context='Draw a sunset', explicit_plugin_request=False))

    assert result['status'] == 'need_information'
    assert 'prepared_plugin' not in mock_agentic_config
    assert mock_agentic_config['plugin_preflight_context']['original_intent'] == 'Draw a sunset'
    assert mock_write_agent_data.call_args.args[0] == 'plugin_preflight_updated'


def test_explicit_plugin_request_cannot_be_rejected_as_not_applicable(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    trigger = next(
        t for t in plugin_manager.build_cold_start_tools()
        if t.__name__ == 'trigger_test_plugin'
    )
    preflight = {
        'decision': 'not_applicable',
        'reason': 'The task is simple enough to answer directly.',
        'missing_information': [],
        'normalized_request': 'Use the test plugin to draw a sunset',
        'first_step_id': '',
        'hand_off': True,
    }

    with patch.object(plugin_manager, '_evaluate_plugin_preflight', return_value=preflight):
        result = json.loads(trigger(
            request_context='Use the test plugin to draw a sunset',
            explicit_plugin_request=True,
        ))

    assert result['status'] == 'ready'
    assert result['launch_plan']['first_step_id'] == 'step_a'
    assert mock_agentic_config['prepared_plugin']['must_advance'] is True


def test_implicit_plugin_request_can_still_be_not_applicable(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    trigger = next(
        t for t in plugin_manager.build_cold_start_tools()
        if t.__name__ == 'trigger_test_plugin'
    )
    preflight = {
        'decision': 'not_applicable',
        'reason': 'The request does not need this plugin.',
        'missing_information': [],
        'normalized_request': 'Say hello',
        'first_step_id': '',
        'hand_off': True,
    }

    with patch.object(plugin_manager, '_evaluate_plugin_preflight', return_value=preflight):
        result = json.loads(trigger(request_context='Say hello', explicit_plugin_request=False))

    assert result['status'] == 'not_applicable'
    assert result['outcome'] == 'not_applicable'
    assert 'prepared_plugin' not in mock_agentic_config


def test_explicit_plugin_choice_persists_across_clarification_turns(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    trigger = next(
        t for t in plugin_manager.build_cold_start_tools()
        if t.__name__ == 'trigger_test_plugin'
    )
    need_info = {
        'decision': 'need_information',
        'reason': 'A required value is missing.',
        'missing_information': [{'key': 'value', 'question': 'Which value?'}],
        'normalized_request': 'Use the test plugin',
        'first_step_id': '',
        'hand_off': True,
    }
    contradictory_follow_up = {
        'decision': 'not_applicable',
        'reason': 'This answer alone does not mention the plugin.',
        'missing_information': [],
        'normalized_request': 'Use the test plugin with value 42',
        'first_step_id': '',
        'hand_off': True,
    }

    with patch.object(
        plugin_manager,
        '_evaluate_plugin_preflight',
        side_effect=[need_info, contradictory_follow_up],
    ):
        first = json.loads(trigger(
            request_context='Use the test plugin',
            explicit_plugin_request=True,
        ))
        second = json.loads(trigger(
            request_context='Use value 42',
            explicit_plugin_request=False,
        ))

    assert first['status'] == 'need_information'
    assert second['status'] == 'ready'
    assert mock_agentic_config['prepared_plugin']['explicit_plugin_request'] is True


def test_retrigger_preserves_original_intent_and_accumulates_confirmations(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    trigger = next(
        t for t in plugin_manager.build_cold_start_tools()
        if t.__name__ == 'trigger_test_plugin'
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
        plugin_manager, '_evaluate_plugin_preflight', side_effect=[need_info, ready]
    ):
        trigger(request_context='Draw a sunset', explicit_plugin_request=False)
        result = json.loads(trigger(
            request_context='Use watercolor style',
            explicit_plugin_request=False,
        ))

    prepared = mock_agentic_config['prepared_plugin']
    assert result['status'] == 'ready'
    assert prepared['original_intent'] == 'Draw a sunset'
    assert prepared['confirmation_answers'] == ['Use watercolor style']
    assert prepared['launch_plan']['normalized_request'] == 'Draw a watercolor sunset'


def test_cold_advance_commits_exact_prepared_plan(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config['prepared_plugin'] = {
        'plugin_id': 'test-plugin',
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
        t for t in plugin_manager.build_cold_advance_tools()
        if t.__name__ == 'advance_step_and_hand_off'
    )

    result = handoff(step_id='step_a')

    assert 'acceptance is pending' in result.lower()
    params = mock_write_agent_data.call_args.kwargs['params']
    assert params['is_cold_start'] is True
    assert params['hand_off'] is True
    assert params['preflight_id'] == 'pf-1'
    assert params['user_input'] == 'Draw a sunset after all confirmations'
    assert mock_agentic_config['prepared_plugin']['advance_committed'] is True


def test_cold_advance_allows_chat_agent_choice_when_launch_has_no_hand_off(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config['prepared_plugin'] = {
        'plugin_id': 'test-plugin',
        'preflight_id': 'pf-choice',
        'must_advance': True,
        'advance_committed': False,
        'fallback_hand_off': True,
        'launch_plan': {
            'first_step_id': 'step_a',
            'normalized_request': 'Continue to Step D, then ask for confirmation',
        },
    }

    result = plugin_manager._commit_prepared_plugin(
        'step_a', hand_off=False, wait_for_result=False
    )

    assert 'acceptance is pending' in result
    params = mock_write_agent_data.call_args.kwargs['params']
    assert params['hand_off'] is False
    assert mock_agentic_config['prepared_plugin']['advance_committed'] is True


def test_cold_advance_rejects_tool_that_disagrees_with_launch_plan(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config['prepared_plugin'] = {
        'plugin_id': 'test-plugin',
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
        t for t in plugin_manager.build_cold_advance_tools()
        if t.__name__ == 'advance_step_and_hand_off'
    )

    with pytest.raises(ValueError, match='requires advance_step'):
        handoff(step_id='step_a')
    assert not mock_write_agent_data.called


def test_deterministic_fallback_executes_only_the_validated_plan(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config['prepared_plugin'] = {
        'plugin_id': 'test-plugin',
        'preflight_id': 'pf-fallback',
        'must_advance': True,
        'advance_committed': False,
        'launch_plan': {
            'first_step_id': 'step_a',
            'normalized_request': 'Run continuously without interruption',
            'hand_off': False,
        },
    }

    result = plugin_manager.commit_prepared_plugin_fallback()

    assert 'acceptance is pending' in result
    params = mock_write_agent_data.call_args.kwargs['params']
    assert params['step_id'] == 'step_a'
    assert params['hand_off'] is False
    assert params['preflight_id'] == 'pf-fallback'
    assert mock_agentic_config['prepared_plugin']['advance_committed'] is True


def test_preflight_model_uses_llm_role_json_mode_and_timeout():
    from lazymind.chat.plugin import plugin_manager
    llm = MagicMock(return_value=json.dumps({
        'decision': 'ready',
        'reason': 'matches',
        'missing_information': [],
        'normalized_request': 'Draw a sunset',
        'first_step_id': 'step_a',
        'hand_off': True,
    }))
    with (
        patch.object(plugin_manager, 'is_model_role_available', return_value=True),
        patch.object(plugin_manager.lazyllm, 'AutoModel', return_value=llm) as auto_model,
    ):
        result = plugin_manager._evaluate_plugin_preflight(
            plugin_id='test-plugin',
            plugin_name='Test Plugin',
            description='Test',
            when_to_use='Use for tests',
            scenario='Scenario',
            request_context='Draw a sunset',
            previous=None,
            first_steps=['step_a'],
            plugin_mode='dynamic',
        )

    assert result['decision'] == 'ready'
    auto_model.assert_called_once_with(model='llm')
    assert llm.call_args.kwargs['response_format'] == {'type': 'json_object'}
    assert llm.call_args.kwargs['stream_output'] is False
    assert llm.call_args.kwargs['timeout'] == plugin_manager._PREFLIGHT_TIMEOUT_SECONDS
    assert 'hand_off' not in llm.call_args.args[0]
    assert 'Default approval' not in llm.call_args.args[0]


def test_preflight_without_approval_choice_hides_mode_and_hand_off_policy():
    from lazymind.chat.plugin import plugin_manager
    llm = MagicMock(return_value=json.dumps({
        'decision': 'ready',
        'reason': 'matches',
        'missing_information': [],
        'normalized_request': 'Draw a sunset',
        'first_step_id': 'step_a',
    }))
    with (
        patch.object(plugin_manager, 'is_model_role_available', return_value=True),
        patch.object(plugin_manager.lazyllm, 'AutoModel', return_value=llm),
    ):
        result = plugin_manager._evaluate_plugin_preflight(
            plugin_id='test-plugin',
            plugin_name='Test Plugin',
            description='Test',
            when_to_use='Use for tests',
            scenario='Scenario',
            request_context='Draw a sunset',
            previous=None,
            first_steps=['step_a'],
            plugin_mode='auto',
        )

    prompt = llm.call_args.args[0]
    assert result['hand_off'] is True
    assert 'hand_off' not in prompt
    assert 'Default approval' not in prompt
    assert 'Plugin mode' not in prompt
    assert 'dynamic mode' not in prompt.lower()
    assert 'auto mode' not in prompt.lower()


def test_preflight_json_repair_is_also_hidden_from_user_stream():
    from lazymind.chat.plugin import plugin_manager
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
        patch.object(plugin_manager, 'is_model_role_available', return_value=True),
        patch.object(plugin_manager.lazyllm, 'AutoModel', return_value=llm),
    ):
        result = plugin_manager._evaluate_plugin_preflight(
            plugin_id='test-plugin',
            plugin_name='Test Plugin',
            description='Test',
            when_to_use='Use for tests',
            scenario='Scenario',
            request_context='Draw a sunset',
            previous=None,
            first_steps=['step_a'],
            plugin_mode='dynamic',
        )

    assert result['decision'] == 'ready'
    assert llm.call_count == 2
    assert all(call.kwargs['stream_output'] is False for call in llm.call_args_list)


def test_cold_injection_without_approval_choice_registers_only_hand_off_tool(
        loaded_plugin, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config['enable_plugin'] = True

    tools, _, stop_tools, patch_config, context = plugin_manager.resolve_plugin_injection({
        'plugin_mode': 'auto',
        'plugin_preflight': {
            'preflight_id': 'pf-old',
            'plugin_id': 'test-plugin',
            'status': 'collecting',
            'original_intent': 'Original request ten turns ago',
            'normalized_request': 'Original request plus answers',
        },
    })

    names = {tool.__name__ for tool in tools}
    assert 'trigger_test_plugin' in names
    assert 'advance_step' not in names
    assert 'advance_step_and_hand_off' in names
    assert stop_tools == ['advance_step_and_hand_off']
    assert 'trigger_test_plugin' not in stop_tools
    assert patch_config['plugin_mode'] == 'auto'
    assert patch_config['plugin_preflight_context']['preflight_id'] == 'pf-old'
    assert 'Original request ten turns ago' in context
    assert 'Current Plugin Launch Policy' in context
    assert 'approval or continuation decision' in context


def test_compact_step_name_index_has_names_but_no_graph_details(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager

    index = plugin_manager._build_step_name_index('test-plugin')

    assert 'step_a(Step A)' in index
    assert 'step_b(Step B)' in index
    assert 'step_c(Step C)' in index
    assert 'step_d(Step D)' in index
    assert 'default approval' not in index.lower()
    assert 'condition' not in index.lower()
    assert 'route:' not in index.lower()


def test_active_injection_switches_tools_and_request_local_policy_per_turn(
        loaded_plugin, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config['enable_plugin'] = True
    plugin_context = {
        'session_id': 'session-1',
        'plugin_id': 'test-plugin',
        'current_step': 'step_a',
    }

    with (
        patch.object(plugin_manager, '_fetch_succeeded_steps', return_value=set()),
        patch.object(plugin_manager, '_build_session_artifact_section', return_value='artifacts'),
        patch.object(plugin_manager, '_build_intent_section', return_value=''),
        patch.object(plugin_manager, '_build_step_status_section', return_value='step status'),
    ):
        auto_result = plugin_manager.resolve_plugin_injection({
            **plugin_context,
            'plugin_mode': 'auto',
        })
        dynamic_result = plugin_manager.resolve_plugin_injection({
            **plugin_context,
            'plugin_mode': 'dynamic',
        })

    auto_tools, auto_system_prompt, _, _, auto_context = auto_result
    dynamic_tools, dynamic_system_prompt, _, _, dynamic_context = dynamic_result
    auto_names = {tool.__name__ for tool in auto_tools}
    dynamic_names = {tool.__name__ for tool in dynamic_tools}

    assert 'advance_step_and_hand_off' in auto_names
    assert 'advance_step' not in auto_names
    assert {'advance_step', 'advance_step_and_hand_off'} <= dynamic_names
    assert 'Current Plugin Execution Policy' not in auto_system_prompt
    assert 'Current Plugin Execution Policy' not in dynamic_system_prompt
    assert 'Current Plugin Execution Policy' in auto_context
    assert 'Current Plugin Execution Policy' in dynamic_context
    assert 'Plugin Step Name Index' in auto_context
    assert 'step_a(Step A)' in auto_context
    assert 'step_d(Step D)' in dynamic_context
    assert 'default approval' not in auto_context.lower()
    assert '[default approval: ...]' in dynamic_context
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


def test_plugin_stream_guard_is_noop_without_ready_preflight(mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager

    async def initial_stream():
        yield 'event', {'tag': 'text', 'delta': 'ordinary answer'}
        yield 'final', 'ordinary answer'

    async def collect():
        return [item async for item in plugin_manager.guard_plugin_agent_stream(
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


def test_plugin_stream_guard_suppresses_prose_while_advance_is_pending(mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config['prepared_plugin'] = {
        'must_advance': True,
        'advance_committed': False,
    }

    assert plugin_manager._should_suppress_prepared_plugin_text({
        'tag': 'text', 'delta': 'I will explain instead',
    }) is True
    assert plugin_manager._should_suppress_prepared_plugin_text({
        'tag': 'tool_calls', 'tool_calls': [],
    }) is False


# ---------------------------------------------------------------------------
# build_advance_step_tool
# ---------------------------------------------------------------------------

def test_advance_step_tool_rejects_unreachable_step(loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-123',
        'plugin_step': 'step_a',
    })
    advance = plugin_manager.build_advance_step_tool('test-plugin', 'step_a')

    # step_c is not reachable directly from step_a.
    result = advance(step_id='step_c', user_input='redo')
    assert 'error' in result.lower()
    assert not mock_write_agent_data.called


def test_advance_step_tool_triggers_reachable_step(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_dynamic_step_waits):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'session_id': 'chat-sid-123',
        'plugin_session_id': 'ps-456',
        'plugin_step': 'step_a',
    })
    advance = plugin_manager.build_advance_step_tool('test-plugin', 'step_a')

    # step_b is reachable from step_a.
    _ = advance(step_id='step_b', user_input='proceed')
    assert mock_write_agent_data.called
    call_kwargs = mock_write_agent_data.call_args.kwargs
    assert call_kwargs['params']['step_id'] == 'step_b'
    assert call_kwargs['params']['is_cold_start'] is False
    assert call_kwargs['params']['chat_session_id'] == 'chat-sid-123'


def test_advance_step_tool_waits_for_started_before_done(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_dynamic_step_waits):
    from lazymind.chat.plugin import plugin_manager
    started, done = mock_dynamic_step_waits
    calls = []
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-ack',
        'plugin_step': 'step_a',
    })
    started.side_effect = lambda step_id: calls.append(('started', step_id)) or 'task-ack'

    def set_local_step(step_id):
        calls.append(('set_local', step_id))
        mock_agentic_config['plugin_step'] = step_id

    done.side_effect = lambda step_id, result: calls.append(
        ('done', step_id, mock_agentic_config['plugin_step'])
    ) or 'step done'
    advance = plugin_manager.build_advance_step_tool('test-plugin', 'step_a')

    with (
        patch('lazymind.chat.plugin.plugin_manager._clear_step_signal_queues'),
        patch('lazymind.chat.plugin.plugin_manager._trigger_plugin_step', return_value='triggered'),
        patch('lazymind.chat.plugin.plugin_manager._set_local_plugin_step', side_effect=set_local_step),
    ):
        result = advance(step_id='step_b', user_input='proceed')

    assert result.startswith('step done')
    assert 'Current step: step_b' in result
    assert 'step_c' in result
    assert calls == [('started', 'step_b'), ('set_local', 'step_b'), ('done', 'step_b', 'step_b')]


def test_advance_step_tool_raises_when_started_ack_missing(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_dynamic_step_waits):
    from lazymind.chat.plugin import plugin_manager
    started, done = mock_dynamic_step_waits
    started.side_effect = TimeoutError('missing start ack')
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-missing-ack',
        'plugin_step': 'step_a',
    })
    advance = plugin_manager.build_advance_step_tool('test-plugin', 'step_a')

    with pytest.raises(TimeoutError, match='missing start ack'):
        with (
            patch('lazymind.chat.plugin.plugin_manager._clear_step_signal_queues'),
            patch('lazymind.chat.plugin.plugin_manager._trigger_plugin_step', return_value='triggered'),
        ):
            advance(step_id='step_b', user_input='proceed')
    assert not done.called


def test_advance_step_tool_retrigger_same_step(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_dynamic_step_waits):
    """step_d can re-trigger step_d itself (full retry or partial retry via list slot)."""
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-789',
        'plugin_step': 'step_d',
    })
    advance = plugin_manager.build_advance_step_tool('test-plugin', 'step_d')

    _ = advance(step_id='step_d', user_input='enhance again')
    assert mock_write_agent_data.called
    call_kwargs = mock_write_agent_data.call_args.kwargs
    assert call_kwargs['params']['step_id'] == 'step_d'


def test_advance_step_and_hand_off_uses_live_current_step(
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
    """Final hand-off after synchronous steps must validate against live state."""
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'session_id': 'chat-sid-live',
        'plugin_session_id': 'ps-live',
        'plugin_step': 'step_a',
    })
    handoff = plugin_manager.build_advance_step_and_hand_off_tool('test-plugin', 'step_a')

    # Simulate advance_step(step_b) having already updated local state in the
    # same ChatAgent turn. A stale hand-off tool built at step_a would reject
    # step_c; the live state should allow it.
    mock_agentic_config['plugin_step'] = 'step_b'
    result = handoff(step_id='step_c', user_input='finish boundary')

    assert 'error' not in result.lower()
    assert mock_write_agent_data.called
    call_kwargs = mock_write_agent_data.call_args.kwargs
    assert call_kwargs['params']['step_id'] == 'step_c'
    assert call_kwargs['params']['chat_session_id'] == 'chat-sid-live'


# ---------------------------------------------------------------------------
# _render_step_objective
# ---------------------------------------------------------------------------

def test_render_step_objective_replaces_user_input():
    from lazymind.chat.plugin.plugin_manager import _render_step_objective
    cfg = {'prompt': 'Analyze {{user_input}} carefully.'}
    rendered = _render_step_objective(cfg, 'a sunset over the ocean')
    assert 'a sunset over the ocean' in rendered
    assert '{{user_input}}' not in rendered


def test_render_step_objective_leaves_other_placeholders():
    from lazymind.chat.plugin.plugin_manager import _render_step_objective
    cfg = {'prompt': 'Enhance {{image_url}} based on {{user_input}}.'}
    rendered = _render_step_objective(cfg, 'high contrast')
    assert '{{image_url}}' in rendered       # Python runner injects this via _enrich_objective_with_artifacts
    assert '{{user_input}}' not in rendered
    assert 'high contrast' in rendered


def test_render_step_objective_empty_prompt():
    from lazymind.chat.plugin.plugin_manager import _render_step_objective
    rendered = _render_step_objective({}, 'anything')
    assert rendered == ''


def test_state_machine_filters_transition_targets_without_step_config():
    from lazymind.chat.plugin.plugin_loader import StateMachine
    sm = StateMachine(
        initial='step_a',
        transitions={'step_a': [{'to': 'missing_step'}]},
        steps={'step_a': {'prompt': 'ok'}},
    )

    assert sm.get_reachable_steps('step_a') == []
    assert sm.is_reachable('step_a', 'missing_step') is False


def test_trigger_plugin_step_rejects_missing_step_config(mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager

    class _FakeStateMachine:
        def is_reachable(self, current_step, target_step):
            return True

    mock_agentic_config.update({'plugin_step': 'step_a', 'plugin_session_id': 'ps-missing'})
    with (
        patch.object(plugin_manager.plugin_loader, 'get_state_machine', return_value=_FakeStateMachine()),
        patch.object(plugin_manager.plugin_loader, 'get_step_config', return_value={}),
    ):
        with pytest.raises(ValueError, match='not defined'):
            plugin_manager._trigger_plugin_step(
                'test-plugin',
                'missing_step',
                'go',
                is_cold_start=False,
            )


# ---------------------------------------------------------------------------
# _trigger_plugin_step — layer 1 format validation (no DB / HTTP needed)
# ---------------------------------------------------------------------------

def test_trigger_plugin_step_unknown_plugin(mock_agentic_config, mock_write_agent_data):
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step
    result = _trigger_plugin_step('nonexistent-plugin', 'step_a', 'hello', is_cold_start=True)
    assert 'error' in result.lower()
    assert not mock_write_agent_data.called


def test_trigger_plugin_step_unreachable_step(loaded_plugin, mock_agentic_config, mock_write_agent_data):
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step
    mock_agentic_config['plugin_step'] = 'step_a'

    # step_c is not directly reachable from step_a.
    result = _trigger_plugin_step('test-plugin', 'step_c', 'hi', is_cold_start=False)
    assert 'error' in result.lower()
    assert 'reachable' in result.lower()
    assert not mock_write_agent_data.called


def test_trigger_plugin_step_output_keys_emitted(loaded_plugin, mock_agentic_config, mock_write_agent_data):
    """Verify output_slots is set correctly from state.yml step outputs."""
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step
    mock_agentic_config['plugin_step'] = '__start__'

    _trigger_plugin_step('test-plugin', 'step_a', 'hello', is_cold_start=True)

    assert mock_write_agent_data.called
    kwargs = mock_write_agent_data.call_args.kwargs
    assert 'analysis' in kwargs['output_slots']


# ---------------------------------------------------------------------------
# Framework tools injection
# ---------------------------------------------------------------------------

def test_framework_tools_always_present_even_when_step_declares_none(
        loaded_plugin, mock_agentic_config, mock_write_agent_data):
    """step_a declares no tools in state.yml; framework tools must still be injected."""
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step, _FRAMEWORK_TOOLS
    mock_agentic_config['plugin_step'] = '__start__'

    _trigger_plugin_step('test-plugin', 'step_a', 'hello', is_cold_start=True)

    assert mock_write_agent_data.called
    tools = mock_write_agent_data.call_args.kwargs['tools']
    for fw_tool in _FRAMEWORK_TOOLS:
        assert fw_tool in tools, f'framework tool {fw_tool!r} missing from tools list'


def test_framework_tools_prepended_before_plugin_tools(
        loaded_plugin, mock_agentic_config, mock_write_agent_data):
    """Framework tools are first in the merged list; plugin-declared tools come after."""
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step, _FRAMEWORK_TOOLS
    mock_agentic_config['plugin_step'] = 'step_c'

    _trigger_plugin_step('test-plugin', 'step_d', 'enhance it', is_cold_start=False)

    tools = mock_write_agent_data.call_args.kwargs['tools']
    for i, fw_tool in enumerate(_FRAMEWORK_TOOLS):
        assert tools[i] == fw_tool, (
            f'expected framework tool at position {i}: {fw_tool!r}, got {tools[i]!r}'
        )
    # Plugin-declared tool must also be present.
    assert 'enhance_tool' in tools


def test_framework_tools_no_duplicates(
        loaded_plugin, mock_agentic_config, mock_write_agent_data):
    """If a plugin explicitly declares a framework tool, there should be no duplicate."""
    from lazymind.chat.plugin.plugin_manager import _merge_tools
    merged = _merge_tools(['save_artifact', 'my_custom_tool', 'load_artifact'])
    assert merged.count('save_artifact') == 1
    assert merged.count('load_artifact') == 1
    assert 'my_custom_tool' in merged


# ---------------------------------------------------------------------------
# runtime_instruction
# ---------------------------------------------------------------------------

def test_render_step_objective_replaces_runtime_instruction():
    from lazymind.chat.plugin.plugin_manager import _render_step_objective
    cfg = {'prompt': 'Do {{user_input}}. {{runtime_instruction}}'}
    rendered = _render_step_objective(cfg, 'draw a cat', 'Only draw the left eye.')
    assert 'draw a cat' in rendered
    assert 'Only draw the left eye.' in rendered
    assert '{{runtime_instruction}}' not in rendered
    assert '{{user_input}}' not in rendered


def test_render_step_objective_empty_runtime_instruction_removed():
    from lazymind.chat.plugin.plugin_manager import _render_step_objective
    cfg = {'prompt': 'Do {{user_input}}. {{runtime_instruction}} Done.'}
    rendered = _render_step_objective(cfg, 'draw a cat')
    assert '{{runtime_instruction}}' not in rendered
    # Placeholder replaced with empty string, surrounding text intact.
    assert 'Done.' in rendered


def test_advance_step_passes_runtime_instruction(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_dynamic_step_waits):
    """runtime_instruction is forwarded into the step objective."""
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-partial',
        'plugin_step': 'step_d',
    })
    advance = plugin_manager.build_advance_step_tool('test-plugin', 'step_d')

    advance(
        step_id='step_d',
        user_input='redo enhancement',
        runtime_instruction='Re-enhance only image at index 1; keep others.',
    )

    assert mock_write_agent_data.called
    objective = mock_write_agent_data.call_args.kwargs['objective']
    assert 'Re-enhance only image at index 1' in objective


def test_advance_step_no_runtime_instruction_leaves_no_placeholder(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_dynamic_step_waits):
    """When runtime_instruction is omitted, {{runtime_instruction}} must not appear in objective."""
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-normal',
        'plugin_step': 'step_d',
    })
    advance = plugin_manager.build_advance_step_tool('test-plugin', 'step_d')
    advance(step_id='step_d', user_input='enhance all images')

    objective = mock_write_agent_data.call_args.kwargs['objective']
    assert '{{runtime_instruction}}' not in objective


# ---------------------------------------------------------------------------
# _enrich_objective_with_artifacts (runner-side artifact injection)
# ---------------------------------------------------------------------------

def test_enrich_objective_no_placeholders():
    """Objective without {{ }} is returned as-is without hitting the DB."""
    from lazymind.chat.engine.subagent.runner import _enrich_objective_with_artifacts
    from unittest.mock import MagicMock

    db = MagicMock()
    result = _enrich_objective_with_artifacts('Analyze the image.', {'session_id': 'ps-1'}, db)
    assert result == 'Analyze the image.'
    db.load_plugin_session_steps.assert_not_called()


def test_enrich_objective_no_session_id():
    """Missing session_id falls back to original objective."""
    from lazymind.chat.engine.subagent.runner import _enrich_objective_with_artifacts
    from unittest.mock import MagicMock

    db = MagicMock()
    result = _enrich_objective_with_artifacts('Do {{something}}.', {}, db)
    assert result == 'Do {{something}}.'
    db.load_plugin_session_steps.assert_not_called()


def test_enrich_objective_replaces_placeholders():
    """Artifacts from succeeded steps are substituted into the objective."""
    from lazymind.chat.engine.subagent.runner import _enrich_objective_with_artifacts
    from unittest.mock import MagicMock

    db = MagicMock()
    db.load_plugin_session_steps.return_value = [
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
    db.load_plugin_session_steps.return_value = [
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
    db.load_plugin_session_steps.side_effect = Exception('DB unavailable')
    objective = 'Enhance: {{image_url}}.'
    result = _enrich_objective_with_artifacts(objective, {'session_id': 'ps-err'}, db)
    assert result == objective


# ---------------------------------------------------------------------------
# _resolve_plugin_step_tools (runner-side tools resolution)
# ---------------------------------------------------------------------------

def test_resolve_plugin_step_tools_returns_merged_list(loaded_plugin):
    """Tools for a known step_id are resolved from plugin_loader."""
    from lazymind.chat.engine.subagent.runner import _resolve_plugin_step_tools

    # step_d declares enhance_tool in state.yml; framework tools must be prepended.
    tools = _resolve_plugin_step_tools({'plugin_id': 'test-plugin', 'step_id': 'step_d'})
    assert tools is not None
    assert 'save_artifact' in tools
    assert 'enhance_tool' in tools
    # Framework tools come first.
    assert tools.index('save_artifact') < tools.index('enhance_tool')


def test_resolve_plugin_step_tools_no_declared_tools_returns_only_framework(loaded_plugin):
    """step_a declares no tools; only framework tools are returned."""
    from lazymind.chat.engine.subagent.runner import _resolve_plugin_step_tools

    tools = _resolve_plugin_step_tools({'plugin_id': 'test-plugin', 'step_id': 'step_a'})
    assert tools is not None
    assert 'save_artifact' in tools
    assert 'get_artifact' in tools


def test_resolve_plugin_step_tools_unknown_plugin_returns_none(loaded_plugin):
    """Unknown plugin_id returns None so caller can fall back."""
    from lazymind.chat.engine.subagent.runner import _resolve_plugin_step_tools

    result = _resolve_plugin_step_tools({'plugin_id': 'nonexistent-plugin', 'step_id': 'step_a'})
    assert result is None


def test_resolve_plugin_step_tools_missing_params_returns_none(loaded_plugin):
    """Empty params returns None."""
    from lazymind.chat.engine.subagent.runner import _resolve_plugin_step_tools

    assert _resolve_plugin_step_tools({}) is None


# ---------------------------------------------------------------------------
# Four reachability scenarios (ancestor rewind + dependency guard)
# ---------------------------------------------------------------------------

def _make_session_steps_payload(*steps):
    """Build the dict that _fetch_succeeded_steps / _trigger_plugin_step expect from the API."""
    return {'session': {'steps': [{'step_id': s, 'status': 'succeeded'} for s in steps]}}


@pytest.fixture()
def mock_fetch_succeeded():
    """Patch _fetch_succeeded_steps to return a controlled set."""
    with patch('lazymind.chat.plugin.plugin_manager._fetch_succeeded_steps') as m:
        yield m


# Scenario 1: current=step_b, target=step_a  → allowed (step_a is ancestor + succeeded)
def test_scenario1_rewind_to_ancestor_allowed(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_fetch_succeeded):
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-s1',
        'plugin_step': 'step_b',
    })
    # step_a has succeeded previously in this session.
    mock_fetch_succeeded.return_value = {'step_a'}

    result = _trigger_plugin_step('test-plugin', 'step_a', 're-run analysis', is_cold_start=False)
    assert 'error' not in result.lower(), f'Expected success but got: {result}'
    assert mock_write_agent_data.called
    assert mock_write_agent_data.call_args.kwargs['params']['step_id'] == 'step_a'


# Scenario 1b: same but step_a never succeeded → rejected
def test_scenario1_rewind_to_ancestor_rejected_if_not_succeeded(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_fetch_succeeded):
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-s1b',
        'plugin_step': 'step_b',
    })
    mock_fetch_succeeded.return_value = set()  # step_a never ran

    result = _trigger_plugin_step('test-plugin', 'step_a', 're-run analysis', is_cold_start=False)
    assert 'error' in result.lower()
    assert not mock_write_agent_data.called


# Scenario 2: after rewinding to step_b, step_d is not reachable (not neighbour, not ancestor of step_b)
def test_scenario2_forward_only_from_rewound_step(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_fetch_succeeded):
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-s2',
        'plugin_step': 'step_b',
    })
    # Even though step_d succeeded before, it is not a topological ancestor of step_b.
    mock_fetch_succeeded.return_value = {'step_a', 'step_d'}

    result = _trigger_plugin_step('test-plugin', 'step_d', 'skip to enhance', is_cold_start=False)
    assert 'error' in result.lower()
    assert not mock_write_agent_data.called


# Scenario 3: current=step_c (re-run), target=step_d  → allowed (direct forward neighbour)
def test_scenario3_forward_after_rerun_allowed(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_fetch_succeeded):
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-s3',
        'plugin_step': 'step_c',
    })
    mock_fetch_succeeded.return_value = {'step_a', 'step_b', 'step_c', 'step_d'}

    result = _trigger_plugin_step('test-plugin', 'step_d', 'proceed to enhance', is_cold_start=False)
    assert 'error' not in result.lower(), f'Expected success but got: {result}'
    assert mock_write_agent_data.called


# Scenario 4: dependency check catches missing required input (handled by Layer 2 in real env)
# Here we verify that a non-ancestor, non-neighbour step is rejected by Layer 1.
def test_scenario4_non_ancestor_non_neighbour_rejected(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_fetch_succeeded):
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-s4',
        'plugin_step': 'step_b',
    })
    # step_d is neither a direct neighbour of step_b nor an ancestor.
    mock_fetch_succeeded.return_value = {'step_a', 'step_b', 'step_c', 'step_d'}

    result = _trigger_plugin_step('test-plugin', 'step_d', 'jump ahead', is_cold_start=False)
    assert 'error' in result.lower()
    assert not mock_write_agent_data.called


# ---------------------------------------------------------------------------
# Dynamic docstring candidate list
# ---------------------------------------------------------------------------

def test_build_advance_step_tool_docstring_contains_forward_steps(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager
    advance = plugin_manager.build_advance_step_tool(
        'test-plugin', 'step_a',
        rewind_steps=[],
        step_labels={'step_b': 'Optimize'},
    )
    doc = advance.__doc__ or ''
    assert 'step_b' in doc
    assert 'Forward' in doc
    assert 'Optimize' in doc
    assert 'default approval: required' in doc


def test_hand_off_tool_doc_is_mode_neutral(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager

    hand_off = plugin_manager.build_advance_step_and_hand_off_tool(
        'test-plugin', 'step_a', rewind_steps=[]
    )
    doc = hand_off.__doc__ or ''

    assert 'Start the next plugin step asynchronously' in doc
    assert 'dynamic' not in doc
    assert 'auto' not in doc


def test_step_choice_doc_uses_configured_default_approval(loaded_plugin):
    from lazymind.chat.plugin import plugin_loader, plugin_manager

    spec = plugin_loader.get_plugin('test-plugin')
    assert spec is not None
    spec._steps['step_b']['mode'] = 'auto'
    advance = plugin_manager.build_advance_step_tool(
        'test-plugin', 'step_a',
        rewind_steps=[],
        step_labels={'step_b': 'Optimize'},
    )

    assert 'step_b' in (advance.__doc__ or '')
    assert 'default approval: not required' in (advance.__doc__ or '')


def test_build_advance_step_tool_docstring_contains_rewind_steps(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager
    advance = plugin_manager.build_advance_step_tool(
        'test-plugin', 'step_b',
        rewind_steps=['step_a'],
        step_labels={'step_a': 'Analyze Subject', 'step_c': 'Generate Image'},
    )
    doc = advance.__doc__ or ''
    assert 'step_a' in doc
    assert 'Rewind' in doc
    assert 'Analyze Subject' in doc
    assert 'previously completed' in doc


def test_build_advance_step_tool_docstring_no_rewind_when_empty(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager
    advance = plugin_manager.build_advance_step_tool(
        'test-plugin', 'step_a',
        rewind_steps=[],
    )
    doc = advance.__doc__ or ''
    assert 'Rewind' not in doc


def test_dynamic_guidance_respects_explicit_target_boundary(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager

    guidance = plugin_manager._build_mode_guidance(
        'dynamic',
        terminal_steps=['step_d'],
        step_labels={'step_d': 'Finalize'},
    )

    assert 'target boundary' in guidance
    assert 'Match X against the full compact' in guidance
    assert 'Plugin Step Name Index' in guidance
    assert 'name index does not imply reachability or execution order' in guidance
    assert 'higher priority than generic uninterrupted phrases' in guidance
    assert 'Do NOT hand off an' in guidance
    assert 'confirmation at the later' in guidance
    assert 'Execute the target boundary step with `advance_step_and_hand_off`' in guidance
    assert 'Do NOT wait for the boundary step with `advance_step`' in guidance
    assert 'Do NOT call downstream steps and do NOT call `__end__`' in guidance
    assert 'persisted session intent wins' in guidance
    assert "target step's" in guidance
    assert '[default approval: ...]' in guidance
    assert 'returns the next decision to the user' in guidance


def test_guidance_without_approval_choice_assigns_continuation_to_backend(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager

    guidance = plugin_manager._build_mode_guidance('auto')

    assert 'backend controller evaluates the result' in guidance
    assert 'Only `advance_step_and_hand_off` is available' in guidance
    assert 'default approval' not in guidance.lower()
    assert 'auto mode' not in guidance.lower()
    assert 'dynamic mode' not in guidance.lower()


def test_build_advance_step_tool_rewind_step_is_accepted(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_fetch_succeeded,
        mock_dynamic_step_waits):
    """advance_step should accept a step_id listed in rewind_steps."""
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
        'plugin_session_id': 'ps-rewind',
        'plugin_step': 'step_b',
    })
    mock_fetch_succeeded.return_value = {'step_a'}

    advance = plugin_manager.build_advance_step_tool(
        'test-plugin', 'step_b',
        rewind_steps=['step_a'],
    )
    result = advance(step_id='step_a', user_input='redo analysis')
    assert 'error' not in result.lower(), f'Expected rewind to be accepted but got: {result}'
    assert mock_write_agent_data.called


# ---------------------------------------------------------------------------
# 必修D — _build_intent_section no longer injects step-level intent
# ---------------------------------------------------------------------------

def test_build_intent_section_no_step_intent(loaded_plugin):
    """Step-level intent must NOT appear in ChatAgent's prompt context."""
    from lazymind.chat.plugin import plugin_manager

    mock_db_instance = MagicMock()
    mock_db_instance.get_session_intent.return_value = 'Global constraint A'
    mock_db_instance.get_step_intent.return_value = 'Step constraint X'
    mock_db_class = MagicMock(return_value=mock_db_instance)

    with patch('lazymind.chat.engine.subagent.db.TaskQueryDB', mock_db_class):
        result = plugin_manager._build_intent_section('sess-1', step_id='step_a')

    # Global intent should be present.
    assert 'Global constraint A' in result
    # Step intent must NOT be injected by this function.
    assert 'Step constraint X' not in result


def test_build_intent_section_global_only(loaded_plugin):
    """When only session intent exists, it is still injected."""
    from lazymind.chat.plugin import plugin_manager

    mock_db_instance = MagicMock()
    mock_db_instance.get_session_intent.return_value = 'Only global rule'
    mock_db_class = MagicMock(return_value=mock_db_instance)

    with patch('lazymind.chat.engine.subagent.db.TaskQueryDB', mock_db_class):
        result = plugin_manager._build_intent_section('sess-2')

    assert 'Only global rule' in result
