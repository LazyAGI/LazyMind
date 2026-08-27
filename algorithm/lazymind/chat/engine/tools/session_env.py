from __future__ import annotations

import re
from typing import Any, MutableMapping

from lazyllm import globals as lazyllm_globals
from lazyllm.tools import inject_env_vars


SESSION_ENV_TOOL_NAME = 'set_session_env'
REDACTED_ENV_VALUE = '<redacted>'

_ENV_NAME_RE = re.compile(r'^[A-Za-z_][A-Za-z0-9_]*$')
_BLOCKED_ENV_NAMES = {
    'HOME',
    'PATH',
    'PYTHONPATH',
    'PYTHONHOME',
    'PYTHONSTARTUP',
    'PYTHONEXECUTABLE',
    'LD_LIBRARY_PATH',
    'LD_PRELOAD',
    'DYLD_LIBRARY_PATH',
    'DYLD_INSERT_LIBRARIES',
    'SHELL',
    'PWD',
    'IFS',
    'ENV',
    'BASH_ENV',
    'HTTP_PROXY',
    'HTTPS_PROXY',
    'ALL_PROXY',
    'NO_PROXY',
    'FTP_PROXY',
    'SSL_CERT_FILE',
    'SSL_CERT_DIR',
    'REQUESTS_CA_BUNDLE',
    'CURL_CA_BUNDLE',
    'SSLKEYLOGFILE',
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


def redact_session_env_arguments(tool_name: str, arguments: Any) -> Any:
    if str(tool_name or '') != SESSION_ENV_TOOL_NAME:
        return arguments
    if not isinstance(arguments, dict) or 'value' not in arguments:
        return arguments
    redacted = dict(arguments)
    if redacted.get('value') not in (None, REDACTED_ENV_VALUE):
        redacted['value'] = REDACTED_ENV_VALUE
    return redacted


def build_session_env_tool(
    conversation_env_store: MutableMapping[str, dict[str, str]],
    conversation_id: str,
) -> Any:
    """Build a ChatAgent-scoped tool for setting session environment variables."""

    def set_session_env(name: str, value: str) -> dict[str, Any]:
        """Set an environment variable for the current conversation only.

        Stored values apply only to this conversation. Other conversations,
        including a newly opened chat, cannot read them.

        When a skill or `run_script` fails because an API key, token, or env var
        is missing: if this turn already has the name and value, call this tool
        then immediately retry. Otherwise call `ask_user` with `type=text` for
        the missing variable(s), preferring names from a `missing_env` tool
        result. After the user answers, call this tool then immediately retry
        the same skill/`run_script`. The user may also proactively provide
        `NAME=value`; call this tool then continue the original task. Do not
        ask the user to restart. Do not echo the secret. Do not change system
        variables such as PATH, HOME, or PYTHONPATH.

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
