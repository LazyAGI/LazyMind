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
import hashlib
import logging
import os
import shutil
import sys
import tempfile
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
_runtime_registry: Dict[tuple[str, str, str], 'PluginSpec'] = {}


def resolve_remote_plugin(entry: Dict[str, Any]) -> tuple[str, 'PluginSpec']:
    """Materialize and cache one immutable RemoteFS plugin revision."""
    plugin_ref = str(entry.get('plugin_ref') or '').strip()
    revision_id = str(entry.get('revision_id') or '').strip()
    tree_hash = str(entry.get('tree_hash') or '').removeprefix('sha256:').strip()
    remote_root = str(entry.get('remote_root') or '').strip()
    if not all((plugin_ref, revision_id, tree_hash, remote_root)):
        raise ValueError('plugin catalog entry is missing runtime identity fields')
    key = (plugin_ref, revision_id, tree_hash)
    if key in _runtime_registry:
        spec = _runtime_registry[key]
        return spec.plugin_id, spec
    runtime_id = f'user_{hashlib.sha256(plugin_ref.encode()).hexdigest()[:12]}_{entry.get("plugin_id", "plugin")}'
    cache_root = Path(os.getenv('LAZYMIND_PLUGIN_RUNTIME_CACHE', tempfile.gettempdir())) / 'lazymind-plugin-runtime'
    cache_root.mkdir(parents=True, exist_ok=True)
    final_dir = cache_root / hashlib.sha256(plugin_ref.encode()).hexdigest()[:16] / revision_id
    if not final_dir.exists():
        tmp_dir = Path(tempfile.mkdtemp(prefix='plugin-', dir=str(cache_root)))
        try:
            from lazymind.common.integrations.remote_fs import RemoteFS
            RemoteFS().materialize_dir(remote_root, str(tmp_dir), revision_id=revision_id)
            rows = []
            for file_path in sorted(p for p in tmp_dir.rglob('*') if p.is_file()):
                rel = file_path.relative_to(tmp_dir).as_posix()
                rows.append(f'{rel}\0file\0{hashlib.sha256(file_path.read_bytes()).hexdigest()}')
            actual = hashlib.sha256('\n'.join(rows).encode()).hexdigest()
            if actual != tree_hash:
                raise ValueError(f'plugin tree hash mismatch: expected {tree_hash}, got {actual}')
            final_dir.parent.mkdir(parents=True, exist_ok=True)
            try:
                tmp_dir.rename(final_dir)
            except FileExistsError:
                shutil.rmtree(tmp_dir, ignore_errors=True)
        except Exception:
            shutil.rmtree(tmp_dir, ignore_errors=True)
            raise
    spec = PluginSpec(plugin_id=runtime_id, plugin_dir=final_dir)
    _runtime_registry[key] = spec
    _registry[runtime_id] = spec
    return runtime_id, spec


def _normalise_steps(raw_steps: Any) -> Dict[str, Dict[str, Any]]:
    """Return state.yml steps keyed by id for both supported YAML shapes.

    Older/built-in plugins use a mapping (``steps: {step_id: {...}}``), while
    the visual editor serialises a list (``steps: [{id: step_id, ...}]``).
    Runtime code consumes one canonical mapping so metadata such as ``mode`` is
    never lost merely because the plugin was saved by the editor.
    """
    def _normalise_config(step_id: str, config: Dict[str, Any]) -> Dict[str, Any]:
        normalised = {'id': step_id, **config}
        for field in ('inputs', 'outputs'):
            refs = normalised.get(field)
            if not isinstance(refs, list):
                continue
            normalised_refs: List[Dict[str, Any]] = []
            for ref in refs:
                if isinstance(ref, str) and ref.strip():
                    normalised_refs.append({'slot': ref.strip()})
                elif isinstance(ref, dict):
                    slot = str(ref.get('slot') or ref.get('artifact_id') or '').strip()
                    if slot:
                        normalised_refs.append({'slot': slot, **ref})
            normalised[field] = normalised_refs
        return normalised

    if isinstance(raw_steps, dict):
        result: Dict[str, Dict[str, Any]] = {}
        for step_id, config in raw_steps.items():
            if not isinstance(step_id, str) or not isinstance(config, dict):
                continue
            result[step_id] = _normalise_config(step_id, config)
        return result
    if isinstance(raw_steps, list):
        result = {}
        for config in raw_steps:
            if not isinstance(config, dict):
                continue
            step_id = str(config.get('id') or '').strip()
            if step_id:
                result[step_id] = _normalise_config(step_id, config)
        return result
    return {}


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

        # Normalise editor (list) and legacy (mapping) step shapes before any
        # runtime consumer reads step metadata.
        self._steps: Dict[str, Dict[str, Any]] = _normalise_steps(self.state.get('steps', {}))

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

        required_framework_tools = self.yaml.get('required_framework_tools') or []
        if required_framework_tools:
            from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS, tool_is_active
            by_name = {cfg.name: cfg for cfg in DEFAULT_TOOLS}
            unavailable = [
                name for name in required_framework_tools
                if name not in by_name or not tool_is_active(by_name[name])
            ]
            if unavailable:
                raise ValueError(f'plugin requires unavailable framework tools: {unavailable}')

        # If driver.md is missing, we emit a warning but don't hard-fail load.
        # auto mode will be silently degraded to manual at runtime if driver.md absent.
        if not self.driver_md:
            LOG.warning(
                '[PluginLoader] plugin=%s has no driver.md; auto mode will be disabled',
                self.plugin_id,
            )

    def get_step_config(self, step_id: str) -> Dict[str, Any]:
        return dict(self._steps.get(step_id, {}))

    def get_step_mode(self, step_id: str) -> str:
        """Return the step's default approval mode; legacy omissions are human."""
        return 'auto' if self._steps.get(step_id, {}).get('mode') == 'auto' else 'human'

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


def get_step_config(plugin_id: str, step_id: str) -> Dict[str, Any]:
    spec = get_plugin(plugin_id)
    return spec.get_step_config(step_id) if spec else {}


def get_step_mode(plugin_id: str, step_id: str) -> str:
    """Return ``auto`` or ``human`` for the step's default approval policy."""
    spec = get_plugin(plugin_id)
    return spec.get_step_mode(step_id) if spec else 'human'


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
    lines = [f'## Workflow: {plugin_id_val}']
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
