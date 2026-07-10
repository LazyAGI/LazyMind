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


def _join_conditions(c1: str, c2: str) -> str:
    """Combine two natural-language conditions with AND.

    Returns the non-empty side when one is empty, or 'c1 AND c2' when both present.
    Pure string concatenation — no LLM involved.
    """
    c1, c2 = c1.strip(), c2.strip()
    if not c1:
        return c2
    if not c2:
        return c1
    return f'{c1} AND {c2}'


class StateMachine:
    """Minimal state machine parsed from state.yml transitions block.

    Supports extended control-flow fields on each step:
      route: 'all' | 'choice'  — how to follow outgoing transitions.
        'all' (default): all matching exits are triggered simultaneously (parallel).
        'choice': only the first matching exit is taken (conditional routing).
      skipif: str  — natural-language condition under which this step is skipped entirely.
        When set, the step is treated as having an implicit bypass transition that the
        LLM can evaluate; get_reachable_steps returns it as optional.
    """

    _RESERVED = {'__start__', '__end__'}

    def __init__(
        self,
        initial: str,
        transitions: Dict[str, List[Dict[str, Any]]],
        steps: Optional[Dict[str, Any]] = None,
    ) -> None:
        self.initial = initial
        self._raw_transitions: Dict[str, List[Dict[str, Any]]] = {}
        for src, edges in transitions.items():
            valid = [e for e in edges if isinstance(e, dict) and 'to' in e]
            self._raw_transitions[src] = valid
        self._transitions: Dict[str, List[str]] = {
            src: [e['to'] for e in edges]
            for src, edges in self._raw_transitions.items()
        }
        # Per-step route and skipif metadata, keyed by step id.
        steps_raw: Dict[str, Any] = steps or {}
        self._step_ids: set[str] = {
            step_id
            for step_id, step_cfg in steps_raw.items()
            if isinstance(step_id, str) and isinstance(step_cfg, dict)
        }
        self._route: Dict[str, str] = {}
        self._skipif: Dict[str, str] = {}
        for step_id, step_cfg in steps_raw.items():
            if not isinstance(step_cfg, dict):
                continue
            if step_cfg.get('route') in ('all', 'choice'):
                self._route[step_id] = step_cfg['route']
            if step_cfg.get('skipif') and isinstance(step_cfg['skipif'], str):
                self._skipif[step_id] = step_cfg['skipif']

        # Build expanded transitions: skipif on successors are inlined as bypass conditions.
        self._expanded_transitions: Dict[str, List[Dict[str, Any]]] = {}
        self._expand_skipif_transitions()

    # ------------------------------------------------------------------
    # skipif expansion
    # ------------------------------------------------------------------

    def _expand_skipif_transitions(self) -> None:
        """Populate _expanded_transitions by inlining skipif as bypass conditions.

        For each source node, in addition to its direct successors, we also emit
        bypass edges that skip over any successor with a skipif condition.  This
        lets the LLM see all reachable targets (and the conditions required) in a
        single flat list, without needing to reason about the skipif chain itself.

        Example: A -> B(skipif=c1) -> C(skipif=c2) -> D
          A's expanded exits: B (no extra cond), C (cond=c1), D (cond=c1 AND c2)
          B's expanded exits: C (no extra cond), D (cond=c2)
          C's expanded exits: D (no extra cond)
        """
        for src in self._raw_transitions:
            self._expanded_transitions[src] = list(self._expand_from(src, frozenset()))

    def _expand_from(self, src: str, visited: frozenset) -> List[Dict[str, Any]]:
        """Yield expanded {to, condition} edges reachable from src.

        visited prevents re-entering the same node during recursive bypass traversal,
        guarding against cycles in the graph.
        """
        for edge in self._raw_transitions.get(src, []):
            tgt = edge['to']
            base_cond = edge.get('condition', '')
            yield {'to': tgt, 'condition': base_cond}
            # Only expand bypass if the target has a skipif and we haven't visited it.
            if tgt in self._RESERVED or tgt in visited:
                continue
            skipif = self._skipif.get(tgt)
            if skipif:
                new_visited = visited | {src}
                for bypass in self._expand_from(tgt, new_visited):
                    yield {
                        'to': bypass['to'],
                        'condition': _join_conditions(skipif, bypass['condition']),
                    }

    def get_route(self, step_id: str) -> str:
        """Return 'all' or 'choice' for this step (default: 'all')."""
        return self._route.get(step_id, 'all')

    def get_skipif(self, step_id: str) -> Optional[str]:
        """Return the skipif condition string, or None if not set."""
        return self._skipif.get(step_id)

    def get_expanded_transitions(self, step_id: str) -> List[Dict[str, Any]]:
        """Return expanded {to, condition} edges for step_id.

        Includes bypass edges generated from skipif on successors.
        Each item: {'to': str, 'condition': str}.
        """
        return list(self._expanded_transitions.get(step_id or '__start__', []))

    def get_reachable_steps(self, current_step: str) -> List[str]:
        """Return step IDs reachable from current_step (excluding reserved states).

        Uses the expanded transitions so that steps reachable via skipif bypass
        are included alongside normal successors.  For each reachable target the
        LLM can read the associated condition (via get_expanded_transitions) to
        decide whether to advance directly or skip.
        """
        edges = self._expanded_transitions.get(current_step or '__start__', [])
        seen: List[str] = []
        visited: set = set()
        for e in edges:
            tgt = e['to']
            if tgt not in self._RESERVED and tgt in self._step_ids and tgt not in visited:
                visited.add(tgt)
                seen.append(tgt)
        return seen

    def is_reachable(self, current_step: str, target_step: str) -> bool:
        """Return True if target_step is directly reachable from current_step.

        A step is always reachable from itself (retry semantics).
        """
        if target_step == current_step and target_step in self._step_ids:
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
            self.plugin_yaml_raw: str = f.read()
        self.yaml: Dict[str, Any] = yaml.safe_load(self.plugin_yaml_raw) or {}

        # Load scenario files
        scenario_dir = plugin_dir / 'scenario'
        self.scenario_md: str = self._read_text(scenario_dir / 'scenario.md')
        state_path = scenario_dir / 'state.yml'
        with state_path.open('r', encoding='utf-8') as f:
            state_text = f.read()
        self.state_yaml_raw: str = state_text
        self.state: Dict[str, Any] = yaml.safe_load(state_text) or {}
        self.driver_md: Optional[str] = self._read_text(scenario_dir / 'driver.md', optional=True)

        # Build state machine
        self.state_machine = StateMachine(
            initial=str(self.state.get('initial', '__start__')),
            transitions=self.state.get('transitions', {}),
            steps=self.state.get('steps'),
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
        """Find a slot definition by slot_id from the top-level slots list."""
        for slot in self.yaml.get('slots', []):
            if slot.get('id') == slot_id:
                return dict(slot)
        return None

    def get_slot(self, slot: str) -> Optional[Dict[str, Any]]:
        """Return the slot definition (id, type, cardinality, ordered …) for a slot id."""
        for s in self.yaml.get('slots', []):
            if s.get('id') == slot:
                return dict(s)
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

    import copy

    # Build a slot lookup from the top-level slots[] definition.
    top_slots: Dict[str, Dict[str, Any]] = {
        s['id']: s for s in spec.yaml.get('slots', []) if s.get('id')
    }

    # Expand ui.tabs[].slots[] from id-only references to full slot defs.
    # Each tab slot entry is merged: top-level slot attrs first, then any
    # tab-local overrides (currently only 'id' is present, but kept for
    # forward-compatibility).
    raw_ui = copy.deepcopy(spec.yaml.get('ui', {}))
    for tab in raw_ui.get('tabs', []):
        expanded = []
        for slot_ref in tab.get('slots', []):
            slot_id = slot_ref.get('id', '')
            base = dict(top_slots.get(slot_id, {}))
            base.update(slot_ref)  # tab-local keys win if ever added
            expanded.append(base)
        tab['slots'] = expanded

    raw: Dict[str, Any] = {
        'id': spec.plugin_id,
        'name': spec.yaml.get('name', spec.plugin_id),
        'description': spec.yaml.get('description', ''),
        'when_to_use': spec.yaml.get('when_to_use', ''),
        'steps': list(spec.yaml.get('steps', [])),
        'ui': raw_ui,
        'i18n': spec.yaml.get('i18n', {}),
    }

    if not lang:
        return raw

    # Apply i18n overrides to a deep copy so the registry cache is untouched.
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


def find_producer_steps(plugin_id: str, slot: str) -> List[str]:
    """Return all step_ids that can produce slot, preserving state.yml order."""
    spec = get_plugin(plugin_id)
    if not spec:
        return []
    producers: List[str] = []
    for step_id, step_cfg in spec._steps.items():
        for out in step_cfg.get('outputs', []):
            if out.get('slot') == slot:
                producers.append(step_id)
                break
    return producers


def find_producer_step(plugin_id: str, slot: str) -> Optional[str]:
    """Return one step_id that produces slot, or None."""
    producers = find_producer_steps(plugin_id, slot)
    return producers[0] if producers else None


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
