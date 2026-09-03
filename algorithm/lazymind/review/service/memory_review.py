from __future__ import annotations

from time import time_ns
from typing import Any, Dict, List, Optional

from lazymind.common.maintenance import initialize_context, check_cancelled

import lazyllm
from lazyllm import AutoModel, LOG
from lazyllm.tools.fs.client import FS
from lazymind.review.memory_review.schemas import MemoryReviewError, MemoryReviewResult

from lazymind.chat.engine.tools.memory import MemoryReviewEpisodeTools, MemoryTools
from lazymind.chat.service.component.history import normalize_history_for_agent
from lazymind.common.memory import EpisodeReadError, get_episode_store, load_memory_context
from lazymind.config import config as _cfg
from lazymind.model_config import inject_model_config
from lazymind.review.memory_review.prompts import build_memory_review_prompt


_WRITE_TOOLS = frozenset(
    {
        'soul_editor',
        'profile_editor',
        'preference_editor',
        'episode_create',
        'episode_delete',
    }
)
_SAFE_REVIEW_ERROR_MESSAGES = {
    'preference_organizing': ('Preference maintenance is running; the new preference was not saved.'),
    'partial_failure': 'A memory operation was only partially applied and requires reconciliation.',
    'storage_unavailable': 'Persistent memory storage is temporarily unavailable.',
    'storage_read_failed': 'Persistent memory storage could not be read.',
    'storage_timeout': 'A memory write timed out; completion is unknown.',
    'storage_failed': 'Persistent memory storage could not complete the operation.',
    'invalid_arguments': 'A memory tool rejected invalid arguments.',
    'missing_context': 'Required context for the memory operation is missing.',
    'write_failed': 'A memory write failed.',
}


def _truncate_log_text(value: Any, limit: int = 4000) -> str:
    text = str(value)
    if len(text) <= limit:
        return text
    return f'{text[:limit]}...<truncated {len(text) - limit} chars>'


def _write_retry_fingerprint(entry: Dict[str, Any]) -> tuple[str, str] | None:
    key = str(entry.get('retry_fingerprint') or '').strip()
    if not key:
        return None
    return str(entry.get('operation') or ''), key


