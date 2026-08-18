from __future__ import annotations

from functools import wraps
from typing import Any, Dict

import lazyllm


TOOL_ERROR_CATEGORIES = frozenset({
    'UNKNOWN_TOOL',
    'INVALID_ARGS',
    'TRANSIENT_ERROR',
    'PERMISSION_ERROR',
    'DOMAIN_FAILURE',
})
_TRANSIENT_ERROR_NAMES = {
    'ConnectTimeout', 'ConnectTimeoutError', 'ConnectionError', 'ConnectionResetError',
    'ReadTimeout', 'ReadTimeoutError', 'Timeout', 'TimeoutError',
}


def runtime_tool_failure(
    tool_name: str,
    message: str,
    *,
    category: str = 'DOMAIN_FAILURE',
    code: str = 'TOOL_REPORTED_FAILURE',
    retryable: bool = False,
    recovery_attempts_remaining: int = 0,
    details: Dict[str, Any] | None = None,
) -> Dict[str, Any]:
    if category not in TOOL_ERROR_CATEGORIES:
        raise ValueError(f'unsupported tool error category: {category}')
    error = {
        'category': category,
        'code': code,
        'tool': tool_name,
        'message': message,
        'retryable': bool(retryable),
        'recovery_attempts_remaining': max(0, int(recovery_attempts_remaining)),
        'details': dict(details or {}),
    }
    return {'ok': False, 'value': None, 'error': error, 'msg': message}


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
    category: str = 'DOMAIN_FAILURE',
    code: str = 'TOOL_REPORTED_FAILURE',
    retryable: bool = False,
    recovery_attempts_remaining: int = 0,
    details: Dict[str, Any] | None = None,
    error_type: str | None = None,
    detail: str | None = None,
    log_message: str | None = None,
    log_level: str = 'warning',
    meta: Dict[str, Any] | None = None,
) -> Dict[str, Any]:
    if log_message:
        logger = getattr(lazyllm.LOG, log_level, lazyllm.LOG.warning)
        logger(log_message)

    structured_details = dict(details or {})
    if error_type:
        structured_details.setdefault('error_type', error_type)
    if detail and detail != reason:
        structured_details.setdefault('detail', detail)
    payload = runtime_tool_failure(
        tool_name,
        reason,
        category=category,
        code=code,
        retryable=retryable,
        recovery_attempts_remaining=recovery_attempts_remaining,
        details=structured_details,
    )
    # Keep the old fields for direct tool consumers while ToolManager exposes only
    # the canonical error object to the model.
    payload['success'] = False
    payload['tool'] = tool_name
    payload['retryable'] = bool(retryable)
    payload['error']['reason'] = reason
    if error_type:
        payload['error']['type'] = error_type
    if meta:
        payload['meta'] = meta
    return payload


def tool_failure(tool_name: str, exc: Exception) -> Dict[str, Any]:
    error_name = type(exc).__name__
    if isinstance(exc, PermissionError) or 'Permission' in error_name or 'Forbidden' in error_name:
        category, code = 'PERMISSION_ERROR', 'PERMISSION_DENIED'
    elif isinstance(exc, (TimeoutError, ConnectionError)) or error_name in _TRANSIENT_ERROR_NAMES:
        category, code = 'TRANSIENT_ERROR', 'TEMPORARY_TOOL_FAILURE'
    else:
        category, code = 'DOMAIN_FAILURE', 'TOOL_EXECUTION_FAILED'
    return tool_error(
        tool_name,
        f'{tool_name} failed: {exc}',
        category=category,
        code=code,
        error_type=error_name,
    )


def handle_tool_errors(func):
    @wraps(func)
    def wrapper(*args, **kwargs):
        try:
            return func(*args, **kwargs)
        except Exception as exc:
            return tool_failure(func.__name__, exc)
    return wrapper
