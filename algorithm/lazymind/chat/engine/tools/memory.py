import re

from typing import Any, Dict, List, Literal, Union

import lazyllm
from pydantic import ValidationError

from lazymind.chat.engine.tools.infra import tool_error, tool_success
from lazymind.common.memory import (
    PREFERENCE_PATH,
    PROFILE_PATH,
    SOUL_PATH,
    EpisodeConflictError,
    EpisodeCreateInput,
    EpisodeReadError,
    EpisodeSource,
    EpisodeType,
    MemoryStore,
    build_episode_idempotency_key,
    get_episode_store,
    preference_name_to_reference_name,
    split_reference_ref,
)

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


def _memory_write_error(tool_name: str, message: str) -> Dict[str, Any]:
    text = str(message or '').strip()
    if text.lower() == 'conflict':
        return tool_error(
            tool_name,
            'There are pending changes. Please resolve them before modifying.',
            error_type='conflict',
        )
    return tool_error(tool_name, f'Failed to write via RemoteFS: {text}', error_type='store')


def _memory_applied(tool_name: str, **result: Any) -> Dict[str, Any]:
    return tool_success(tool_name, {'status': 'applied', **result})


def _memory_result_error(tool_name: str, result: Dict[str, Any]) -> Dict[str, Any]:
    error_type = str(result.get('type') or 'validation')
    reason = str(result.get('error') or f'{tool_name} failed.')
    if error_type in {'store', 'conflict'}:
        return _memory_write_error(tool_name, reason)
    return tool_error(tool_name, reason, error_type=error_type)


# Per-reference read limits: truncate when either bound is reached first.
MAX_REFERENCE_READ_LINES = 200
MAX_REFERENCE_READ_CHARS = 8000
MAX_REFERENCE_READ_COUNT = 10

_REFERENCE_TRUNCATION_WARNING = (
    '\n\n<!-- WARNING: This reference content was truncated because it exceeded '
    'the limits. Use a narrower #anchor, read fewer refs in one call, '
    'or ask the user if the omitted detail matters. -->\n'
)


def truncate_reference_content(
    content: str,
    *,
    max_lines: int = MAX_REFERENCE_READ_LINES,
    max_chars: int = MAX_REFERENCE_READ_CHARS,
) -> tuple[str, bool]:
    """Truncate reference text when line count or character count exceeds limits."""
    if max_lines < 1:
        raise ValueError('max_lines must be >= 1')
    if max_chars < 1:
        raise ValueError('max_chars must be >= 1')

    text = content if isinstance(content, str) else ''
    if not text:
        return '', False

    lines = text.splitlines(keepends=True)
    if not lines:
        lines = [text]

    parts: list[str] = []
    char_count = 0
    truncated = False

    for line_idx, line in enumerate(lines):
        if line_idx >= max_lines:
            truncated = True
            break
        if char_count + len(line) <= max_chars:
            parts.append(line)
            char_count += len(line)
            continue
        remaining = max_chars - char_count
        if remaining > 0:
            parts.append(line[:remaining])
        truncated = True
        break

    result = ''.join(parts)
    if not truncated and result != text:
        truncated = True
    return result, truncated


def _normalize_refs(refs: Union[str, List[str]]) -> list[str]:
    if isinstance(refs, str):
        raw_items = [refs]
    elif isinstance(refs, list):
        raw_items = refs
    else:
        return []

    normalized: list[str] = []
    seen: set[str] = set()
    for item in raw_items:
        ref = str(item or '').strip()
        if not ref or ref in seen:
            continue
        seen.add(ref)
        normalized.append(ref)
    return normalized


def _read_single_reference(store: MemoryStore, raw_ref: str) -> dict[str, Any]:
    path, anchor = split_reference_ref(raw_ref)
    content = store.read_reference(raw_ref)
    truncated_content, truncated = truncate_reference_content(content)
    if truncated:
        truncated_content = f'{truncated_content.rstrip()}{_REFERENCE_TRUNCATION_WARNING}'
    return {
        'ref': raw_ref,
        'path': path,
        'anchor': anchor or None,
        'content': truncated_content,
        'content_length': len(truncated_content),
        'original_content_length': len(content),
        'truncated': truncated,
    }


