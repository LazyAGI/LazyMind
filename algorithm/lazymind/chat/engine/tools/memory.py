import re

from typing import Any, Dict, List, Literal, Union

import lazyllm
import requests
from lazyllm.tools import tool_concurrency
from pydantic import ValidationError

from lazymind.chat.engine.tools.infra import tool_error, tool_success
from lazymind.common.memory import (
    PREFERENCE_PATH,
    PROFILE_PATH,
    SOUL_PATH,
    EpisodeCreateInput,
    EpisodeReadError,
    EpisodeSource,
    EpisodeType,
    MemoryStore,
    get_episode_store,
    preference_name_to_reference_name,
    split_reference_ref,
)
from lazymind.common.memory.models import build_episode_retry_fingerprint

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
_INTERNAL_MEMORY_METADATA = re.compile(r'\bschema_version\b', re.IGNORECASE)


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
    return _visible_memory_message(message)[:500]


def _visible_memory_message(message: object) -> str:
    return _INTERNAL_MEMORY_METADATA.sub(
        'internal memory metadata',
        str(message or ''),
    )


def _is_transient(exc: Exception) -> bool:
    if isinstance(
        exc,
        (
            ConnectionError,
            TimeoutError,
            requests.exceptions.ConnectionError,
            requests.exceptions.Timeout,
        ),
    ):
        return True
    message = str(exc).casefold()
    return any(marker in message for marker in _TRANSIENT_MARKERS)


def _is_timeout(exc: Exception) -> bool:
    message = str(exc).casefold()
    return (
        isinstance(exc, (TimeoutError, requests.exceptions.Timeout))
        or 'timed out' in message
        or 'timeout' in message
    )


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
        ledger_error = dict(error)
        if not ledger_error.get('code'):
            ledger_error['code'] = str(ledger_error.get('type') or 'write_failed')
        if not ledger_error.get('message'):
            ledger_error['message'] = str(
                ledger_error.get('reason') or 'A memory tool operation failed.'
            )
        entry['error'] = ledger_error
    entry['retryable'] = payload.get('retryable') is True
    ledger.append(entry)
    return payload


def _record_state_memory_result(
    payload: dict[str, Any],
    *,
    mutation: bool,
    store_result: dict[str, Any] | None = None,
) -> dict[str, Any]:
    ledger_result = payload.get('result')
    if not isinstance(ledger_result, dict) and isinstance(store_result, dict):
        details = {
            key: value
            for key, value in store_result.items()
            if key not in {'ok', 'error', 'type', 'content'}
        }
        ledger_result = details or None
    return _record_tool_result(
        payload,
        mutation=mutation,
        ledger_result=ledger_result if isinstance(ledger_result, dict) else None,
    )


def _log_tool_exception(tool: str, exc: Exception) -> None:
    lazyllm.LOG.error(
        f'[MemoryTools] {tool} failed: {type(exc).__name__}: '
        f'{_safe_exception_message(exc)}'
    )


def _memory_write_error(tool_name: str, message: str) -> Dict[str, Any]:
    text = _visible_memory_message(message).strip()
    return tool_error(tool_name, f'Failed to write via RemoteFS: {text}', error_type='store')


def _memory_applied(tool_name: str, **result: Any) -> Dict[str, Any]:
    return tool_success(tool_name, {'status': 'applied', **result})


def _memory_result_error(tool_name: str, result: Dict[str, Any]) -> Dict[str, Any]:
    error_type = str(result.get('type') or 'validation')
    reason = _visible_memory_message(
        result.get('error') or f'{tool_name} failed.'
    )
    if error_type == 'store':
        return _memory_write_error(tool_name, reason)
    return tool_error(tool_name, reason, error_type=error_type)


MAX_REFERENCE_READ_COUNT = 10
_CURRENT_MEMORY_PATHS = {
    'soul': SOUL_PATH,
    'profile': PROFILE_PATH,
    'preference': PREFERENCE_PATH,
}
_REFERENCE_COLLECTION_KEY = ('memory-reference-collection',)


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


def _read_memory_keys(arguments: dict[str, Any]):
    target = str(arguments.get('target') or '').strip().lower()
    return 'memory', _CURRENT_MEMORY_PATHS[target]


def _read_memory_reference_keys(arguments: dict[str, Any]):
    refs = _normalize_refs(arguments.get('refs'))
    return [
        _REFERENCE_COLLECTION_KEY,
        *[('memory', split_reference_ref(ref)[0]) for ref in refs],
    ]


