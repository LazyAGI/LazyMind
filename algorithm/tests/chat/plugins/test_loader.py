"""Unit tests for plugins/loader.py"""
from __future__ import annotations

import os
import tempfile

import yaml

from lazymind.chat.plugins.loader import LegacyStateMachine, PluginLoader, StateMachine


def _build_plugin_dir(tmp: str, with_state_yml: bool = True,
                      auto_mode: bool = True) -> str:
    os.makedirs(os.path.join(tmp, 'scenario'), exist_ok=True)
    plugin_yaml = {
        'id': 'test-plugin',
        'steps': [
            {'id': 'step_a', 'default_mode': 'auto' if auto_mode else 'human'},
            {'id': 'step_b', 'default_mode': 'auto' if auto_mode else 'human'},
        ],
    }
    with open(os.path.join(tmp, 'plugin.yaml'), 'w') as f:
        yaml.dump(plugin_yaml, f)

    scenario_md = 'trigger_step_a and trigger_step_b are the available steps.'
    with open(os.path.join(tmp, 'scenario', 'scenario.md'), 'w') as f:
        f.write(scenario_md)

    driver_md = '# Driver\nEvaluate the result carefully.'
    with open(os.path.join(tmp, 'scenario', 'driver.md'), 'w') as f:
        f.write(driver_md)

    if with_state_yml:
        state_yml = {
            'initial': 'step_a',
            'transitions': {
                'step_a': [{'to': 'step_b', 'condition': 'done'}],
                'step_b': [{'to': 'step_a', 'condition': 'retry'}],
            },
            'steps': {
                'step_a': {'prompt': 'do step_a {{user_input}}', 'tools': []},
                'step_b': {'prompt': 'do step_b {{step_a_output}}', 'tools': []},
            },
        }
        with open(os.path.join(tmp, 'scenario', 'state.yml'), 'w') as f:
            yaml.dump(state_yml, f)

    return tmp


def test_loader_standard_mode_loads_both_scenario_md_and_state_yml():
    with tempfile.TemporaryDirectory() as root:
        plugin_dir = os.path.join(root, 'test-plugin')
        _build_plugin_dir(plugin_dir)
        loader = PluginLoader(root)
        loader.load_all()
        assert loader.is_loaded('test-plugin')
        assert not loader.is_legacy_mode('test-plugin')
        sm = loader.get_state_machine('test-plugin')
        assert isinstance(sm, StateMachine)


def test_loader_legacy_mode_when_no_state_yml():
    with tempfile.TemporaryDirectory() as root:
        plugin_dir = os.path.join(root, 'test-plugin')
        _build_plugin_dir(plugin_dir, with_state_yml=False, auto_mode=False)
        loader = PluginLoader(root)
        loader.load_all()
        assert loader.is_loaded('test-plugin')
        assert loader.is_legacy_mode('test-plugin')
        sm = loader.get_state_machine('test-plugin')
        assert isinstance(sm, LegacyStateMachine)


def test_loader_legacy_mode_does_not_use_driver_md_as_step_guidance():
    with tempfile.TemporaryDirectory() as root:
        plugin_dir = os.path.join(root, 'test-plugin')
        _build_plugin_dir(plugin_dir, with_state_yml=False, auto_mode=False)
        loader = PluginLoader(root)
        loader.load_all()
        cfg = loader.get_step_config('test-plugin', 'step_a')
        # Legacy mode uses scenario.md as prompt, not driver.md.
        assert 'Driver' not in cfg.get('prompt', '')
        assert 'trigger_step_a' in cfg.get('prompt', '')


def test_loader_consistency_warning_does_not_block_load():
    with tempfile.TemporaryDirectory() as root:
        plugin_dir = os.path.join(root, 'test-plugin')
        _build_plugin_dir(plugin_dir)
        # Add a step in state.yml that is NOT in scenario.md.
        state_path = os.path.join(plugin_dir, 'scenario', 'state.yml')
        with open(state_path) as f:
            state_yml = yaml.safe_load(f)
        state_yml['steps']['step_c_undocumented'] = {'prompt': 'x', 'tools': []}
        with open(state_path, 'w') as f:
            yaml.dump(state_yml, f)

        loader = PluginLoader(root)
        loader.load_all()
        # Plugin still loads despite consistency warning.
        assert loader.is_loaded('test-plugin')


def test_get_step_config_returns_spec_from_state_yml():
    with tempfile.TemporaryDirectory() as root:
        plugin_dir = os.path.join(root, 'test-plugin')
        _build_plugin_dir(plugin_dir)
        loader = PluginLoader(root)
        loader.load_all()
        cfg = loader.get_step_config('test-plugin', 'step_a')
        assert '{{user_input}}' in cfg.get('prompt', '')


def test_get_step_config_legacy_returns_scenario_md_only():
    with tempfile.TemporaryDirectory() as root:
        plugin_dir = os.path.join(root, 'test-plugin')
        _build_plugin_dir(plugin_dir, with_state_yml=False, auto_mode=False)
        loader = PluginLoader(root)
        loader.load_all()
        cfg = loader.get_step_config('test-plugin', 'step_a')
        assert cfg['tools'] == []
        assert 'trigger_step_a' in cfg['prompt']


def test_loader_skips_invalid_plugin_on_error():
    with tempfile.TemporaryDirectory() as root:
        plugin_dir = os.path.join(root, 'bad-plugin')
        os.makedirs(os.path.join(plugin_dir, 'scenario'), exist_ok=True)
        # auto mode with no driver.md — validation should fail.
        plugin_yaml = {'id': 'bad-plugin', 'steps': [{'id': 's', 'default_mode': 'auto'}]}
        with open(os.path.join(plugin_dir, 'plugin.yaml'), 'w') as f:
            yaml.dump(plugin_yaml, f)
        with open(os.path.join(plugin_dir, 'scenario', 'scenario.md'), 'w') as f:
            f.write('trigger_s step.')

        loader = PluginLoader(root)
        loader.load_all()
        assert not loader.is_loaded('bad-plugin')


def test_is_legacy_mode_returns_true_without_state_yml():
    with tempfile.TemporaryDirectory() as root:
        plugin_dir = os.path.join(root, 'test-plugin')
        _build_plugin_dir(plugin_dir, with_state_yml=False, auto_mode=False)
        loader = PluginLoader(root)
        loader.load_all()
        assert loader.is_legacy_mode('test-plugin') is True


def test_state_machine_empty_current_step_returns_all_steps():
    """Session start (current_step='') must expose all declared steps as reachable."""
    sm = StateMachine(
        transitions={
            'step_a': [{'to': 'step_b', 'condition': 'done'}],
        },
        step_ids=['step_a', 'step_b'],
    )
    reachable = sm.get_reachable_steps('')
    assert set(reachable) == {'step_a', 'step_b'}
    assert sm.is_reachable('', 'step_a')
    assert sm.is_reachable('', 'step_b')


def test_state_machine_normal_traversal():
    sm = StateMachine(
        transitions={
            'step_a': [{'to': 'step_b', 'condition': 'done'}],
            'step_b': [],
        },
        step_ids=['step_a', 'step_b'],
    )
    assert set(sm.get_reachable_steps('step_a')) == {'step_a', 'step_b'}
    assert set(sm.get_reachable_steps('step_b')) == {'step_b'}
