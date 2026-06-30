"""Tests for plugin_loader — plugin discovery, state machine, and slot/artifact helpers."""
from __future__ import annotations

import textwrap
from pathlib import Path
from unittest.mock import patch

import pytest

# ---------------------------------------------------------------------------
# Helpers to build a temporary plugin directory
# ---------------------------------------------------------------------------

_PLUGIN_YAML = textwrap.dedent("""\
    id: test-plugin
    name: Test Plugin
    description: A plugin for unit testing.
    steps:
      - id: step_a
        label: Step A
      - id: step_b
        label: Step B
      - id: step_c
        label: Step C
      - id: step_d
        label: Step D
    ui:
      tabs:
        - id: output
          label: Output
          slots:
            - id: text_result
              type: text
              cardinality: single
            - id: image_gallery
              type: image
              cardinality: list
""")

_STATE_YML = textwrap.dedent("""\
    initial: __start__
    transitions:
      __start__:
        - to: step_a
      step_a:
        - to: step_b
        - to: step_a    # full retry
      step_b:
        - to: step_c
        - to: step_b    # full retry
      step_c:
        - to: step_d
        - to: step_b    # re-run from step_b
      step_d:
        - to: step_d    # full retry or partial retry (list slot)
        - to: __end__
    steps:
      step_a:
        prompt: |
          Analyze the input: {{user_input}}.
          {{runtime_instruction}}
          Save result: save_artifact(key='analysis', content_type='text', value=<text>).
        outputs:
          - artifact_id: analysis
            content_type: text
            slot_id: text_result
      step_b:
        prompt: |
          Optimize based on analysis: {{analysis}}.
          {{runtime_instruction}}
        inputs:
          - artifact_id: analysis
            required: true
        outputs:
          - artifact_id: optimized
            content_type: text
            slot_id: text_result
      step_c:
        prompt: |
          Generate image from: {{optimized}}.
          {{runtime_instruction}}
        tools: [gen_tool]
        inputs:
          - artifact_id: optimized
            required: true
        outputs:
          - artifact_id: image_url
            content_type: image
            slot_id: image_gallery
      step_d:
        prompt: |
          Enhance image: {{image_url}}.
          {{runtime_instruction}}
        tools: [enhance_tool]
        inputs:
          - artifact_id: image_url
            required: true
        outputs:
          - artifact_id: enhanced_url
            content_type: image
            slot_id: image_gallery
""")

_SCENARIO_MD = 'Call trigger_test_plugin when user wants to test.\n'
_DRIVER_MD = 'Evaluate step results and describe whether the step is complete.\n'


def make_plugin_dir(tmp_path: Path) -> Path:
    """Create a complete valid plugin directory under tmp_path."""
    plugin_dir = tmp_path / 'plugins' / 'test-plugin'
    scenario_dir = plugin_dir / 'scenario'
    scenario_dir.mkdir(parents=True)
    (plugin_dir / 'plugin.yaml').write_text(_PLUGIN_YAML)
    (scenario_dir / 'state.yml').write_text(_STATE_YML)
    (scenario_dir / 'scenario.md').write_text(_SCENARIO_MD)
    (scenario_dir / 'driver.md').write_text(_DRIVER_MD)
    return tmp_path / 'plugins'


# ---------------------------------------------------------------------------
# PluginSpec loading
# ---------------------------------------------------------------------------

