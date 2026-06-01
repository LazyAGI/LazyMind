from __future__ import annotations

import logging
from pathlib import Path
from typing import Any

from evo.runtime.fs import atomic_write_json
from evo.service.core import state as thread_state
from evo.service.core.errors import StateError

log = logging.getLogger('evo.runtime.model_config')
MODEL_CONFIG_FILENAME = 'model_config.json'


def extract_model_config(payload: dict | None) -> dict[str, Any] | None:
    cfg = (payload or {}).get('llm_config')
    return cfg if isinstance(cfg, dict) and cfg else None


def thread_model_config(base_dir: Path | str, thread_id: str | None) -> dict[str, Any] | None:
    if not thread_id:
        return None
    cfg = thread_state.read_json(_thread_dir(base_dir, thread_id) / MODEL_CONFIG_FILENAME)
    return cfg if isinstance(cfg, dict) and cfg else None


def save_thread_model_config(base_dir: Path | str, thread_id: str, model_config: dict) -> None:
    atomic_write_json(_thread_dir(base_dir, thread_id) / MODEL_CONFIG_FILENAME, model_config)


def require_evo_llm(model_config: dict[str, Any] | None, role: str = 'evo_llm') -> dict[str, Any]:
    evo_llm = (model_config or {}).get(role)
    data = evo_llm if isinstance(evo_llm, dict) else {}
    missing = [k for k in ('source', 'model', 'api_key') if not str(data.get(k) or '').strip()]
    if not isinstance(evo_llm, dict) or missing:
        fields = ', '.join(missing or ('source', 'model', 'api_key'))
        raise StateError(
            'EVO_LLM_CONFIG_MISSING', f'llm_config.{role} missing required fields: {fields}', kind='permanent'
        )
    return evo_llm


def require_thread_model_config(base_dir: Path | str, thread_id: str | None, role: str = 'evo_llm') -> dict[str, Any]:
    cfg = thread_model_config(base_dir, thread_id)
    require_evo_llm(cfg, role)
    return cfg or {}


def _thread_dir(base_dir: Path | str, thread_id: str) -> Path:
    return Path(base_dir) / 'state' / 'threads' / thread_id


def activate_model_config(model_config: dict[str, Any] | None, *, session_id: str) -> bool:
    if not model_config:
        return False

    import lazyllm
    from algorithm.chat.utils.load_config import inject_model_config, summarize_model_config_for_log

    lazyllm.globals._init_sid(sid=session_id)
    lazyllm.locals._init_sid(sid=session_id)
    inject_model_config(model_config)
    log.info(
        '[Evo] [MODEL_CONFIG_INJECTED] [sid=%s] [%s]',
        session_id,
        summarize_model_config_for_log(model_config),
    )
    return True


def activate_thread_model_config(base_dir: Path | str, thread_id: str | None, *, session_id: str) -> bool:
    return activate_model_config(thread_model_config(base_dir, thread_id), session_id=session_id)


def wrap_model_call(producer, model_config: dict[str, Any] | None, *, session_id: str):
    def wrapped():
        activate_model_config(model_config, session_id=session_id)
        return producer()

    return wrapped
