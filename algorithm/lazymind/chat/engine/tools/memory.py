import re

from typing import Any, Dict, Literal

import lazyllm
from pydantic import ValidationError

from lazymind.common.memory import (
    MEMORY_TARGET_PATHS,
    EpisodeConflictError,
    EpisodeCreateInput,
    EpisodeReadError,
    EpisodeSource,
    EpisodeType,
    MemoryRemoteStore,
    build_episode_idempotency_key,
    get_episode_store,
)


MemoryReadTarget = Literal['memory', 'user_preference']
_TRANSIENT_MARKERS = (
    'backend down',
    'connection',
    'rate limit',
    'temporarily unavailable',
    'temporary failure',
    'timed out',
    'timeout',
    'unavailable',
)
_URL_CREDENTIALS = re.compile(r'(?P<scheme>[a-z][a-z0-9+.-]*://)[^/@\s:]+:[^/@\s]+@', re.I)
_SECRET_VALUE = re.compile(
    r'''(?ix)
    ["']?(password|passwd|token|secret|api[_-]?key)["']?\s*[=:]\s*
    (?:"[^"]*"|'[^']*'|[^\s,;}]+)
    '''
)
_AUTHORIZATION_VALUE = re.compile(
    r'''(?ix)
    (?P<prefix>["']?authorization["']?\s*[:=]\s*)
    (?:"[^"]*"|'[^']*'|(?:bearer|basic)\s+[^\s,;}]+|[^\s,;}]+)
    '''
)
_HTTP_AUTH_VALUE = re.compile(
    r'''(?ix)
    ["']?(http_auth|basic_auth)["']?\s*[:=]\s*
    (?:\{[^}]*\}|\([^)]*\)|\[[^]]*\]|"[^"]*"|'[^']*'|[^\s,;]+)
    '''
)
_BEARER_VALUE = re.compile(r'''(?ix)\bbearer\s+[^\s,;'"}\]]+''')


def _agentic_config() -> dict[str, Any]:
    config = lazyllm.globals.get('agentic_config')
    return config if isinstance(config, dict) else {}


def _safe_exception_message(exc: Exception) -> str:
    message = ' '.join(str(exc).split()).strip() or type(exc).__name__
    message = _URL_CREDENTIALS.sub(r'\g<scheme>***@', message)
    message = _HTTP_AUTH_VALUE.sub(lambda match: f'{match.group(1)}=<redacted>', message)
    message = _AUTHORIZATION_VALUE.sub(
        lambda match: f'{match.group("prefix")}<redacted>',
        message,
    )
    message = _BEARER_VALUE.sub('Bearer <redacted>', message)
    message = _SECRET_VALUE.sub(lambda match: f'{match.group(1)}=<redacted>', message)
    return message[:500]


def _is_transient(exc: Exception) -> bool:
    if isinstance(exc, (ConnectionError, TimeoutError)):
        return True
    message = str(exc).casefold()
    return any(marker in message for marker in _TRANSIENT_MARKERS)


def _is_timeout(exc: Exception) -> bool:
    message = str(exc).casefold()
    return isinstance(exc, TimeoutError) or 'timed out' in message or 'timeout' in message


def _record_tool_result(
    payload: dict[str, Any],
    *,
    mutation: bool | None,
    ledger_result: dict[str, Any] | None = None,
) -> dict[str, Any]:
    config = _agentic_config()
    ledger = config.get('memory_tool_results')
    if not isinstance(ledger, list):
        ledger = []
        config['memory_tool_results'] = ledger
    entry: dict[str, Any] = {
        'tool': str(payload.get('tool') or ''),
        'success': payload.get('success') is True,
        'mutation': mutation,
    }
    if ledger_result is not None:
        entry['result'] = ledger_result
    error = payload.get('error')
    if isinstance(error, dict):
        entry['error'] = error
    entry['retryable'] = payload.get('retryable') is True
    ledger.append(entry)
    return payload


