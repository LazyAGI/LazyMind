from __future__ import annotations

import ipaddress
import socket
from typing import Any, Dict, List
from urllib.parse import urljoin, urlparse

import requests
from bs4 import BeautifulSoup

from lazymind.chat.engine.tools._utils import absolute_url, truncate_text
from lazymind.config import config as _cfg

_MAX_FETCH_TEXT_LEN = 4000
_MAX_FETCH_BYTES = 1024 * 1024
_MAX_REDIRECTS = 5
_ALLOWED_URL_SCHEMES = {'http', 'https'}


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
    for chunk in response.iter_content(chunk_size=16384):
        if not chunk:
            continue
        remaining = max_bytes - total
        if remaining <= 0:
            break
        chunks.append(chunk[:remaining])
        total += len(chunk[:remaining])
        if len(chunk) > remaining:
            break
    response._content = b''.join(chunks)


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


def extract_web_page_text(html: str) -> str:
    soup = BeautifulSoup(html, 'html.parser')

    for tag in soup(['script', 'style', 'noscript']):
        tag.decompose()

    content_root = soup.find('main') or soup.find('article') or soup.body or soup
    lines: List[str] = []
    for node in content_root.find_all(['h1', 'h2', 'h3', 'p', 'li']):
        text = node.get_text(' ', strip=True)
        if text:
            lines.append(text)

    if not lines:
        text = content_root.get_text('\n', strip=True)
        lines = [line.strip() for line in text.splitlines() if line.strip()]

    deduped_lines: List[str] = []
    seen: set[str] = set()
    for line in lines:
        if line in seen:
            continue
        seen.add(line)
        deduped_lines.append(line)
    return '\n'.join(deduped_lines)


def extract_web_page_title(soup: BeautifulSoup) -> str:
    if soup.title and soup.title.string:
        return soup.title.string.strip()

    og_title = soup.find('meta', attrs={'property': 'og:title'})
    if og_title and og_title.get('content'):
        return str(og_title['content']).strip()
    return ''


def extract_web_page_description(soup: BeautifulSoup) -> str:
    candidates = [
        {'name': 'description'},
        {'property': 'og:description'},
    ]
    for attrs in candidates:
        tag = soup.find('meta', attrs=attrs)
        if tag and tag.get('content'):
            return str(tag['content']).strip()
    return ''


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
    if 'text/html' not in content_type and 'application/xhtml+xml' not in content_type:
        raw_text = response.text.strip()
        return {
            'status': 'ok',
            'url': normalized_url,
            'final_url': response.url,
            'status_code': response.status_code,
            'content_type': content_type,
            'title': '',
            'description': '',
            'content': truncate_text(raw_text, text_limit),
        }

    soup = BeautifulSoup(response.text, 'html.parser')
    return {
        'status': 'ok',
        'url': normalized_url,
        'final_url': response.url,
        'status_code': response.status_code,
        'content_type': content_type,
        'title': extract_web_page_title(soup),
        'description': truncate_text(extract_web_page_description(soup), 500),
        'content': truncate_text(extract_web_page_text(response.text), text_limit),
    }
