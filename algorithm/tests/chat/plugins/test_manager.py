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


def test_advance_step_tool_rerunnable_step(loaded_plugin, mock_write_agent_data, mock_agentic_config):
    """step_d is re_runnable=True; from step_d the user can re-trigger step_d itself."""
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
    assert '{{image_url}}' in rendered       # Go injects this later
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
