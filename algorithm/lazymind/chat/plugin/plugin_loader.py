"""Plugin loader — discovers and validates plugin packages under plugin/plugins/.

Each plugin lives at plugin/plugins/<plugin-id>/ and must contain:
  - plugin.yaml          (required) — registration metadata
  - scenario/scenario.md (required) — ChatAgent intent-recognition guide
  - scenario/state.yml   (required) — state machine + step execution spec
  - scenario/driver.md   (optional, required for auto mode) — DriverAgent prompt

Loaded plugins are cached at import time (startup). Hot-reload is not supported.
"""
from __future__ import annotations

import logging
from pathlib import Path
from typing import Any, Dict, List, Optional

import yaml

LOG = logging.getLogger(__name__)

# Base directory for all plugin packages.
_PLUGINS_DIR = Path(__file__).parent / 'plugins'

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
        """Return True if target_step is directly reachable from current_step."""
        return target_step in self.get_reachable_steps(current_step)


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

        # Validate: auto-capable steps need driver.md
        self._validate()

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
        })
    return out


def get_state_machine(plugin_id: str) -> Optional[StateMachine]:
    spec = get_plugin(plugin_id)
    return spec.state_machine if spec else None


def get_step_config(plugin_id: str, step_id: str) -> Dict[str, Any]:
    spec = get_plugin(plugin_id)
    return spec.get_step_config(step_id) if spec else {}


def get_scenario(plugin_id: str) -> str:
    spec = get_plugin(plugin_id)
    return spec.scenario_md if spec else ''


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


# Auto-load on import.
load_all()
