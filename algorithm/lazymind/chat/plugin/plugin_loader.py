"""Plugin loader — discovers and validates plugin packages under the plugins directory.

Each plugin lives at <plugins-dir>/<plugin-id>/ and must contain:
  - plugin.yaml          (required) — registration metadata
  - scenario/scenario.md (required) — ChatAgent intent-recognition guide
  - scenario/state.yml   (required) — state machine + step execution spec
  - scenario/driver.md   (optional, required for auto mode) — DriverAgent prompt
  - scripts/             (optional) — plugin-local tool implementations

The plugins directory is configured via lazymind.config['plugins_dir'] (env: LAZYMIND_PLUGINS_DIR),
falling back to plugin/plugins/ relative to this file for local development.

Loaded plugins are cached at import time (startup). Hot-reload is not supported.
"""
from __future__ import annotations

import importlib.util
import logging
import sys
import types
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

import yaml

from lazymind.config import config as _cfg

LOG = logging.getLogger(__name__)

# Base directory for all plugin packages, configured via lazymind config.
_PLUGINS_DIR = Path(_cfg['plugins_dir'])

# Registry: {plugin_id: PluginSpec}
_registry: Dict[str, 'PluginSpec'] = {}


class StateMachine:
    """Minimal state machine parsed from state.yml transitions block."""

    _RESERVED = {'__start__', '__end__'}

    def __init__(self, initial: str, transitions: Dict[str, List[Dict[str, Any]]]) -> None:
        self.initial = initial
        self._transitions: Dict[str, List[str]] = {}
        for src, edges in transitions.items():
            targets = [e['to'] for e in edges if isinstance(e, dict) and 'to' in e]
            self._transitions[src] = targets

    def get_reachable_steps(self, current_step: str) -> List[str]:
        """Return step IDs reachable from current_step (excluding reserved states)."""
        targets = self._transitions.get(current_step or '__start__', [])
        return [t for t in targets if t not in self._RESERVED]

    def is_reachable(self, current_step: str, target_step: str) -> bool:
        """Return True if target_step is directly reachable from current_step.

        A step is always reachable from itself (retry semantics).
        """
        if target_step == current_step and target_step not in self._RESERVED:
            return True
        return target_step in self.get_reachable_steps(current_step)

    def get_terminal_steps(self, from_step: Optional[str] = None) -> List[str]:
        """Return terminal step IDs whose only forward transitions lead to __end__.

        When from_step is given, only the current step and its direct successors
        are considered — past steps are irrelevant and distant future steps would
        only add noise to the LLM prompt.
        """
        def _is_terminal(step: str) -> bool:
            targets = self._transitions.get(step, [])
            non_reserved = [t for t in targets if t not in self._RESERVED]
            return '__end__' in targets and not non_reserved

        if from_step is None:
            return [s for s in self._transitions if s not in self._RESERVED and _is_terminal(s)]

        candidates = {from_step}
        candidates.update(
            t for t in self._transitions.get(from_step or '__start__', [])
            if t not in self._RESERVED
        )
        return [s for s in candidates if _is_terminal(s)]

    def get_ancestors(self, step: str) -> set:
        """Return all ancestor step IDs of step in the state machine graph.

        An ancestor is any node from which step is reachable via one or more
        forward transitions.  Reserved nodes (__start__, __end__) are excluded.
        Self-loops do not contribute ancestors.
        """
        reverse: Dict[str, List[str]] = {}
        for src, targets in self._transitions.items():
            for t in targets:
                if t != src:  # skip self-loops
                    reverse.setdefault(t, []).append(src)
        visited: set = {step}  # seed with step itself to prevent cycles back to the origin
        queue: List[str] = [step]
        while queue:
            node = queue.pop()
            for parent in reverse.get(node, []):
                if parent not in visited and parent not in self._RESERVED:
                    visited.add(parent)
                    queue.append(parent)
        visited.discard(step)  # remove origin; only true ancestors should be in the result
        return visited