class MemoryTools:
    """Persistent memory APIs for Chat and Memory Review agents."""

    __public_apis__ = [
        'read_memory_reference',
        'soul_editor',
        'profile_editor',
        'preference_editor',
        'episode_create',
    ]

    def __lazy_source__(self) -> bool:
        return False

    def read_memory_reference(self, refs: Union[str, List[str]]) -> Dict[str, Any]:
        """Read detailed user-preference reference files on demand.

        The User Preference Index injected in the system prompt lists short
        summaries with optional ``ref`` pointers under
        ``memory/users/references/``. Call this only when the current task
        matches listed preferences AND the injected summaries are not enough.
        Pass exact ``ref`` values from those index entries.

        Args:
            refs: One preference-index ref or a list of refs to read in order.
        """
        normalized_refs = _normalize_refs(refs)
        if not normalized_refs:
            return tool_error('read_memory_reference', 'refs is required.')

        if len(normalized_refs) > MAX_REFERENCE_READ_COUNT:
            return tool_error(
                'read_memory_reference',
                f'At most {MAX_REFERENCE_READ_COUNT} refs may be read per call; '
                f'got {len(normalized_refs)}.',
            )

        store = MemoryStore()
        items: list[dict[str, Any]] = []
        for raw_ref in normalized_refs:
            try:
                split_reference_ref(raw_ref)
            except ValueError as exc:
                return tool_error(
                    'read_memory_reference',
                    f'Invalid ref {raw_ref!r}: {exc}',
                )
            try:
                items.append(_read_single_reference(store, raw_ref))
            except FileNotFoundError:
                return tool_error(
                    'read_memory_reference',
                    f'Reference not found for ref={raw_ref!r}.',
                    error_type='not_found',
                )
            except RuntimeError as exc:
                return tool_error(
                    'read_memory_reference',
                    f'Failed to read {raw_ref!r}: {exc}',
                    error_type='store',
                )
            except Exception as exc:
                return tool_error(
                    'read_memory_reference',
                    f'Failed to read {raw_ref!r}: {exc}',
                    error_type='store',
                )

        truncated_count = sum(1 for item in items if item.get('truncated'))
        return tool_success('read_memory_reference', {
            'items': items,
            'ref_count': len(items),
            'truncated_count': truncated_count,
        })

    def soul_editor(self, field: str, value: str) -> Dict[str, Any]:
        """Update one existing leaf value in the agent soul document.

        Use this only when the user explicitly asks to change the assistant's
        default identity, mission, interaction style, or epistemic behavior.
        Do not use it for user-specific facts; those belong in profile or
        preference editors. Only fields already present in the loaded soul
        document can be updated; keys cannot be added or renamed.

        Args:
            field: Dot-path of an existing soul leaf field.
            value: New non-empty string value for the field.
        """
        raw_field = str(field or '').strip()
        raw_value = str(value if value is not None else '')
        if not raw_field:
            return tool_error('soul_editor', 'field is required.', error_type='validation')

        result = MemoryStore().apply_soul_field(raw_field, raw_value)
        if not result.get('ok'):
            return _memory_result_error('soul_editor', result)

        return _memory_applied(
            'soul_editor',
            field=raw_field,
            path=SOUL_PATH,
            value=raw_value.strip(),
            content_length=len(result['content']),
        )

    def profile_editor(self, field: str, value: str) -> Dict[str, Any]:
        """Update one existing leaf value in the user profile document.

        Use this for stable user facts such as preferred name, locale, role,
        organization, or accessibility needs. Do not use it for long-form
        behavioral preferences; those belong in ``preference_editor``.
        Only fields already present in the loaded profile document can be
        updated; keys cannot be added or renamed. Value type follows the
        currently stored leaf (string/null or string list).

        For list fields, pass a JSON string array such as
        ``["zh-CN","en-US"]`` or a comma-separated list.

        Args:
            field: Dot-path of an existing profile leaf field.
            value: Serialized value for the field.
        """
        raw_field = str(field or '').strip()
        raw_value = '' if value is None else str(value)
        if not raw_field:
            return tool_error('profile_editor', 'field is required.', error_type='validation')

        result = MemoryStore().apply_profile_field(raw_field, raw_value)
        if not result.get('ok'):
            return _memory_result_error('profile_editor', result)

        return _memory_applied(
            'profile_editor',
            field=raw_field,
            path=PROFILE_PATH,
            value=raw_value,
            content_length=len(result['content']),
        )

    def preference_editor(
        self,
        op: Literal['add', 'delete'],
        name: str,
        summary: str = '',
        scenario: str = '',
        reason: str = '',
    ) -> Dict[str, Any]:
        """Add or delete a user preference index entry.

        Use this for stable long-term preferences that should appear in the
        injected preference index. Each added entry writes ``preference.md``
        and a matching reference file under ``memory/users/references/``.
        Updating an existing entry is not supported yet; delete and re-add.

        Args:
            op: ``add`` to create a new preference entry, or ``delete`` to remove.
            name: Preference identifier such as ``pref.response.concise``.
            summary: Short executable summary for the index. Required for ``add``.
            scenario: When the preference should apply. Required for ``add``.
            reason: Why the preference should be saved. Required for ``add``.
        """
        raw_op = str(op or '').strip().lower()
        raw_name = str(name or '').strip()
        if raw_op not in {'add', 'delete'}:
            return tool_error(
                'preference_editor',
                "op must be 'add' or 'delete'.",
                error_type='validation',
            )
        if not raw_name:
            return tool_error('preference_editor', 'name is required.', error_type='validation')

        store = MemoryStore()
        if raw_op == 'add':
            result = store.add_preference_with_reference(
                name=raw_name,
                summary=summary,
                scenario=scenario,
                reason=reason,
            )
            if not result.get('ok'):
                return _memory_result_error('preference_editor', result)
            item = result['item']
            return _memory_applied(
                'preference_editor',
                op='add',
                name=item.name,
                summary=item.summary,
                ref=item.ref,
                path=PREFERENCE_PATH,
                reference_name=preference_name_to_reference_name(item.name),
            )

        result = store.remove_preference_with_reference(raw_name)
        if not result.get('ok'):
            return _memory_result_error('preference_editor', result)
        item = result['item']
        return _memory_applied(
            'preference_editor',
            op='delete',
            name=item.name,
            ref=item.ref,
            path=PREFERENCE_PATH,
            reference_name=preference_name_to_reference_name(item.name),
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
