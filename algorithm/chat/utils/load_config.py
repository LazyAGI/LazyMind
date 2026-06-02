import os
import re
from functools import lru_cache
from pathlib import Path
from typing import Any, Dict, List, NamedTuple, Optional

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

# Prefix convention for embed-type roles in the flat yaml format.
# Any top-level key starting with this prefix is treated as an embed role.
_EMBED_KEY_PREFIX = 'embed_'
_EMBED_TYPES = {'embed', 'cross_modal_embed'}
_IMAGE_EMBED_TYPES = {'cross_modal_embed'}


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
    from config import config as _cfg
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


def _make_bucket(cfg: Dict[str, Any]) -> Dict[str, Any]:
    '''Extract the fields that _DynamicSourceRouterMixin understands from a config dict.

    Note: api_key is intentionally excluded here.  It is stored separately in
    globals.config['{source}_api_key'] (a ConfigsDict keyed by role name) so
    that _default_api_key() can retrieve it dynamically via the stack lookup
    mechanism in _GlobalConfig.__getitem__.  See inject_model_config for details.
    '''
    return {k: v for k, v in {'source': cfg.get('source'), 'model': cfg.get('model'), 'url': cfg.get('base_url'),
                              'skip_auth': coerce_bool(cfg.get('skip_auth'))}.items() if v is not None}


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


@lru_cache(maxsize=1)
def get_embed_keys(config_path: Optional[str] = None) -> list:
    '''Return the list of embed-type role names defined in the active config.

    A role is considered an embed role when its first-entry ``type`` is one of
    ``embed`` / ``rerank`` / ``cross_modal_embed``. For backward compatibility,
    keys that start with ``embed_`` are also treated as embed roles.
    The order matches the yaml definition order, so the first key is always the
    primary (dense) embed.

    Returns an empty list when no embed roles are found (caller should handle
    this as a configuration error).
    '''
    raw = load_model_config(config_path)
    return [role for role, entries in raw.items() if _is_embed_role(role, entries)]


def _first_entry_type(entries: Any) -> str:
    '''Return the lower-cased ``type`` field from the first entry.

    Supports both yaml shapes:
      - static (inner/online): ``role: [{type: ...}, ...]``
      - dynamic              : ``role: {type: ...}``
    '''
    if isinstance(entries, list) and entries:
        entry = entries[0]
    elif isinstance(entries, dict):
        entry = entries
    else:
        return ''
    if not isinstance(entry, dict):
        return ''
    return (entry.get('type') or '').lower()


def _is_embed_role(role: str, entries: Any) -> bool:
    '''Return whether the role should be treated as an embed role.'''
    entry_type = _first_entry_type(entries)
    if entry_type in _EMBED_TYPES:
        return True
    return role.startswith(_EMBED_KEY_PREFIX)


@lru_cache(maxsize=1)
def get_image_embed_key(config_path: Optional[str] = None) -> Optional[str]:
    '''Return the embed role name identified as the image embed.

    A role is treated as the image embed when its first entry has
    ``type: cross_modal_embed``. For backward compatibility, it also falls
    back to ``name: siglip`` (case-insensitive) when type is not provided.
    Returns None when no such role exists, in which case callers should skip
    the image retrieval branch.
    '''
    raw = load_model_config(config_path)
    for role, entries in raw.items():
        if not _is_embed_role(role, entries): continue
        if isinstance(entries, list) and entries:
            entry = entries[0]
        elif isinstance(entries, dict):
            entry = entries
        else:
            continue
        if not isinstance(entry, dict):
            continue
        entry_type = str(entry.get('type') or '').strip().lower()
        if entry_type in _IMAGE_EMBED_TYPES:
            return role
        model_name = str(entry.get('name') or '').strip().lower()
        if model_name == 'siglip':
            return role
    return None


@lru_cache(maxsize=1)
def get_text_embed_keys(config_path: Optional[str] = None) -> list:
    '''Return embed role names excluding the cross-modal image embed.'''
    image_key = get_image_embed_key(config_path)
    return [k for k in get_embed_keys(config_path) if k != image_key]


_DEFAULT_DENSE_INDEX_KWARGS = {
    'index_type': 'IVF_FLAT',
    'metric_type': 'COSINE',
    'params': {'nlist': 128},
}

_DEFAULT_SPARSE_INDEX_KWARGS = {
    'index_type': 'SPARSE_INVERTED_INDEX',
    'metric_type': 'IP',
}


@lru_cache(maxsize=1)
def get_embed_index_kwargs(config_path: Optional[str] = None) -> list:
    '''Return a list of index_kwargs dicts (one per embed role) for the vector store.

    Each dict contains an `embed_key` field plus the Milvus index parameters.
    The index params are read from the yaml entry's `index_kwargs` field when
    present; otherwise a default is inferred from the model name:
      - names containing "sparse" → SPARSE_INVERTED_INDEX / IP
      - everything else           → IVF_FLAT / COSINE
    '''
    from copy import deepcopy
    raw = load_model_config(config_path)
    result = []
    for role, entries in raw.items():
        if not _is_embed_role(role, entries): continue
        entry = entries[0] if isinstance(entries, list) and entries else entries
        model_name = (entry.get('name') or entry.get('model') or '').lower()
        ik = deepcopy(_DEFAULT_SPARSE_INDEX_KWARGS if 'sparse' in model_name else _DEFAULT_DENSE_INDEX_KWARGS)
        ik['embed_key'] = role
        result.append(ik)
    return result


class RetrievalSettings(NamedTuple):
    embed_keys: List[str]
    file_search_embed_key: str
    temp_doc_embed_key: str
    index_kwargs: List[dict]
    retriever_configs: List[dict]


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


@lru_cache(maxsize=1)
def get_retrieval_settings(config_path: str | None = None) -> RetrievalSettings:
    embed_keys = get_embed_keys(config_path) or ['embed_main']
    index_kwargs = get_embed_index_kwargs(config_path)
    file_search = embed_keys[-1] if embed_keys else 'embed_main'
    temp_doc = embed_keys[0] if embed_keys else 'embed_main'
    retriever_configs = [
        {'group_name': 'line', 'embed_keys': embed_keys, 'topk': 20, 'target': 'block'},
        {'group_name': 'block', 'embed_keys': embed_keys, 'topk': 20},
    ]
    return RetrievalSettings(
        embed_keys=embed_keys,
        file_search_embed_key=file_search,
        temp_doc_embed_key=temp_doc,
        index_kwargs=index_kwargs,
        retriever_configs=retriever_configs,
    )