class PluginSpec:
    """Holds all parsed artifacts for one plugin."""

    def __init__(self, plugin_id: str, plugin_dir: Path) -> None:
        self.plugin_id = plugin_id
        self.plugin_dir = plugin_dir

        # Load plugin.yaml
        plugin_yaml_path = plugin_dir / 'plugin.yaml'
        with plugin_yaml_path.open('r', encoding='utf-8') as f:
            self.yaml: Dict[str, Any] = yaml.safe_load(f) or {}

        # Load scenario files
        scenario_dir = plugin_dir / 'scenario'
        self.scenario_md: str = self._read_text(scenario_dir / 'scenario.md')
        state_raw: Dict[str, Any] = {}
        state_path = scenario_dir / 'state.yml'
        with state_path.open('r', encoding='utf-8') as f:
            state_raw = yaml.safe_load(f) or {}
        self.state: Dict[str, Any] = state_raw
        self.driver_md: Optional[str] = self._read_text(scenario_dir / 'driver.md', optional=True)

        # Build state machine
        self.state_machine = StateMachine(
            initial=str(self.state.get('initial', '__start__')),
            transitions=self.state.get('transitions', {}),
        )

        # Extract step configs from state.yml
        self._steps: Dict[str, Dict[str, Any]] = self.state.get('steps', {})

        # Load plugin-local script tools declared in plugin.yaml tool_scripts.
        self._script_tools: Dict[str, Callable] = self._load_script_tools()

        # Validate: auto-capable steps need driver.md
        self._validate()

    def _load_script_tools(self) -> Dict[str, Callable]:
        """Dynamically import functions declared in plugin.yaml tool_scripts.

        Each entry under tool_scripts must have:
          - path: relative path from the plugin directory to the Python file
          - functions: list of function names to import from that file

        Returns a dict mapping function_name -> callable.
        """
        result: Dict[str, Callable] = {}
        entries: List[Dict[str, Any]] = self.yaml.get('tool_scripts', []) or []
        for entry in entries:
            rel_path = entry.get('path', '')
            func_names: List[str] = entry.get('functions', []) or []
            if not rel_path or not func_names:
                continue
            script_path = self.plugin_dir / rel_path
            if not script_path.exists():
                LOG.warning(
                    '[PluginLoader] plugin=%s tool_script not found: %s',
                    self.plugin_id, script_path,
                )
                continue
            # Use a unique module name to avoid collisions across plugins.
            module_name = f'_plugin_script_{self.plugin_id}_{script_path.stem}'
            if module_name in sys.modules:
                module: types.ModuleType = sys.modules[module_name]
            else:
                spec = importlib.util.spec_from_file_location(module_name, script_path)
                if spec is None or spec.loader is None:
                    LOG.warning(
                        '[PluginLoader] plugin=%s cannot load script: %s',
                        self.plugin_id, script_path,
                    )
                    continue
                module = importlib.util.module_from_spec(spec)
                sys.modules[module_name] = module
                try:
                    spec.loader.exec_module(module)  # type: ignore[union-attr]
                except Exception as exc:
                    LOG.error(
                        '[PluginLoader] plugin=%s script exec failed (%s): %s',
                        self.plugin_id, script_path, exc,
                    )
                    del sys.modules[module_name]
                    continue
            for fn_name in func_names:
                fn = getattr(module, fn_name, None)
                if fn is None or not callable(fn):
                    LOG.warning(
                        '[PluginLoader] plugin=%s script %s has no callable %r',
                        self.plugin_id, rel_path, fn_name,
                    )
                    continue
                result[fn_name] = fn
                LOG.info(
                    '[PluginLoader] plugin=%s registered script tool: %s',
                    self.plugin_id, fn_name,
                )
        return result

    def get_script_tool(self, name: str) -> Optional[Callable]:
        """Return a script tool callable by name, or None if not found."""
        return self._script_tools.get(name)

    def list_script_tool_names(self) -> List[str]:
        """Return the names of all registered script tools for this plugin."""
        return list(self._script_tools.keys())

    @staticmethod
    def _read_text(path: Path, optional: bool = False) -> Optional[str]:
        if not path.exists():
            if optional:
                return None
            raise FileNotFoundError(f'Required file missing: {path}')
        return path.read_text(encoding='utf-8')

    def _validate(self) -> None:
        # plugin.yaml must declare 'id' and 'steps'
        if not self.yaml.get('id'):
            raise ValueError(f'plugin.yaml missing id in {self.plugin_dir}')
        if not self.yaml.get('steps'):
            raise ValueError(f'plugin.yaml missing steps in {self.plugin_dir}')

        # If driver.md is missing, we emit a warning but don't hard-fail load.
        # auto mode will be silently degraded to manual at runtime if driver.md absent.
        if not self.driver_md:
            LOG.warning(
                '[PluginLoader] plugin=%s has no driver.md; auto mode will be disabled',
                self.plugin_id,
            )

    def get_step_config(self, step_id: str) -> Dict[str, Any]:
        return dict(self._steps.get(step_id, {}))

    def get_slot_def(self, slot_id: str) -> Optional[Dict[str, Any]]:
        """Find a slot definition in plugin.yaml ui.tabs by slot_id."""
        for tab in (self.yaml.get('ui') or {}).get('tabs', []):
            for slot in tab.get('slots', []):
                if slot.get('id') == slot_id:
                    return dict(slot)
        return None

    def get_slot_for_artifact(self, artifact_id: str) -> Optional[str]:
        """Return the slot_id bound to artifact_id in any step output, or None."""
        for step_cfg in self._steps.values():
            for out in step_cfg.get('outputs', []):
                if out.get('artifact_id') == artifact_id:
                    return out.get('slot_id')
        return None

    def get_slot_for_artifact_key(self, artifact_key: str) -> Optional[Dict[str, Any]]:
        """Return the slot definition (id, cardinality, ordered, caption_key …) for an artifact_key."""
        for tab in (self.yaml.get('ui') or {}).get('tabs', []):
            for slot in tab.get('slots', []):
                if slot.get('artifact_key') == artifact_key:
                    return dict(slot)
        return None

    def get_i18n_label(self, lang: str, key_path: str, fallback: str = '') -> str:
        """Return a translated label from plugin.yaml i18n section.

        key_path uses dot-notation: e.g. 'tabs.materials', 'slots.material_images',
        'steps.generate_image'.

        Args:
            lang: BCP-47 language tag, e.g. 'zh-CN'.
            key_path: Dot-separated path into the i18n subtree.
            fallback: Value to return when the key is missing.
        """
        i18n = self.yaml.get('i18n') or {}
        node: Any = i18n.get(lang) or i18n.get(lang.split('-')[0]) or {}
        for part in key_path.split('.'):
            if not isinstance(node, dict):
                return fallback
            node = node.get(part)
            if node is None:
                return fallback
        if isinstance(node, dict):
            return str(node.get('label', fallback))
        return str(node) if node else fallback


