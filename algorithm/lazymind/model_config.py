import os
import re
from functools import lru_cache
from pathlib import Path
from typing import Any, Dict, Optional

import yaml
from lazyllm.tools.agent.skill_manager import SkillManager as LazySkillManager

# Maps runtime_models.yaml type values to _dynamic_module_slot names used by
# _DynamicSourceRouterMixin subclasses (OnlineChatModule / OnlineEmbeddingModule).
_TYPE_TO_SLOT: Dict[str, str] = {
    'llm': 'chat',
    'chat': 'chat',
    'vlm': 'chat',
    'embed': 'embed',
    'rerank': 'embed',
    'cross_modal_embed': 'embed',
}


def get_config_path() -> str:
    '''Return the active runtime_models config file path as a string.

    Controlled entirely by LAZYMIND_MODEL_CONFIG_PATH.  Three shorthand aliases
    are accepted in addition to an explicit file path:

        inner    → runtime_models.inner.yaml   (intranet / on-prem deployment)
        online   → runtime_models.online.yaml  (public cloud API deployment)
        dynamic  → runtime_models.yaml         (fully dynamic, key injected per request)

    Alias resolution is handled by algorithm/config.py (Config alias mechanism),
    so config['model_config_path'] always contains the resolved absolute path.
    '''
    from lazymind.config import config as _cfg
    return _cfg['model_config_path']


def load_model_config(config_path: str | None = None, *, expand_env: bool = False) -> Dict[str, Any]:
    '''Load and return the raw model config dict (yaml parsed).

    When config_path is None, falls back to the path resolved by get_config_path()
    (controlled by LAZYMIND_MODEL_CONFIG_PATH).

    If expand_env is True, environment variables in ${VAR} or $VAR format
    are replaced with their values from os.environ.
    '''
    with Path(config_path or get_config_path()).open(encoding='utf-8') as f:
        raw = yaml.safe_load(f) or {}
    if expand_env:
        raw = _deep_expand_env(raw)
    return raw


def extract_skill_fs_source(path: Any) -> str:
    raw = str(path or '').strip()
    if not raw:
        return 'file'
    protocol = LazySkillManager._extract_protocol(raw)
    if protocol == 'remote':
        return 'remote'
    return protocol or 'file'


@lru_cache(maxsize=1)
def get_dynamic_role_slot_map(config_path: Optional[str] = None) -> Dict[str, str]:
    '''Return a mapping of {role_name: slot} for all roles with source=dynamic.

    slot is the _dynamic_module_slot value used by the corresponding online module
    class ('chat' for OnlineChatModule, 'embed' for OnlineEmbeddingModule).

    Example result for the default runtime_models.yaml:
        {
            'llm':        'chat',
            'reranker':   'embed',
            'embed_main': 'embed',
        }

    When config_path is None, reads from get_config_path() (LAZYMIND_MODEL_CONFIG_PATH).
    '''
    raw = load_model_config(config_path or get_config_path())
    result: Dict[str, str] = {}
    for role, cfg in raw.items():
        if not isinstance(cfg, dict):
            continue
        if (cfg.get('source') or '').lower() != 'dynamic':
            continue
        role_type = (cfg.get('type') or 'llm').lower()
        slot = _TYPE_TO_SLOT.get(role_type, 'chat')
        result[role] = slot
    return result


def coerce_bool(value: Any) -> Optional[bool]:
    '''Normalize a value to bool, handling string representations from HTTP JSON.

    JSON booleans deserialize correctly (true -> True), but if the client sends
    a string (e.g. "true", "false", "1", "0") we handle that too.
    Returns None when value is None so callers can distinguish "not provided".
    '''
    if value is None: return None
    if isinstance(value, int): return bool(value)  # bool is subclass of int
    if isinstance(value, str): return value.strip().lower() not in ('false', '0', 'no', '')
    return bool(value)


def _api_key_state(value: Any) -> str:
    return 'set' if value else 'empty'


def summarize_model_config_for_log(model_config: Optional[Dict[str, Any]]) -> str:
    '''Return a deterministic, API-key-safe summary for model_config logs.'''
    if not model_config:
        return 'roles=[]'

    parts = []
    for role in sorted(str(k) for k in model_config.keys()):
        role_cfg = model_config.get(role)
        if not isinstance(role_cfg, dict):
            parts.append(f'{role}(type={type(role_cfg).__name__})')
            continue
        fields = [
            f'source={role_cfg.get("source", "")}',
            f'model={role_cfg.get("model", "")}',
            f'base_url={role_cfg.get("base_url", "")}',
            f'api_key={_api_key_state(role_cfg.get("api_key"))}',
        ]
        if 'skip_auth' in role_cfg:
            fields.append(f'skip_auth={coerce_bool(role_cfg.get("skip_auth"))}')
        parts.append(f'{role}(' + ', '.join(fields) + ')')
    return f'roles={sorted(str(k) for k in model_config.keys())} ' + '; '.join(parts)


def inject_model_config(model_config: Optional[Dict[str, Any]]) -> None:
    '''Inject per-request model configuration into lazyllm globals.

    Delegates to lazyllm.inject_model_config. Kept here for backward compatibility.
    '''
    import lazyllm
    lazyllm.inject_model_config(model_config)


def _expand_env(value: str) -> str:
    """Expand ${VAR} and $VAR patterns in a string using environment variables."""
    def replacer(m: re.Match) -> str:
        var = m.group(1) or m.group(2)
        return os.environ.get(var, m.group(0))
    return re.sub(r'\$\{(\w+)\}|\$(\w+)', replacer, value)


def _deep_expand_env(obj: Any) -> Any:
    """Recursively expand environment variables in a dict/list/string structure."""
    if isinstance(obj, dict):
        return {k: _deep_expand_env(v) for k, v in obj.items()}
    if isinstance(obj, list):
        return [_deep_expand_env(v) for v in obj]
    if isinstance(obj, str):
        return _expand_env(obj)
    return obj
