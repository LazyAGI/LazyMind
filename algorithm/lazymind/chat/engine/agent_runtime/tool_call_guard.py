from __future__ import annotations

import hashlib
import json
import time
from dataclasses import dataclass
from typing import Any

import lazyllm

from lazymind.chat.engine.tools.session_env import redact_session_env_arguments
from .telemetry import emit_tool_call, emit_tool_result


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


@dataclass
class _RepeatedCallState:
    tool_name: str
    arguments_digest: str
    result_digest: str
    count: int


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


def _parse_tool_arguments(function: dict[str, Any]) -> Any:
    arguments = function.get('arguments', {})
    if isinstance(arguments, str):
        try:
            return json.loads(arguments)
        except Exception:
            return arguments
    return arguments


def _stable_digest(value: Any) -> str:
    try:
        normalized = json.dumps(
            value,
            ensure_ascii=False,
            sort_keys=True,
            separators=(',', ':'),
            default=str,
        )
    except Exception:
        normalized = str(value)
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


class ToolCallGuard:
    """Monitor tool calls and queue internal runtime notices without changing results."""

    def __init__(self, manager: Any, expanded_round_limit: int | None = None, cancel_check: Any = None):
        self._manager = manager
        self._expanded_round_limit = expanded_round_limit
        self._cancel_check = cancel_check
        self._repeated_call_state: _RepeatedCallState | None = None
        self._internal_runtime_notices: dict[tuple[str, ...], str] = {}

    def __getattr__(self, name: str) -> Any:
        return getattr(self._manager, name)

    def reset_internal_runtime_notice_state(self) -> None:
        self._repeated_call_state = None
        self._internal_runtime_notices.clear()

    def consume_internal_runtime_notices(self, batch_tool_call_ids) -> list[str]:
        ids = tuple(str(item) for item in batch_tool_call_ids)
        if not self._internal_runtime_notices:
            return []
        if ids not in self._internal_runtime_notices:
            self.reset_internal_runtime_notice_state()
            return []
        notice = self._internal_runtime_notices.pop(ids)
        return [notice]

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

    def _update_repeated_state(self, tool_call: dict[str, Any], result: Any, access: Any) -> int:
        if bool(getattr(access, 'polling', False)) or bool(getattr(access, 'counts_as_progress', False)):
            self._repeated_call_state = None
            return 0
        function = tool_call.get('function') or {}
        name = str(function.get('name') or '')
        arguments_digest = _stable_digest(_parse_tool_arguments(function))
        result_digest = _stable_digest(result)
        previous = self._repeated_call_state
        if (
            previous is not None
            and previous.tool_name == name
            and previous.arguments_digest == arguments_digest
            and previous.result_digest == result_digest
        ):
            count = previous.count + 1
        else:
            count = 1
        self._repeated_call_state = _RepeatedCallState(name, arguments_digest, result_digest, count)
        return count

    def __call__(self, tools: Any, verbose: bool = False,
                 allowed_tool_names: set[str] | None = None) -> Any:
        try:
            if self._cancel_check is not None:
                self._cancel_check(None)
            tool_calls = [tools] if isinstance(tools, dict) else list(tools or [])
            normalizer = getattr(self._manager, 'normalize_tool_calls', None)
            if callable(normalizer):
                normalizer(tool_calls)
            for tool_call in tool_calls:
                function = tool_call.get('function') or {}
                name = str(function.get('name') or '')
                self._expand_round_limit(name)
                _log_tool_call(
                    'start', name,
                    args=redact_session_env_arguments(name, _parse_tool_arguments(function)),
                )
                emit_tool_call(tool_call)
            resolver = getattr(self._manager, 'resolve_tool_accesses', None)
            accesses = resolver(tool_calls, allowed_tool_names) if callable(resolver) else [None] * len(tool_calls)
            started_at = time.perf_counter()
            results = self._manager(
                tool_calls,
                verbose=verbose,
                allowed_tool_names=allowed_tool_names,
            )
            elapsed = time.perf_counter() - started_at
        except Exception:
            self.reset_internal_runtime_notice_state()
            raise

        highest_count = 0
        result_items = list(results)
        if len(result_items) != len(tool_calls) or len(accesses) != len(tool_calls):
            self.reset_internal_runtime_notice_state()
            return results
        for tool_call, result, access in zip(tool_calls, result_items, accesses):
            emit_tool_result(tool_call, result)
            name = str((tool_call.get('function') or {}).get('name') or '')
            _log_tool_call('done', name, elapsed=f'{elapsed:.3f}s', **_summarize_tool_result(result))
            highest_count = max(highest_count, self._update_repeated_state(tool_call, result, access))
        if highest_count >= _REPEATED_CALL_THRESHOLD:
            ids = tuple(str(tool_call.get('id') or '') for tool_call in tool_calls)
            self._internal_runtime_notices[ids] = (
                '[Internal runtime notice]\n'
                f'The same tool call has returned the same result {highest_count} consecutive times. '
                'Review the result and change the approach or arguments instead of repeating it unchanged.'
            )
        return results