def load_all() -> None:
    """Discover and load all plugins from the plugins directory. Called at startup."""
    global _registry
    _registry = {}
    if not _PLUGINS_DIR.is_dir():
        LOG.warning('[PluginLoader] plugins directory not found: %s', _PLUGINS_DIR)
        return

    for entry in sorted(_PLUGINS_DIR.iterdir()):
        if not entry.is_dir():
            continue
        plugin_yaml = entry / 'plugin.yaml'
        if not plugin_yaml.exists():
            continue
        plugin_id = entry.name
        try:
            spec = PluginSpec(plugin_id=plugin_id, plugin_dir=entry)
            _registry[plugin_id] = spec
            LOG.info('[PluginLoader] loaded plugin: %s', plugin_id)
        except Exception as exc:
            LOG.error('[PluginLoader] failed to load plugin %s: %s', plugin_id, exc)


def get_plugin(plugin_id: str) -> Optional[PluginSpec]:
    return _registry.get(plugin_id)


def list_plugins() -> List[Dict[str, Any]]:
    """Return summary info for all loaded plugins."""
    out = []
    for spec in _registry.values():
        steps = [
            {'id': s.get('id', ''), 'label': s.get('label', '')}
            for s in spec.yaml.get('steps', [])
        ]
        out.append({
            'id': spec.plugin_id,
            'name': spec.yaml.get('name', spec.plugin_id),
            'description': spec.yaml.get('description', ''),
            'steps': steps,
            'ui': spec.yaml.get('ui', {}),
            'i18n': spec.yaml.get('i18n', {}),
        })
    return out


