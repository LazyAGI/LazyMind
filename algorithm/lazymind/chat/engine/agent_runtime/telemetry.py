from __future__ import annotations

import json
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional

import lazyllm

from lazymind.config import config

_PREVIEW_CHARS = 400

_FILE_READ_TOOLS = {
    'read_file',
    'read_user_attachment',
    'feishuwikifs_read',
    'cat_file',
    'LocalFileToolkit_read',
}
_HARNESS_TOOLS = {
    'create_subagent',
    'advance_step',
    'advance_step_and_hand_off',
    'create_plugin_draft',
    'ask_user',
}
_EXTERNAL_TOOLS = {
    'url_fetch',
    'web_search',
    'kb_search',
    'kb_tmp_search',
    'kb_keyword',
    'academic_search',
    'search_provider',
}


def resolve_event_path() -> str:
    """Return configured JSONL path, or empty string when telemetry is off."""
    for key in ('agent_lab_event_path', 'context_compression_event_path'):
        try:
            value = str(config[key] or '').strip()
        except Exception:  # noqa: BLE001 — config may lack older keys in stubs
            continue
        if value:
            return value
    return ''


def telemetry_enabled() -> bool:
    return bool(resolve_event_path())


def classify_tool(name: str) -> str:
    lower = (name or '').lower()
    if lower in _FILE_READ_TOOLS or lower.endswith('_read_file') or lower.endswith('_read_with_references'):
        return 'file_read'
    if lower in _HARNESS_TOOLS or lower.startswith('trigger_'):
        return 'harness'
    if lower in _EXTERNAL_TOOLS or lower.endswith('_search') or 'search' in lower:
        return 'external'
    if lower in {'run_script', 'shell', 'terminal', 'bash', 'execute_command', 'cmd'}:
        return 'shell'
    return 'tool'


def _preview(value: Any, limit: int = _PREVIEW_CHARS) -> str:
    if value is None:
        return ''
    if isinstance(value, str):
        text = value
    else:
        try:
            text = json.dumps(value, ensure_ascii=False, default=str)
        except Exception:  # noqa: BLE001
            text = str(value)
    if len(text) <= limit:
        return text
    return f'{text[:limit]}…[{len(text) - limit} more chars]'


def _size_bytes(value: Any) -> int:
    if value is None:
        return 0
    if isinstance(value, (bytes, bytearray)):
        return len(value)
    if isinstance(value, str):
        return len(value.encode('utf-8', errors='replace'))
    try:
        return len(json.dumps(value, ensure_ascii=False, default=str).encode('utf-8', errors='replace'))
    except Exception:  # noqa: BLE001
        return len(str(value).encode('utf-8', errors='replace'))


def append_event(kind: str, **payload: Any) -> None:
    """Best-effort JSONL append for agent-lab. Never raises into agent path."""
    try:
        path_raw = resolve_event_path()
        if not path_raw:
            return
        path = Path(path_raw)
        path.parent.mkdir(parents=True, exist_ok=True)
        record = {
            'ts': datetime.now(timezone.utc).isoformat(),
            'kind': kind,
            **payload,
        }
        with path.open('a', encoding='utf-8') as handle:
            handle.write(json.dumps(record, ensure_ascii=False, default=str) + '\n')
    except Exception as exc:  # noqa: BLE001
        try:
            lazyllm.LOG.warning(f'[AgentLabTelemetry] write failed: {exc}')
        except Exception:  # noqa: BLE001
            pass


def make_runtime_observer(*, role: str = '', run_id: str = '') -> Any:
    """Return a FunctionCall-compatible observer(kind, **payload)."""

    def _observe(kind: str, **payload: Any) -> None:
        if role and 'role' not in payload:
            payload['role'] = role
        if run_id and 'run_id' not in payload:
            payload['run_id'] = run_id
        if kind in {'turn_start', 'history_ready', 'turn_end'}:
            history = payload.pop('history', None)
            if history is not None and 'estimated_tokens' not in payload:
                try:
                    from .pruner import estimate_history_tokens

                    payload['estimated_tokens'] = estimate_history_tokens(history)
                    payload['history_len'] = len(history)
                except Exception:  # noqa: BLE001
                    pass
        append_event(kind, **payload)

    return _observe


def emit_tool_call(tool_call: dict[str, Any], *, blocked: bool = False, reason: str = '') -> None:
    function = tool_call.get('function') or {}
    name = str(function.get('name') or '')
    arguments = function.get('arguments', {})
    category = classify_tool(name)
    append_event(
        'tool_call',
        name=name,
        category=category,
        args_bytes=_size_bytes(arguments),
        args_preview=_preview(arguments),
        blocked=blocked,
        reason=reason,
        tool_call_id=tool_call.get('id') or '',
    )
    if category == 'file_read':
        append_event(
            'file_read',
            name=name,
            args_preview=_preview(arguments),
            blocked=blocked,
        )
    if category == 'harness':
        append_event(
            'harness',
            name=name,
            args_preview=_preview(arguments),
            phase='call',
            blocked=blocked,
        )


def emit_tool_result(tool_call: dict[str, Any], result: Any) -> None:
    function = tool_call.get('function') or {}
    name = str(function.get('name') or '')
    category = classify_tool(name)
    ok = True
    if isinstance(result, dict) and 'ok' in result:
        ok = bool(result.get('ok'))
    append_event(
        'tool_result',
        name=name,
        category=category,
        ok=ok,
        result_bytes=_size_bytes(result),
        result_preview=_preview(result),
        tool_call_id=tool_call.get('id') or '',
    )
    if category == 'harness':
        append_event(
            'harness',
            name=name,
            phase='result',
            ok=ok,
            result_preview=_preview(result),
        )


def sid() -> Optional[str]:
    try:
        return getattr(lazyllm.globals, '_sid', None)
    except Exception:  # noqa: BLE001
        return None