def _read_single_reference(store: MemoryStore, raw_ref: str) -> dict[str, Any]:
    path, anchor = split_reference_ref(raw_ref)
    content = store.read_reference(raw_ref)
    return {
        'ref': raw_ref,
        'path': path,
        'anchor': anchor or None,
        'content': content,
        'content_length': len(content),
    }


class MemoryTools:
    """Persistent memory APIs for Chat and Memory Review agents."""

    __public_apis__ = [
        'read_memory',
        'read_memory_reference',
        'soul_editor',
        'profile_editor',
        'preference_editor',
        'episode_create',
    ]

    def __lazy_source__(self) -> bool:
        return False

    @tool_concurrency(read_keys=_read_memory_keys)
    def read_memory(
        self,
        target: Literal['soul', 'profile', 'preference'],
    ) -> Dict[str, Any]:
        """Read one complete current-memory YAML document.

        Use this when the exact current Soul, Profile, or Preference index is
        needed. Preference reference details remain available through
        ``read_memory_reference``.

        Args:
            target: One of ``soul``, ``profile``, or ``preference``.
        """
        raw_target = str(target or '').strip().lower()
        readers = {
            'soul': ('read_soul', SOUL_PATH),
            'profile': ('read_profile', PROFILE_PATH),
            'preference': ('read_preference', PREFERENCE_PATH),
        }
        if raw_target not in readers:
            return _record_state_memory_result(
                tool_error(
                    'read_memory',
                    "target must be 'soul', 'profile', or 'preference'.",
                    error_type='validation',
                ),
                mutation=False,
            )

        reader_name, path = readers[raw_target]
        try:
            content = getattr(MemoryStore(), reader_name)()
        except FileNotFoundError:
            return _record_state_memory_result(
                tool_error(
                    'read_memory',
                    f'{raw_target} document not found.',
                    error_type='not_found',
                ),
                mutation=False,
            )
        except (RuntimeError, ValueError) as exc:
            return _record_state_memory_result(
                tool_error(
                    'read_memory',
                    f'Failed to read {raw_target}: {_safe_exception_message(exc)}',
                    error_type='store',
                ),
                mutation=False,
            )
        except Exception as exc:
            return _record_state_memory_result(
                tool_error(
                    'read_memory',
                    f'Failed to read {raw_target}: {_safe_exception_message(exc)}',
                    error_type='store',
                ),
                mutation=False,
            )

        payload = tool_success('read_memory', {
            'target': raw_target,
            'path': path,
            'content': content,
            'content_length': len(content),
        })
        return _record_tool_result(
            payload,
            mutation=False,
            ledger_result={
                'target': raw_target,
                'path': path,
                'content_length': len(content),
            },
        )

    @tool_concurrency(read_keys=_read_memory_reference_keys)
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
            return _record_state_memory_result(
                tool_error('read_memory_reference', 'refs is required.'),
                mutation=False,
            )

        if len(normalized_refs) > MAX_REFERENCE_READ_COUNT:
            return _record_state_memory_result(
                tool_error(
                    'read_memory_reference',
                    f'At most {MAX_REFERENCE_READ_COUNT} refs may be read per call; '
                    f'got {len(normalized_refs)}.',
                ),
                mutation=False,
            )

        store = MemoryStore()
        items: list[dict[str, Any]] = []
        for raw_ref in normalized_refs:
            try:
                split_reference_ref(raw_ref)
            except ValueError as exc:
                return _record_state_memory_result(
                    tool_error(
                        'read_memory_reference',
                        f'Invalid ref {raw_ref!r}: {exc}',
                    ),
                    mutation=False,
                )
            try:
                items.append(_read_single_reference(store, raw_ref))
            except FileNotFoundError:
                return _record_state_memory_result(
                    tool_error(
                        'read_memory_reference',
                        f'Reference not found for ref={raw_ref!r}.',
                        error_type='not_found',
                    ),
                    mutation=False,
                )
            except RuntimeError as exc:
                return _record_state_memory_result(
                    tool_error(
                        'read_memory_reference',
                        f'Failed to read {raw_ref!r}: {exc}',
                        error_type='store',
                    ),
                    mutation=False,
                )
            except Exception as exc:
                return _record_state_memory_result(
                    tool_error(
                        'read_memory_reference',
                        f'Failed to read {raw_ref!r}: {exc}',
                        error_type='store',
                    ),
                    mutation=False,
                )

        payload = tool_success('read_memory_reference', {
            'items': items,
            'ref_count': len(items),
        })
        return _record_tool_result(
            payload,
            mutation=False,
            ledger_result={
                'ref_count': len(items),
            },
        )

    @tool_concurrency(write_keys=('memory', SOUL_PATH))
    def soul_editor(self, operations: List[Dict[str, Any]]) -> Dict[str, Any]:
        """Apply one atomic batch of operations to the agent Soul.

        Use this only when the user explicitly asks to change an Agent
        definition or stable behavior represented by an existing Soul field.
        Do not use it for user-specific facts; those belong in profile or
        preference editors. Include every Soul change from the current turn in
        one call. The loaded YAML determines editable leaf paths and operations:
        string/null leaves support ``set`` and ``clear``; string-list leaves
        support ``add``, ``remove``, and ``clear``.

        Args:
            operations: Non-empty list of operation mappings with ``op``,
                ``path``, and ``value`` when required.
        """
        if not isinstance(operations, list) or not operations:
            return _record_state_memory_result(
                tool_error(
                    'soul_editor',
                    'operations must be a non-empty list.',
                    error_type='validation',
                ),
                mutation=False,
            )

        result = MemoryStore().apply_soul_operations(operations)
        if not result.get('ok'):
            return _record_state_memory_result(
                _memory_result_error('soul_editor', result),
                mutation=result.get('type') == 'partial',
                store_result=result,
            )

        return _record_state_memory_result(
            _memory_applied(
                'soul_editor',
                operations=list(result.get('operations') or operations),
                change_count=len(operations),
                path=SOUL_PATH,
                content=result['content'],
                content_length=len(result['content']),
            ),
            mutation=True,
        )

    @tool_concurrency(write_keys=('memory', PROFILE_PATH))
    def profile_editor(self, operations: List[Dict[str, Any]]) -> Dict[str, Any]:
        """Apply one atomic batch of operations to the user Profile.

        Use this only for a current, stable user fact that the user explicitly
        states or corrects and that is represented by an existing Profile
        field. Do not infer a fact, and do not use this for long-form behavioral
        preferences; those belong in ``preference_editor``.
        Include every Profile change from the current turn in one call. The
        currently loaded YAML determines the editable leaf paths and operations.
        String and null fields support ``set`` and ``clear``; clearing a string
        writes an empty string. String-list fields support ``add``, ``remove``,
        and ``clear``. Operate on individual list values instead of replacing
        complete arrays.

        Args:
            operations: Non-empty list of operation mappings with ``op``,
                ``path``, and ``value`` when required.
        """
        if not isinstance(operations, list) or not operations:
            return _record_state_memory_result(
                tool_error(
                    'profile_editor',
                    'operations must be a non-empty list.',
                    error_type='validation',
                ),
                mutation=False,
            )

        result = MemoryStore().apply_profile_operations(operations)
        if not result.get('ok'):
            return _record_state_memory_result(
                _memory_result_error('profile_editor', result),
                mutation=result.get('type') == 'partial',
                store_result=result,
            )

        return _record_state_memory_result(
            _memory_applied(
                'profile_editor',
                operations=list(result.get('operations') or operations),
                change_count=len(operations),
                path=PROFILE_PATH,
                content=result['content'],
                content_length=len(result['content']),
            ),
            mutation=True,
        )

    @tool_concurrency(write_keys=[
        ('memory', PREFERENCE_PATH),
        _REFERENCE_COLLECTION_KEY,
    ])
    def preference_editor(
        self,
        op: Literal['add', 'delete'],
        name: str,
        summary: str = '',
        scenario: str = '',
        details: str = '',
        reason: str = '',
    ) -> Dict[str, Any]:
        """Add or delete a user preference index entry.

        Use this conservatively and only for a service preference the user
        explicitly states that is stable, long-term, and reusable across
        conversations. Do not save fragmented remarks, one-off requests,
        temporary task details, casual statements, or objective user facts.
        Objective user facts belong in Profile when a matching field exists.
        Each added entry writes ``preference.yaml`` and a matching reference
        file under ``memory/users/references/``.
        This tool cannot update or reorder preferences.
        Never delete and re-add an entry as an update because ordering is
        controlled only by the user.

        Args:
            op: ``add`` to create a new preference entry, or ``delete`` to remove.
            name: Preference identifier such as ``pref.response.concise``.
            summary: Short executable summary for the index. Required for ``add``.
            scenario: When the preference should apply. Required for ``add``.
            details: Detailed preference behavior. Required for ``add``.
            reason: Why the preference should be saved. Required for ``add``.
        """
        raw_op = str(op or '').strip().lower()
        raw_name = str(name or '').strip()
        if raw_op not in {'add', 'delete'}:
            return _record_state_memory_result(
                tool_error(
                    'preference_editor',
                    "op must be 'add' or 'delete'.",
                    error_type='validation',
                ),
                mutation=False,
            )
        if not raw_name:
            return _record_state_memory_result(
                tool_error('preference_editor', 'name is required.', error_type='validation'),
                mutation=False,
            )

        store = MemoryStore()
        if raw_op == 'add':
            config = _agentic_config()
            source_kind = str(
                config.get('memory_source_kind')
                or config.get('episode_source_kind')
                or ''
            ).strip()
            conversation_id = str(config.get('conversation_id') or '').strip()
            if source_kind not in {'chat_explicit', 'memory_review'}:
                return _record_state_memory_result(
                    tool_error(
                        'preference_editor',
                        (
                            'memory_source_kind must be set by runtime to either '
                            "'chat_explicit' or 'memory_review'."
                        ),
                        error_type='missing_context',
                    ),
                    mutation=False,
                )
            if not conversation_id:
                return _record_state_memory_result(
                    tool_error(
                        'preference_editor',
                        'conversation_id must be set by runtime.',
                        error_type='missing_context',
                    ),
                    mutation=False,
                )
            result = store.add_preference_with_reference(
                name=raw_name,
                summary=summary,
                scenario=scenario,
                details=details,
                reason=reason,
                source_kind=source_kind,
                conversation_id=conversation_id,
            )
            if not result.get('ok'):
                return _record_state_memory_result(
                    _memory_result_error('preference_editor', result),
                    mutation=result.get('type') == 'partial',
                    store_result=result,
                )
            item = result['item']
            return _record_state_memory_result(
                _memory_applied(
                    'preference_editor',
                    op='add',
                    name=item.name,
                    summary=item.summary,
                    ref=item.ref,
                    path=PREFERENCE_PATH,
                    reference_name=preference_name_to_reference_name(item.name),
                ),
                mutation=True,
            )

        result = store.remove_preference_with_reference(raw_name)
        if not result.get('ok'):
            return _record_state_memory_result(
                _memory_result_error('preference_editor', result),
                mutation=result.get('type') == 'partial',
                store_result=result,
            )
        item = result['item']
        return _record_state_memory_result(
            _memory_applied(
                'preference_editor',
                op='delete',
                name=item.name,
                ref=item.ref,
                path=PREFERENCE_PATH,
                reference_name=preference_name_to_reference_name(item.name),
            ),
            mutation=True,
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

        source_kind = str(config.get('episode_source_kind') or '').strip()
        if not source_kind:
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'episode_create',
                    'error': {
                        'code': 'missing_context',
                        'message': 'episode_source_kind is required in agentic_config.',
                        'detail': {'field': 'episode_source_kind'},
                    },
                    'retryable': False,
                },
                mutation=False,
            )
        if source_kind not in {'chat_explicit', 'memory_review'}:
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'episode_create',
                    'error': {
                        'code': 'invalid_arguments',
                        'message': (
                            'episode_source_kind must be either '
                            "'chat_explicit' or 'memory_review'."
                        ),
                        'detail': {'field': 'episode_source_kind'},
                    },
                    'retryable': False,
                },
                mutation=False,
            )

        timestamp_field = 'episode_occurred_at_ms'
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
                episode_type=EpisodeType(episode_type),
                summary=summary,
                source=EpisodeSource(
                    kind=source_kind,
                    conversation_id=values['conversation_id'],
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

        retry_fingerprint = build_episode_retry_fingerprint(
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
                    'retry_fingerprint': retry_fingerprint,
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
                    'retry_fingerprint': retry_fingerprint,
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
                    'retry_fingerprint': retry_fingerprint,
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
                'retry_fingerprint': retry_fingerprint,
            },
        )


