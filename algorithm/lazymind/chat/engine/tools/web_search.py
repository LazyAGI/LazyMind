from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor
from typing import Any, Dict, List, Optional

from lazymind.chat.engine.tools.infra import (
    fetch_url_content,
    tool_success,
)


def url_fetch(url: str = '', urls: Optional[List[str]] = None) -> Dict[str, Any]:
    """Fetch readable content from one or more public web pages.

    Use this for public web pages. Do not use it for authenticated cloud-file
    URLs such as Feishu/Lark Wiki or Docs and Notion; use CloudFileToolkit for
    those links instead. Never invent or guess a URL: use a URL supplied by the
    user or returned by a search tool. When several public URLs need inspection, pass all of
    them in `urls` in one call instead of relying on multiple parallel tool
    calls. The pages are fetched concurrently with bounded concurrency.

    Args:
        url: One absolute URL, or a domain/path that can be normalized to HTTPS.
            Use this for a single page and omit it when `urls` is supplied.
        urls: Public URLs to fetch as one batch. Duplicate URLs are fetched once.

    Returns:
        For one URL, the existing page metadata and extracted text payload. For
        a batch, a dict containing total/succeeded/failed counts and one result
        per URL; an individual failure does not discard successful pages.
    """
    requested = [str(item).strip() for item in (urls or []) if str(item).strip()]
    if str(url or '').strip():
        requested.insert(0, str(url).strip())
    requested = list(dict.fromkeys(requested))
    if not requested:
        raise ValueError('url or urls is required')
    if len(requested) == 1:
        return tool_success('url_fetch', fetch_url_content(requested[0]))

    def fetch_one(item: str) -> Dict[str, Any]:
        try:
            return {'url': item, 'success': True, 'result': fetch_url_content(item)}
        except Exception as exc:
            return {
                'url': item,
                'success': False,
                'error': f'{type(exc).__name__}: {exc}',
            }

    with ThreadPoolExecutor(max_workers=min(len(requested), 5)) as executor:
        results = list(executor.map(fetch_one, requested))
    succeeded = sum(bool(item['success']) for item in results)
    return tool_success('url_fetch', {
        'total': len(results),
        'succeeded': succeeded,
        'failed': len(results) - succeeded,
        'results': results,
    })
