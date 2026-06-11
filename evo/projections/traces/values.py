from __future__ import annotations

import ast
import json
from typing import Any

from evo.projections.traces.models import TraceIOKind, TraceIOView


_RAW_PROJECTION_EXCLUDED_KEYS = frozenset({
    'uid',
    'user_id',
    'session_id',
    'files',
    'image_files',
})
_RAW_PROJECTION_PARSE_STRING_KEYS = frozenset({'arguments', 'content'})
_SUMMARY_KEYS = (
    'value',
    'query',
    'expression',
    'keyword',
    'url',
    'final_url',
    'title',
    'name',
    'docid',
    'document_id',
    'node_id',
    'target',
    'content',
    'description',
    'text',
)
_ARGUMENT_SUMMARY_KEYS = ('query', 'expression', 'keyword', 'name', 'url', 'node_id', 'docid', 'target')
_TOOL_RESULT_SUMMARY_KEYS = ('success', 'status', 'tool', 'expression', 'total')


def raw_projection(
    value: Any,
    *,
    key: str | None = None,
    list_limit: int = 20,
    dict_limit: int = 40,
) -> Any:
    if isinstance(value, str):
        parsed = parse_jsonish(value) if key in _RAW_PROJECTION_PARSE_STRING_KEYS else value
        if parsed is not value:
            return raw_projection(parsed, list_limit=list_limit, dict_limit=dict_limit)
        return text_summary(value, limit=1000)
    parsed = parse_jsonish(value)
    if isinstance(parsed, (int, float, bool)) or parsed is None:
        return parsed
    if isinstance(parsed, list):
        return [
            raw_projection(item, list_limit=list_limit, dict_limit=dict_limit)
            for item in parsed[:list_limit]
            if item not in (None, '', [], {})
        ]
    if isinstance(parsed, dict):
        projected: dict[str, Any] = {}
        for raw_key, item in list(parsed.items())[:dict_limit]:
            if raw_key in _RAW_PROJECTION_EXCLUDED_KEYS or item in (None, '', [], {}):
                continue
            projected[str(raw_key)] = raw_projection(
                item,
                key=str(raw_key),
                list_limit=list_limit,
                dict_limit=dict_limit,
            )
        return projected
    return text_summary(str(parsed), limit=1000)


def display_summary(value: Any) -> str:
    parsed = parse_jsonish(value)
    if isinstance(parsed, dict):
        for key in _SUMMARY_KEYS:
            if parsed.get(key) not in (None, '', [], {}):
                return text_summary(parsed[key])
        if isinstance(parsed.get('input'), list):
            return f'{len(parsed["input"])} messages'
        if isinstance(parsed.get('items'), list):
            return f'{len(parsed["items"])} items'
        return object_summary(parsed)
    if isinstance(parsed, list):
        return f'{len(parsed)} items'
    return text_summary(parsed)


def compact_display_value(value: Any) -> Any:
    parsed = parse_jsonish(value)
    if isinstance(parsed, str):
        return text_summary(parsed, limit=1000)
    if isinstance(parsed, (int, float, bool)) or parsed is None:
        return parsed
    if isinstance(parsed, list):
        return raw_projection(parsed, list_limit=8, dict_limit=8)
    if isinstance(parsed, dict):
        return raw_projection(parsed, list_limit=8, dict_limit=8)
    return text_summary(str(parsed), limit=1000)


def parse_jsonish(value: Any) -> Any:
    if not isinstance(value, str):
        return value
    text = value.strip()
    if not text:
        return value
    try:
        return json.loads(text)
    except (TypeError, ValueError):
        pass
    try:
        return ast.literal_eval(text)
    except (SyntaxError, ValueError, TypeError):
        return value


def drop_empty(value: dict[str, Any]) -> dict[str, Any]:
    return {key: item for key, item in value.items() if item not in (None, '', [], {})}


def project_dict(value: Any) -> dict[str, Any] | None:
    return raw_projection(value) if isinstance(value, dict) else None


def io(kind: TraceIOKind, summary: str, data: dict[str, Any] | None = None) -> TraceIOView:
    return TraceIOView(kind=kind, summary=summary, data=data or {})


def pick(value: dict[str, Any], *keys: str) -> dict[str, Any]:
    return {key: value.get(key) for key in keys if value.get(key) is not None}


def string_field(value: dict[str, Any], key: str, *, limit: int = 1000) -> str | None:
    item = value.get(key)
    if item in (None, '', [], {}):
        return None
    return text_summary(item, limit=limit)


def arguments_summary(arguments: dict[str, Any]) -> str:
    suggestions = arguments.get('suggestions')
    if isinstance(suggestions, list):
        return f'{len(suggestions)} suggestions'
    for key in _ARGUMENT_SUMMARY_KEYS:
        if arguments.get(key) not in (None, ''):
            return text_summary(arguments[key], limit=160)
    return object_summary(arguments)


def tool_result_summary(value: Any) -> str:
    if not isinstance(value, dict):
        return text_summary(str(value))
    result = value.get('result') if isinstance(value.get('result'), dict) else value
    parts: list[str] = []
    for key in _TOOL_RESULT_SUMMARY_KEYS:
        source = result if isinstance(result, dict) and key in result else value
        if key in source and source.get(key) not in (None, ''):
            parts.append(f'{key}={text_summary(source.get(key), limit=80)}')
    if value.get('result') is not None and not isinstance(value.get('result'), dict):
        parts.append(f'result={text_summary(value.get("result"), limit=160)}')
    if value.get('value') not in (None, ''):
        parts.append(f'value={text_summary(value.get("value"), limit=80)}')
    items = result.get('items') if isinstance(result.get('items'), list) else None
    if items is not None:
        parts.append(f'items={len(items)}')
    if not parts and value.get('error'):
        parts.append(f'error={text_summary(value.get("error"), limit=160)}')
    return ' '.join(parts) or object_summary(value)


def object_summary(value: dict[str, Any]) -> str:
    suggestions = value.get('suggestions')
    if isinstance(suggestions, list):
        return f'{len(suggestions)} suggestions'
    parts: list[str] = []
    for key, item in value.items():
        if isinstance(item, (dict, list)):
            continue
        if item not in (None, ''):
            parts.append(f'{key}={text_summary(item, limit=80)}')
        if len(parts) >= 3:
            break
    return ' '.join(parts) or f'{len(value)} fields'


def looks_internal_call_repr(value: str) -> bool:
    text = value.strip()
    return text.startswith("{'args':") and "'kwargs':" in text


def text_summary(value: Any, *, limit: int = 240) -> str:
    text = ' '.join(str(value or '').split())
    if len(text) > limit:
        return text[:limit - 3] + '...'
    return text


def safe_json(value: Any) -> str:
    try:
        return json.dumps(value, ensure_ascii=False, default=str)
    except TypeError:
        return str(value)
