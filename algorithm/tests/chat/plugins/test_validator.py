"""Unit tests for plugins/validator.py"""
from __future__ import annotations

import os
import tempfile

import yaml

from lazymind.chat.plugins.validator import (
    validate_all,
    validate_consistency,
    validate_driver_mode,
    validate_state_yml,
)


def _write_plugin_dir(tmp: str, plugin_yaml: dict, state_yml: dict | None,
                      scenario_md: str, driver_md: str | None = None) -> str:
    os.makedirs(os.path.join(tmp, 'scenario'), exist_ok=True)
    with open(os.path.join(tmp, 'plugin.yaml'), 'w') as f:
        yaml.dump(plugin_yaml, f)
    with open(os.path.join(tmp, 'scenario', 'scenario.md'), 'w') as f:
        f.write(scenario_md)
    if state_yml is not None:
        with open(os.path.join(tmp, 'scenario', 'state.yml'), 'w') as f:
            yaml.dump(state_yml, f)
    if driver_md is not None:
        with open(os.path.join(tmp, 'scenario', 'driver.md'), 'w') as f:
            f.write(driver_md)
    return tmp


VALID_PLUGIN_YAML = {
    'id': 'test-plugin',
    'steps': [{'id': 'step_a', 'default_mode': 'auto'}, {'id': 'step_b', 'default_mode': 'auto'}],
}
VALID_STATE_YML = {
    'initial': 'step_a',
    'transitions': {
        'step_a': [{'to': 'step_b', 'condition': 'done'}],
        'step_b': [{'to': 'step_a', 'condition': 'retry'}],
    },
    'steps': {
        'step_a': {'prompt': 'do step_a', 'tools': []},
        'step_b': {'prompt': 'do step_b', 'tools': []},
    },
}
SCENARIO_MD = 'trigger_step_a and trigger_step_b are the available steps.'


def test_validate_state_yml_valid():
    result = validate_state_yml(VALID_STATE_YML, VALID_PLUGIN_YAML)
    assert result.is_valid
    assert not result.errors


def test_validate_state_yml_missing_step():
    bad_state = {
        'initial': 'step_a',
        'transitions': {
            'step_a': [{'to': 'nonexistent', 'condition': 'done'}],
        },
        'steps': {'step_a': {'prompt': 'x', 'tools': []}},
    }
    result = validate_state_yml(bad_state, VALID_PLUGIN_YAML)
    assert not result.is_valid
    assert any('nonexistent' in e for e in result.errors)


def test_validate_state_yml_initial_not_in_steps():
    bad_state = {**VALID_STATE_YML, 'initial': 'missing_step'}
    result = validate_state_yml(bad_state, VALID_PLUGIN_YAML)
    assert not result.is_valid


def test_validate_consistency_warns_on_step_missing_in_scenario_md():
    extra_steps = {**VALID_STATE_YML['steps'], 'step_c': {'prompt': 'x', 'tools': []}}
    state_with_extra = {**VALID_STATE_YML, 'steps': extra_steps}
    result = validate_consistency(state_with_extra, SCENARIO_MD)
    assert any('step_c' in w for w in result.warnings)


def test_validate_consistency_passes_when_all_steps_mentioned():
    result = validate_consistency(VALID_STATE_YML, SCENARIO_MD)
    assert not result.errors
    assert not result.warnings


def test_validate_driver_mode_auto_without_driver_md_is_error():
    result = validate_driver_mode(VALID_PLUGIN_YAML, driver_md_exists=False)
    assert not result.is_valid
    assert any('auto' in e.lower() or 'driver' in e.lower() for e in result.errors)


def test_validate_driver_mode_human_without_driver_md_ok():
    plugin_yaml = {
        'id': 'test',
        'steps': [{'id': 'step_a', 'default_mode': 'human'}],
    }
    result = validate_driver_mode(plugin_yaml, driver_md_exists=False)
    assert result.is_valid


def test_validate_all_blocks_on_error():
    with tempfile.TemporaryDirectory() as tmp:
        _write_plugin_dir(tmp, VALID_PLUGIN_YAML, VALID_STATE_YML, SCENARIO_MD, driver_md=None)
        result = validate_all(tmp)
        assert not result.is_valid


def test_validate_all_passes_with_driver_md():
    with tempfile.TemporaryDirectory() as tmp:
        _write_plugin_dir(tmp, VALID_PLUGIN_YAML, VALID_STATE_YML, SCENARIO_MD,
                          driver_md='# Driver\nEvaluate the step output.')
        result = validate_all(tmp)
        assert result.is_valid, result.errors
