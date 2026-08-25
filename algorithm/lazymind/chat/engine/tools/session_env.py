from __future__ import annotations

import re
from typing import Any, MutableMapping

from lazyllm import globals as lazyllm_globals
from lazyllm.tools import inject_env_vars


_ENV_NAME_RE = re.compile(r'^[A-Za-z_][A-Za-z0-9_]*$')
_BLOCKED_ENV_NAMES = {
    'HOME',
    'PATH',
    'PYTHONPATH',
    'LD_LIBRARY_PATH',
    'LD_PRELOAD',
    'DYLD_LIBRARY_PATH',
    'DYLD_INSERT_LIBRARIES',
    'SHELL',
    'PWD',
}


def _validate_env_name(name: str) -> str:
    cleaned = str(name or '').strip()
    if not cleaned:
        raise ValueError('env name is required')
    if not _ENV_NAME_RE.fullmatch(cleaned):
        raise ValueError('env name must match ^[A-Za-z_][A-Za-z0-9_]*$')
    if cleaned.upper() in _BLOCKED_ENV_NAMES:
        raise ValueError(f'env name {cleaned!r} is reserved and cannot be changed from chat')
    return cleaned


def build_session_env_tool(
    conversation_env_store: MutableMapping[str, dict[str, str]],
    conversation_id: str,
) -> Any:
    """Build a ChatAgent-scoped tool for setting session environment variables."""

    def set_session_env(name: str, value: str) -> dict[str, Any]:
        """Set an environment variable for the current conversation.

        Use this when the user provides an environment variable name and value,
        including when they ask to configure it and continue a skill. Call this
        before retrying or continuing `run_script`. The value is immediately
        available to skill scripts in this conversation; do not ask the user to
        restart the service. Do not echo the secret value in the final answer.
        Do not use it to change system variables such as PATH, HOME, or PYTHONPATH.

        Args:
            name (str): Environment variable name, e.g. REDFOX_API_KEY.
            value (str): Environment variable value provided by the user.
        """
        try:
            env_name = _validate_env_name(name)
        except ValueError as exc:
            return {'status': 'error', 'error_type': 'InvalidEnvName', 'error': str(exc)}
        env_value = str(value or '').strip()
        if not env_value:
            return {
                'status': 'error',
                'name': env_name,
                'error_type': 'InvalidEnvValue',
                'error': 'env value must not be empty',
            }
        scope_key = (conversation_id or lazyllm_globals._sid or '').strip()
        if not scope_key:
            return {
                'status': 'error',
                'name': env_name,
                'error_type': 'MissingConversation',
                'error': 'conversation id is required to store session env',
            }
        scoped_env = conversation_env_store.setdefault(scope_key, {})
        scoped_env[env_name] = env_value
        inject_env_vars({env_name: env_value})
        return {
            'status': 'ok',
            'name': env_name,
            'scope': 'conversation',
            'conversation_id': scope_key,
            'available_to': ['run_script'],
            'value_set': True,
        }

    return set_session_env
