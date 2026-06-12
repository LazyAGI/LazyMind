from __future__ import annotations

import os
import sys
from pathlib import Path
from typing import Any

_ROLE_DEFAULTS = {'dynamic': 'online'}


def load_core_model_config() -> dict[str, Any]:
    """Return LazyMind runtime model config for per-request dynamic injection."""
    _ensure_lazymind_runtime()
    from lazymind.model_config import get_config_path, load_model_config

    path = Path(get_config_path())
    raw = load_model_config(str(path), expand_env=True)
    if any(isinstance(cfg, dict) and cfg.get('source') == 'dynamic' for cfg in raw.values()):
        raw = load_model_config(str(path.with_name(f'runtime_models.{_ROLE_DEFAULTS["dynamic"]}.yaml')),
                                expand_env=True)
    return {role: cfg for role, cfg in ((role, _role_config(role, entries)) for role, entries in raw.items()) if cfg}


def activate_model_config(model_config: dict[str, Any] | None, *, session_id: str | None = None) -> bool:
    if not model_config: return False
    _ensure_lazymind_runtime()
    import lazyllm
    from lazymind.model_config import inject_model_config

    if session_id is not None:
        lazyllm.globals._init_sid(sid=session_id)
        lazyllm.locals._init_sid(sid=session_id)
    inject_model_config(model_config)
    return True


def evo_llm(model_config: dict[str, Any] | None = None):
    _ensure_lazymind_runtime()
    from lazyllm import AutoModel

    role = os.getenv('LAZYMIND_EVO_LLM_ROLE') or 'evo_llm'
    config = model_config or load_core_model_config()
    activate_model_config(config)
    module = AutoModel(source='dynamic', dynamic_auth=True, type=_role_type(config.get(role)), name=role) \
        if _dynamic_role_slot(role) else AutoModel(model=role)
    return _ConfiguredRoleModel(role, config, module)


class _ConfiguredRoleModel:
    def __init__(self, role: str, model_config: dict[str, Any], module: Any):
        self.role = role
        self.model_config = model_config
        self.module = module

    def __call__(self, *args: Any, **kwargs: Any):
        activate_model_config(self.model_config)
        return self.module(*args, **kwargs)

    def __getattr__(self, name: str) -> Any:
        return getattr(self.module, name)


def _ensure_lazymind_runtime() -> None:
    root = _algorithm_root()
    if root is None: return
    desired = root / 'lazyllm' / 'lazyllm'
    for finder in list(sys.meta_path):
        known = getattr(finder, 'known_source_files', None)
        if isinstance(known, dict) and str(known.get('lazyllm') or '').startswith(str(root.parents[1] / 'LazyLLM')):
            sys.meta_path.remove(finder)
    loaded = getattr(sys.modules.get('lazyllm'), '__file__', '')
    if loaded and not str(loaded).startswith(str(desired)):
        for name in [name for name in sys.modules if name == 'lazyllm' or name.startswith('lazyllm.')]:
            sys.modules.pop(name, None)
    for path in (root / 'lazyllm', root):
        if path.exists() and str(path) not in sys.path:
            sys.path.insert(0, str(path))


def _algorithm_root() -> Path | None:
    local = Path(__file__).resolve().parents[3] / 'LazyRAG' / 'algorithm'
    for root in (local, Path('/app/algorithm')):
        if (root / 'lazymind').exists() and (root / 'lazyllm' / 'lazyllm').exists(): return root
    return None


def _dynamic_role_slot(role: str) -> str:
    from lazymind.model_config import get_dynamic_role_slot_map

    return get_dynamic_role_slot_map().get(role, '')


def _role_type(role_config: Any) -> str:
    value = role_config.get('type') if isinstance(role_config, dict) else ''
    return str(value or 'llm')


def _role_config(role: str, entries: Any) -> dict[str, Any]:
    entry = entries[0] if isinstance(entries, list) and entries else entries
    if not isinstance(entry, dict): return {}
    source = str(entry.get('source') or '').strip().lower()
    cfg = {
        'source': source,
        'model': entry.get('model') or entry.get('name'),
        'base_url': entry.get('base_url') or entry.get('url'),
        'type': entry.get('type'),
        'skip_auth': entry.get('skip_auth'),
        'api_key': entry.get('api_key') or _source_api_key(source),
    }
    return {key: value for key, value in cfg.items() if value not in (None, '')}


def _source_api_key(source: str) -> str:
    key = source.upper().replace('-', '_')
    return os.getenv(f'LAZYLLM_{key}_API_KEY') or os.getenv(f'{key}_API_KEY') or ''
