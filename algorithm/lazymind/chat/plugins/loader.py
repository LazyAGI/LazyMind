from __future__ import annotations

import importlib.util
import logging
import os
import re
from typing import Callable

import yaml

from .config import PLUGIN_DIR
from .validator import validate_all

logger = logging.getLogger(__name__)


class StateMachine:
    """State machine loaded from state.yml transitions."""

    def __init__(self, transitions: dict, step_ids: list[str]) -> None:
        self._transitions = transitions
        self._step_ids = set(step_ids)

    def is_valid_transition(self, from_step: str, to_step: str) -> bool:
        for edge in self._transitions.get(from_step, []):
            if edge.get('to') == to_step:
                return True
        return False

    def reachable_edges(self, from_step: str) -> list[dict]:
        return list(self._transitions.get(from_step, []))

    def get_reachable_steps(self, from_step: str) -> list[str]:
        # Empty from_step means the session just started; all declared steps are reachable.
        if not from_step:
            return list(self._step_ids)
        # BFS over transition edges, starting from the *successors* of from_step
        # (not from_step itself, which has already completed).
        visited: set[str] = set()
        queue: list[str] = [
            edge.get('to', '')
            for edge in self._transitions.get(from_step, [])
            if edge.get('to')
        ]
        while queue:
            current = queue.pop(0)
            if current in visited:
                continue
            visited.add(current)
            for edge in self._transitions.get(current, []):
                to = edge.get('to', '')
                if to and to not in visited:
                    queue.append(to)
        return list(visited)

    def is_reachable(self, current: str, target: str) -> bool:
        # Empty current means session start; all steps are reachable.
        if not current:
            return target in self._step_ids
        return target in self.get_reachable_steps(current)


class LegacyStateMachine:
    """Fallback when no state.yml is present: all steps are reachable from any step."""

    def __init__(self, step_ids: list[str]) -> None:
        self._step_ids = list(step_ids)

    def get_reachable_steps(self, from_step: str) -> list[str]:
        return list(self._step_ids)

    def is_reachable(self, current: str, target: str) -> bool:
        return True


def _extract_trigger_steps_from_md(scenario_md: str) -> list[str]:
    """Extract step IDs from trigger_<step_id> patterns in scenario.md."""
    pattern = re.compile(r'trigger_([a-zA-Z_][a-zA-Z0-9_]*)')
    return list(dict.fromkeys(pattern.findall(scenario_md)))


