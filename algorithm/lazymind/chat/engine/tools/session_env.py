from __future__ import annotations

from lazyllm.tools import fc_register

import json
import re
import threading
from collections.abc import Mapping
from weakref import WeakValueDictionary
from urllib.parse import quote
from typing import Any, MutableMapping

from lazyllm import globals as lazyllm_globals
from lazyllm.tools import inject_env_vars
from lazymind.chat.engine.tools.infra.core_api_client import (
    CoreAPIError,
    get_core_api,
    patch_core_api,
    post_core_api,
)


SESSION_ENV_TOOL_NAME = 'set_session_env'
USER_ENV_TOOL_NAME = 'set_user_env'
REDACTED_ENV_VALUE = '<redacted>'
VOCABULARY_ASK_TOOL_NAME = 'ask_words'

_ENV_NAME_RE = re.compile(r'^[A-Za-z_][A-Za-z0-9_]*$')
_CREDENTIAL_ENV_NAME_RE = re.compile(
    r'(^|_)(API_)?(KEY|TOKEN|SECRET|PASSWORD|PASS|CREDENTIAL|CREDENTIALS|'
    r'AUTH|ACCESS|REFRESH)(_|$)|(_API_KEY$)',
    re.IGNORECASE,
)
_CONTROL_ENV_NAME_RE = re.compile(
    r'(^|_)(PATH|HOME|SHELL|ENV|PROXY|PRELOAD|LIBRARY|CERT|BUNDLE|'
    r'OPTIONS?|OPTS|CONFIG|RC|PROFILE|STARTUP)(_|$)',
    re.IGNORECASE,
)
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


class ConversationEnvLease:
    """Identity of an active conversation generation, invalidated by cleanup."""


class StaleConversationEnvError(ValueError):
    pass


class ConversationEnvStore:
    """Ephemeral, process-local conversation credentials; never persisted to DB."""

    def __init__(self, backing: MutableMapping[str, dict[str, str]] | None = None) -> None:
        self._backing = backing if backing is not None else {}
        self._lock = threading.RLock()
        self._leases: WeakValueDictionary[str, ConversationEnvLease] = WeakValueDictionary()

    def snapshot(self, conversation_id: str) -> tuple[dict[str, str], ConversationEnvLease]:
        key = str(conversation_id or '').strip()
        with self._lock:
            lease = self._leases.get(key)
            if lease is None:
                lease = ConversationEnvLease()
                self._leases[key] = lease
            return dict(self._backing.get(key) or {}), lease

    def get_many(self, conversation_id: str, names: list[str] | None = None) -> dict[str, str]:
        key = str(conversation_id or '').strip()
        if not key:
            return {}
        with self._lock:
            scoped = dict(self._backing.get(key) or {})
        if names is None:
            return scoped
        requested = {_validate_env_name(name) for name in names}
        return {name: value for name, value in scoped.items() if name in requested}

    def set(self, conversation_id: str, name: str, value: str, *, lease: ConversationEnvLease | None = None) -> None:
        key = str(conversation_id or '').strip()
        if not key:
            raise ValueError('conversation id is required to store session env')
        env_name = _validate_env_name(name)
        with self._lock:
            if lease is not None and self._leases.get(key) is not lease:
                raise StaleConversationEnvError('conversation environment was cleared; start a new turn')
            scoped = self._backing.setdefault(key, {})
            scoped[env_name] = value

    def clear(self, conversation_id: str) -> bool:
        key = str(conversation_id or '').strip()
        if not key:
            return False
        with self._lock:
            self._leases.pop(key, None)
            return self._backing.pop(key, None) is not None


def inject_runtime_env(
    user_env_vars: Mapping[str, str] | None,
    conversation_env_vars: Mapping[str, str] | None = None,
) -> None:
    """Replace this execution's credentials, retaining only explicit conversation overrides."""
    overrides = dict(conversation_env_vars or {})
    merged = dict(user_env_vars or {})
    merged.update(overrides)
    lazyllm_globals['conversation_env_overrides'] = overrides
    lazyllm_globals['dynamic_env_vars'] = {}
    inject_env_vars(merged)


def _as_conversation_env_store(
    store: ConversationEnvStore | MutableMapping[str, dict[str, str]],
) -> ConversationEnvStore:
    if isinstance(store, ConversationEnvStore):
        return store
    return ConversationEnvStore(store)