def _log_tool_exception(tool: str, exc: Exception) -> None:
    lazyllm.LOG.error(
        f'[MemoryTools] {tool} failed: {type(exc).__name__}: '
        f'{_safe_exception_message(exc)}'
    )


class MemoryTools:
    """Persistent memory APIs for Chat and Memory Review agents."""

    __public_apis__ = ['read_memory', 'episode_create']

    def __lazy_source__(self) -> bool:
        return False

    def read_memory(self, target: MemoryReadTarget) -> Dict[str, Any]:
        """Read the agent's current working memory or user profile text.

        This reads optional persistent, cross-conversation notes. It does NOT
        read the current conversation history, which is already present in the
        model's messages. Never call this tool to recall earlier turns in the
        current chat, summarize the conversation, or resolve a follow-up
        question. Empty persistent memory does not mean that conversation
        history is unavailable. Use this tool only when the user explicitly
        asks about saved memory/profile content or when persistent
        cross-conversation notes are specifically needed.

        Args:
            target: Selects the document to read. Use 'memory' for agent
                working memory, or 'user_preference' for user profile and
                preference text.
        """
        raw_target = str(target).strip()
        if raw_target not in MEMORY_TARGET_PATHS:
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'read_memory',
                    'error': {
                        'code': 'invalid_arguments',
                        'message': (
                            f'Unknown target {raw_target!r}; expected one of '
                            '\'memory\', \'user_preference\'.'
                        ),
                        'detail': {'target': raw_target},
                    },
                    'retryable': False,
                },
                mutation=False,
            )
        try:
            content = MemoryRemoteStore().read(raw_target)
        except Exception as exc:
            _log_tool_exception('read_memory', exc)
            safe_message = _safe_exception_message(exc)
            transient = _is_transient(exc)
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'read_memory',
                    'error': {
                        'code': 'storage_unavailable' if transient else 'storage_read_failed',
                        'message': f'Failed to read {raw_target} via RemoteFS: {safe_message}',
                        'detail': {
                            'target': raw_target,
                            'exception_type': type(exc).__name__,
                        },
                    },
                    'retryable': transient,
                },
                mutation=False,
            )
        result = {
            'target': raw_target,
            'content': content,
            'content_length': len(content),
        }
        return _record_tool_result(
            {
                'success': True,
                'tool': 'read_memory',
                'result': result,
                'retryable': False,
            },
            mutation=False,
            ledger_result={
                'status': 'read',
                'target': raw_target,
                'content_length': len(content),
            },
        )

    def episode_create(
        self,
        summary: str,
        episode_type: str,
    ) -> Dict[str, Any]:
        """Persist exactly one immutable historical Episode.

        Call once per Episode. In Chat, call only when the user explicitly asks
        to record, remember or save a historical event. Memory Review may call
        it for durable decisions, progress, results, blockers and events. All
        provenance and timestamp fields come from agentic_config.

        Args:
            summary: Concise factual summary of this one historical Episode.
            episode_type: One of decision, progress, result, blocker, or event.
        """
        config = _agentic_config()
        required_context = (
            ('user_id', str(config.get('user_id') or '').strip()),
            ('conversation_id', str(config.get('conversation_id') or '').strip()),
        )
        values: dict[str, str] = {}
        for field, value in required_context:
            if not value:
                return _record_tool_result(
                    {
                        'success': False,
                        'tool': 'episode_create',
                        'error': {
                            'code': 'missing_context',
                            'message': f'{field} is required in agentic_config.',
                            'detail': {'field': field},
                        },
                        'retryable': False,
                    },
                    mutation=False,
                )
            values[field] = value

        task_id = str(config.get('task_id') or '').strip()
        is_review = task_id.startswith('memory_review_')
        timestamp_field = 'review_started_at_ms' if is_review else 'episode_occurred_at_ms'
        occurred_at_ms = config.get(timestamp_field)
        if isinstance(occurred_at_ms, bool):
            occurred_at_ms = None
        try:
            occurred_at_ms = int(occurred_at_ms) if occurred_at_ms is not None else None
        except (TypeError, ValueError):
            occurred_at_ms = None
        if not occurred_at_ms or occurred_at_ms <= 0:
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'episode_create',
                    'error': {
                        'code': 'missing_context',
                        'message': f'{timestamp_field} is required in agentic_config.',
                        'detail': {'field': timestamp_field},
                    },
                    'retryable': False,
                },
                mutation=False,
            )

        try:
            item = EpisodeCreateInput(
                occurred_at_ms=occurred_at_ms,
                thread_key=values['conversation_id'],
                episode_type=EpisodeType(episode_type),
                summary=summary,
                source=EpisodeSource(
                    kind='memory_review' if is_review else 'chat_explicit',
                    task_id=task_id if is_review else None,
                    conversation_id=values['conversation_id'],
                    message_ids=[],
                ),
            )
        except (TypeError, ValueError, ValidationError) as exc:
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'episode_create',
                    'error': {
                        'code': 'invalid_arguments',
                        'message': f'Invalid Episode arguments: {_safe_exception_message(exc)}',
                        'detail': {'episode_type': str(episode_type)},
                    },
                    'retryable': False,
                },
                mutation=False,
            )

        idempotency_key = build_episode_idempotency_key(
            user_id=values['user_id'],
            conversation_id=values['conversation_id'],
            summary=item.summary,
        )

        try:
            store = get_episode_store()
        except Exception as exc:
            _log_tool_exception('episode_create', exc)
            transient = _is_transient(exc)
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'episode_create',
                    'error': {
                        'code': 'storage_unavailable' if transient else 'storage_failed',
                        'message': (
                            'Episode storage is temporarily unavailable.'
                            if transient
                            else 'Failed to initialize Episode storage.'
                        ),
                        'detail': {'exception_type': type(exc).__name__},
                    },
                    'retryable': transient,
                },
                mutation=False,
                ledger_result={
                    'status': 'failed',
                    'idempotency_key': idempotency_key,
                },
            )

        try:
            create_result = store.create(values['user_id'], item)
        except EpisodeReadError as exc:
            root_exc = exc.__cause__ if isinstance(exc.__cause__, Exception) else exc
            _log_tool_exception('episode_create', root_exc)
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'episode_create',
                    'error': {
                        'code': exc.code,
                        'message': (
                            'Episode storage is temporarily unavailable.'
                            if exc.retryable
                            else 'Failed to read existing Episodes.'
                        ),
                        'detail': {'exception_type': type(root_exc).__name__},
                    },
                    'retryable': exc.retryable,
                },
                mutation=False,
                ledger_result={
                    'status': 'failed',
                    'idempotency_key': idempotency_key,
                },
            )
        except EpisodeConflictError as exc:
            _log_tool_exception('episode_create', exc)
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'episode_create',
                    'error': {
                        'code': 'episode_conflict',
                        'message': _safe_exception_message(exc),
                        'detail': {'exception_type': type(exc).__name__},
                    },
                    'retryable': False,
                },
                mutation=False,
                ledger_result={
                    'status': 'failed',
                    'idempotency_key': idempotency_key,
                },
            )
        except Exception as exc:
            _log_tool_exception('episode_create', exc)
            safe_message = _safe_exception_message(exc)
            timed_out = _is_timeout(exc)
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'episode_create',
                    'error': {
                        'code': 'storage_timeout' if timed_out else 'storage_failed',
                        'message': (
                            'Episode storage timed out and write completion is unknown: '
                            f'{safe_message}'
                            if timed_out
                            else f'Failed to create Episode: {safe_message}'
                        ),
                        'detail': {'exception_type': type(exc).__name__},
                    },
                    'retryable': False,
                },
                mutation=None,
                ledger_result={
                    'status': 'failed',
                    'idempotency_key': idempotency_key,
                },
            )

        result = create_result.model_dump(mode='json')
        return _record_tool_result(
            {
                'success': True,
                'tool': 'episode_create',
                'result': result,
                'retryable': False,
            },
            mutation=create_result.status == 'created',
            ledger_result={
                'status': create_result.status,
                'idempotency_key': create_result.idempotency_key,
            },
        )
