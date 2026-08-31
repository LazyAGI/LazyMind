from __future__ import annotations

import hashlib
import json
import time
from collections import Counter
from dataclasses import dataclass
from typing import Any

import lazyllm
from lazyllm.tools.agent import (
    PreparedToolCall,
    RuntimeContext,
    RuntimeDelta,
    ToolExecutionBatch,
    ToolExecutionRecord,
)
from lazyllm.tools.agent.toolError import tool_failure

from lazymind.chat.engine.tools.session_env import redact_session_env_arguments
from .telemetry import append_event, emit_tool_call, emit_tool_result


_EXPANDED_BUDGET_TOOLS = {
    'advance_step',
    'advance_step_and_hand_off',
    'create_workflow_draft',
    'create_subagent',
}
_MAX_TOOL_LOG_CHARS = 800
_RESULT_LOG_KEYS = (
    'target', 'display_name', 'kind', 'file_id', 'offset', 'end_line',
    'total_lines', 'eof', 'next_offset', 'limit', 'pattern', 'total',
    'truncated', 'status', 'filename', 'corpus', 'skipped', 'channels',
)
_REPEATED_CALL_THRESHOLD = 3


def _requires_expanded_budget(tool_name: str) -> bool:
    return tool_name in _EXPANDED_BUDGET_TOOLS or tool_name.startswith('trigger_')


def _tool_call_session_id() -> str:
    cfg = lazyllm.globals.get('agentic_config') or {}
    if isinstance(cfg, dict) and cfg.get('session_id'):
        return str(cfg['session_id'])
    try:
        return str(getattr(lazyllm.globals, '_sid', '') or '')
    except Exception:
        return ''


def _compact_json(value: Any, limit: int = _MAX_TOOL_LOG_CHARS) -> str:
    try:
        text = json.dumps(value, ensure_ascii=False, default=str)
    except Exception:
        text = str(value)
    if len(text) > limit:
        return text[:limit] + f'...<{len(text) - limit} more chars>'
    return text


def _stable_digest(value: Any) -> str | None:
    try:
        normalized = json.dumps(
            value,
            ensure_ascii=False,
            sort_keys=True,
            separators=(',', ':'),
        )
    except (TypeError, ValueError):
        return None
    return hashlib.sha256(normalized.encode('utf-8')).hexdigest()


def _summarize_tool_result(result: Any) -> dict[str, Any]:
    summary: dict[str, Any] = {}
    if not isinstance(result, dict):
        summary['result_type'] = type(result).__name__
        return summary
    if 'ok' in result:
        summary['ok'] = result.get('ok')
    msg = result.get('msg')
    if msg:
        summary['msg'] = str(msg)[:240]
    value = result.get('value') if 'value' in result else result
    if not isinstance(value, dict):
        return summary
    if 'success' in value:
        summary['success'] = value.get('success')
    error = value.get('error')
    if isinstance(error, dict) and error.get('reason'):
        summary['error'] = str(error.get('reason'))[:240]
    payload = value.get('result') if isinstance(value.get('result'), dict) else value
    if not isinstance(payload, dict):
        return summary
    for key in _RESULT_LOG_KEYS:
        if key in payload and payload[key] is not None:
            summary[key] = payload[key]
    matches = payload.get('matches')
    if isinstance(matches, list):
        summary['match_count'] = len(matches)
    footer = payload.get('footer')
    if isinstance(footer, str) and footer.strip():
        summary['footer'] = footer.strip()[:240]
    return summary


def _format_log_fields(fields: dict[str, Any]) -> str:
    parts: list[str] = []
    for key, value in fields.items():
        if value is None:
            continue
        rendered = _compact_json(value, 240) if isinstance(value, (dict, list)) else str(value)
        parts.append(f'[{key}={rendered}]')
    return ' '.join(parts)


def _log_tool_call(event: str, name: str, **fields: Any) -> None:
    extras = _format_log_fields(fields)
    suffix = f' {extras}' if extras else ''
    lazyllm.LOG.info(
        f'[ToolCall] [sid={_tool_call_session_id()}] [event={event}] [name={name}]{suffix}'
    )


@dataclass(frozen=True)
class _FailureBatchDecision:
    pending_indices: tuple[int, ...]
    blocked_results: dict[int, Any]
    duplicate_sources: dict[int, int]


