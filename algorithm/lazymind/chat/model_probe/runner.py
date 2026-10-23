"""Execute exactly one bounded inference request and return only safe metadata."""
import asyncio
import base64
import hashlib
import hmac
import json
import math
import time
from urllib.parse import urlsplit

import httpx

from .contracts import ProbeInput, UnsupportedProbe
from .profiles import build_plan, normalize_type

TOTAL_TIMEOUT_SECONDS = 35.0
MAX_RESPONSE_BYTES = 8 * 1024 * 1024
MAX_JSON_RESPONSE_BYTES = 2 * 1024 * 1024
HTTP_TIMEOUT = httpx.Timeout(connect=5.0, read=30.0, write=5.0, pool=5.0)


def _valid_url(value: str) -> bool:
    try:
        parsed = urlsplit(value)
        _ = parsed.port
        return parsed.scheme in {'http', 'https'} and bool(parsed.hostname) and not any((
            parsed.username, parsed.password, parsed.query, parsed.fragment,
        ))
    except ValueError:
        return False


def _headers(request: ProbeInput, auth: str) -> dict[str, str]:
    headers = {'Accept-Encoding': 'identity'}
    if auth == 'anthropic':
        headers['anthropic-version'] = '2023-06-01'
    if request.skip_auth:
        return headers
    key = request.api_key
    if request.source.strip().lower() == 'sensenova' and ':' in key and not key.startswith('sk-'):
        access, secret = key.split(':', 1)

        def encode(value):
            return base64.urlsafe_b64encode(value).decode('ascii').rstrip('=')
        now = int(time.time())
        token = '.'.join((encode(b'{"alg":"HS256","typ":"JWT"}'),
                          encode(json.dumps({'iss': access, 'nbf': now, 'exp': now + 300}).encode())))
        key = token + '.' + encode(hmac.new(secret.encode(), token.encode(), hashlib.sha256).digest())
    if auth == 'anthropic':
        if key:
            headers['x-api-key'] = key
    elif key:
        headers['Authorization'] = 'Bearer ' + key
    return headers


def _number(value) -> bool:
    try:
        return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)
    except OverflowError:
        return False


def _vector(value) -> bool:
    return isinstance(value, list) and bool(value) and all(_number(item) for item in value)


def _nonempty_string(value) -> bool:
    return isinstance(value, str) and bool(value.strip())


def _media_reference(value: dict) -> bool:
    url = value.get('url')
    if isinstance(url, str):
        try:
            parsed = urlsplit(url)
            # Generated URLs commonly carry signatures. Inspect only; never fetch.
            if parsed.scheme in {'http', 'https'} and parsed.hostname:
                return True
        except ValueError:
            pass
    encoded = value.get('b64_json')
    if not isinstance(encoded, str) or not encoded:
        return False
    try:
        return bool(base64.b64decode(encoded.split(',', 1)[-1], validate=True))
    except (ValueError, TypeError):
        return False


def _validate_response(kind: str, payload) -> tuple[str, str]:
    if not isinstance(payload, dict):
        return 'failed', 'invalid_response'
    if kind == 'chat':
        choices = payload.get('choices')
        if not isinstance(choices, list) or not choices:
            return 'failed', 'empty_response'
        choice = choices[0]
        if not isinstance(choice, dict) or not isinstance(choice.get('message'), dict):
            return 'failed', 'invalid_response'
        message = choice['message']
        valid = (choice.get('finish_reason') == 'length' or _nonempty_string(message.get('content'))
                 or _nonempty_string(message.get('reasoning_content')) or bool(message.get('tool_calls')))
    elif kind == 'anthropic':
        blocks = payload.get('content')
        valid = payload.get('stop_reason') == 'max_tokens' or any(
            isinstance(item, dict) and item.get('type') == 'text' and _nonempty_string(item.get('text'))
            for item in (blocks if isinstance(blocks, list) else [])
        )
    elif kind in {'embedding', 'qwen_embedding', 'sensenova_embedding'}:
        rows = payload.get('data')
        if kind == 'qwen_embedding':
            output = payload.get('output')
            rows = output.get('embeddings') if isinstance(output, dict) else None
        elif kind == 'sensenova_embedding':
            rows = payload.get('embeddings')
        if isinstance(rows, dict):
            rows = [rows]
        valid = isinstance(rows, list) and bool(rows) and all(
            isinstance(row, dict) and _vector(row.get('embedding')) for row in rows
        )
    elif kind in {'rerank', 'qwen_rerank'}:
        root = payload.get('output') if kind == 'qwen_rerank' else payload
        rows = root.get('results') if isinstance(root, dict) else None
        valid = isinstance(rows, list) and bool(rows) and all(
            isinstance(row, dict) and type(row.get('index')) is int and row['index'] == 0
            and _number(row.get('relevance_score')) for row in rows
        )
    elif kind == 'image':
        rows = payload.get('data')
        valid = isinstance(rows, list) and bool(rows) and all(
            isinstance(row, dict) and _media_reference(row) for row in rows
        )
    elif kind == 'video':
        if payload.get('status') in {'failed', 'cancelled', 'expired'}:
            return 'failed', 'upstream_error'
        return ('accepted', 'accepted') if _nonempty_string(payload.get('id')) else ('failed', 'invalid_response')
    elif kind == 'stt':
        # An empty transcript for the intentionally silent probe is a valid result.
        valid = isinstance(payload.get('text'), str)
    else:
        valid = False
    return ('passed', 'ok') if valid else ('failed', 'empty_response')


