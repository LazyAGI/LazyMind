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
    start_plan = MagicMock()
    start_plan.status_code = 200
    start_plan.json.return_value = {'data': {'projection': {'ready': ['step_a']}}}
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
        return real_import(name, *args, **kwargs)

    with patch('builtins.__import__', side_effect=patched_import):
        yield


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
        patch.object(plugin_manager, '_fetch_go_projection', return_value={'past': [], 'ready': ['step_b']}),
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

    auto_tools, auto_system_prompt, auto_stop_tools, _, auto_context = auto_result
    dynamic_tools, dynamic_system_prompt, dynamic_stop_tools, _, dynamic_context = dynamic_result
    auto_names = {tool.__name__ for tool in auto_tools}
    dynamic_names = {tool.__name__ for tool in dynamic_tools}

    assert 'advance_step_and_hand_off' in auto_names
    assert 'advance_steps_and_hand_off' in auto_names
    assert 'advance_step' not in auto_names
    assert {
        'advance_step', 'advance_steps',
        'advance_step_and_hand_off', 'advance_steps_and_hand_off',
    } <= dynamic_names
    assert set(auto_stop_tools) == {'advance_step_and_hand_off', 'advance_steps_and_hand_off'}
    assert set(dynamic_stop_tools) == {'advance_step_and_hand_off', 'advance_steps_and_hand_off'}
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


def test_export_parent_agentic_config_preserves_runtime_context_without_credentials():
    from lazymind.chat.plugin.plugin_manager import _export_parent_agentic_config

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


def test_trigger_plugin_step_rejects_missing_step_config(mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager

    mock_agentic_config.update({'plugin_step': 'step_a', 'plugin_session_id': 'ps-missing'})
    with (
        patch.object(plugin_manager.plugin_loader, 'get_plugin', return_value=object()),
        patch.object(plugin_manager.plugin_loader, 'get_step_config', return_value={}),
    ):
        with pytest.raises(ValueError, match='not defined'):
            plugin_manager._trigger_plugin_step(
                'test-plugin',
                'missing_step',
                'go',
                is_cold_start=False,
            )


def test_trigger_plugin_step_uses_unified_advance_operation(
        loaded_plugin, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager

    mock_agentic_config.update({
        'plugin_step': 'step_a', 'plugin_session_id': 'session-advance',
    })
    accepted = plugin_manager._TransitionSubmission(True, 'accepted', task_id='task-b')
    with patch.object(plugin_manager, '_submit_transition_to_core', return_value=accepted) as submit:
        plugin_manager._trigger_plugin_step('test-plugin', 'step_b', 'continue')

    assert submit.call_args.kwargs['operation'] == 'advance'


def test_trigger_plugin_steps_submits_one_atomic_batch(
        loaded_plugin, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager

    mock_agentic_config.update({
        'plugin_session_id': 'session-batch',
        'query': 'continue workflow',
    })
    accepted = plugin_manager._TransitionSubmission(
        accepted=True,
        message='accepted',
        command_id='command-batch',
        task_id='task-b',
        tasks=[
            {'step_id': 'step_b', 'task_id': 'task-b', 'step_state': 'pending'},
            {'step_id': 'step_c', 'task_id': 'task-c', 'step_state': 'pending'},
        ],
    )
    with patch.object(plugin_manager, '_submit_transition_to_core', return_value=accepted) as submit:
        result = plugin_manager._trigger_plugin_steps('test-plugin', [
            {'step_id': 'step_b', 'user_input': 'run B', 'runtime_instruction': 'instruction B'},
            {'step_id': 'step_c', 'user_input': 'run C', 'runtime_instruction': 'instruction C'},
        ])

    assert result.accepted is True
    kwargs = submit.call_args.kwargs
    assert kwargs['operation'] == 'execute_batch'
    assert [target['target_step_id'] for target in kwargs['targets']] == ['step_b', 'step_c']
    assert kwargs['targets'][0]['runtime_instruction'] == 'instruction B'
    assert kwargs['targets'][1]['runtime_instruction'] == 'instruction C'
    assert mock_agentic_config['_last_plugin_tasks'][1]['task_id'] == 'task-c'


def test_trigger_plugin_steps_rejects_duplicate_step_locally(
        loaded_plugin, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager

    mock_agentic_config['plugin_session_id'] = 'session-batch'
    with pytest.raises(ValueError, match='duplicate batch step_id'):
        plugin_manager._trigger_plugin_steps('test-plugin', [
            {'step_id': 'step_b', 'user_input': 'first'},
            {'step_id': 'step_b', 'user_input': 'second'},
        ])


# ---------------------------------------------------------------------------
# _trigger_plugin_step — layer 1 format validation (no DB / HTTP needed)
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


def test_build_advance_step_tool_docstring_contains_rerunnable_steps(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager
    advance = plugin_manager.build_advance_step_tool(
        'test-plugin', 'step_b',
        rewind_steps=['step_a'],
        step_labels={'step_a': 'Analyze Subject', 'step_c': 'Generate Image'},
    )
    doc = advance.__doc__ or ''
    assert 'step_a' in doc
    assert 'Previously attempted steps that may be run again' in doc
    assert 'Analyze Subject' in doc
    assert 'previously completed' in doc


def test_build_advance_step_tool_docstring_no_rerun_when_empty(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager
    advance = plugin_manager.build_advance_step_tool(
        'test-plugin', 'step_a',
        rewind_steps=[],
    )
    doc = advance.__doc__ or ''
    assert 'Previously attempted steps that may be run again' not in doc


def test_live_projection_does_not_offer_succeeded_current_step_as_retry(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager

    config = {'plugin_session_id': 'writer-session', 'plugin_step': 'step_a'}
    projection = {
        'past': ['step_a'],
        'ready': ['step_b'],
        'nodes': {'step_a': {'execution': 'succeeded'}},
    }
    with (
        patch.object(plugin_manager, '_agentic_config', return_value=config),
        patch.object(plugin_manager, '_fetch_go_projection', return_value=projection),
    ):
        advance = plugin_manager.build_advance_step_tool('test-plugin', 'step_a')

    doc = advance.__doc__ or ''
    assert 'Retry (re-run current step):' not in doc
    assert 'step_b' in doc
    assert 'step_a' in doc
    assert 'Previously attempted steps that may be run again' in doc


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
    assert '`advance_steps_and_hand_off` exactly once' in guidance
    assert 'default approval' not in guidance.lower()
    assert 'auto mode' not in guidance.lower()
    assert 'dynamic mode' not in guidance.lower()


def test_batch_guidance_requires_one_atomic_call_for_ready_frontier(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager

    guidance = plugin_manager._build_mode_guidance('dynamic')

    assert 'ONE batch call' in guidance
    assert 'Do not issue repeated' in guidance
    assert 'Running an attempted step again remains single-step' in guidance
    assert 'valid parallel choices' in guidance


def test_step_status_exposes_multi_ready_batch_hint(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager

    with patch.object(plugin_manager, '_fetch_go_projection', return_value={
        'past': ['step_a'], 'ready': ['step_b', 'step_c'],
    }):
        section = plugin_manager._build_step_status_section(
            'test-plugin', 'session-batch', '', [],
            step_labels={'step_b': 'B', 'step_c': 'C'},
        )

    assert 'step_b (B), step_c (C)' in section
    assert 'parallel frontier' in section
    assert 'one plural advancement tool call' in section


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
