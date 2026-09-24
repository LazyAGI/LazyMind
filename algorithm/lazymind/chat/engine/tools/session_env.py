from __future__ import annotations

import uuid
from typing import Any

from lazyllm.tools import fc_register
from lazyllm.tools.agent.base import _write_agent_data
from lazyllm.tools.agent.toolError import ToolExecutionError
from lazymind.chat.engine.agent_runtime.conversation_env import ConversationEnvLease, ConversationEnvStore
from lazymind.chat.engine.agent_runtime.env_input import session_env_inputs
from lazymind.chat.engine.agent_runtime.env_policy import validate_env_name
from lazymind.chat.engine.agent_runtime.env_runtime import remove_conversation_override
from lazymind.chat.engine.tools.infra.core_api_client import get_core_api


def _request_input(name: str, scope: str, *, ask_id: str | None = None, **metadata: Any) -> dict[str, Any]:
    ask_id = ask_id or str(uuid.uuid4())
    _write_agent_data(
        'ask_pending', ask_id=ask_id, questions=[],
        env_input={'name': name, 'scope': scope, **metadata},
    )
    return {'status': 'input_required', 'name': name, 'scope': scope, 'ask_id': ask_id}


def build_session_env_tool(
    conversation_env_store: ConversationEnvStore | dict[str, dict[str, str]],
    conversation_id: str,
    lease: ConversationEnvLease | None = None,
) -> Any:
    """Request an out-of-band value; model arguments never carry the value."""
    declared = type(conversation_id) is str and (
        type(conversation_env_store) is ConversationEnvStore or (
            type(conversation_env_store) is dict and all(
                type(key) is str and type(value) is dict for key, value in conversation_env_store.items()
            )
        )
    )
    store = (conversation_env_store if isinstance(conversation_env_store, ConversationEnvStore)
             else ConversationEnvStore(conversation_env_store))
    if declared and lease is None:
        _, lease = store.snapshot(conversation_id)
    register = fc_register(host_file='NONE') if declared else (lambda function: function)

    @register
    def set_session_env(name: str) -> dict[str, Any]:
        """Request secure input of a temporary environment variable for this conversation.

        This displays a dedicated value input card and ends the turn. The user
        submits directly to the backend, not through chat or ask_user. Only the
        configured/canceled status reaches the next turn. Resume the original task
        after configuration. Default to this tool unless persistence is explicit.
        Never copy a value from chat into tool calls or ask_user answers.

        Args:
            name: Exact, case-sensitive environment variable name (not its value).
        """
        nonlocal lease
        try:
            env_name = validate_env_name(name)
            if lease is None:
                _, lease = store.snapshot(conversation_id)
            ask_id = str(uuid.uuid4())
            session_env_inputs.register(ask_id, store, conversation_id, lease, env_name)
        except ValueError as exc:
            raise ToolExecutionError(str(exc)) from None
        return _request_input(env_name, 'conversation', ask_id=ask_id)

    return set_session_env


def build_user_env_tool() -> Any:
    def set_user_env(name: str, description: str | None = None, enabled: bool | None = None) -> dict[str, Any]:
        """Request secure input of a persistent user-level environment variable.

        Use only for explicit permanent/user-level/Settings requests. The dedicated
        card submits the value directly to the backend; never collect it through
        chat or ask_user. This tool ends the turn without saving a value. Continue
        the original task after receiving the configured status.

        Args:
            name: Exact, case-sensitive environment variable name (not its value).
            description: Optional non-sensitive note; omit to preserve the existing note.
            enabled: Optional status; omit to preserve existing status. New variables default to enabled.
        """
        try:
            env_name = validate_env_name(name)
        except ValueError as exc:
            raise ToolExecutionError(str(exc)) from None
        try:
            items = get_core_api('/user/env-vars').get('items') or []
            existing = next((item for item in items if item.get('name') == env_name), None)
        except Exception:  # noqa: BLE001
            raise ToolExecutionError('Unable to load user environment variables. Retry from Settings.') from None
        metadata = {}
        if existing:
            metadata.update(id=existing['id'], expected_updated_at=existing['updated_at'])
        if description is not None:
            if len(description) > 512:
                raise ToolExecutionError('environment variable description is too long')
            metadata['description'] = description
        if enabled is not None:
            metadata['enabled'] = enabled
        return _request_input(env_name, 'user', **metadata)

    return set_user_env


def build_delete_session_env_tool(
    conversation_env_store: ConversationEnvStore,
    conversation_id: str,
    lease: ConversationEnvLease,
) -> Any:
    def delete_session_env(name: str) -> dict[str, Any]:
        """Delete a current-conversation environment override without a confirmation card.

        Use for temporary/session deletion, or deletion without an explicit scope.
        This never deletes a user-level variable. A same-name enabled user variable
        becomes effective again. For explicit permanent/user-level deletion, use
        delete_user_env, which requires a confirmation card.

        Args:
            name: Exact environment variable name (case-sensitive).
        """
        try:
            env_name = validate_env_name(name)
            existed = conversation_env_store.remove(conversation_id, env_name, lease=lease)
        except ValueError as exc:
            raise ToolExecutionError(str(exc)) from None
        effective_source = remove_conversation_override(env_name)
        return {
            'status': 'ok', 'name': env_name, 'scope': 'conversation', 'deleted': existed,
            'effective_source': effective_source,
        }

    return delete_session_env


def build_delete_user_env_tool(language: str = 'en') -> Any:
    def delete_user_env(name: str) -> dict[str, Any]:
        """Request deletion of an explicitly user-level/permanent environment variable.

        Displays a confirmation card and ends this turn; nothing is deleted yet.
        The backend deletes only after the user submits Yes on that card. Never
        substitute ask_user, shell commands, or a confirmed flag for this tool.
        A session override is not deleted. For unspecified/session scope use
        delete_session_env instead. Do not claim deletion before confirmation.

        Args:
            name: Exact environment variable name (case-sensitive).
        """
        try:
            env_name = validate_env_name(name)
        except ValueError as exc:
            raise ToolExecutionError(str(exc)) from None
        try:
            items = get_core_api('/user/env-vars').get('items') or []
            existing = next((item for item in items if item.get('name') == env_name), None)
        except Exception:  # noqa: BLE001
            raise ToolExecutionError('Unable to load user environment variables. Retry from Settings.') from None
        if not existing:
            return {'status': 'ok', 'scope': 'user', 'name': env_name, 'deleted': False, 'reason': 'not_found'}
        chinese = language.lower().startswith('zh')
        ask_id = str(uuid.uuid4())
        _write_agent_data(
            'ask_pending', ask_id=ask_id,
            title='删除用户级环境变量' if chinese else 'Delete user environment variable',
            description=(
                '删除后将不再注入后续执行；同名会话级变量不受影响。'
                if chinese else 'It will no longer be injected into future runs. Session overrides are unchanged.'
            ),
            questions=[{
                'text': f'确认删除用户级环境变量 {env_name}？' if chinese else f'Delete user variable {env_name}?',
                'type': 'boolean', 'choices': ['__ask_user_yes__', '__ask_user_no__'],
            }],
            user_env_delete={
                'id': existing['id'], 'name': env_name, 'expected_updated_at': existing['updated_at'],
            },
        )
        return {'status': 'confirmation_required', 'scope': 'user', 'name': env_name, 'ask_id': ask_id}

    return delete_user_env
