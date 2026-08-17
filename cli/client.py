"""HTTP client with automatic Bearer-token injection and token refresh."""

import json
import mimetypes
import sys
import tempfile
import uuid
from typing import Any, BinaryIO, Dict, List, Optional, Tuple
from urllib import error, request

from cli import credentials
from cli.config import AUTH_API_PREFIX, DEFAULT_SERVER_URL


class ApiError(RuntimeError):
    """Raised when the server returns a non-success response."""

    def __init__(
        self,
        status_code: int,
        message: str,
        payload: Optional[Dict[str, Any]] = None,
        *,
        is_http_error: bool = True,
    ):
        super().__init__(status_code, message, payload or {})
        self.status_code = status_code
        self.payload = payload or {}
        # True when raised from a real HTTP 4xx/5xx response; False when
        # raised from an envelope business-error code (so auth_request can
        # avoid triggering token refresh on a business code of 401).
        self.is_http_error = is_http_error

    def __str__(self) -> str:
        return str(self.args[1])


# ---------------------------------------------------------------------------
# low-level helpers
# ---------------------------------------------------------------------------

def _decode_body(body: bytes) -> Dict[str, Any]:
    text = body.decode('utf-8', errors='replace').strip()
    if not text:
        return {}
    try:
        payload = json.loads(text)
    except json.JSONDecodeError as exc:
        raise RuntimeError(f'Invalid JSON response: {text}') from exc
    if not isinstance(payload, dict):
        raise RuntimeError(f'Unexpected response type: {type(payload).__name__}')
    return payload


def _build_api_error(
    status_code: int,
    response_body: bytes,
    default_message: str,
) -> ApiError:
    try:
        parsed = _decode_body(response_body)
    except RuntimeError:
        text = response_body.decode('utf-8', errors='replace').strip()
        return ApiError(status_code, text or default_message)
    message = (
        parsed.get('message')
        or parsed.get('msg')
        or parsed.get('detail')
        or default_message
    )
    return ApiError(status_code, str(message), parsed)


def _normalize_response_payload(parsed: Dict[str, Any]) -> Dict[str, Any]:
    """Unwrap service envelopes while preserving error semantics."""
    code = parsed.get('code')
    if not isinstance(code, int) or 'message' not in parsed:
        return parsed

    if code not in (0, 200):
        raise ApiError(
            code,
            str(parsed.get('message') or 'request failed'),
            parsed,
            is_http_error=False,
        )

    data = parsed.get('data')
    if isinstance(data, dict):
        return data
    if data is None:
        return {}
    return {'data': data}


# ---------------------------------------------------------------------------
# generic request
# ---------------------------------------------------------------------------

def raw_request(
    method: str,
    url: str,
    payload: Optional[Dict[str, Any]] = None,
    headers: Optional[Dict[str, str]] = None,
    body: Optional[bytes] = None,
    timeout: float = 30.0,
) -> Dict[str, Any]:
    """Send an HTTP request and return the parsed JSON response dict."""
    req_headers: Dict[str, str] = {'Accept': 'application/json'}
    if headers:
        req_headers.update(headers)

    data = body
    if data is None and payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode('utf-8')
        req_headers.setdefault('Content-Type', 'application/json')

    req = request.Request(
        url=url, data=data, headers=req_headers, method=method.upper(),
    )
    try:
        with request.urlopen(req, timeout=timeout) as resp:
            resp_body = resp.read()
    except error.HTTPError as exc:
        resp_body = exc.read()
        raise _build_api_error(
            exc.code, resp_body, str(exc.reason or f'HTTP {exc.code}'),
        )
    except error.URLError as exc:
        raise RuntimeError(f'Cannot reach {url}: {exc}') from exc

    parsed = _decode_body(resp_body)
    return _normalize_response_payload(parsed)


# ---------------------------------------------------------------------------
# server URL resolution
# ---------------------------------------------------------------------------

