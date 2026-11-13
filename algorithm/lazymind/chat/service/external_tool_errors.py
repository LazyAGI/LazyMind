"""Public failures for the external tool boundary, without credentials or traceback data."""

import re

import httpx
import requests
from lazyllm.tools.agent import ToolExecutionError


class ExternalToolFailure(Exception):
    def __init__(self, code, message, retryable=False, upstream=None):
        super().__init__(code, message, retryable, upstream)
        self.detail = {'code': code, 'message': message, 'retryable': retryable}
        if upstream:
            self.detail['upstream'] = upstream

    def __str__(self):
        return self.args[1]


def _secrets(value, *, credentials=False):
    if isinstance(value, dict):
        for key, child in value.items():
            sensitive = credentials or bool(re.search(r'key|token|secret|password|authorization', key, re.I))
            yield from _secrets(child, credentials=sensitive)
    elif isinstance(value, (list, tuple)):
        for child in value:
            yield from _secrets(child, credentials=credentials)
    elif credentials and isinstance(value, str) and value:
        yield value


def _public_text(value, payload, limit=1024):
    if not isinstance(value, (str, int)) or isinstance(value, bool):
        return ''
    text = str(value)
    secrets = set(_secrets(payload.get('llm_config', {})))
    secrets.update(_secrets(payload.get('tool_config', {}), credentials=True))
    for secret in sorted(secrets, key=len, reverse=True):
        text = text.replace(secret, '[redacted]')
    text = re.sub(r'https?://[^\s<>\"\']+', '[url]', text)
    text = re.sub(r'(?i)\bBearer\s+\S+', 'Bearer [redacted]', text)
    text = re.sub(r'(?i)\b(api[_-]?key|token|secret|password|authorization)\s*[=:]\s*[^\s,;]+',
                  r'\1=[redacted]', text)
    text = re.sub(r'(?<!\w)(?:[A-Za-z]:[\\/]|/(?:Users|home|private|tmp|var|etc|opt|root)/)[^\s\"\']+',
                  '[path]', text)
    return ' '.join(text.split())[:limit]


def _http_failure(status, message, payload, *, provider_code=None, request_id=None):
    if status in (400, 422):
        code = 'INVALID_ARGUMENT'
    elif status == 429:
        code = 'RATE_LIMITED'
    elif 400 <= status < 500:
        code = 'PROVIDER_REJECTED'
    else:
        code = 'UNAVAILABLE'
    reason = _public_text(message, payload) or 'The upstream service returned no public error details.'
    upstream = {'http_status': status}
    for key, value in (('code', provider_code), ('request_id', request_id)):
        if text := _public_text(value, payload, 128):
            upstream[key] = text
    return ExternalToolFailure(
        code, f'{payload.get("tool_name", "Tool")} failed (HTTP {status}): {reason}',
        retryable=status in (429, 502, 503), upstream=upstream,
    )


def from_exception(error, payload):
    causes, seen = [], set()
    while error is not None and id(error) not in seen:
        seen.add(id(error))
        causes.append(error)
        error = error.__cause__ or error.__context__
    for cause in causes:
        response = getattr(cause, 'response', None)
        status = getattr(response, 'status_code', None)
        if not isinstance(status, int) or not 400 <= status <= 599:
            continue
        detail = {}
        try:
            if len(response.content) <= 32768:
                body = response.json()
                if isinstance(body, dict):
                    detail = body.get('error', body)
                    if isinstance(detail, str):
                        detail = {'message': detail}
        except (ValueError, TypeError, httpx.ResponseNotRead):
            pass
        if not isinstance(detail, dict):
            detail = {}
        headers = response.headers
        request_id = (headers.get('x-siliconcloud-trace-id') or headers.get('x-request-id')
                      or headers.get('request-id') or detail.get('request_id'))
        return _http_failure(status, detail.get('message') or detail.get('detail'), payload,
                             provider_code=detail.get('code'), request_id=request_id)
    if any(isinstance(cause, (TimeoutError, requests.Timeout, httpx.TimeoutException)) for cause in causes):
        return ExternalToolFailure('DEADLINE_EXCEEDED',
                                   'The tool request timed out; its outcome is unknown. Check existing results '
                                   'before starting a new operation.')
    if any(isinstance(cause, (requests.ConnectionError, httpx.NetworkError)) for cause in causes):
        return ExternalToolFailure('UNAVAILABLE', 'Could not connect to the tool service.', retryable=True)
    for cause in reversed(causes):
        if isinstance(cause, ToolExecutionError):
            return from_result({'value': str(cause)}, payload)
        if isinstance(cause, ValueError):
            return from_result({'value': str(cause)}, payload, code='INVALID_ARGUMENT')
    return ExternalToolFailure('TOOL_EXECUTION_FAILED',
                               'The tool failed without public error details; check its service logs.')


def from_result(result, payload, *, code='TOOL_EXECUTION_FAILED'):
    detail = result.get('value') or result.get('error') or result.get('message')
    if isinstance(detail, dict):
        detail = detail.get('message')
    message = _public_text(detail, payload) or 'The tool failed without public error details.'
    return ExternalToolFailure(code, message)
