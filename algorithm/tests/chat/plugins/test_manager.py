"""Unit tests for plugins/manager.py"""
from __future__ import annotations

import os
import tempfile
from unittest.mock import patch

import yaml

import lazyllm


def _setup_loader_with_plugin(root: str, auto_mode: bool = True) -> None:
    plugin_dir = os.path.join(root, 'test-plugin')
    os.makedirs(os.path.join(plugin_dir, 'scenario'), exist_ok=True)
    plugin_yaml = {
        'id': 'test-plugin',
        'steps': [
            {'id': 'step_a', 'default_mode': 'auto' if auto_mode else 'human'},
            {'id': 'step_b', 'default_mode': 'auto' if auto_mode else 'human'},
        ],
    }
    with open(os.path.join(plugin_dir, 'plugin.yaml'), 'w') as f:
        yaml.dump(plugin_yaml, f)
    with open(os.path.join(plugin_dir, 'scenario', 'scenario.md'), 'w') as f:
        f.write('trigger_step_a and trigger_step_b steps.')
    with open(os.path.join(plugin_dir, 'scenario', 'driver.md'), 'w') as f:
        f.write('# Driver\nEvaluate.')
    state_yml = {
        'initial': 'step_a',
        'transitions': {
            'step_a': [{'to': 'step_b', 'condition': 'done'}, {'to': 'step_a', 'condition': 'retry'}],
            'step_b': [{'to': 'step_a', 'condition': 'retry'}],
        },
        'steps': {
            'step_a': {'prompt': 'do step_a', 'tools': [], 'inputs': []},
            'step_b': {
                'prompt': 'do step_b',
                'tools': [],
                'inputs': [{'artifact_id': 'step_a_output', 'required': True}],
            },
        },
    }
    with open(os.path.join(plugin_dir, 'scenario', 'state.yml'), 'w') as f:
        yaml.dump(state_yml, f)


def _mock_globals(plugin_id: str = 'test-plugin', step: str = 'step_a',
                  session_id: str = 'session-1'):
    lazyllm.globals['agentic_config'] = {
        'plugin_id': plugin_id,
        'plugin_session_id': session_id,
        'plugin_step': step,
        'db_session_factory': None,
    }
    lazyllm.globals['plugin_event_queue'] = []


def test_trigger_tool_appends_step_trigger_to_event_queue():
    with tempfile.TemporaryDirectory() as root:
        _setup_loader_with_plugin(root)
        from lazymind.chat.plugins.loader import PluginLoader
        loader = PluginLoader(root)
        loader.load_all()
        with patch('lazymind.chat.plugins.manager.plugin_loader', loader):
            _mock_globals()
            from lazymind.chat.plugins.manager import trigger_plugin_step
            trigger_plugin_step('step_a', 'generate a cat image')
            queue = lazyllm.globals.get('plugin_event_queue', [])
            assert any(e.get('type') == 'step_trigger' for e in queue)


def test_trigger_tool_step_trigger_event_contains_inputs_field():
    with tempfile.TemporaryDirectory() as root:
        _setup_loader_with_plugin(root)
        from lazymind.chat.plugins.loader import PluginLoader
        loader = PluginLoader(root)
        loader.load_all()
        with patch('lazymind.chat.plugins.manager.plugin_loader', loader):
            _mock_globals(step='step_a')
            from lazymind.chat.plugins.manager import trigger_plugin_step
            lazyllm.globals['plugin_event_queue'] = []
            trigger_plugin_step('step_a', 'test')
            queue = lazyllm.globals.get('plugin_event_queue', [])
            ev = next((e for e in queue if e.get('type') == 'step_trigger'), None)
            assert ev is not None
            assert 'inputs' in ev


def test_trigger_tool_does_not_invoke_step_agent():
    with tempfile.TemporaryDirectory() as root:
        _setup_loader_with_plugin(root)
        from lazymind.chat.plugins.loader import PluginLoader
        loader = PluginLoader(root)
        loader.load_all()
        with patch('lazymind.chat.plugins.manager.plugin_loader', loader):
            _mock_globals()
            from lazymind.chat.plugins.manager import trigger_plugin_step
            with patch('lazymind.chat.plugins.step_agent.create_step_agent') as mock_agent:
                trigger_plugin_step('step_a', 'test')
                mock_agent.assert_not_called()


def test_trigger_plugin_unreachable_step_returns_error_string():
    with tempfile.TemporaryDirectory() as root:
        _setup_loader_with_plugin(root)
        from lazymind.chat.plugins.loader import PluginLoader
        loader = PluginLoader(root)
        loader.load_all()
        with patch('lazymind.chat.plugins.manager.plugin_loader', loader):
            _mock_globals(step='step_b')
            from lazymind.chat.plugins.manager import trigger_plugin_step
            result = trigger_plugin_step('step_nonexistent', 'test')
            assert 'Error' in result


def test_trigger_plugin_empty_user_input_returns_error_string():
    with tempfile.TemporaryDirectory() as root:
        _setup_loader_with_plugin(root)
        from lazymind.chat.plugins.loader import PluginLoader
        loader = PluginLoader(root)
        loader.load_all()
        with patch('lazymind.chat.plugins.manager.plugin_loader', loader):
            _mock_globals()
            from lazymind.chat.plugins.manager import trigger_plugin_step
            result = trigger_plugin_step('step_a', '')
            assert 'Error' in result
            result2 = trigger_plugin_step('step_a', '   ')
            assert 'Error' in result2


def test_trigger_return_string_instructs_llm_to_stop():
    with tempfile.TemporaryDirectory() as root:
        _setup_loader_with_plugin(root)
        from lazymind.chat.plugins.loader import PluginLoader
        loader = PluginLoader(root)
        loader.load_all()
        with patch('lazymind.chat.plugins.manager.plugin_loader', loader):
            _mock_globals()
            from lazymind.chat.plugins.manager import trigger_plugin_step
            lazyllm.globals['plugin_event_queue'] = []
            result = trigger_plugin_step('step_a', 'generate a cat')
            assert 'stop' in result.lower() or 'triggered' in result.lower()
