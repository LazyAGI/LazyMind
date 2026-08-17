from __future__ import annotations

import ipaddress
import socket
from typing import Any, Dict, List
from urllib.parse import urljoin, urlparse

import requests
from bs4 import BeautifulSoup

from lazymind.chat.engine.tools._utils import absolute_url
from lazymind.config import config as _cfg

_MAX_FETCH_TEXT_LEN = 4000
_MAX_FETCH_BYTES = 1024 * 1024
_MAX_REDIRECTS = 5
_MAX_PAGE_LINKS = 50
_ALLOWED_URL_SCHEMES = {'http', 'https'}
_TEXT_CONTENT_TYPES = {
    'application/atom+xml',
    'application/json',
    'application/rss+xml',
    'application/xml',
}


def coerce_web_int(value: Any, default: int) -> int:
    if value is None or value == '':
        return default
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def is_public_ip_address(value: str) -> bool:
    try:
        return ipaddress.ip_address(value).is_global
    except ValueError:
        return False


def resolve_public_host(hostname: str) -> None:
    try:
        addrinfos = socket.getaddrinfo(hostname, None, type=socket.SOCK_STREAM)
    except socket.gaierror as exc:
        raise ValueError(f'could not resolve url host: {hostname}') from exc

    resolved_ips = {item[4][0] for item in addrinfos}
    if not resolved_ips:
        raise ValueError(f'could not resolve url host: {hostname}')

    blocked_ips = [ip for ip in resolved_ips if not is_public_ip_address(ip)]
    if blocked_ips:
        raise ValueError('url host resolves to a non-public address')


def validate_public_http_url(url: str) -> str:
    parsed = urlparse(url)
    if parsed.scheme not in _ALLOWED_URL_SCHEMES:
        raise ValueError('url scheme must be http or https')
    if not parsed.hostname:
        raise ValueError('url host is required')
    if parsed.username or parsed.password:
        raise ValueError('url credentials are not allowed')

    hostname = parsed.hostname.rstrip('.')
    if not hostname:
        raise ValueError('url host is required')
    resolve_public_host(hostname)
    return url


def read_limited_response(response: requests.Response, max_bytes: int = _MAX_FETCH_BYTES) -> None:
    chunks: List[bytes] = []
    total = 0
    truncated = False
    for chunk in response.iter_content(chunk_size=16384):
        if not chunk:
            continue
        remaining = max_bytes - total
        if remaining <= 0:
            truncated = True
            break
        chunks.append(chunk[:remaining])
        total += len(chunk[:remaining])
        if len(chunk) > remaining:
            truncated = True
            break
    response._content = b''.join(chunks)
    response._lazymind_response_truncated = truncated


def decode_response_text(response: requests.Response) -> str:
    encoding = str(response.encoding or '').lower()
    if not encoding or encoding in {'iso-8859-1', 'latin-1', 'latin1'}:
        encoding = str(response.apparent_encoding or 'utf-8')
    try:
        return response.content.decode(encoding, errors='replace')
    except LookupError:
        return response.content.decode('utf-8', errors='replace')


def fetch_public_url(
    session: requests.Session,
    url: str,
    *,
    timeout: int,
    headers: Dict[str, str],
) -> requests.Response:
    current_url = validate_public_http_url(url)
    for _ in range(_MAX_REDIRECTS + 1):
        response = session.get(
            current_url,
            timeout=timeout,
            headers=headers,
            allow_redirects=False,
            stream=True,
        )

        if not response.is_redirect:
            read_limited_response(response)
            return response

        location = response.headers.get('Location')
        response.close()
        if not location:
            raise ValueError('redirect response is missing Location header')
        current_url = validate_public_http_url(urljoin(current_url, location))

    raise ValueError('too many redirects while fetching url')