class FailureRetryPolicy:
    """Preserve configured hard failure budgets independently of repeat notices."""

    def __init__(self, failure_limits: dict[str, int] | None = None):
        self._failure_limits = dict(failure_limits or {})
        self._consecutive_failures: dict[str, int] = {}
        self._failed_signatures: set[str] = set()

    @staticmethod
    def _failed(result: Any) -> bool:
        return isinstance(result, dict) and result.get('ok') is False

    @staticmethod
    def _signature(prepared: PreparedToolCall) -> str:
        arguments = prepared.validated_arguments
        if arguments is None:
            arguments = prepared.arguments
        digest = _stable_digest(arguments) or str(arguments)
        return f'{prepared.tool_name}:{digest}'

    @staticmethod
    def _blocked(name: str, message: str) -> dict[str, Any]:
        return tool_failure(f'[Repeated Tool Failure] {name}: {message}')

    def decide(self, prepared_calls: list[PreparedToolCall]) -> _FailureBatchDecision:
        pending_indices = []
        blocked_results: dict[int, Any] = {}
        duplicate_sources: dict[int, int] = {}
        pending_signatures: dict[str, int] = {}
        for index, prepared in enumerate(prepared_calls):
            name = prepared.tool_name
            limit = self._failure_limits.get(name)
            if limit is None or not prepared.ready:
                pending_indices.append(index)
                continue
            signature = self._signature(prepared)
            if signature in self._failed_signatures:
                blocked_results[index] = self._blocked(
                    name,
                    'this exact call already failed; do not retry it with the same arguments.',
                )
                continue
            failures = self._consecutive_failures.get(name, 0)
            if failures >= limit:
                blocked_results[index] = self._blocked(
                    name,
                    f'{failures} consecutive attempts failed. Use another grounded source or '
                    'explain that the evidence is unavailable.',
                )
                continue
            if signature in pending_signatures:
                duplicate_sources[index] = pending_signatures[signature]
                continue
            pending_signatures[signature] = index
            pending_indices.append(index)
        return _FailureBatchDecision(
            pending_indices=tuple(pending_indices),
            blocked_results=blocked_results,
            duplicate_sources=duplicate_sources,
        )

    def observe(self, records: list[ToolExecutionRecord]) -> None:
        for record in records:
            name = record.tool_name
            if name not in self._failure_limits or not record.executed:
                continue
            signature = self._signature(record.prepared)
            if self._failed(record.result):
                self._consecutive_failures[name] = self._consecutive_failures.get(name, 0) + 1
                self._failed_signatures.add(signature)
            else:
                self._consecutive_failures[name] = 0
                prefix = f'{name}:'
                self._failed_signatures = {
                    item for item in self._failed_signatures if not item.startswith(prefix)
                }


class ExactRepeatMonitor:
    """Emit soft runtime context for exact repeated observations."""

    def __init__(self, threshold: int = _REPEATED_CALL_THRESHOLD):
        self._threshold = max(2, int(threshold))
        self._previous_batch_digest: str | None = None
        self._batch_count = 0
        self._pending_notice = False

    def _reset(self) -> None:
        self._previous_batch_digest = None
        self._batch_count = 0
        self._pending_notice = False

    def begin_run(self, context: dict[str, Any]) -> None:
        del context
        self._reset()

    def end_run(self, reason: str) -> None:
        del reason
        self._reset()

    def runtime_context_delivered(self, batch_ids) -> None:
        if self._pending_notice:
            append_event('repeat_notice_delivered', batch_ids=list(batch_ids))
            self._pending_notice = False

    @staticmethod
    def _record_fingerprint(record: ToolExecutionRecord):
        arguments = record.validated_arguments
        if arguments is None:
            arguments = record.arguments
        arguments_digest = _stable_digest(arguments)
        result_digest = _stable_digest(record.result)
        if arguments_digest is None or result_digest is None:
            return None
        return record.tool_name, arguments_digest, result_digest

    def after_tool_batch(self, records) -> RuntimeDelta:
        self._pending_notice = False
        eligible = [
            record for record in records
            if record.executed and not record.access.polling
        ]
        if not eligible:
            self._reset()
            return RuntimeDelta()
        fingerprints = [self._record_fingerprint(record) for record in eligible]
        if any(item is None for item in fingerprints):
            self._reset()
            return RuntimeDelta()
        batch_digest = _stable_digest(fingerprints)
        if batch_digest is None:
            self._reset()
            return RuntimeDelta()
        previous_count = self._batch_count
        if batch_digest == self._previous_batch_digest:
            self._batch_count += 1
        else:
            if previous_count >= self._threshold:
                append_event('post_notice_strategy_changed', previous_streak=previous_count)
            self._previous_batch_digest = batch_digest
            self._batch_count = 1
        intra_batch_count = max(Counter(fingerprints).values(), default=0)
        repeat_count = max(self._batch_count, intra_batch_count)
        if repeat_count < self._threshold:
            return RuntimeDelta()
        batch_ids = [record.call_id for record in records]
        append_event(
            'exact_repeat_detected',
            streak=repeat_count,
            batch_ids=batch_ids,
            tool_names=[record.tool_name for record in eligible],
        )
        self._pending_notice = True
        return RuntimeDelta(model_context=(RuntimeContext(
            '[Internal runtime notice]\n'
            f'The same tool call batch has returned the same result {repeat_count} consecutive times. '
            'Review the result and change the approach or arguments instead of repeating it unchanged.'
        ),))


