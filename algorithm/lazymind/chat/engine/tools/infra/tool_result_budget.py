"""One model/UI result projection; full payloads live in the request workspace."""
from __future__ import annotations

import copy
import json
from dataclasses import replace
from typing import Any

import lazyllm
from lazyllm.tools.agent import ToolExecutionBatch


RESULT_BYTES = 16 * 1024
PREVIEW_CHARS = 700
_CONTROL_KEYS = {'ok', 'status', 'code', '_agent_control', 'control', 'action', 'type',
                 'requires_approval', 'needs_approval', 'approval_id', 'request_id', 'session_id', 'run_id',
                 'accepted', 'command_id', 'state_version', 'ready', 'retryable', 'rewindable',
                 'ready_steps', 'retryable_steps', 'rewindable_steps', 'continue_steps'}
_IDENTITY_KEYS = {'url', 'target_url', 'ref', 'citation_index', 'target', 'next_cursor', 'doc_id', 'doi'}


def encoded_size(value: Any) -> int:
    return len(json.dumps(value, ensure_ascii=False, default=str).encode('utf-8'))


def _original(value: Any) -> Any:
    if isinstance(value, dict):
        full = getattr(value, 'full_result', None)
        source = {**value, **full} if isinstance(full, dict) else value
        # Citations are assigned after provider normalization.
        source = {**source, **{k: value[k] for k in ('ref', 'citation_index') if k in value}}
        return {k: _original(v) for k, v in source.items()}
    if isinstance(value, list):
        return [_original(v) for v in value]
    return value


def _snapshot(workspace: str | None, name: str, value: Any) -> str:
    from lazymind.chat.engine.agent_runtime.compactors import spill_tool_result_to_workspace
    from lazymind.chat.engine.tools.local_file.store import workspace_for_request
    root = workspace or workspace_for_request()
    content = json.dumps(value, ensure_ascii=False, indent=2, default=str) if not isinstance(value, str) else value
    path = spill_tool_result_to_workspace(root, name, content)
    if not path:
        raise OSError('snapshot unavailable')
    return path


def _continuation(path: str) -> dict[str, Any]:
    return {'target': path, 'offset': 1, 'tool': 'read_file', 'offset_unit': 'line'}


def _search_preview(value: Any, workspace: str | None, name: str) -> Any:
    if isinstance(value, list):
        return [_search_preview(v, workspace, name) for v in value]
    if not isinstance(value, dict):
        return value
    if isinstance(value.get('items'), list):
        result = dict(value, items=_search_preview(value['items'], workspace, name))
        result['returned_count'] = len(result['items'])
        return result
    if not any(k in value for k in ('snippet', 'content', 'source')):
        return value
    result = dict(value)
    extra = result['extra'] = dict(value.get('extra') or {})
    original = _original(value)
    full_extra = original.get('extra') or {}
    text = (original.get('content') or full_extra.get('raw_content')
            or full_extra.get('content') or original.get('snippet') or '')
    for key in ('snippet', 'content'):
        if isinstance(result.get(key), str):
            result[key] = result[key][:PREVIEW_CHARS]
    for key in ('raw_content', 'content'):
        if isinstance(extra.get(key), str):
            extra[key] = extra[key][:PREVIEW_CHARS]
    if isinstance(text, str) and len(text) > PREVIEW_CHARS:
        path = _snapshot(workspace, name + '_content', text)
        read = dict(extra.get('content_read') or {})
        # A Sciverse response may be one provider page, not the full document.
        extra['content_snapshot'] = {**_continuation(path), 'char_count': len(text),
                                     'source_read': read}
        extra['truncated'] = True
    return result


def _shrink(value: Any, chars: int, count: int) -> Any:
    if isinstance(value, str):
        return value if len(value) <= chars else value[:chars]
    if isinstance(value, list):
        return [_shrink(v, chars, count) for v in value[:count]]
    if isinstance(value, dict):
        return {k: (v if k in _CONTROL_KEYS | _IDENTITY_KEYS else _shrink(v, chars, count)) for k, v in value.items()}
    return value