def _validate_env_name(name: str) -> str:
    cleaned = str(name or '').strip()
    if not cleaned:
        raise ValueError('env name is required')
    if len(cleaned) > 128 or not _ENV_NAME_RE.fullmatch(cleaned):
        raise ValueError('env name must match ^[A-Za-z_][A-Za-z0-9_]*$')
    if cleaned.upper() in _BLOCKED_ENV_NAMES:
        raise ValueError(f'env name {cleaned!r} is reserved and cannot be changed from chat')
    if _CONTROL_ENV_NAME_RE.search(cleaned):
        raise ValueError(f'env name {cleaned!r} controls runtime behavior and cannot be changed from chat')
    if not _CREDENTIAL_ENV_NAME_RE.search(cleaned):
        raise ValueError(
            'env name must look like a credential name, such as *_API_KEY, *_TOKEN, or *_SECRET'
        )
    return cleaned


def redact_session_env_arguments(tool_name: str, arguments: Any) -> Any:
    if str(tool_name or '') == VOCABULARY_ASK_TOOL_NAME and isinstance(arguments, Mapping):
        redacted = dict(arguments)
        questions = []
        for raw_question in arguments.get('questions') or []:
            if not isinstance(raw_question, Mapping):
                questions.append(raw_question)
                continue
            question = dict(raw_question)
            if 'correct_answer' in question:
                question['correct_answer'] = REDACTED_ENV_VALUE
            # Criteria can trivially repeat the answer (for example "must equal
            # diverse"), so it is sensitive for cloze questions as well.
            if str(arguments.get('type') or '').strip().lower() == 'cloze':
                question.pop('grading_criteria', None)
            questions.append(question)
        if 'questions' in redacted:
            redacted['questions'] = questions
        return redacted
    if str(tool_name or '') not in {SESSION_ENV_TOOL_NAME, USER_ENV_TOOL_NAME}:
        return arguments
    if isinstance(arguments, str):
        try:
            arguments = json.loads(arguments)
        except Exception:  # noqa: BLE001
            return REDACTED_ENV_VALUE if arguments else arguments
    if not isinstance(arguments, Mapping):
        return REDACTED_ENV_VALUE if arguments is not None else None
    redacted = dict(arguments)
    for key, value in list(redacted.items()):
        if key == 'name' or value in (None, REDACTED_ENV_VALUE):
            continue
        redacted[key] = REDACTED_ENV_VALUE
    return redacted


def build_session_env_tool(
    conversation_env_store: ConversationEnvStore | MutableMapping[str, dict[str, str]],
    conversation_id: str,
    lease: ConversationEnvLease | None = None,
) -> Any:
    """Build a ChatAgent-scoped tool for setting session environment variables."""

    declared = (
        type(conversation_id) is str
        and (
            isinstance(conversation_env_store, ConversationEnvStore)
            or (
                type(conversation_env_store) is dict
                and all(type(key) is str and type(value) is dict
                        for key, value in conversation_env_store.items())
            )
        )
    )
    register_capability = fc_register(host_file='NONE') if declared else (lambda function: function)
    store = _as_conversation_env_store(conversation_env_store)
    scope_key = (conversation_id or '').strip()
    if scope_key and lease is None:
        _, lease = store.snapshot(scope_key)

    @register_capability
    def set_session_env(name: str, value: str) -> dict[str, Any]:
        """Set an environment variable for the current conversation only.

        Stored values apply only to this conversation. Other conversations,
        including a newly opened chat, cannot read them.

        Use this tool only when the user has NOT requested persistent storage.
        If the user explicitly asks for a permanent, user-level, future-conversation,
        or Settings-saved variable, use `set_user_env` instead; do not also create
        a conversation override. The instructions below apply only to temporary setup.

        When a skill or `run_script` fails because an API key, token, or env var
        is missing: if this turn already has the name and value, call this tool
        then immediately retry. Otherwise call `ask_user` with `type=text` for
        the missing variable(s), preferring names from a `missing_env` tool
        result. After the user answers, call this tool then immediately retry
        the same skill/`run_script`. The user may also proactively provide
        `NAME=value`; call this tool then continue the original task. Do not
        ask the user to restart. Pass the actual user-provided value to this
        tool, never a redaction placeholder. Do not echo the secret. Do not change system
        variables such as PATH, HOME, or PYTHONPATH.

        Args:
            name (str): Environment variable name, e.g. REDFOX_API_KEY.
            value (str): Environment variable value provided by the user.
        """
        nonlocal scope_key, lease
        try:
            env_name = _validate_env_name(name)
        except ValueError as exc:
            return {'status': 'error', 'error_type': 'InvalidEnvName', 'error': str(exc)}
        env_value = str(value or '')
        if env_value.strip() == REDACTED_ENV_VALUE:
            return {
                'status': 'error', 'name': env_name, 'error_type': 'RedactedEnvValue',
                'error': 'A redaction placeholder is not a credential. Use the actual value from the current user request; if unavailable, ask the user. Existing configuration was not changed.',
            }
        if not env_value.strip() or '\0' in env_value:
            return {
                'status': 'error',
                'name': env_name,
                'error_type': 'InvalidEnvValue',
                'error': 'env value must not be empty or contain NUL',
            }
        if not scope_key:
            scope_key = (lazyllm_globals._sid or '').strip()
            if scope_key:
                _, lease = store.snapshot(scope_key)
        if not scope_key:
            return {
                'status': 'error',
                'name': env_name,
                'error_type': 'MissingConversation',
                'error': 'conversation id is required to store session env',
            }
        try:
            store.set(scope_key, env_name, env_value, lease=lease)
        except StaleConversationEnvError as exc:
            return {'status': 'error', 'name': env_name, 'error_type': 'StaleConversation', 'error': str(exc)}
        overrides = dict(lazyllm_globals.get('conversation_env_overrides') or {})
        overrides[env_name] = env_value
        lazyllm_globals['conversation_env_overrides'] = overrides
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


