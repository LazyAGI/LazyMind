"""Retrieve Sciverse sources and deduplicate them against supplied review materials."""
import copy
import hashlib
import re
import unicodedata
from urllib.parse import unquote, urlsplit

from httpx import HTTPError
from lazyllm.common import KeyAuthError


RETRIEVAL_ERRORS = (HTTPError, KeyAuthError, ValueError)


def identities(paper):
    metadata = paper.get('metadata') or {}
    keys = set()
    title = re.sub(r'\.(?:pdf|docx?|md|txt)$', '', paper['title'], flags=re.I)
    title = unicodedata.normalize('NFKC', title).casefold()
    keys.add(('title', ''.join(char for char in title if char.isalnum())))
    url = paper.get('url') or paper.get('paper_url') or ''
    parts = urlsplit(url)
    if parts.netloc:
        keys.add(('url', parts.netloc.casefold() + unquote(parts.path).rstrip('/')
                  + ('?' + parts.query if parts.query else '')))
    doi = str(paper.get('doi') or metadata.get('doi') or '').casefold()
    doi = re.sub(r'^https?://(?:dx\.)?doi\.org/', '', doi).strip()
    if parts.netloc.casefold() in {'doi.org', 'dx.doi.org'}:
        doi = unquote(parts.path).lstrip('/').casefold()
    if doi:
        keys.add(('doi', doi))
    arxiv = str(paper.get('arxiv_id') or metadata.get('arxiv_id') or '')
    if parts.netloc.casefold() in {'arxiv.org', 'www.arxiv.org'}:
        arxiv = re.sub(r'^/(?:abs|pdf)/', '', parts.path).removesuffix('.pdf')
    if arxiv:
        keys.add(('arxiv', re.sub(r'v\d+$', '', arxiv)))
    if metadata.get('kb_id') and metadata.get('doc_id'):
        keys.add(('knowledge_base', (metadata['kb_id'], metadata['doc_id'])))
    elif metadata.get('doc_id'):
        keys.add((paper.get('source', 'input'), str(metadata['doc_id'])))
    return {key for key in keys if key[1]}


def unique_candidates(existing, candidates):
    # Existing documents retain their IDs, user notes and full text.
    seen = set().union(*(identities(paper) for paper in existing))
    result = []
    for paper in candidates:
        keys = identities(paper)
        duplicate = bool(keys & seen)
        seen.update(keys)
        if not duplicate:
            result.append(paper)
    return result


def sciverse_provider():
    from lazyllm.tools.tools.search import SciverseSearch
    provider = SciverseSearch()
    try:
        provider.inject_auth_header()
    except ValueError as error:
        if 'credential is not configured' not in str(error):
            raise
        from .crossref import CrossrefSearch
        return CrossrefSearch()
    return provider


def search_sciverse(provider, queries, year_from, year_to, warnings, *, page=1):
    candidates, more_queries = [], {}
    source = provider.source_name if isinstance(provider.source_name, str) else 'sciverse'
    label = 'Crossref' if source == 'crossref' else 'Sciverse'
    for query, count in queries.items():
        try:
            pagination = {'page': page} if page > 1 else {}
            result = provider.meta_search(query=query, page_size=count, include_content=False,
                                          year_from=year_from, year_to=year_to, **pagination)
            items = result['items']
        except RETRIEVAL_ERRORS as error:
            warnings.append(f'{label} 检索「{query}」失败（{type(error).__name__}），未用模型知识替代检索结果。')
            continue
        if not items:
            warnings.append(f'{label} 检索「{query}」未找到可用文献。')
        total_pages = result.get('total_pages')
        if (total_pages > page if isinstance(total_pages, int) else len(items) >= count):
            more_queries[query] = count
        for item in items:
            if not item.get('title', '').strip():
                continue
            metadata = copy.deepcopy(item.get('extra') or {})
            identity = metadata.get('doc_id') or metadata.get('doi') or item.get('url') or item['title']
            paper = {**copy.deepcopy(item), 'metadata': metadata, 'kind': 'paper', 'source': source,
                     'id': source + '-' + hashlib.sha256(str(identity).encode()).hexdigest()[:20],
                     'text': item.get('snippet') or '', 'reading': {'content_type': 'search_preview'}}
            paper.update({key: metadata[key] for key in ('doi', 'authors', 'year', 'venue') if metadata.get(key)})
            candidates.append(paper)
    return candidates, more_queries


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