def get_plugin_with_i18n(plugin_id: str, lang: str = '') -> Optional[Dict[str, Any]]:
    """Return full plugin spec with labels resolved for lang.

    When lang is supplied (e.g. 'zh-CN'), labels for tabs and slots are
    overwritten with the i18n values if available.  Falls back to the
    static labels in plugin.yaml when a translation is absent.
    """
    spec = get_plugin(plugin_id)
    if not spec:
        return None

    raw: Dict[str, Any] = {
        'id': spec.plugin_id,
        'name': spec.yaml.get('name', spec.plugin_id),
        'description': spec.yaml.get('description', ''),
        'when_to_use': spec.yaml.get('when_to_use', ''),
        'steps': list(spec.yaml.get('steps', [])),
        'ui': spec.yaml.get('ui', {}),
        'i18n': spec.yaml.get('i18n', {}),
    }

    if not lang:
        return raw

    # Apply i18n overrides to a deep copy so the registry cache is untouched.
    import copy
    raw = copy.deepcopy(raw)

    name_i18n = spec.get_i18n_label(lang, 'name', '')
    if name_i18n:
        raw['name'] = name_i18n

    for step in raw.get('steps', []):
        step_id = step.get('id', '')
        label_i18n = spec.get_i18n_label(lang, f'steps.{step_id}', '')
        if label_i18n:
            step['label'] = label_i18n

    ui = raw.get('ui') or {}
    for tab in ui.get('tabs', []):
        tab_id = tab.get('id', '')
        label_i18n = spec.get_i18n_label(lang, f'tabs.{tab_id}', '')
        if label_i18n:
            tab['label'] = label_i18n
        for slot in tab.get('slots', []):
            slot_id = slot.get('id', '')
            label_i18n = spec.get_i18n_label(lang, f'slots.{slot_id}', '')
            if label_i18n:
                slot['label'] = label_i18n

    return raw


def get_state_machine(plugin_id: str) -> Optional[StateMachine]:
    spec = get_plugin(plugin_id)
    return spec.state_machine if spec else None


def get_step_config(plugin_id: str, step_id: str) -> Dict[str, Any]:
    spec = get_plugin(plugin_id)
    return spec.get_step_config(step_id) if spec else {}


def get_scenario(plugin_id: str) -> str:
    spec = get_plugin(plugin_id)
    return spec.scenario_md if spec else ''


def get_plugin_intro(plugin_id: str) -> str:
    """Return a short intro (id + description + when_to_use) for cold-start injection.

    Only the trigger-relevant fields are included so the full scenario.md is not
    leaked into the system prompt before the plugin is activated.
    """
    spec = get_plugin(plugin_id)
    if not spec:
        return ''
    plugin_id_val = spec.plugin_id
    description = (spec.yaml.get('description') or '').strip()
    when_to_use = (spec.yaml.get('when_to_use') or '').strip()
    lines = [f'## Plugin: {plugin_id_val}']
    if description:
        lines.append(description)
    if when_to_use:
        lines.append(f'When to use: {when_to_use}')
    return '\n'.join(lines)


def get_driver(plugin_id: str) -> Optional[str]:
    spec = get_plugin(plugin_id)
    return spec.driver_md if spec else None


def get_plugin_yaml(plugin_id: str) -> Dict[str, Any]:
    spec = get_plugin(plugin_id)
    return spec.yaml if spec else {}


def find_producer_step(plugin_id: str, artifact_id: str) -> Optional[str]:
    """Return the step_id that produces artifact_id, or None."""
    spec = get_plugin(plugin_id)
    if not spec:
        return None
    for step_id, step_cfg in spec._steps.items():
        for out in step_cfg.get('outputs', []):
            if out.get('artifact_id') == artifact_id:
                return step_id
    return None


def get_script_tool(plugin_id: str, tool_name: str) -> Optional[Callable]:
    """Return a plugin script tool callable by name, or None."""
    spec = get_plugin(plugin_id)
    return spec.get_script_tool(tool_name) if spec else None


def list_script_tool_names(plugin_id: str) -> List[str]:
    """Return names of all script tools registered for a plugin."""
    spec = get_plugin(plugin_id)
    return spec.list_script_tool_names() if spec else []


# Auto-load on import.
load_all()
