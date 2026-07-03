from __future__ import annotations

import re
import urllib.parse

_ID_PATTERN = re.compile(r'^[A-Za-z0-9][A-Za-z0-9_.:#-]*$')
HTTP_SCHEMES = {'http', 'https'}
URL_DELIMITERS = (';', '?', '#')
QUESTION_TYPES = {
    'single_hop',
    'single_doc_multi_hop',
    'multi_doc_multi_hop',
    'table_list',
    'formula',
}
RESERVED_CASE_IDS = QUESTION_TYPES | {f'case_{item}' for item in QUESTION_TYPES}


def validate_id(value: str, kind: str = 'id') -> str:
    if not isinstance(value, str) or not value:
        raise ValueError(f'invalid {kind}: {value!r}')
    if value in {'.', '..'} or '..' in value:
        raise ValueError(f'invalid {kind}: {value!r}')
    if not _ID_PATTERN.fullmatch(value):
        raise ValueError(f'invalid {kind}: {value!r}')
    return value


def validate_case_id(value: str) -> str:
    case_id = validate_id(value.strip(), 'case_id')
    if case_id in RESERVED_CASE_IDS or case_id.startswith('case_preparation_'):
        raise ValueError(
            'case_id must be a unique sample id, '
            f'not a question type label: {case_id}'
        )
    return case_id


def normalize_chat_stream_url(url: str, field: str) -> str:
    parsed, netloc = _parse_http_url(url, field)
    if (
        _has_url_delimiter(url, parsed)
        or parsed.path not in {'/api/chat', '/api/chat/stream'}
    ):
        raise ValueError(f'{field} must end with /api/chat or /api/chat/stream')
    if parsed.path == '/api/chat':
        return f'{parsed.scheme}://{netloc}/api/chat/stream'
    return f'{parsed.scheme}://{netloc}{parsed.path}'


def normalize_http_origin(url: str, field: str) -> str:
    parsed, netloc = _parse_http_url(url, field)
    if _has_url_delimiter(url, parsed) or parsed.path not in {'', '/'}:
        raise ValueError(f'{field} must be an origin, not an API path')
    return f'{parsed.scheme}://{netloc}'


def _parse_http_url(url: str, field: str) -> tuple[urllib.parse.ParseResult, str]:
    parsed = urllib.parse.urlparse(url)
    if not parsed.scheme or not parsed.netloc:
        raise ValueError(f'invalid {field}: {url!r}')
    if parsed.scheme not in HTTP_SCHEMES:
        raise ValueError(f'{field} must use http or https')
    return parsed, _canonical_netloc(parsed, field)


def _canonical_netloc(parsed: urllib.parse.ParseResult, field: str) -> str:
    if parsed.username is not None or parsed.password is not None or '@' in parsed.netloc:
        raise ValueError(f'{field} must not include userinfo')
    host = parsed.hostname
    if not host:
        raise ValueError(f'invalid {field}: {parsed.geturl()!r}')
    try:
        port = parsed.port
    except ValueError as exc:
        raise ValueError(f'invalid {field}: {parsed.geturl()!r}') from exc
    netloc = f'[{host}]' if ':' in host and not host.startswith('[') else host
    return f'{netloc}:{port}' if port is not None else netloc


def _has_url_delimiter(url: str, parsed: urllib.parse.ParseResult) -> bool:
    if any(delimiter in parsed.netloc for delimiter in URL_DELIMITERS):
        return True
    tail = url.split('://', 1)[1][len(parsed.netloc):]
    return any(delimiter in tail for delimiter in URL_DELIMITERS)
