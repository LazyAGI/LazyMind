"""Keyless discovery of real image files with their Commons attribution metadata."""
from __future__ import annotations

from html import unescape
import re

import httpx


def search_commons_images(query: str, limit: int = 5) -> dict:
    """Return candidates, never claim that a discovered file has passed validation."""
    if not query.strip():
        return {'status': 'empty', 'items': []}
    try:
        response = httpx.get(
            'https://commons.wikimedia.org/w/api.php',
            params={
                'action': 'query', 'generator': 'search', 'gsrsearch': f'{query} filetype:bitmap',
                'gsrnamespace': 6, 'gsrlimit': min(max(limit, 1), 10),
                'prop': 'imageinfo', 'iiprop': 'url|size|extmetadata',
                'iiurlwidth': 1280, 'format': 'json',
            },
            headers={'User-Agent': 'LazyMind/1.0 (https://github.com/LazyAGI/LazyMind; image-search)'},
            timeout=8,
        )
        response.raise_for_status()
        data = response.json()
        if data.get('error'):
            return {'status': 'unavailable', 'items': [], 'reason': 'commons_api_error'}
        pages = (data.get('query') or {}).get('pages') or {}
        items = []
        for page in sorted(pages.values(), key=lambda item: item.get('index', 0)):
            info = (page.get('imageinfo') or [{}])[0]
            url = info.get('thumburl') or info.get('url') or ''
            if not url.startswith('https://'):
                continue
            metadata = info.get('extmetadata') or {}

            def plain(key):
                value = str((metadata.get(key) or {}).get('value') or '')
                return unescape(re.sub(r'<[^>]+>', '', value))[:1000]

            items.append({
                'url': url, 'title': page.get('title', ''), 'source': 'wikimedia_commons',
                'source_page_url': info.get('descriptionurl', ''),
                'license': plain('LicenseShortName'), 'license_url': plain('LicenseUrl'),
                'attribution': plain('Artist'), 'credit': plain('Credit'),
            })
        return {'status': 'ok' if items else 'empty', 'items': items[:limit]}
    except Exception:
        # Provider errors must not turn optional image collection into a failed workflow.
        return {'status': 'unavailable', 'items': [], 'reason': 'commons_unavailable'}