def resolve_server_url(cli_url: Optional[str] = None) -> str:
    """Pick server URL: CLI flag > stored credential > env/default."""
    if cli_url:
        return cli_url.rstrip('/')
    stored = credentials.server_url()
    if stored:
        return stored.rstrip('/')
    return DEFAULT_SERVER_URL.rstrip('/')


# ---------------------------------------------------------------------------
# token refresh
# ---------------------------------------------------------------------------

def _try_refresh(server: str) -> bool:
    """Attempt to refresh the access token.  Returns True on success."""
    before = credentials.load() or {}
    rt = before.get('refresh_token')
    if not rt:
        return False
    url = f'{server}{AUTH_API_PREFIX}/refresh'
    try:
        data = raw_request('POST', url, payload={'refresh_token': rt})
    except (ApiError, RuntimeError):
        # The Go MCP bridge and Python CLI share one rotating refresh token.
        # If the other process won the race, reuse its atomically-written pair.
        latest = credentials.load() or {}
        return bool(
            latest.get('access_token')
            and latest.get('refresh_token')
            and (
                latest.get('access_token') != before.get('access_token')
                or latest.get('refresh_token') != rt
            )
        )
    new_access = data.get('access_token')
    new_refresh = data.get('refresh_token')
    if not new_access or not new_refresh:
        return False
    creds = credentials.load() or {}
    creds['access_token'] = new_access
    creds['refresh_token'] = new_refresh
    creds['expires_in'] = data.get('expires_in', 0)
    # Preserve the originally-logged-in server URL instead of overwriting
    # it with a transient --server flag value used only for this refresh.
    creds.setdefault('server_url', server)
    credentials.save(creds)
    return True


# ---------------------------------------------------------------------------
# authenticated requests
# ---------------------------------------------------------------------------

def _auth_headers(token: str) -> Dict[str, str]:
    return {'Authorization': f'Bearer {token}'}


def auth_request(
    method: str,
    path: str,
    server: Optional[str] = None,
    payload: Optional[Dict[str, Any]] = None,
    headers: Optional[Dict[str, str]] = None,
    body: Optional[Any] = None,
    timeout: float = 30.0,
) -> Dict[str, Any]:
    """Like *raw_request* but injects the stored Bearer token.

    Automatically refreshes the token once if it looks expired.
    """
    server = resolve_server_url(server)
    token = credentials.access_token()
    if token is None:
        print('Not logged in. Run `lazymind login` first.', file=sys.stderr)
        raise SystemExit(1)

    if credentials.is_token_expired():
        if not _try_refresh(server):
            print(
                'Session expired.  Run `lazymind login` to re-authenticate.',
                file=sys.stderr,
            )
            raise SystemExit(1)
        token = credentials.access_token()
        if token is None:
            print(
                'Session expired.  Run `lazymind login` to re-authenticate.',
                file=sys.stderr,
            )
            raise SystemExit(1)

    url = f'{server}{path}'
    hdrs = _auth_headers(token)
    if headers:
        hdrs.update(headers)

    try:
        return raw_request(method, url, payload=payload, headers=hdrs,
                           body=body, timeout=timeout)
    except ApiError as exc:
        # Only a true HTTP 401 from the server should trigger token refresh;
        # an envelope business code of 401 must not loop through refresh.
        if exc.is_http_error and exc.status_code == 401:
            if _try_refresh(server):
                token = credentials.access_token()
                if token is None:
                    print(
                        'Session expired.  Run `lazymind login` to '
                        're-authenticate.',
                        file=sys.stderr,
                    )
                    raise SystemExit(1)
                hdrs = _auth_headers(token)
                if headers:
                    hdrs.update(headers)
                return raw_request(method, url, payload=payload, headers=hdrs,
                                   body=body, timeout=timeout)
            print(
                'Session expired.  Run `lazymind login` to re-authenticate.',
                file=sys.stderr,
            )
            raise SystemExit(1)
        raise


# ---------------------------------------------------------------------------
# multipart upload helper
# ---------------------------------------------------------------------------