def bound_tool_result(name: str, result: Any, *, workspace: str | None = None,
                      search: bool = False) -> Any:
    """Bound the whole envelope without turning a successful action into a retry."""
    original = _original(result)
    raw_size = encoded_size(original)
    envelope = isinstance(result, dict) and isinstance(result.get('ok'), bool) and 'value' in result
    value = result['value'] if envelope else result
    path = None
    snapshot_error = False
    try:
        preview = _search_preview(value, workspace, name) if search else copy.deepcopy(value)
        projected = {**result, 'value': preview} if envelope else preview
        changed = original != projected
        if not changed and encoded_size(projected) <= RESULT_BYTES:
            return projected
        path = _snapshot(workspace, name, original)
    except (OSError, ValueError, RuntimeError):
        snapshot_error = True
        projected = copy.deepcopy(result)
    receipt = {'original_bytes': raw_size, 'truncated': True}
    if path:
        receipt['full_result'] = _continuation(path)
    else:
        receipt['snapshot_error'] = 'Full result could not be saved; do not repeat completed actions automatically.'
    payload = projected['value'] if envelope else projected
    # Keep lists as lists; the first item carries the complete-results locator.

    def attach(v):
        if isinstance(v, list):
            if v and isinstance(v[0], dict):
                v[0] = {**v[0], 'extra': {**(v[0].get('extra') or {}), 'result_read': receipt}}
                return v
            return {'items': v, 'result_read': receipt}
        if isinstance(v, dict):
            v = ({**v, 'extra': {**(v.get('extra') or {}), 'result_read': receipt}}
                 if search else {**v, 'result_read': receipt})
            if isinstance(v.get('items'), list):
                v['returned_count'] = len(v['items'])
            return v
        return {'preview': v, 'result_read': receipt}
    for chars, count in ((700, 2000), (700, 25), (350, 10), (160, 5), (60, 1)):
        limited = attach(_shrink(payload, chars, count))
        candidate = {**_shrink({k: v for k, v in projected.items() if k != 'value'}, chars, count),
                     'value': limited} if envelope else limited
        if encoded_size(candidate) <= RESULT_BYTES:
            lazyllm.LOG.info(f'[ToolResultBudget] tool={name} original_bytes={raw_size} '
                             f'returned_bytes={encoded_size(candidate)} snapshot_ok={not snapshot_error}')
            return candidate
    # Large top-level diagnostics must not bypass the budget either.
    controls = {k: v for k, v in projected.items() if k in _CONTROL_KEYS} if isinstance(projected, dict) else {}
    if isinstance(payload, list) and payload and isinstance(payload[0], dict):
        # A single enormous URL must not force broken links or a different list schema.
        first = payload[0]
        minimal = [{key: str(first[key])[:PREVIEW_CHARS] for key in ('title', 'source') if key in first}]
    elif isinstance(payload, dict):
        minimal = {k: v for k, v in payload.items() if k in _CONTROL_KEYS}
    else:
        minimal = {}
    limited = attach(minimal)
    candidate = {**controls, 'value': limited} if envelope else limited
    if encoded_size(candidate) > RESULT_BYTES:
        raise ValueError('tool control payload exceeds result budget')
    return candidate


class ToolResultBudgetMiddleware:
    def __init__(self, manager: Any, workspace: str | None = None):
        self._manager = manager
        self._workspace = workspace

    def __getattr__(self, name: str) -> Any:
        return getattr(self._manager, name)

    def execute_with_records(self, *args, **kwargs):
        batch = self._manager.execute_with_records(*args, **kwargs)
        results = []
        for record, result in zip(batch.records, batch.results):
            tool = (getattr(self._manager, 'tools_info', None) or {}).get(record.tool_name)
            from lazyllm.tools.tools.search import SearchBase
            search = isinstance(getattr(tool, '_instance', None), SearchBase)
            results.append(bound_tool_result(record.tool_name, result, workspace=self._workspace, search=search))
        return ToolExecutionBatch(results=results,
                                  records=tuple(replace(r, result=v) for r, v in zip(batch.records, results)),
                                  duration_ms=batch.duration_ms)

    def __call__(self, *args, **kwargs):
        return self.execute_with_records(*args, **kwargs).stamped_results()