class MemoryReviewEpisodeTools:
    """Episode maintenance tools available only to the Memory Review agent."""

    __public_apis__ = [
        'episode_search',
        'episode_delete',
    ]

    def __lazy_source__(self) -> bool:
        return False

    @staticmethod
    def _review_context(tool_name: str) -> tuple[str, Dict[str, Any] | None]:
        config = _agentic_config()
        source_kind = str(
            config.get('memory_source_kind')
            or config.get('episode_source_kind')
            or ''
        ).strip()
        if source_kind != 'memory_review':
            return '', tool_error(
                tool_name,
                'This tool is available only during Memory Review.',
                error_type='missing_context',
            )
        user_id = str(config.get('user_id') or '').strip()
        if not user_id:
            return '', tool_error(
                tool_name,
                'user_id is required in agentic_config.',
                error_type='missing_context',
            )
        return user_id, None

    def episode_search(self, query: str, limit: int = 20) -> Dict[str, Any]:
        """Search the current user's Episodes for Preference deduplication.

        Call this exactly once after each successful Preference addition, using
        the Preference's executable summary plus distinctive scenario terms.
        Search results are candidates only; compare their complete summaries
        before deciding whether any Episode is a pure duplicate. This
        maintenance search does not increment Episode hit counts.

        Args:
            query: Preference summary plus distinctive application-scenario terms.
            limit: Maximum results to return, between 1 and 20.
        """
        user_id, context_error = self._review_context('episode_search')
        if context_error is not None:
            return _record_tool_result(
                context_error,
                mutation=False,
            )
        normalized_query = str(query or '').strip()
        if not normalized_query:
            return _record_tool_result(
                tool_error(
                    'episode_search',
                    'query is required.',
                    error_type='validation',
                ),
                mutation=False,
            )
        if isinstance(limit, bool) or not isinstance(limit, int) or not 1 <= limit <= 20:
            return _record_tool_result(
                tool_error(
                    'episode_search',
                    'limit must be an integer between 1 and 20.',
                    error_type='validation',
                ),
                mutation=False,
            )

        try:
            results = get_episode_store().search(user_id, normalized_query)[:limit]
        except Exception as raw_exc:
            exc = (
                raw_exc
                if isinstance(raw_exc, EpisodeReadError)
                else EpisodeReadError.from_exception(raw_exc)
            )
            root_exc = exc.__cause__ if isinstance(exc.__cause__, Exception) else raw_exc
            _log_tool_exception('episode_search', root_exc)
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'episode_search',
                    'error': {
                        'code': exc.code,
                        'message': (
                            'Episode storage is temporarily unavailable.'
                            if exc.retryable
                            else 'Failed to search Episodes.'
                        ),
                        'detail': {'exception_type': type(root_exc).__name__},
                    },
                    'retryable': exc.retryable,
                },
                mutation=False,
            )

        items = [
            {
                'id': result.episode.id,
                'summary': result.episode.summary,
                'episode_type': result.episode.episode_type.value,
                'occurred_at_ms': result.episode.occurred_at_ms,
                'conversation_id': result.episode.source.conversation_id,
                'score': result.score,
            }
            for result in results
        ]
        return _record_tool_result(
            tool_success(
                'episode_search',
                {
                    'items': items,
                    'candidate_count': len(items),
                },
            ),
            mutation=False,
            ledger_result={'candidate_count': len(items)},
        )

    def episode_delete(self, episode_id: str) -> Dict[str, Any]:
        """Delete one pure-duplicate Episode during Memory Review.

        Use only after ``episode_search`` returned the Episode and comparison
        proves that it merely restates a newly added Preference. Keep the
        Episode whenever it contains independent time, reason, result, phase
        transition, blocker, or other historical context.

        Args:
            episode_id: Exact Episode ID returned by ``episode_search``.
        """
        user_id, context_error = self._review_context('episode_delete')
        if context_error is not None:
            return _record_tool_result(
                context_error,
                mutation=False,
            )
        normalized_episode_id = str(episode_id or '').strip()
        if not normalized_episode_id:
            return _record_tool_result(
                tool_error(
                    'episode_delete',
                    'episode_id is required.',
                    error_type='validation',
                ),
                mutation=False,
            )
        retry_fingerprint = f'episode_delete:{normalized_episode_id}'

        try:
            result = get_episode_store().delete(user_id, normalized_episode_id)
        except Exception as raw_exc:
            exc = (
                raw_exc
                if isinstance(raw_exc, EpisodeReadError)
                else EpisodeReadError.from_exception(raw_exc)
            )
            root_exc = exc.__cause__ if isinstance(exc.__cause__, Exception) else raw_exc
            _log_tool_exception('episode_delete', root_exc)
            return _record_tool_result(
                {
                    'success': False,
                    'tool': 'episode_delete',
                    'error': {
                        'code': (
                            'storage_unavailable'
                            if exc.retryable
                            else 'storage_failed'
                        ),
                        'message': (
                            'Episode storage is temporarily unavailable.'
                            if exc.retryable
                            else 'Failed to delete Episode.'
                        ),
                        'detail': {'exception_type': type(root_exc).__name__},
                    },
                    'retryable': exc.retryable,
                },
                mutation=False,
                ledger_result={
                    'status': 'failed',
                    'retry_fingerprint': retry_fingerprint,
                },
            )

        payload = result.model_dump(mode='json')
        return _record_tool_result(
            {
                'success': True,
                'tool': 'episode_delete',
                'result': payload,
                'retryable': False,
            },
            mutation=result.status == 'deleted',
            ledger_result={
                **payload,
                'retry_fingerprint': retry_fingerprint,
            },
        )