def test_pluginspec_loads_valid_plugin(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')
    assert spec.plugin_id == 'test-plugin'
    assert spec.yaml['name'] == 'Test Plugin'
    assert spec.scenario_md.strip() == _SCENARIO_MD.strip()
    assert spec.driver_md is not None


def test_pluginspec_missing_plugin_yaml_raises(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugin_dir = tmp_path / 'bad-plugin'
    plugin_dir.mkdir()
    with pytest.raises(FileNotFoundError):
        PluginSpec('bad-plugin', plugin_dir)


def test_pluginspec_missing_scenario_md_raises(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    (plugins_dir / 'test-plugin' / 'scenario' / 'scenario.md').unlink()
    with pytest.raises(FileNotFoundError):
        PluginSpec('test-plugin', plugins_dir / 'test-plugin')


def test_pluginspec_no_driver_md_warns(tmp_path, caplog):
    import logging
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    (plugins_dir / 'test-plugin' / 'scenario' / 'driver.md').unlink()
    with caplog.at_level(logging.WARNING, logger='lazymind.chat.plugin.plugin_loader'):
        spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')
    assert spec.driver_md is None
    assert 'auto mode' in caplog.text.lower() or 'driver.md' in caplog.text


# ---------------------------------------------------------------------------
# get_step_config
# ---------------------------------------------------------------------------

def test_get_step_config_returns_full_dict(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')

    cfg = spec.get_step_config('step_b')
    assert len(cfg['inputs']) == 1
    assert cfg['inputs'][0]['artifact_id'] == 'analysis'
    assert 'outputs' in cfg
    # Retry is expressed via state machine transitions, not a step-level flag.
    assert 're_runnable' not in cfg


def test_get_step_config_unknown_step_returns_empty(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')
    assert spec.get_step_config('nonexistent') == {}


# ---------------------------------------------------------------------------
# get_slot_def
# ---------------------------------------------------------------------------

def test_get_slot_def_found(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')

    slot = spec.get_slot_def('text_result')
    assert slot is not None
    assert slot['cardinality'] == 'single'
    assert slot['type'] == 'text'

    slot_list = spec.get_slot_def('image_gallery')
    assert slot_list is not None
    assert slot_list['cardinality'] == 'list'


def test_get_slot_def_not_found_returns_none(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')
    assert spec.get_slot_def('no_such_slot') is None


# ---------------------------------------------------------------------------
# get_slot_for_artifact
# ---------------------------------------------------------------------------

def test_get_slot_for_artifact_returns_slot_id(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')

    assert spec.get_slot_for_artifact('image_url') == 'image_gallery'
    assert spec.get_slot_for_artifact('optimized') == 'text_result'


def test_get_slot_for_artifact_no_slot(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')
    assert spec.get_slot_for_artifact('unknown_artifact') is None


# ---------------------------------------------------------------------------
# StateMachine
# ---------------------------------------------------------------------------

def test_state_machine_get_reachable_from_start(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')
    sm = spec.state_machine
    reachable = sm.get_reachable_steps('__start__')
    assert reachable == ['step_a']


def test_state_machine_get_reachable_mid_flow(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')
    sm = spec.state_machine

    # From step_c user can go to step_d or retry step_b.
    reachable = sm.get_reachable_steps('step_c')
    assert 'step_d' in reachable
    assert 'step_b' in reachable
    assert '__end__' not in reachable


def test_state_machine_get_reachable_excludes_reserved(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')
    sm = spec.state_machine

    # step_d can reach __end__ but it must be excluded from reachable steps.
    reachable = sm.get_reachable_steps('step_d')
    assert '__end__' not in reachable
    assert '__start__' not in reachable


def test_state_machine_is_reachable(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')
    sm = spec.state_machine

    assert sm.is_reachable('step_a', 'step_b') is True
    assert sm.is_reachable('step_a', 'step_a') is True  # retry path
    assert sm.is_reachable('step_a', 'step_c') is False
    assert sm.is_reachable('step_d', 'step_a') is False  # no backward edge


def test_state_machine_empty_current_defaults_to_start(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    spec = PluginSpec('test-plugin', plugins_dir / 'test-plugin')
    sm = spec.state_machine

    # Empty string should behave the same as '__start__'.
    reachable = sm.get_reachable_steps('')
    assert 'step_a' in reachable


# ---------------------------------------------------------------------------
# StateMachine — get_ancestors
# ---------------------------------------------------------------------------

def test_state_machine_get_ancestors_of_step_d(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    sm = PluginSpec('test-plugin', plugins_dir / 'test-plugin').state_machine

    # step_d is reachable from step_a -> step_b -> step_c -> step_d,
    # so all of step_a, step_b, step_c are ancestors.
    ancestors = sm.get_ancestors('step_d')
    assert 'step_a' in ancestors
    assert 'step_b' in ancestors
    assert 'step_c' in ancestors
    # step_d self-loops; it must not appear as its own ancestor.
    assert 'step_d' not in ancestors
    # Reserved nodes must be excluded.
    assert '__start__' not in ancestors
    assert '__end__' not in ancestors


def test_state_machine_get_ancestors_of_step_a(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    sm = PluginSpec('test-plugin', plugins_dir / 'test-plugin').state_machine

    # step_a has no non-reserved, non-self ancestors.
    ancestors = sm.get_ancestors('step_a')
    assert len(ancestors) == 0


def test_state_machine_get_ancestors_of_step_c(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    sm = PluginSpec('test-plugin', plugins_dir / 'test-plugin').state_machine

    # step_c is reachable from step_a -> step_b -> step_c.
    # step_c -> step_b creates a cycle; BFS must not loop.
    ancestors = sm.get_ancestors('step_c')
    assert 'step_a' in ancestors
    assert 'step_b' in ancestors
    assert 'step_c' not in ancestors


def test_state_machine_get_ancestors_excludes_reserved(tmp_path):
    from lazymind.chat.plugin.plugin_loader import PluginSpec
    plugins_dir = make_plugin_dir(tmp_path)
    sm = PluginSpec('test-plugin', plugins_dir / 'test-plugin').state_machine

    for step in ('step_a', 'step_b', 'step_c', 'step_d'):
        ancestors = sm.get_ancestors(step)
        assert '__start__' not in ancestors
        assert '__end__' not in ancestors


# ---------------------------------------------------------------------------
# load_all registry
# ---------------------------------------------------------------------------

def test_load_all_populates_registry(tmp_path):
    from lazymind.chat.plugin import plugin_loader
    plugins_dir = make_plugin_dir(tmp_path)
    with patch.object(plugin_loader, '_PLUGINS_DIR', plugins_dir):
        plugin_loader.load_all()
    try:
        spec = plugin_loader.get_plugin('test-plugin')
        assert spec is not None
        assert spec.plugin_id == 'test-plugin'
    finally:
        plugin_loader.load_all()   # restore original registry


def test_load_all_skips_non_plugin_dirs(tmp_path):
    from lazymind.chat.plugin import plugin_loader
    plugins_dir = make_plugin_dir(tmp_path)
    # Add a directory without plugin.yaml.
    (plugins_dir / 'not-a-plugin').mkdir()
    with patch.object(plugin_loader, '_PLUGINS_DIR', plugins_dir):
        plugin_loader.load_all()
    try:
        assert plugin_loader.get_plugin('not-a-plugin') is None
    finally:
        plugin_loader.load_all()


def test_list_plugins_returns_summary(tmp_path):
    from lazymind.chat.plugin import plugin_loader
    plugins_dir = make_plugin_dir(tmp_path)
    with patch.object(plugin_loader, '_PLUGINS_DIR', plugins_dir):
        plugin_loader.load_all()
    try:
        plugins = plugin_loader.list_plugins()
        names = [p['id'] for p in plugins]
        assert 'test-plugin' in names
        p = next(x for x in plugins if x['id'] == 'test-plugin')
        assert len(p['steps']) == 4
        assert p['steps'][0]['id'] == 'step_a'
    finally:
        plugin_loader.load_all()


# ---------------------------------------------------------------------------
# find_producer_step
# ---------------------------------------------------------------------------

def test_find_producer_step(tmp_path):
    from lazymind.chat.plugin import plugin_loader
    plugins_dir = make_plugin_dir(tmp_path)
    with patch.object(plugin_loader, '_PLUGINS_DIR', plugins_dir):
        plugin_loader.load_all()
    try:
        assert plugin_loader.find_producer_step('test-plugin', 'image_url') == 'step_c'
        assert plugin_loader.find_producer_step('test-plugin', 'enhanced_url') == 'step_d'
        assert plugin_loader.find_producer_step('test-plugin', 'nonexistent') is None
    finally:
        plugin_loader.load_all()
