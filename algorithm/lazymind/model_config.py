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
    'stt': 'multimodal',
    'tts': 'multimodal',
    'text2image': 'multimodal',
    'image_editing': 'multimodal',
    'text2video': 'multimodal',
}


def _role_entry(entries: Any) -> Optional[Dict[str, Any]]:
    '''Normalize a role block from runtime_models yaml (dict or single-item list).'''
    if isinstance(entries, list):
        return entries[0] if entries else None
    return entries if isinstance(entries, dict) else None


def is_model_role_available(role: str, *, config_path: Optional[str] = None) -> bool:
    '''Return whether a model role is configured and injectable for the current request.

    Static roles (source != dynamic) are available when declared in runtime_models.
    Dynamic roles additionally require inject_model_config to have supplied that role.
    '''
    entry = _role_entry(load_model_config(config_path or get_config_path()).get(role))
    if not entry:
        return False
    if (entry.get('source') or '').lower() != 'dynamic':
        return True
    import lazyllm
    dynamic_cfg = lazyllm.globals['config'].get('dynamic_model_configs') or {}
    buckets = dynamic_cfg.get(role) or {}
    return any(
        (v.get('source') or v.get('model') or v.get('url'))
        for v in buckets.values()
        if isinstance(v, dict)
    )


def get_config_path() -> str:
    '''Return the active runtime_models config file path as a string.

    Controlled entirely by LAZYMIND_MODEL_CONFIG_PATH.  Shorthand aliases
    are accepted in addition to an explicit file path:

        inner    → runtime_models.inner.yaml   (intranet / on-prem deployment)
        local    → runtime_models.local.yaml   (gitignored machine override)
        online   → runtime_models.online.yaml  (public cloud API deployment)
        dynamic  → runtime_models.yaml         (fully dynamic, key injected per request)

    If inner is selected and a sibling runtime_models.local.yaml exists, that
    file is used instead so local tunnel overrides stay out of git.

    Alias resolution is handled by algorithm/config.py (Config alias mechanism),
    so config['model_config_path'] always contains the resolved absolute path.
    '''
    from lazymind.config import apply_local_model_config_override, config as _cfg
    return apply_local_model_config_override(_cfg['model_config_path'])


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
    for role, entries in raw.items():
        entry = _role_entry(entries)
        if not entry or (entry.get('source') or '').lower() != 'dynamic':
            continue
        role_type = (entry.get('type') or 'llm').lower()
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


# Maps frontend model_key values to runtime_models.yaml role names.
_MODEL_CONFIG_ROLE_ALIASES: Dict[str, str] = {
    'stt': 'speech_to_text',
    'text2image': 'image_generator',
    'image_editing': 'image_editor',
    'text2video': 'video_generator',
}


def _normalize_model_config(model_config: Optional[Dict[str, Any]]) -> Optional[Dict[str, Any]]:
    if not model_config:
        return model_config
    normalized: Dict[str, Any] = {}
    for role, role_cfg in model_config.items():
        target = _MODEL_CONFIG_ROLE_ALIASES.get(role, role)
        if target in normalized:
            continue
        normalized[target] = role_cfg
    return _mirror_image_roles(normalized)


def _clone_role_cfg_with_type(role_cfg: Any, role_type: str) -> Any:
    if not isinstance(role_cfg, dict):
        return role_cfg
    mirrored = dict(role_cfg)
    mirrored['type'] = role_type
    return mirrored


def _mirror_image_roles(model_config: Dict[str, Any]) -> Dict[str, Any]:
    '''Unidirectional mirror: editable image_editor also fills image_generator.

    - image_editor only → also inject image_generator (same model, type=text2image)
    - image_generator only → keep generator only (do not invent an editor)
    - both present → leave each role as configured
    '''
    normalized = dict(model_config)
    has_generator = 'image_generator' in normalized
    has_editor = 'image_editor' in normalized
    if has_editor and not has_generator:
        normalized['image_generator'] = _clone_role_cfg_with_type(
            normalized['image_editor'],
            'text2image',
        )
    return normalized


def _enrich_role_types(model_config: Dict[str, Any]) -> Dict[str, Any]:
    yaml_cfg = load_model_config()
    enriched: Dict[str, Any] = {}
    for role, role_cfg in model_config.items():
        if not isinstance(role_cfg, dict):
            enriched[role] = role_cfg
            continue
        merged = dict(role_cfg)
        entry = _role_entry(yaml_cfg.get(role))
        if entry:
            if not merged.get('type'):
                merged['type'] = entry.get('type')
            yaml_tokens = entry.get('max_input_tokens')
            if yaml_tokens is not None and 'max_input_tokens' not in merged:
                merged['max_input_tokens'] = yaml_tokens
        enriched[role] = merged
    return enriched


def inject_model_config(model_config: Optional[Dict[str, Any]]) -> None:
    '''Inject per-request model configuration into lazyllm globals.

    Delegates to lazyllm.module.llms.model_config_inject. Kept here for
    backward compatibility and Lazymind-specific role alias normalization.
    '''
    from lazyllm.module.llms.model_config_inject import inject_model_config as _lazyllm_inject
    normalized = _normalize_model_config(model_config)
    if isinstance(normalized, dict):
        normalized = _enrich_role_types(normalized)
    _lazyllm_inject(normalized)


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
