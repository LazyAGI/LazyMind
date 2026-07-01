from __future__ import annotations

from typing import Any, Dict

import lazyllm


def tool_success(tool_name: str, result: Any, meta: Dict[str, Any] | None = None) -> Dict[str, Any]:
    payload: Dict[str, Any] = {
        'success': True,
        'tool': tool_name,
        'result': result,
    }
    if meta:
        payload['meta'] = meta
    return payload


def tool_error(
    tool_name: str,
    reason: str,
    *,
    error_type: str | None = None,
    detail: str | None = None,
    log_message: str | None = None,
    log_level: str = 'warning',
    meta: Dict[str, Any] | None = None,
) -> Dict[str, Any]:
    if log_message:
        logger = getattr(lazyllm.LOG, log_level, lazyllm.LOG.warning)
        logger(log_message)

    payload: Dict[str, Any] = {
        'success': False,
        'tool': tool_name,
        'error': {
            'reason': reason,
        },
    }
    if error_type:
        payload['error']['type'] = error_type
    if detail:
        payload['error']['detail'] = detail
    if meta:
        payload['meta'] = meta
    return payload


def tool_failure(tool_name: str, exc: Exception) -> Dict[str, Any]:
    return tool_error(
        tool_name,
        f'{tool_name} failed: {exc}',
        error_type=type(exc).__name__,
        detail=str(exc),
    )
