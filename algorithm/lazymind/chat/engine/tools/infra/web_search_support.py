from __future__ import annotations

import ipaddress
import re
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
_MAX_PAGE_IMAGES = 20
_ALLOWED_URL_SCHEMES = {'http', 'https'}
_LOW_VALUE_REGION_PATTERN = re.compile(
    r'cookie|subscribe|newsletter|share|social|related|recommend|advert|login|signup|comment|breadcrumb',
    re.IGNORECASE,
)


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
    response._lazymind_raw_bytes = total
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
    content_root = (
        soup.find('article')
        or soup.find('main')
        or soup.find(attrs={'role': 'main'})
        or soup.body
        or soup
    )
    for tag in content_root.find_all(['script', 'style', 'noscript', 'nav', 'header', 'footer', 'aside',
                                      'form', 'button', 'iframe']):
        tag.decompose()
    for tag in content_root.find_all(True):
        if tag.parent is None or tag.attrs is None:
            continue
        classes = tag.get('class') or []
        if isinstance(classes, str):
            classes = [classes]
        marker = ' '.join([
            str(tag.get('id') or ''),
            ' '.join(str(value) for value in classes),
        ])
        if marker.strip() and _LOW_VALUE_REGION_PATTERN.search(marker):
            tag.decompose()

    lines: List[str] = []
    for node in content_root.find_all(['h1', 'h2', 'h3', 'p', 'li', 'table', 'blockquote', 'pre', 'code']):
        if node.name == 'code' and node.find_parent('pre') is not None:
            continue
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


def extract_web_page_text(html: str) -> str:
    return _extract_readable_text(BeautifulSoup(html, 'html.parser'))


def extract_web_page_title(soup: BeautifulSoup) -> str:
    for attrs in ({'property': 'og:title'}, {'name': 'twitter:title'}):
        tag = soup.find('meta', attrs=attrs)
        if tag and tag.get('content'):
            return str(tag['content']).strip()
    if soup.title and soup.title.string:
        return soup.title.string.strip()
    heading = soup.find('h1')
    if heading:
        return heading.get_text(' ', strip=True)
    return ''


def extract_web_page_description(soup: BeautifulSoup) -> str:
    candidates = [
        {'name': 'description'},
        {'property': 'og:description'},
        {'name': 'twitter:description'},
    ]
    for attrs in candidates:
        tag = soup.find('meta', attrs=attrs)
        if tag and tag.get('content'):
            return str(tag['content']).strip()
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
            'id': len(links) + 1,
            'text': tag.get_text(' ', strip=True)[:200],
            'target_url': normalized,
        })
        if len(links) >= _MAX_PAGE_LINKS:
            break
    return links


def _extract_page_metadata(soup: BeautifulSoup, page_url: str) -> Dict[str, Any]:
    domain = (urlparse(page_url).hostname or '').lower()
    canonical_tag = soup.select_one('link[rel~="canonical"]')
    canonical_url = urljoin(page_url, canonical_tag.get('href')) if canonical_tag and canonical_tag.get('href') else ''
    site_name_tag = soup.find('meta', attrs={'property': 'og:site_name'})
    site_name = str(site_name_tag.get('content') or '').strip() if site_name_tag else ''
    favicon_tag = soup.select_one('link[rel~="icon"], link[rel="apple-touch-icon"]')
    favicon_url = urljoin(page_url, favicon_tag.get('href')) if favicon_tag and favicon_tag.get('href') else ''

    images: List[Dict[str, Any]] = []
    seen: set[str] = set()
    for attrs in ({'property': 'og:image'}, {'name': 'twitter:image'}):
        tag = soup.find('meta', attrs=attrs)
        image_url = urljoin(page_url, str(tag.get('content') or '')) if tag else ''
        if image_url and image_url not in seen:
            seen.add(image_url)
            images.append({'url': image_url, 'alt': '', 'source_url': page_url})
    for tag in soup.find_all('img', src=True):
        image_url = urljoin(page_url, str(tag.get('src') or ''))
        if not image_url or image_url in seen:
            continue
        seen.add(image_url)
        images.append({'url': image_url, 'alt': str(tag.get('alt') or '').strip()[:200], 'source_url': page_url})
        if len(images) >= _MAX_PAGE_IMAGES:
            break
    return {
        'domain': domain,
        'canonical_url': canonical_url,
        'site_name': site_name or domain,
        'favicon_url': favicon_url,
        'image_urls': images[:_MAX_PAGE_IMAGES],
    }


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
    raw_bytes = int(getattr(response, '_lazymind_raw_bytes', len(response.content)))
    response_truncated = bool(getattr(response, '_lazymind_response_truncated', False))
    final_url = response.url or normalized_url
    if content_type.startswith('image/'):
        return {
            'status': 'ok',
            'source_status': 'image',
            'url': normalized_url,
            'final_url': final_url,
            'status_code': response.status_code,
            'content_type': content_type,
            'raw_bytes': raw_bytes,
            'response_bytes_truncated': response_truncated,
            'title': (urlparse(final_url).path.rsplit('/', 1)[-1] or 'Image')[:300],
            'description': '',
            'content': f'Image resource ({content_type.split(";", 1)[0]})',
            'content_chars': 0,
            'returned_content_chars': 0,
            'content_max_chars': text_limit,
            'content_truncated': False,
            'truncation_strategy': 'none',
            'links': [],
            'metadata': {
                'domain': (urlparse(final_url).hostname or '').lower(),
                'image_urls': [{'url': final_url, 'alt': '', 'source_url': final_url}],
            },
        }
    response_text = decode_response_text(response)
    if 'text/html' not in content_type and 'application/xhtml+xml' not in content_type:
        raw_text = response_text.strip()
        content, content_truncated = _truncate_page_content(raw_text, text_limit)
        return {
            'status': 'ok',
            'source_status': 'non_html',
            'url': normalized_url,
            'final_url': response.url,
            'status_code': response.status_code,
            'content_type': content_type,
            'raw_bytes': raw_bytes,
            'response_bytes_truncated': response_truncated,
            'title': '',
            'description': '',
            'content': content,
            'content_chars': len(raw_text),
            'returned_content_chars': len(content),
            'content_max_chars': text_limit,
            'content_truncated': content_truncated,
            'truncation_strategy': 'head',
            'links': [],
            'metadata': {'domain': (urlparse(response.url).hostname or '').lower()},
        }

    soup = BeautifulSoup(response_text, 'html.parser')
    title = extract_web_page_title(soup)
    description = extract_web_page_description(soup)
    links = _extract_page_links(soup, final_url)
    metadata = _extract_page_metadata(soup, final_url)
    readable_content = _extract_readable_text(soup)
    content, content_truncated = _truncate_page_content(readable_content, text_limit)
    return {
        'status': 'ok',
        'source_status': 'ok',
        'url': normalized_url,
        'final_url': final_url,
        'status_code': response.status_code,
        'content_type': content_type,
        'raw_bytes': raw_bytes,
        'response_bytes_truncated': response_truncated,
        'title': (title or metadata['domain'])[:300],
        'description': description[:500],
        'content': content,
        'content_chars': len(readable_content),
        'returned_content_chars': len(content),
        'content_max_chars': text_limit,
        'content_truncated': content_truncated,
        'truncation_strategy': 'head',
        'links': links,
        'metadata': metadata,
    }
