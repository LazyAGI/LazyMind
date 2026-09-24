from __future__ import annotations

from collections.abc import Mapping

from lazyllm import globals as lazyllm_globals
from lazyllm.tools import get_dynamic_env_vars, inject_env_vars


def inject_runtime_env(
    user_env_vars: Mapping[str, str] | None,
    conversation_env_vars: Mapping[str, str] | None = None,
) -> None:
    """Replace an execution snapshot: conversation > user > process environment."""
    overrides = dict(conversation_env_vars or {})
    defaults = dict(user_env_vars or {})
    merged = {**defaults, **overrides}
    lazyllm_globals['conversation_env_overrides'] = overrides
    lazyllm_globals['user_env_defaults'] = defaults
    inject_env_vars({**dict.fromkeys(get_dynamic_env_vars(), ''), **merged})


def remove_conversation_override(name: str) -> str:
    overrides = dict(lazyllm_globals.get('conversation_env_overrides') or {})
    overrides.pop(name, None)
    defaults = lazyllm_globals.get('user_env_defaults') or {}
    inject_runtime_env(defaults, overrides)
    return 'user' if name in defaults else 'process_or_unset'