def _unresolved_write_failures(write_results: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    later_success_keys: set[tuple[str, str]] = set()
    unresolved_reversed: List[Dict[str, Any]] = []
    for entry in reversed(write_results):
        key = _write_retry_fingerprint(entry)
        if entry.get('status') == 'succeeded':
            if key is not None:
                later_success_keys.add(key)
        elif key is None or key not in later_success_keys:
            unresolved_reversed.append(entry)
    return list(reversed(unresolved_reversed))


def _summarize_tool_errors(
    failures: List[Dict[str, Any]],
    *,
    multiple_code: str = 'multiple_write_failures',
) -> MemoryReviewError:
    codes: List[str] = []
    messages: List[str] = []
    for entry in failures:
        code = str(entry.get('error_code') or 'write_failed')
        message = _SAFE_REVIEW_ERROR_MESSAGES.get(code, 'A memory tool failed.')
        if code not in codes:
            codes.append(code)
        if message not in messages:
            messages.append(message)
    return MemoryReviewError(
        code=codes[0] if len(codes) == 1 else multiple_code,
        message=' | '.join(messages),
    )


def _multiple_failure_code(failures: List[Dict[str, Any]]) -> str:
    tools = {str(entry.get('operation') or '') for entry in failures}
    if tools.issubset(_WRITE_TOOLS):
        return 'multiple_write_failures'
    if tools and tools.isdisjoint(_WRITE_TOOLS):
        return 'multiple_read_failures'
    return 'multiple_tool_failures'


def review_memory(
    task_id: str,
    run_id: str,
    user_id: str,
    conversation_id: str,
    history: List[Dict[str, Any]],
    llm_config: Optional[Dict[str, Any]] = None,
    conversation_last_active_at_ms: Optional[int] = None,
) -> MemoryReviewResult:
    review_started_at_ms = time_ns() // 1_000_000
    episode_occurred_at_ms = (
        conversation_last_active_at_ms
        if (
            isinstance(conversation_last_active_at_ms, int)
            and not isinstance(conversation_last_active_at_ms, bool)
            and conversation_last_active_at_ms > 0
        )
        else review_started_at_ms
    )
    initialize_context(task_id, run_id, user_id)
    lazyllm.set_trace_context(
        {
            'trace_id': conversation_id,
            'session_id': conversation_id,
            'user_id': user_id,
            'sampled': True,
            'request_tags': ['memory_review'],
            'module_trace': {
                'by_class': {
                    'FunctionCall': False,
                    'ToolManager': False,
                    'Pipeline': False,
                    'Diverter': False,
                },
                'by_name': {
                    '_build_history': False,
                    '_post_action': False,
                    '_safe_call': False,
                    '_indexed_call': False,
                },
            },
            'trace_metadata': {
                'task_id': task_id,
                'conversation_id': conversation_id,
                'trigger': 'conversation_idle',
                'history_len': len(history),
            },
        }
    )
    inject_model_config(llm_config)
    LOG.info(
        f'[MemoryReview] review started: user_id={user_id} '
        f'task_id={task_id} history_len={len(history)} '
        f'has_llm_config={bool(llm_config)}'
    )

    config: Dict[str, Any] = {
        **lazyllm.globals['agentic_config'],
        'user_id': user_id,
        'task_id': task_id,
        'conversation_id': conversation_id,
        'episode_occurred_at_ms': episode_occurred_at_ms,
        'episode_source_kind': 'memory_review',
        'memory_source_kind': 'memory_review',
        'memory_operation_ledger': [],
    }
    lazyllm.globals['agentic_config'] = config

    try:
        existing_episodes = get_episode_store().list_by_conversation(
            user_id,
            conversation_id,
        )
    except Exception as raw_exc:
        exc = raw_exc if isinstance(raw_exc, EpisodeReadError) else EpisodeReadError.from_exception(raw_exc)
        LOG.exception(
            f'[MemoryReview] failed to load existing Episodes: '
            f'user_id={user_id} task_id={task_id} conversation_id={conversation_id}: '
            f'{raw_exc}'
        )
        return MemoryReviewResult(
            status='failed',
            task_id=task_id,
            outcome='failed',
            retryable=exc.retryable,
            error=MemoryReviewError(
                code=exc.code,
                message=_SAFE_REVIEW_ERROR_MESSAGES[exc.code],
            ),
        )
    try:
        memory_context = load_memory_context(project_preference=False)
    except Exception as raw_exc:
        exc = EpisodeReadError.from_exception(raw_exc)
        LOG.exception(
            f'[MemoryReview] failed to load fixed Memory files: user_id={user_id} task_id={task_id}: {raw_exc}'
        )
        return MemoryReviewResult(
            status='failed',
            task_id=task_id,
            outcome='failed',
            retryable=exc.retryable,
            error=MemoryReviewError(
                code=exc.code,
                message=_SAFE_REVIEW_ERROR_MESSAGES[exc.code],
            ),
        )
    prompt = build_memory_review_prompt(
        existing_episodes,
        soul=memory_context.soul,
        profile=memory_context.profile,
        preference=memory_context.preference,
    )

    llm = AutoModel(model='llm')
    review_agent = lazyllm.tools.agent.ReactAgent(
        llm=llm,
        tools=[MemoryTools(), MemoryReviewEpisodeTools()],
        max_retries=_cfg['review_max_retries'],
        return_trace=False,
        prompt=' ',
        keep_full_turns=3,
        history_compactor=lambda history, _keep=0, **_: list(history),
        fs=FS,
        enable_builtin_tools=False,
        force_summarize=True,
        extra_stop_condition=check_cancelled,
    )
    lazyllm.locals['_lazyllm_agent'] = {}
    res = review_agent(
        prompt,
        llm_chat_history=normalize_history_for_agent(history),
    )
    LOG.info(
        f'[MemoryReview] review finished: user_id={user_id} '
        f'task_id={task_id} history_len={len(history)} '
        f'has_llm_config={bool(llm_config)} '
        f'res={_truncate_log_text(res)!r}'
    )
    ledger = [entry for entry in config['memory_operation_ledger'] if isinstance(entry, dict)]
    write_results = [entry for entry in ledger if entry.get('operation') in _WRITE_TOOLS]
    successful_writes = [entry for entry in write_results if entry.get('status') == 'succeeded']
    applied_writes = [entry for entry in successful_writes if entry.get('mutation') == 'applied']
    mutated_writes = [entry for entry in write_results if entry.get('mutation') in {'applied', 'unknown'}]
    failed_writes = _unresolved_write_failures(write_results)
    read_failures = [
        entry for entry in ledger if entry.get('operation') not in _WRITE_TOOLS and entry.get('status') != 'succeeded'
    ]
    unresolved_ids = {id(entry) for entry in [*failed_writes, *read_failures]}
    unresolved_failures = [entry for entry in ledger if id(entry) in unresolved_ids]
    if applied_writes and not unresolved_failures:
        return MemoryReviewResult(
            status='success',
            task_id=task_id,
            outcome='saved',
        )
    if mutated_writes and unresolved_failures:
        return MemoryReviewResult(
            status='failed',
            task_id=task_id,
            outcome='partial',
            retryable=False,
            error=_summarize_tool_errors(
                unresolved_failures,
                multiple_code=_multiple_failure_code(unresolved_failures),
            ),
        )
    if successful_writes and not unresolved_failures:
        return MemoryReviewResult(
            status='success',
            task_id=task_id,
            outcome='no_changes',
        )
    if unresolved_failures:
        retryable = (
            all(entry.get('retryable') is True for entry in unresolved_failures)
            and not applied_writes
            and all(entry.get('mutation') == 'none' for entry in unresolved_failures)
        )
        return MemoryReviewResult(
            status='failed',
            task_id=task_id,
            outcome='failed',
            retryable=retryable,
            error=_summarize_tool_errors(
                unresolved_failures,
                multiple_code=_multiple_failure_code(unresolved_failures),
            ),
        )
    if not write_results and str(res).lstrip().startswith('Nothing to save'):
        return MemoryReviewResult(
            status='success',
            task_id=task_id,
            outcome='no_changes',
        )
    return MemoryReviewResult(
        status='failed',
        task_id=task_id,
        outcome='failed',
        error=MemoryReviewError(
            code='no_write_decision',
            message=("Memory Review completed without a write tool call or an explicit 'Nothing to save' decision."),
        ),
    )
