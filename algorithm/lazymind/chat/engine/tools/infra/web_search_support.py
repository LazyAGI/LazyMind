from __future__ import annotations

from typing import Any, Dict, List

import requests
from bs4 import BeautifulSoup

from lazymind.chat.engine.tools._utils import absolute_url, truncate_text
from lazymind.config import config as _cfg

_MAX_FETCH_TEXT_LEN = 4000


def coerce_web_int(value: Any, default: int) -> int:
    if value is None or value == '':
        return default
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


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

    timeout = coerce_web_int(_cfg['web_search_timeout'], 10)
    text_limit = max(200, coerce_web_int(_cfg['url_fetch_max_length'], _MAX_FETCH_TEXT_LEN))
    headers = {
        'User-Agent': (
            'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 '
            '(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'
        )
    }

    with requests.sessions.Session() as session:
        response = session.get(
            normalized_url,
            timeout=timeout,
            headers=headers,
            allow_redirects=True,
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
