"""Retrieve Sciverse sources and deduplicate them against supplied review materials."""
import os

from httpx import HTTPError
from lazyllm import globals as runtime_globals
from lazyllm.common import KeyAuthError

from .search import same_paper


RETRIEVAL_ERRORS = (HTTPError, KeyAuthError, ValueError)


def unique_candidates(existing, candidates):
    # Existing documents retain their IDs, user notes and full text.
    seen, result = list(existing), []
    for paper in candidates:
        if not any(same_paper(paper, previous) for previous in seen):
            result.append(paper)
        seen.append(paper)
    return result


def sciverse_provider():
    from lazyllm.tools.tools.search import SciverseSearch
    dynamic = runtime_globals.config['dynamic_tool_auth'] or {}
    return SciverseSearch(api_key=None if dynamic.get('sciverse') else os.environ.get('SCIVERSE_API_KEY') or None)


def read_sciverse(provider, paper, warnings):
    offset, parts = 0, []
    reading = {'content_type': 'search_preview', 'truncated': True}
    # Bound source reads; disclose an unfinished source rather than silently truncating it.
    for _ in range(32):
        try:
            result = provider.get_content(paper, offset=offset)
            page = result['extra']['content_read']
            content = result['content']
            if page.get('fallback'):
                if not parts and content.strip():
                    parts.append(content)
                break
            if not content.strip():
                break
            parts.append(content)
            reading = {**page, 'truncated': True}
            if page.get('pagination_error'):
                break
            if not page.get('more'):
                reading['truncated'] = False
                break
            next_offset = page.get('next_offset')
            if type(next_offset) is not int or next_offset <= offset:
                break
            offset = next_offset
        except RETRIEVAL_ERRORS:
            break
    if parts:
        paper['text'] = ''.join(parts)
    paper['reading'] = reading
    if reading['truncated']:
        warnings.append(f'《{paper["title"]}》全文未完整取得，仅使用实际取得的摘要或节选。')
    if not paper.get('text', '').strip():
        warnings.append(f'《{paper["title"]}》没有可读内容，本次未纳入综述。')
        return None
    return paper
