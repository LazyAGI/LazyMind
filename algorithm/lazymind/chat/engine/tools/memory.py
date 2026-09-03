import re

from typing import Any, Dict, List, Literal, Union

import lazyllm
import requests
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools import fc_register
from pydantic import ValidationError

from lazymind.common.memory import (
    PREFERENCE_PATH,
    PROFILE_PATH,
    SOUL_PATH,
    EpisodeCreateInput,
    EpisodeReadError,
    EpisodeSource,
    EpisodeType,
    MemoryPartialApplyError,
    MemoryStore,
    MemoryOperationRecord,
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


def _record_memory_operation(
    operation: str,
    value: Any = None,
    *,
    mutation: bool | None,
    error: ToolExecutionError | None = None,
    error_code: str | None = None,
    retryable: bool = False,
    retry_fingerprint: str | None = None,
    ledger_result: dict[str, Any] | None = None,
) -> Any:
    config = _agentic_config()
    ledger = config.get('memory_operation_ledger')
    if not isinstance(ledger, list):
        ledger = []
        config['memory_operation_ledger'] = ledger
    record = MemoryOperationRecord(
        operation=operation,
        status='failed' if error else 'succeeded',
        mutation='unknown' if mutation is None else ('applied' if mutation else 'none'),
        error_code=error_code if error else None,
        retryable=retryable,
        retry_fingerprint=retry_fingerprint,
        result=ledger_result,
    )
    ledger.append(record.model_dump(mode='json', exclude_none=True))
    if error:
        raise error
    return value


def _log_tool_exception(tool: str, exc: Exception) -> None:
    lazyllm.LOG.error(
        f'[MemoryTools] {tool} failed: {type(exc).__name__}: '
        f'{_safe_exception_message(exc)}'
    )


def _memory_storage_error(message: str) -> ToolExecutionError:
    text = _visible_memory_message(message).strip()
    return ToolExecutionError(f'Memory storage operation failed: {text}')


def _memory_applied(**result: Any) -> Dict[str, Any]:
    return {'status': 'applied', **result}


def _record_memory_editor_exception(tool_name: str, exc: Exception) -> Any:
    mutation = False
    ledger_result: dict[str, Any] | None = None

    if isinstance(exc, MemoryPartialApplyError):
        mutation = True
        error = ToolExecutionError(_visible_memory_message(str(exc)))
        error_code = 'partial_failure'
        ledger_result = {
            'operation': exc.operation,
            'applied': list(exc.applied),
            'failed': list(exc.failed),
        }
        item_name = getattr(exc.item, 'name', None)
        if item_name:
            ledger_result['item_name'] = str(item_name)
    elif isinstance(exc, (ValueError, FileNotFoundError)):
        error = ToolExecutionError(_visible_memory_message(str(exc)))
        error_code = 'invalid_arguments'
    elif isinstance(exc, RuntimeError) and 'preference_organizing' in str(exc):
        error = ToolExecutionError(
            'Preference Organizer is running. This preference write was not saved; '
            'do not retry it in this run.'
        )
        error_code = 'preference_organizing'
        ledger_result = {'status': 'blocked', 'mutation': 'none'}
    elif isinstance(exc, RuntimeError):
        error = _memory_storage_error(str(exc))
        error_code = 'storage_failed'
    else:
        _log_tool_exception(tool_name, exc)
        error = _memory_storage_error(_safe_exception_message(exc))
        error_code = 'storage_failed'

    return _record_memory_operation(
        tool_name,
        mutation=mutation,
        error=error,
        error_code=error_code,
        ledger_result=ledger_result,
    )


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

    @fc_register(read_keys=_read_memory_keys)
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
            return _record_memory_operation(
                'read_memory',
                mutation=False,
                error=ToolExecutionError(
                    "target must be 'soul', 'profile', or 'preference'."
                ),
                error_code='invalid_arguments',
            )

        reader_name, path = readers[raw_target]
        try:
            content = getattr(MemoryStore(), reader_name)()
        except FileNotFoundError:
            return _record_memory_operation(
                'read_memory',
                mutation=False,
                error=ToolExecutionError(
                    f'{raw_target} memory document was not found.'
                ),
                error_code='storage_read_failed',
            )
        except (RuntimeError, ValueError) as exc:
            return _record_memory_operation(
                'read_memory',
                mutation=False,
                error=ToolExecutionError(
                    f'Failed to read {raw_target}: {_safe_exception_message(exc)}'
                ),
                error_code='storage_read_failed',
            )
        except Exception as exc:
            return _record_memory_operation(
                'read_memory',
                mutation=False,
                error=ToolExecutionError(
                    f'Failed to read {raw_target}: {_safe_exception_message(exc)}'
                ),
                error_code='storage_read_failed',
            )

        payload = {
            'target': raw_target,
            'path': path,
            'content': content,
            'content_length': len(content),
        }
        return _record_memory_operation(
            'read_memory',
            payload,
            mutation=False,
            ledger_result={
                'target': raw_target,
                'path': path,
                'content_length': len(content),
            },
        )

    @fc_register(read_keys=_read_memory_reference_keys)
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
            return _record_memory_operation(
                'read_memory_reference',
                mutation=False,
                error=ToolExecutionError('refs is required.'),
                error_code='invalid_arguments',
            )

        if len(normalized_refs) > MAX_REFERENCE_READ_COUNT:
            return _record_memory_operation(
                'read_memory_reference',
                mutation=False,
                error=ToolExecutionError(
                    f'At most {MAX_REFERENCE_READ_COUNT} refs may be read per call; '
                    f'got {len(normalized_refs)}.',
                ),
                error_code='invalid_arguments',
            )

        store = MemoryStore()
        items: list[dict[str, Any]] = []
        for raw_ref in normalized_refs:
            try:
                split_reference_ref(raw_ref)
            except ValueError as exc:
                return _record_memory_operation(
                    'read_memory_reference',
                    mutation=False,
                    error=ToolExecutionError(
                        f'Invalid ref {raw_ref!r}: {exc}',
                    ),
                    error_code='invalid_arguments',
                )
            try:
                items.append(_read_single_reference(store, raw_ref))
            except FileNotFoundError:
                return _record_memory_operation(
                    'read_memory_reference',
                    mutation=False,
                    error=ToolExecutionError(
                        f'Reference not found for ref={raw_ref!r}.'
                    ),
                    error_code='storage_read_failed',
                )
            except RuntimeError as exc:
                return _record_memory_operation(
                    'read_memory_reference',
                    mutation=False,
                    error=ToolExecutionError(
                        f'Failed to read {raw_ref!r}: {exc}'
                    ),
                    error_code='storage_read_failed',
                )
            except Exception as exc:
                return _record_memory_operation(
                    'read_memory_reference',
                    mutation=False,
                    error=ToolExecutionError(
                        f'Failed to read {raw_ref!r}: {exc}'
                    ),
                    error_code='storage_read_failed',
                )

        payload = {
            'items': items,
            'ref_count': len(items),
        }
        return _record_memory_operation(
            'read_memory_reference',
            payload,
            mutation=False,
            ledger_result={
                'ref_count': len(items),
            },
        )

    @fc_register(write_keys=('memory', SOUL_PATH))
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
            return _record_memory_operation(
                'soul_editor',
                mutation=False,
                error=ToolExecutionError(
                    'operations must be a non-empty list.'
                ),
                error_code='invalid_arguments',
            )

        try:
            result = MemoryStore().apply_soul_operations(operations)
        except Exception as exc:
            return _record_memory_editor_exception('soul_editor', exc)

        value = _memory_applied(
            operations=list(result.get('operations') or operations),
            change_count=len(operations),
            path=SOUL_PATH,
            content=result['content'],
            content_length=len(result['content']),
        )
        return _record_memory_operation(
            'soul_editor', value,
            mutation=True,
            ledger_result={'status': 'applied'},
        )

    @fc_register(write_keys=('memory', PROFILE_PATH))
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
            return _record_memory_operation(
                'profile_editor',
                mutation=False,
                error=ToolExecutionError(
                    'operations must be a non-empty list.'
                ),
                error_code='invalid_arguments',
            )

        try:
            result = MemoryStore().apply_profile_operations(operations)
        except Exception as exc:
            return _record_memory_editor_exception('profile_editor', exc)

        value = _memory_applied(
            operations=list(result.get('operations') or operations),
            change_count=len(operations),
            path=PROFILE_PATH,
            content=result['content'],
            content_length=len(result['content']),
        )
        return _record_memory_operation(
            'profile_editor', value,
            mutation=True,
            ledger_result={'status': 'applied'},
        )

    @fc_register(write_keys=[
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
            return _record_memory_operation(
                'preference_editor',
                mutation=False,
                error=ToolExecutionError(
                    "op must be 'add' or 'delete'."
                ),
                error_code='invalid_arguments',
            )
        if not raw_name:
            return _record_memory_operation(
                'preference_editor',
                mutation=False,
                error=ToolExecutionError(
                    'name is required.'
                ),
                error_code='invalid_arguments',
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
                return _record_memory_operation(
                    'preference_editor',
                    mutation=False,
                    error=ToolExecutionError(
                        (
                            'memory_source_kind must be set by runtime to either '
                            "'chat_explicit' or 'memory_review'."
                        )
                    ),
                    error_code='missing_context',
                )
            if not conversation_id:
                return _record_memory_operation(
                    'preference_editor',
                    mutation=False,
                    error=ToolExecutionError(
                        'conversation_id must be set by runtime.'
                    ),
                    error_code='missing_context',
                )
            try:
                item = store.add_preference_with_reference(
                    name=raw_name,
                    summary=summary,
                    scenario=scenario,
                    details=details,
                    reason=reason,
                    source_kind=source_kind,
                    conversation_id=conversation_id,
                )
            except Exception as exc:
                return _record_memory_editor_exception('preference_editor', exc)
            value = _memory_applied(
                op='add',
                name=item.name,
                summary=item.summary,
                ref=item.ref,
                path=PREFERENCE_PATH,
                reference_name=preference_name_to_reference_name(item.name),
            )
            return _record_memory_operation(
                'preference_editor', value,
                mutation=True,
                ledger_result={'status': 'applied'},
            )

        try:
            item = store.remove_preference_with_reference(raw_name)
        except Exception as exc:
            return _record_memory_editor_exception('preference_editor', exc)
        value = _memory_applied(
            op='delete',
            name=item.name,
            ref=item.ref,
            path=PREFERENCE_PATH,
            reference_name=preference_name_to_reference_name(item.name),
        )
        return _record_memory_operation(
            'preference_editor', value,
            mutation=True,
            ledger_result={'status': 'applied'},
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
                return _record_memory_operation(
                    'episode_create',
                    mutation=False,
                    error=ToolExecutionError(f'{field} is required in agentic_config.'),
                    error_code='missing_context',
                )
            values[field] = value

        source_kind = str(config.get('episode_source_kind') or '').strip()
        if not source_kind:
            return _record_memory_operation(
                'episode_create',
                mutation=False,
                error=ToolExecutionError(
                    'episode_source_kind is required in agentic_config.'
                ),
                error_code='missing_context',
            )
        if source_kind not in {'chat_explicit', 'memory_review'}:
            return _record_memory_operation(
                'episode_create',
                mutation=False,
                error=ToolExecutionError(
                    "episode_source_kind must be either 'chat_explicit' or 'memory_review'."
                ),
                error_code='invalid_arguments',
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
            return _record_memory_operation(
                'episode_create',
                mutation=False,
                error=ToolExecutionError(
                    f'{timestamp_field} is required in agentic_config.'
                ),
                error_code='missing_context',
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
            return _record_memory_operation(
                'episode_create',
                mutation=False,
                error=ToolExecutionError(
                    f'Invalid Episode arguments for episode_type={episode_type!r}: '
                    f'{_safe_exception_message(exc)}'
                ),
                error_code='invalid_arguments',
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
            return _record_memory_operation(
                'episode_create',
                mutation=False,
                retryable=transient,
                retry_fingerprint=retry_fingerprint,
                error=ToolExecutionError(
                    'Episode storage is temporarily unavailable.'
                    if transient else 'Failed to initialize Episode storage.'
                ),
                error_code='storage_unavailable' if transient else 'storage_failed',
            )

        try:
            create_result = store.create(values['user_id'], item)
        except EpisodeReadError as exc:
            root_exc = exc.__cause__ if isinstance(exc.__cause__, Exception) else exc
            _log_tool_exception('episode_create', root_exc)
            return _record_memory_operation(
                'episode_create',
                mutation=False,
                retryable=exc.retryable,
                retry_fingerprint=retry_fingerprint,
                error=ToolExecutionError(
                    'Episode storage is temporarily unavailable.'
                    if exc.retryable else 'Failed to read existing Episodes.'
                ),
                error_code=exc.code,
            )
        except Exception as exc:
            _log_tool_exception('episode_create', exc)
            safe_message = _safe_exception_message(exc)
            timed_out = _is_timeout(exc)
            return _record_memory_operation(
                'episode_create',
                mutation=None,
                retryable=timed_out,
                retry_fingerprint=retry_fingerprint,
                error=ToolExecutionError(
                    'Episode storage timed out and write completion is unknown: '
                    f'{safe_message}' if timed_out else f'Failed to create Episode: {safe_message}'
                ),
                error_code='storage_timeout' if timed_out else 'storage_failed',
            )

        result = create_result.model_dump(mode='json')
        return _record_memory_operation(
            'episode_create', result,
            mutation=create_result.status == 'created',
            retry_fingerprint=retry_fingerprint,
            ledger_result={'status': create_result.status},
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
    def _review_context() -> tuple[str, ToolExecutionError | None]:
        config = _agentic_config()
        source_kind = str(
            config.get('memory_source_kind')
            or config.get('episode_source_kind')
            or ''
        ).strip()
        if source_kind != 'memory_review':
            return '', ToolExecutionError(
                'This tool is available only during Memory Review.'
            )
        user_id = str(config.get('user_id') or '').strip()
        if not user_id:
            return '', ToolExecutionError(
                'user_id is required in agentic_config.'
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
        user_id, context_error = self._review_context()
        if context_error is not None:
            return _record_memory_operation(
                'episode_search',
                mutation=False,
                error=context_error,
                error_code='missing_context',
            )
        normalized_query = str(query or '').strip()
        if not normalized_query:
            return _record_memory_operation(
                'episode_search',
                mutation=False,
                error=ToolExecutionError(
                    'query is required.'
                ),
                error_code='invalid_arguments',
            )
        if isinstance(limit, bool) or not isinstance(limit, int) or not 1 <= limit <= 20:
            return _record_memory_operation(
                'episode_search',
                mutation=False,
                error=ToolExecutionError(
                    'limit must be an integer between 1 and 20.'
                ),
                error_code='invalid_arguments',
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
            return _record_memory_operation(
                'episode_search',
                mutation=False,
                retryable=exc.retryable,
                error=ToolExecutionError(
                    'Episode storage is temporarily unavailable.'
                    if exc.retryable else 'Failed to search Episodes.'
                ),
                error_code=exc.code,
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
        value = {'items': items, 'candidate_count': len(items)}
        return _record_memory_operation(
            'episode_search', value,
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
        user_id, context_error = self._review_context()
        if context_error is not None:
            return _record_memory_operation(
                'episode_delete',
                mutation=False,
                error=context_error,
                error_code='missing_context',
            )
        normalized_episode_id = str(episode_id or '').strip()
        if not normalized_episode_id:
            return _record_memory_operation(
                'episode_delete',
                mutation=False,
                error=ToolExecutionError(
                    'episode_id is required.'
                ),
                error_code='invalid_arguments',
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
            return _record_memory_operation(
                'episode_delete',
                mutation=False,
                retryable=exc.retryable,
                retry_fingerprint=retry_fingerprint,
                error=ToolExecutionError(
                    'Episode storage is temporarily unavailable.'
                    if exc.retryable else 'Failed to delete Episode.'
                ),
                error_code='storage_unavailable' if exc.retryable else 'storage_failed',
            )

        payload = result.model_dump(mode='json')
        return _record_memory_operation(
            'episode_delete', payload,
            mutation=result.status == 'deleted',
            retry_fingerprint=retry_fingerprint,
            ledger_result=payload,
        )