def _escape_header_value(value: str) -> str:
    """Reject CR/LF to prevent MIME header injection; escape double quotes."""
    if '\r' in value or '\n' in value:
        raise ValueError(
            f'Header value contains illegal line characters: {value!r}',
        )
    return value.replace('"', '\\"')


def build_multipart_body(
    fields: Dict[str, str],
    file_field: str,
    filename: str,
    file_content: bytes,
) -> Tuple[bytes, Dict[str, str]]:
    """Build a multipart/form-data body with one file."""
    boundary = f'----LazyMindBoundary{uuid.uuid4().hex}'
    parts: List[bytes] = []

    for key, value in fields.items():
        safe_key = _escape_header_value(key)
        parts.append(f'--{boundary}\r\n'.encode())
        parts.append(
            f'Content-Disposition: form-data; name="{safe_key}"\r\n\r\n'.encode(),
        )
        parts.append(str(value).encode('utf-8'))
        parts.append(b'\r\n')

    safe_field = _escape_header_value(file_field)
    safe_filename = _escape_header_value(filename)
    content_type = mimetypes.guess_type(filename)[0] or 'application/octet-stream'
    parts.append(f'--{boundary}\r\n'.encode())
    parts.append(
        f'Content-Disposition: form-data; name="{safe_field}"; '
        f'filename="{safe_filename}"\r\n'.encode(),
    )
    parts.append(f'Content-Type: {content_type}\r\n\r\n'.encode())
    parts.append(file_content)
    parts.append(b'\r\n')
    parts.append(f'--{boundary}--\r\n'.encode())

    body = b''.join(parts)
    return body, {'Content-Type': f'multipart/form-data; boundary={boundary}'}


def build_multipart_file(
    fields: Dict[str, str],
    file_field: str,
    filename: str,
    source_path: str,
) -> Tuple[BinaryIO, Dict[str, str]]:
    """Build a multipart/form-data payload backed by a temp file."""
    boundary = f'----LazyMindBoundary{uuid.uuid4().hex}'
    handle = tempfile.SpooledTemporaryFile(max_size=1024 * 1024)

    try:
        def _write(chunk: bytes) -> None:
            handle.write(chunk)

        for key, value in fields.items():
            safe_key = _escape_header_value(key)
            _write(f'--{boundary}\r\n'.encode())
            _write(
                f'Content-Disposition: form-data; name="{safe_key}"\r\n\r\n'.encode(),
            )
            _write(str(value).encode('utf-8'))
            _write(b'\r\n')

        safe_field = _escape_header_value(file_field)
        safe_filename = _escape_header_value(filename)
        content_type = mimetypes.guess_type(filename)[0] or 'application/octet-stream'
        _write(f'--{boundary}\r\n'.encode())
        _write(
            f'Content-Disposition: form-data; name="{safe_field}"; '
            f'filename="{safe_filename}"\r\n'.encode(),
        )
        _write(f'Content-Type: {content_type}\r\n\r\n'.encode())

        with open(source_path, 'rb') as src:
            while True:
                chunk = src.read(1024 * 1024)
                if not chunk:
                    break
                _write(chunk)

        _write(b'\r\n')
        _write(f'--{boundary}--\r\n'.encode())
        size = handle.tell()
        handle.seek(0)
        return handle, {
            'Content-Type': f'multipart/form-data; boundary={boundary}',
            'Content-Length': str(size),
        }
    except BaseException:
        handle.close()
        raise


def auth_upload(
    path: str,
    fields: Dict[str, str],
    file_field: str,
    filename: str,
    source_path: str,
    server: Optional[str] = None,
    timeout: float = 300.0,
) -> Dict[str, Any]:
    """Upload a file via multipart/form-data with Bearer auth."""
    body, hdrs = build_multipart_file(fields, file_field, filename, source_path)
    try:
        return auth_request(
            'POST', path, server=server, headers=hdrs, body=body, timeout=timeout,
        )
    finally:
        body.close()


# ---------------------------------------------------------------------------
# printing helpers
# ---------------------------------------------------------------------------

def print_json(payload: Any) -> None:
    print(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=False))
