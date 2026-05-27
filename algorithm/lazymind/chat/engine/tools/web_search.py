from __future__ import annotations

from typing import Any, Dict, List, Literal, Optional

from lazyllm.tools.tools.search import ArxivSearch

from lazymind.chat.engine.tools.infra import (
    classify_web_search_exception,
    fetch_url_content,
    get_web_search_item_content,
    handle_tool_errors,
    normalize_web_search_lang,
    run_web_search_candidates,
    serialize_web_search_item,
    tool_error,
    tool_success,
)
from lazymind.config import config as _cfg


def _search_failure(query: str, source: str, details: Dict[str, Any], *, lang: Optional[str] = None,
                    tried_sources: Optional[List[str]] = None) -> Dict[str, Any]:
    meta = {
        'status': 'search_error',
        'query': query,
        'resolved_source': source,
        'total': 0,
        'items': [],
    }
    if 'status' in details:
        meta['status'] = details['status']
    if 'http_status' in details:
        meta['http_status'] = details['http_status']
    if lang is not None:
        meta['lang'] = lang
    if tried_sources is not None:
        meta['tried_sources'] = tried_sources
    return tool_error(
        'web_search',
        str(details.get('reason') or 'search failed'),
        error_type=str(details.get('error_type') or '') or None,
        detail=str(details.get('error') or '') or None,
        meta=meta,
    )


class WebSearchToolGroup:
    __public_apis__ = ['web_search', 'arxiv_search', 'url_fetch']

    @handle_tool_errors
    def web_search(
        self,
        query: str,
        source: Literal['auto', 'wikipedia', 'google', 'bing', 'bocha'] = 'auto',
        topk: int = 5,
        lang: Literal['zh', 'en'] = 'zh',
        include_content: bool = False,
    ) -> Dict[str, Any]:
        """Search public web information as a supplement when knowledge-base
        retrieval is insufficient.

        Prefer `kb_search` first. Use this tool only when the knowledge base
        has no relevant results, the returned evidence is clearly insufficient,
        or the user is asking for public information outside the knowledge base.

        This tool supports multiple providers through a single interface:
        `source='auto'|'wikipedia'|'google'|'bing'|'bocha'`.
        In `auto` mode, providers are tried in configured order. Unconfigured
        providers are skipped, runtime failures fall through to the next
        candidate, and Wikipedia is always appended as the final fallback.

        Args:
            query: Natural-language search query.
            source: Search provider selector. Use `auto` unless the user
                explicitly needs a specific provider.
            topk: Maximum number of result items to return.
            lang: Preferred language for Wikipedia fallback. Currently supports
                `zh` and `en`.
            include_content: Whether to fetch and include page content for each
                result item. Keep this `False` unless extra detail is necessary.

        Returns:
            A compact dict containing the resolved provider, query, and items.
        """
        normalized_query = str(query or '').strip()
        if not normalized_query:
            raise ValueError('query is required')

        resolved_lang = normalize_web_search_lang(lang)
        limit = max(1, min(int(topk), 10))
        resolved_source, tried_sources, items, provider, error = run_web_search_candidates(
            source,
            normalized_query,
            limit,
            resolved_lang,
        )
        if error is not None:
            return _search_failure(
                normalized_query,
                resolved_source or str(source),
                error,
                lang=resolved_lang,
                tried_sources=tried_sources,
            )

        serialized_items = []
        for item in items:
            content = (
                get_web_search_item_content(provider, item, include_content)
                if provider is not None
                else None
            )
            serialized_items.append(serialize_web_search_item(item, content=content))

        return tool_success('web_search', {
            'status': 'ok' if serialized_items else 'no_results',
            'query': normalized_query,
            'requested_source': source,
            'resolved_source': resolved_source or str(source),
            'tried_sources': tried_sources,
            'lang': resolved_lang,
            'total': len(serialized_items),
            'items': serialized_items,
        })

    @handle_tool_errors
    def arxiv_search(
        self,
        query: str,
        max_results: int = 5,
        include_content: bool = False,
        sort_by: Literal['relevance', 'lastUpdatedDate', 'submittedDate'] = 'relevance',
    ) -> Dict[str, Any]:
        """Search arXiv papers for academic questions such as paper titles,
        authors, abstracts, or arXiv identifiers.

        Prefer this tool over `web_search` when the user is asking about papers,
        research topics, or arXiv records.

        Args:
            query: Paper title, topic, author keywords, or arXiv id related text.
            max_results: Maximum number of result items to return.
            include_content: Whether to include the paper abstract text in the
                returned items.
            sort_by: arXiv sort field.

        Returns:
            A compact dict with arXiv search results.
        """
        normalized_query = str(query or '').strip()
        if not normalized_query:
            raise ValueError('query is required')

        timeout = _cfg['arxiv_search_timeout']
        limit = max(1, min(int(max_results), 10))
        provider = ArxivSearch(timeout=timeout, source_name='arxiv')
        try:
            items = provider(
                normalized_query,
                max_results=limit,
                sort_by=sort_by,
                raise_on_error=True,
            )[:limit]
        except Exception as exc:
            classified = classify_web_search_exception(exc)
            return tool_error(
                'arxiv_search',
                str(classified.get('reason') or 'search failed'),
                error_type=type(exc).__name__,
                detail=str(exc),
                meta={
                    'status': classified.get('status', 'search_error'),
                    'query': normalized_query,
                    'source': 'arxiv',
                    'total': 0,
                    'items': [],
                },
            )

        serialized_items = []
        for item in items:
            content = get_web_search_item_content(provider, item, include_content)
            serialized_items.append(serialize_web_search_item(item, content=content))

        return tool_success('arxiv_search', {
            'status': 'ok' if serialized_items else 'no_results',
            'query': normalized_query,
            'source': 'arxiv',
            'sort_by': sort_by,
            'total': len(serialized_items),
            'items': serialized_items,
        })

    @handle_tool_errors
    def url_fetch(
        self,
        url: str,
    ) -> Dict[str, Any]:
        """Fetch and summarize the readable content of a public web page.

        Use this when the user provides a concrete URL or when search results
        already identified a page that needs direct inspection.

        Args:
            url: Absolute URL, or a domain/path that can be normalized to HTTPS.

        Returns:
            A compact dict containing page metadata and extracted text content.
        """
        return tool_success('url_fetch', fetch_url_content(url))
