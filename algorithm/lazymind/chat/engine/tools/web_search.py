from __future__ import annotations

from lazyllm.tools import fc_register

from typing import Any, Dict

from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.infra import fetch_url_content


@fc_register(host_file='NONE')
def url_fetch(url: str, offset: int = 0, limit: int | None = None) -> Dict[str, Any]:
    """Fetch readable content from one public web page, or ingest a public PDF.

    Use this for public web pages. PDF URLs are downloaded and ingested as a
    file resource; the result contains file_id rather than document text — use
    search_file_resource and read_file_resource next. Do not use it for authenticated cloud-file
    URLs such as Feishu/Lark Wiki or Docs and Notion; use CloudFileToolkit for
    those links instead. Never invent or guess a URL: use a URL supplied by the
    user or returned by a search tool. To inspect several pages, issue multiple
    url_fetch calls in the same tool-call turn so ToolManager can execute them
    concurrently. To follow a returned link, copy its exact target_url into a new
    url_fetch call.

    Args:
        url: One public HTTP(S) URL, or a domain/path that can be normalized to HTTPS.
        limit: Optional positive page length in characters. Omit to use the configured
            url_fetch_max_length (default 4000); larger values are capped to that setting.
        offset: Character offset in extracted page text, default 0. Continue using
            content_read.next_offset while more is true. PDF ingestion ignores offset and limit.

    Returns:
        Page title, extracted text, truncation state, and links represented as
        text plus target_url. content_read.more=false marks the end of available
        page text. If response_truncated=true and more is absent, the download
        limit was reached; do not claim the complete page was read. Each call
        fetches again, so changing pages may produce unstable pagination.
    """
    if not str(url or '').strip():
        raise ToolExecutionError('url is required')
    try:
        return fetch_url_content(url, offset=offset, limit=limit)
    except ValueError as exc:
        raise ToolExecutionError(str(exc)) from exc
