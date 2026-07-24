from __future__ import annotations

from typing import Any, Dict

from lazymind.chat.engine.tools.infra import (
    fetch_url_content,
    tool_success,
)


def url_fetch(url: str) -> Dict[str, Any]:
    """Fetch and summarize the readable content of a public web page.

    Use this when the user provides a concrete URL or when search results
    already identified a page that needs direct inspection.

    Args:
        url: Absolute URL, or a domain/path that can be normalized to HTTPS.

    Returns:
        A compact dict containing page metadata and extracted text content.
    """
    return tool_success('url_fetch', fetch_url_content(url))