class ToolExecutionMiddleware:
    """Coordinate cancellation, failure policy, telemetry, and one prepared execution."""

    def __init__(self, manager: Any, failure_policy: FailureRetryPolicy | None = None,
                 expanded_round_limit: int | None = None, cancel_check: Any = None):
        self._manager = manager
        self._failure_policy = failure_policy or FailureRetryPolicy()
        self._expanded_round_limit = expanded_round_limit
        self._cancel_check = cancel_check

    def __getattr__(self, name: str) -> Any:
        return getattr(self._manager, name)

    def _expand_round_limit(self, tool_name: str) -> None:
        if not _requires_expanded_budget(tool_name):
            return
        workspace = lazyllm.locals.get('_lazyllm_agent', {}).get('workspace')
        if (
            isinstance(workspace, dict)
            and self._expanded_round_limit is not None
            and workspace.get('_react_round_limit') != self._expanded_round_limit
        ):
            workspace['_react_round_limit'] = self._expanded_round_limit
            lazyllm.LOG.info(
                f'ChatAgent used tool={tool_name}; automatically expanding '
                f'tool round limit to {self._expanded_round_limit}.'
            )

    def execute_prepared_calls(self, prepared_calls):
        if self._cancel_check is not None:
            self._cancel_check(None)
        prepared_calls = list(prepared_calls or [])
        if not prepared_calls:
            return ToolExecutionBatch(results=[], records=())
        decision = self._failure_policy.decide(prepared_calls)
        pending = [prepared_calls[index] for index in decision.pending_indices]
        for index, prepared in enumerate(prepared_calls):
            self._expand_round_limit(prepared.tool_name)
            arguments = redact_session_env_arguments(prepared.tool_name, prepared.arguments)
            if index in decision.blocked_results:
                emit_tool_call(prepared.tool_call, blocked=True, reason='failure_retry_policy')
                _log_tool_call('blocked', prepared.tool_name, reason='failure_retry_policy', args=arguments)
                append_event('failure_retry_blocked', name=prepared.tool_name, call_id=prepared.call_id)
            elif index in decision.duplicate_sources:
                emit_tool_call(prepared.tool_call, blocked=True, reason='duplicate_merged')
                _log_tool_call('merged', prepared.tool_name, reason='duplicate_in_batch', args=arguments)
            else:
                emit_tool_call(prepared.tool_call)
                _log_tool_call('start', prepared.tool_name, args=arguments)
        started_at = time.perf_counter()
        executed_batch = self._manager.execute_prepared_calls(pending)
        elapsed = time.perf_counter() - started_at
        results: list[Any] = [None] * len(prepared_calls)
        records: list[ToolExecutionRecord | None] = [None] * len(prepared_calls)
        for original_index, result, record in zip(
            decision.pending_indices, executed_batch.results, executed_batch.records,
        ):
            results[original_index] = result
            records[original_index] = record
            emit_tool_result(prepared_calls[original_index].tool_call, result)
            _log_tool_call(
                'done',
                prepared_calls[original_index].tool_name,
                elapsed=f'{elapsed:.3f}s',
                **_summarize_tool_result(result),
            )
        for index, result in decision.blocked_results.items():
            results[index] = result
            records[index] = ToolExecutionRecord(prepared_calls[index], result, executed=False)
            emit_tool_result(prepared_calls[index].tool_call, result)
        for index, source_index in decision.duplicate_sources.items():
            result = results[source_index]
            results[index] = result
            records[index] = ToolExecutionRecord(prepared_calls[index], result, executed=False)
            emit_tool_result(prepared_calls[index].tool_call, result)
        completed_records = [record for record in records if record is not None]
        self._failure_policy.observe(completed_records)
        return ToolExecutionBatch(
            results=lazyllm.package(results),
            records=tuple(completed_records),
        )

    def execute_with_records(self, tools: Any, verbose: bool = False,
                             allowed_tool_names: set[str] | None = None):
        del verbose
        prepared = self._manager.prepare_tool_calls(tools, allowed_tool_names)
        return self.execute_prepared_calls(prepared)

    def __call__(self, tools: Any, verbose: bool = False,
                 allowed_tool_names: set[str] | None = None) -> Any:
        return self.execute_with_records(
            tools,
            verbose=verbose,
            allowed_tool_names=allowed_tool_names,
        ).results