def _error_code(status: int, payload) -> str | None:
    base_response = payload.get('base_resp') if isinstance(payload, dict) else None
    if isinstance(base_response, dict) and str(base_response.get('status_code', 0)) not in {'0', 'None'}:
        return {'1001': 'timeout', '1002': 'rate_limited', '1004': 'authentication_failed',
                '1008': 'quota_exceeded', '1041': 'rate_limited', '1042': 'invalid_request',
                '2013': 'invalid_request', '2045': 'rate_limited', '2049': 'authentication_failed',
                '2056': 'quota_exceeded'}.get(str(base_response.get('status_code')), 'upstream_error')
    error = payload.get('error') if isinstance(payload, dict) else None
    if error:
        error = error if isinstance(error, dict) else {}
        code = str(error.get('code') or error.get('type') or '').lower()
        if code in {'insufficient_quota', 'quota_exceeded', 'quotaexceeded', 'balance_exhausted',
                    'credit_balance_exhausted', 'accountquotaexceeded'}:
            return 'quota_exceeded'
        if status < 400:
            return 'upstream_error'
    if 200 <= status < 300:
        return None
    return {400: 'invalid_request', 401: 'authentication_failed', 402: 'quota_exceeded',
            403: 'permission_denied', 404: 'model_not_found', 422: 'invalid_request',
            429: 'rate_limited'}.get(status, 'upstream_error')


async def _execute(client: httpx.AsyncClient, request: ProbeInput, plan) -> tuple[str, str, int]:
    url = request.url.rstrip('/') + '/' + plan.endpoint
    options = {key: value for key, value in {'json': plan.json, 'data': plan.data, 'files': plan.files}.items()
               if value is not None}
    async with client.stream('POST', url, headers=_headers(request, plan.auth), timeout=HTTP_TIMEOUT,
                             follow_redirects=False, **options) as response:
        # The body cap must apply before decompression. Explicitly request identity
        # and reject a server that ignores it instead of inflating unbounded bytes.
        if response.headers.get('content-encoding', 'identity').lower() not in {'', 'identity'}:
            return 'failed', _error_code(response.status_code, None) or 'invalid_response', response.status_code
        content = bytearray()
        limit = MAX_RESPONSE_BYTES if plan.response_kind == 'tts' else min(MAX_RESPONSE_BYTES, MAX_JSON_RESPONSE_BYTES)
        async for chunk in response.aiter_bytes():
            if len(content) + len(chunk) > limit:
                return 'failed', 'invalid_response', response.status_code
            content.extend(chunk)
        audio = bytes(content)
        if plan.response_kind == 'tts' and 200 <= response.status_code < 300:
            is_audio = ((len(audio) >= 12 and audio[:4] == b'RIFF' and audio[8:12] == b'WAVE')
                        or audio.startswith((b'ID3', b'OggS', b'fLaC', b'\xff\xfb', b'\xff\xf3', b'\xff\xf2')))
            if is_audio:
                return 'passed', 'ok', response.status_code
        try:
            payload = json.loads(content)
        except ValueError:
            code = _error_code(response.status_code, None) or ('empty_response' if not content else 'invalid_response')
            return 'failed', code, response.status_code
        if error := _error_code(response.status_code, payload):
            return 'failed', error, response.status_code
        status, code = _validate_response(plan.response_kind, payload)
        return status, code, response.status_code


async def probe_model(request: ProbeInput, *, client: httpx.AsyncClient | None = None) -> dict:
    """Run one model-specific inference; credentials and provider bodies never leave here."""
    started = time.monotonic()
    result = {'status': 'failed', 'code': 'invalid_request', 'model_type': normalize_type(request.model_type)}
    try:
        if not request.model.strip() or not _valid_url(request.url):
            return result | {'elapsed_ms': int((time.monotonic() - started) * 1000)}
        plan = build_plan(request)
        async with asyncio.timeout(TOTAL_TIMEOUT_SECONDS):
            if client is None:
                async with httpx.AsyncClient() as owned_client:
                    status, code, http_status = await _execute(owned_client, request, plan)
            else:
                status, code, http_status = await _execute(client, request, plan)
        result.update(status=status, code=code, http_status=http_status)
    except UnsupportedProbe as exc:
        result.update(status='unsupported', code=exc.code)
    except (TimeoutError, httpx.TimeoutException):
        result['code'] = 'timeout'
    except httpx.RequestError:
        result['code'] = 'connection_error'
    except (TypeError, ValueError, KeyError):
        result['code'] = 'invalid_response'
    result['elapsed_ms'] = max(0, int((time.monotonic() - started) * 1000))
    return result
