from __future__ import annotations

from typing import Any, Optional


def _token(value: Any) -> Optional[int]:
    try:
        parsed = int(value)
    except (TypeError, ValueError):
        return None
    if parsed < 0:
        return None
    return parsed


def _has(mapping: dict[str, Any], key: str) -> bool:
    return key in mapping


def usage_frames(usage: Optional[dict[str, Any]]) -> list[dict[str, Any]]:
    if not isinstance(usage, dict):
        return []
    listed = usage.get('provider_usages')
    if isinstance(listed, list) and any(isinstance(item, dict) for item in listed):
        return [item for item in listed if isinstance(item, dict)]
    nested = usage.get('provider_usage')
    if isinstance(nested, dict):
        return [nested]
    return [usage]


def adapt_usage_frame(raw: Optional[dict[str, Any]]) -> dict[str, Any]:
    if not isinstance(raw, dict):
        return {}

    adapted: dict[str, Any] = {}
    input_tokens = _token(raw.get('input_tokens'))
    if input_tokens is None:
        input_tokens = _token(raw.get('prompt_tokens'))
    output_tokens = _token(raw.get('output_tokens'))
    if output_tokens is None:
        output_tokens = _token(raw.get('completion_tokens'))
    total_tokens = _token(raw.get('total_tokens'))
    if total_tokens is None and input_tokens is not None and output_tokens is not None:
        total_tokens = input_tokens + output_tokens

    if input_tokens is not None:
        adapted['input_tokens'] = input_tokens
    if output_tokens is not None:
        adapted['output_tokens'] = output_tokens
    if total_tokens is not None:
        adapted['total_tokens'] = total_tokens

    cached_tokens = _cached_tokens(raw)
    if cached_tokens is not None:
        adapted['cached_tokens'] = cached_tokens

    reasoning_tokens = _reasoning_tokens(raw)
    if reasoning_tokens is not None:
        adapted['reasoning_tokens'] = reasoning_tokens
    return adapted


def combine_adapted_usage(parts: list[dict[str, Any]]) -> dict[str, Any]:
    if not parts:
        return {}
    if len(parts) == 1:
        combined = dict(parts[0])
        if 'cached_tokens' in combined:
            combined['cache_input_tokens'] = combined.get('input_tokens') or 0
        return combined

    combined: dict[str, Any] = {}
    for key in ('input_tokens', 'output_tokens', 'total_tokens', 'reasoning_tokens'):
        values = [part[key] for part in parts if key in part]
        if values:
            combined[key] = sum(values)
    cache_parts = [part for part in parts if 'cached_tokens' in part]
    if cache_parts:
        combined['cached_tokens'] = sum(part['cached_tokens'] for part in cache_parts)
        combined['cache_input_tokens'] = sum(part.get('input_tokens') or 0 for part in cache_parts)
    return combined


def adapt_provider_usage(usage: Optional[dict[str, Any]]) -> dict[str, Any]:
    combined = combine_adapted_usage([adapt_usage_frame(frame) for frame in usage_frames(usage)])
    combined.pop('cache_input_tokens', None)
    return combined


def _cached_tokens(raw: dict[str, Any]) -> Optional[int]:
    details = raw.get('prompt_tokens_details')
    if isinstance(details, dict) and _has(details, 'cached_tokens'):
        return _token(details.get('cached_tokens'))
    if _has(raw, 'prompt_cache_hit_tokens'):
        return _token(raw.get('prompt_cache_hit_tokens'))
    if _has(raw, 'cache_read_input_tokens'):
        return _token(raw.get('cache_read_input_tokens'))
    if _has(raw, 'cached_tokens') and not isinstance(raw.get('cached_tokens'), dict):
        return _token(raw.get('cached_tokens'))
    return None


def _reasoning_tokens(raw: dict[str, Any]) -> Optional[int]:
    details = raw.get('completion_tokens_details')
    if isinstance(details, dict) and _has(details, 'reasoning_tokens'):
        return _token(details.get('reasoning_tokens'))
    if isinstance(details, dict) and _has(details, 'reasoning'):
        return _token(details.get('reasoning'))
    if _has(raw, 'reasoning_tokens'):
        return _token(raw.get('reasoning_tokens'))
    return None