class PluginLoader:
    """Scans plugin directory and loads all valid plugins on startup."""

    def __init__(self, plugin_dir: str = PLUGIN_DIR) -> None:
        self._plugin_dir = plugin_dir
        self._plugins: dict[str, dict] = {}      # plugin_id -> metadata
        self._legacy_plugins: set[str] = set()

    def load_all(self) -> None:
        if not os.path.isdir(self._plugin_dir):
            logger.warning('Plugin directory not found: %s', self._plugin_dir)
            return

        for entry in os.scandir(self._plugin_dir):
            if not entry.is_dir():
                continue
            plugin_id = entry.name
            plugin_dir = entry.path
            plugin_yaml_path = os.path.join(plugin_dir, 'plugin.yaml')
            if not os.path.exists(plugin_yaml_path):
                continue
            try:
                self._load_plugin(plugin_id, plugin_dir)
            except Exception as exc:
                logger.error('Failed to load plugin "%s": %s', plugin_id, exc)

    def _load_plugin(self, plugin_id: str, plugin_dir: str) -> None:
        result = validate_all(plugin_dir)
        if not result.is_valid:
            for err in result.errors:
                logger.error('[Plugin %s] validation error: %s', plugin_id, err)
            logger.error('Plugin "%s" skipped due to validation errors', plugin_id)
            return

        for warn in result.warnings:
            logger.warning('[Plugin %s] %s', plugin_id, warn)
        for info in result.infos:
            logger.info('[Plugin %s] %s', plugin_id, info)

        with open(os.path.join(plugin_dir, 'plugin.yaml'), encoding='utf-8') as f:
            plugin_yaml = yaml.safe_load(f) or {}

        scenario_md_path = os.path.join(plugin_dir, 'scenario', 'scenario.md')
        with open(scenario_md_path, encoding='utf-8') as f:
            scenario_md = f.read()

        driver_path = os.path.join(plugin_dir, 'scenario', 'driver.md')
        driver_md = ''
        if os.path.exists(driver_path):
            with open(driver_path, encoding='utf-8') as f:
                driver_md = f.read()

        state_yml_path = os.path.join(plugin_dir, 'scenario', 'state.yml')
        step_specs: dict[str, dict] = {}
        state_machine: StateMachine | LegacyStateMachine

        initial_step = ''
        if os.path.exists(state_yml_path):
            with open(state_yml_path, encoding='utf-8') as f:
                state_yml = yaml.safe_load(f) or {}
            initial_step = state_yml.get('initial', '')
            step_ids = [s['id'] for s in plugin_yaml.get('steps', [])]
            state_machine = StateMachine(
                transitions=state_yml.get('transitions', {}),
                step_ids=step_ids,
            )
            step_specs = state_yml.get('steps', {})
        else:
            step_ids = _extract_trigger_steps_from_md(scenario_md)
            state_machine = LegacyStateMachine(step_ids=step_ids)
            # Legacy mode: use the first step from plugin.yaml as the initial step.
            first_steps = plugin_yaml.get('steps', [])
            initial_step = first_steps[0]['id'] if first_steps else ''
            self._legacy_plugins.add(plugin_id)
            logger.warning('Plugin %s loaded in legacy mode (no state.yml)', plugin_id)

        tools = self._load_tools(plugin_id, plugin_dir, plugin_yaml)

        self._plugins[plugin_id] = {
            'plugin_yaml': plugin_yaml,
            'scenario_md': scenario_md,
            'driver_md': driver_md,
            'state_machine': state_machine,
            'step_specs': step_specs,
            'initial_step': initial_step,
            'tools': tools,
            'plugin_dir': plugin_dir,
        }
        logger.info('Plugin "%s" loaded successfully', plugin_id)

    def _load_tools(self, plugin_id: str, plugin_dir: str,
                    plugin_yaml: dict) -> list[Callable]:
        tools: list[Callable] = []
        for script in plugin_yaml.get('tool_scripts', []):
            script_path = os.path.join(plugin_dir, script.get('path', ''))
            if not os.path.exists(script_path):
                logger.warning('[Plugin %s] tool script not found: %s', plugin_id, script_path)
                continue
            spec = importlib.util.spec_from_file_location(
                f'plugin_{plugin_id}_tools', script_path
            )
            module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)
            for fn_name in script.get('functions', []):
                fn = getattr(module, fn_name, None)
                if fn is None:
                    logger.warning('[Plugin %s] function "%s" not found in %s',
                                   plugin_id, fn_name, script_path)
                    continue
                tools.append(fn)
        return tools

    # ---- Public query API ----

    def get_scenario(self, plugin_id: str) -> str:
        return self._plugins[plugin_id]['scenario_md']

    def get_driver(self, plugin_id: str) -> str:
        return self._plugins.get(plugin_id, {}).get('driver_md', '')

    def get_state_machine(self, plugin_id: str) -> StateMachine | LegacyStateMachine:
        return self._plugins[plugin_id]['state_machine']

    def get_step_config(self, plugin_id: str, step_id: str) -> dict:
        """Return the step execution spec.

        Standard mode: returns the spec from state.yml steps.<step_id>.
        Legacy mode: returns {'prompt': scenario_md_full_text, 'tools': []}.
        driver.md is NEVER returned here; it belongs to DriverAgent only.
        """
        plugin = self._plugins[plugin_id]
        if plugin_id in self._legacy_plugins:
            return {'prompt': plugin['scenario_md'], 'tools': []}
        spec = plugin['step_specs'].get(step_id, {})
        return dict(spec)

    def get_plugin_yaml(self, plugin_id: str) -> dict:
        return self._plugins[plugin_id]['plugin_yaml']

    def get_plugin_tools(self, plugin_id: str) -> list[Callable]:
        return list(self._plugins.get(plugin_id, {}).get('tools', []))

    def get_initial_step(self, plugin_id: str) -> str:
        return self._plugins.get(plugin_id, {}).get('initial_step', '')

    def list_plugin_ids(self) -> list[str]:
        return list(self._plugins.keys())

    def is_legacy_mode(self, plugin_id: str) -> bool:
        return plugin_id in self._legacy_plugins

    def is_loaded(self, plugin_id: str) -> bool:
        return plugin_id in self._plugins


# Module-level singleton, loaded at import time.
plugin_loader = PluginLoader()
plugin_loader.load_all()
