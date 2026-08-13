from __future__ import annotations

from typing import Any, Dict

from lazymind.chat.engine.tools.infra import (
    fetch_url_content,
    tool_success,
)


def url_fetch(url: str) -> Dict[str, Any]:
    """Fetch readable content from one public web page.

    Use this for public web pages. Do not use it for authenticated cloud-file
    URLs such as Feishu/Lark Wiki or Docs and Notion; use CloudFileToolkit for
    those links instead. Never invent or guess a URL: use a URL supplied by the
    user or returned by a search tool. To inspect several pages, issue multiple
    url_fetch calls in the same tool-call turn so ToolManager can execute them
    concurrently. To follow a returned link, copy its exact target_url into a new
    url_fetch call.

    Args:
        url: One public HTTP(S) URL, or a domain/path that can be normalized to HTTPS.

    Returns:
        Page title, extracted text, truncation state, and links represented as
        text plus target_url.
    """
    if not str(url or '').strip():
        raise ValueError('url is required')
    return tool_success('url_fetch', fetch_url_content(url))
