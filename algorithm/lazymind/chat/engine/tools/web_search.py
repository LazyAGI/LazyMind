from __future__ import annotations

from typing import Any, Dict

from lazymind.chat.engine.tools.infra import (
    fetch_url_content,
    tool_success,
)


def url_fetch(url: str) -> Dict[str, Any]:
    """Fetch readable content from one public web page, or ingest a public PDF.

    Use this for public web pages. PDF URLs are downloaded and ingested as a
    file resource; the result contains file_id rather than document text — use
    grep and read_file next. Do not use it for authenticated cloud-file
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
