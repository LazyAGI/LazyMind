"""Tests for plugin_manager — cold-start triggers and advance_step tool builder.

External dependencies (_write_agent_data, lazyllm.globals, httpx) are fully mocked
so these tests run without a real LLM or algorithm service.
"""
from __future__ import annotations

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


# ---------------------------------------------------------------------------
# build_cold_start_tools
# ---------------------------------------------------------------------------

def test_build_cold_start_tools_creates_one_trigger_per_plugin(loaded_plugin):
    from lazymind.chat.plugin import plugin_manager
    tools = plugin_manager.build_cold_start_tools()
    assert len(tools) >= 1
    names = [t.__name__ for t in tools]
    assert 'trigger_test_plugin' in names


def test_cold_start_trigger_calls_write_agent_data(loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    tools = plugin_manager.build_cold_start_tools()
    trigger = next(t for t in tools if t.__name__ == 'trigger_test_plugin')

    result = trigger(user_input='Draw a sunset')

    assert mock_write_agent_data.called
    call_kwargs = mock_write_agent_data.call_args
    assert call_kwargs.kwargs.get('agent_type') == 'plugin_step'
    assert call_kwargs.kwargs.get('params', {}).get('is_cold_start') is True
    assert call_kwargs.kwargs.get('params', {}).get('step_id') == 'step_a'
    assert 'triggered' in result.lower() or 'step' in result.lower()


def test_cold_start_trigger_rejects_empty_input(loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    tools = plugin_manager.build_cold_start_tools()
    trigger = next(t for t in tools if t.__name__ == 'trigger_test_plugin')

    result = trigger(user_input='   ')
    assert 'error' in result.lower()
    assert not mock_write_agent_data.called


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


def test_advance_step_tool_triggers_reachable_step(loaded_plugin, mock_write_agent_data, mock_agentic_config):
    from lazymind.chat.plugin import plugin_manager
    mock_agentic_config.update({
        'plugin_id': 'test-plugin',
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


def test_advance_step_tool_retrigger_same_step(loaded_plugin, mock_write_agent_data, mock_agentic_config):
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
    """Verify output_artifact_keys is set correctly from state.yml step outputs."""
    from lazymind.chat.plugin.plugin_manager import _trigger_plugin_step
    mock_agentic_config['plugin_step'] = '__start__'

    _trigger_plugin_step('test-plugin', 'step_a', 'hello', is_cold_start=True)

    assert mock_write_agent_data.called
    kwargs = mock_write_agent_data.call_args.kwargs
    assert 'analysis' in kwargs['output_artifact_keys']


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
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
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
        loaded_plugin, mock_write_agent_data, mock_agentic_config):
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
        {'task_id': 'task-001', 'artifact_key': 'optimized_prompt', 'content_type': 'text',
         'value': {'text': 'a beautiful sunset'}, 'seq': 1},
    ]

    objective = 'Generate image from: {{optimized_prompt}}.'
    result = _enrich_objective_with_artifacts(objective, {'session_id': 'ps-1'}, db)
    assert 'a beautiful sunset' in result
    assert '{{optimized_prompt}}' not in result


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


def test_build_advance_step_tool_rewind_step_is_accepted(
        loaded_plugin, mock_write_agent_data, mock_agentic_config, mock_fetch_succeeded):
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