def build_user_env_tool() -> Any:
    """Build a tool for persistently storing user-level environment variables."""

    def set_user_env(name: str, value: str, description: str | None = None, enabled: bool | None = None) -> dict[str, Any]:
        """Persist an environment variable for the current user across conversations.

        Call this tool only when the user explicitly asks to save an environment
        variable permanently, as a user-level setting, for future conversations,
        or in Settings. If the user does not clearly ask for persistence, use
        `set_session_env` instead. Pass the actual user-provided value to this
        tool, never a redaction placeholder. Do not echo the secret value.

        Args:
            name (str): Environment variable name, e.g. TAVILY_API_KEY.
            value (str): Environment variable value provided by the user.
            description (str): Optional note; omit to preserve the existing note.
            enabled (bool): Optional status; omit to preserve existing status. New variables default to enabled.
        """
        try:
            env_name = _validate_env_name(name)
        except ValueError as exc:
            return {'status': 'error', 'error_type': 'InvalidEnvName', 'error': str(exc)}
        env_value = str(value or '')
        if env_value.strip() == REDACTED_ENV_VALUE:
            return {
                'status': 'error', 'name': env_name, 'error_type': 'RedactedEnvValue',
                'error': 'A redaction placeholder is not a credential. Use the actual value from the current user request; if unavailable, ask the user. Existing configuration was not changed.',
            }
        if not env_value.strip() or '\0' in env_value:
            return {
                'status': 'error',
                'name': env_name,
                'error_type': 'InvalidEnvValue',
                'error': 'env value must not be empty or contain NUL',
            }
        payload = {
            'name': env_name,
            'value': env_value,
        }
        if description is not None:
            payload['description'] = str(description).strip()
        if enabled is not None:
            payload['enabled'] = bool(enabled)
        saved_enabled = True if enabled is None else bool(enabled)
        action = 'created'
        try:
            saved = post_core_api('/user/env-vars', payload)
        except CoreAPIError as exc:
            if exc.status_code != 409:
                return {
                    'status': 'error',
                    'name': env_name,
                    'error_type': exc.__class__.__name__,
                    'error': 'Unable to save user environment variable. Check Settings and retry.',
                }
            try:
                items = (get_core_api('/user/env-vars').get('items') or [])
                existing = next(
                    (item for item in items if isinstance(item, dict) and item.get('name') == env_name),
                    None,
                )
                if not existing or not existing.get('id'):
                    raise RuntimeError('existing env var was not found after conflict')
                saved = patch_core_api(
                    f"/user/env-vars/{quote(str(existing['id']), safe='')}",
                    payload,
                )
                if enabled is None:
                    saved_enabled = bool(existing.get('enabled', True))
                action = 'updated'
            except Exception as update_exc:  # noqa: BLE001
                return {
                    'status': 'error',
                    'name': env_name,
                    'error_type': update_exc.__class__.__name__,
                    'error': 'Unable to update user environment variable. Check Settings and retry.',
                }
        except Exception as exc:  # noqa: BLE001
            return {
                'status': 'error',
                'name': env_name,
                'error_type': exc.__class__.__name__,
                'error': 'Unable to save user environment variable. Check Settings and retry.',
            }
        saved_enabled = bool(((saved.get('response') or {}).get('data') or {}).get('enabled', saved_enabled))
        overrides = lazyllm_globals.get('conversation_env_overrides') or {}
        effective_value = overrides.get(env_name, env_value if saved_enabled else '')
        inject_env_vars({env_name: effective_value})
        return {
            'status': 'ok',
            'name': env_name,
            'scope': 'user',
            'action': action,
            'enabled': saved_enabled,
            'available_to': ['future_conversations', 'run_script'] if saved_enabled else [],
            'value_set': True,
        }

    return set_user_env