def _extract_readable_text(soup: BeautifulSoup) -> str:
    content_root = soup.body or soup
    for tag in content_root.find_all(['script', 'style', 'noscript']):
        tag.decompose()
    text = content_root.get_text('\n', strip=True)
    return '\n'.join(line.strip() for line in text.splitlines() if line.strip())


def extract_web_page_title(soup: BeautifulSoup) -> str:
    if soup.title and soup.title.string:
        return soup.title.string.strip()
    heading = soup.find('h1')
    if heading:
        return heading.get_text(' ', strip=True)
    return ''


def _extract_page_links(soup: BeautifulSoup, base_url: str) -> List[Dict[str, Any]]:
    links: List[Dict[str, Any]] = []
    seen: set[str] = set()
    for tag in soup.find_all(['a', 'area'], href=True):
        href = str(tag.get('href') or '').strip()
        if not href or href.startswith('#'):
            continue
        target = urljoin(base_url, href)
        parsed = urlparse(target)
        if (parsed.scheme not in _ALLOWED_URL_SCHEMES or not parsed.hostname
                or parsed.username or parsed.password):
            continue
        normalized = parsed._replace(fragment='').geturl()
        if normalized in seen:
            continue
        seen.add(normalized)
        links.append({
            'text': tag.get_text(' ', strip=True)[:200],
            'target_url': normalized,
        })
        if len(links) >= _MAX_PAGE_LINKS:
            break
    return links


def _truncate_page_content(content: str, max_chars: int) -> tuple[str, bool]:
    if len(content) <= max_chars:
        return content, False
    suffix = '...'
    return content[:max(0, max_chars - len(suffix))] + suffix, True


def fetch_url_content(url: str) -> Dict[str, Any]:
    normalized_url = absolute_url(url)
    if not normalized_url:
        raise ValueError('url is required')
    normalized_url = validate_public_http_url(normalized_url)

    timeout = coerce_web_int(_cfg['web_search_timeout'], 10)
    text_limit = max(200, coerce_web_int(_cfg['url_fetch_max_length'], _MAX_FETCH_TEXT_LEN))
    headers = {
        'User-Agent': (
            'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 '
            '(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'
        )
    }

    with requests.sessions.Session() as session:
        response = fetch_public_url(
            session,
            normalized_url,
            timeout=timeout,
            headers=headers,
        )
        response.raise_for_status()

    content_type = str(response.headers.get('Content-Type') or '').lower()
    response_truncated = bool(getattr(response, '_lazymind_response_truncated', False))
    final_url = response.url or normalized_url
    media_type = content_type.split(';', 1)[0].strip()
    is_html = media_type in {'text/html', 'application/xhtml+xml'}
    is_text = media_type.startswith('text/') or media_type in _TEXT_CONTENT_TYPES
    if not is_html and not is_text:
        display_content_type = media_type or 'unknown'
        raise ValueError(f'unsupported url content type: {display_content_type}')

    response_text = decode_response_text(response)
    if not is_html:
        raw_text = response_text.strip()
        content, content_truncated = _truncate_page_content(raw_text, text_limit)
        return {
            'status': 'ok',
            'source_status': 'non_html',
            'url': normalized_url,
            'final_url': final_url,
            'status_code': response.status_code,
            'content_type': content_type,
            'title': '',
            'content': content,
            'content_truncated': content_truncated or response_truncated,
            'links': [],
        }

    soup = BeautifulSoup(response_text, 'html.parser')
    title = extract_web_page_title(soup)
    links = _extract_page_links(soup, final_url)
    readable_content = _extract_readable_text(soup)
    content, content_truncated = _truncate_page_content(readable_content, text_limit)
    return {
        'status': 'ok',
        'source_status': 'ok',
        'url': normalized_url,
        'final_url': final_url,
        'status_code': response.status_code,
        'content_type': content_type,
        'title': (title or (urlparse(final_url).hostname or ''))[:300],
        'content': content,
        'content_truncated': content_truncated or response_truncated,
        'links': links,
    }
