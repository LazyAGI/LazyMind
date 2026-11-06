"""Public Crossref discovery with publisher-deposited abstracts."""
import math
import re
import time
from html import unescape

import httpx
from lazyllm.tools.tools.search.base import SearchBase, _make_content_result, _make_result


def _plain_text(value):
    return re.sub(r'\s+', ' ', unescape(re.sub(r'<[^>]+>', ' ', value or ''))).strip()


class CrossrefSearch(SearchBase):
    def __init__(self):
        super().__init__(source_name='crossref', skip_auth=True)

    def meta_search(self, query, page_size, *, page=1, include_content=False, year_from=None, year_to=None):
        filters = ['has-abstract:true']
        if year_from:
            filters.append(f'from-pub-date:{year_from:04d}-01-01')
        if year_to:
            filters.append(f'until-pub-date:{year_to:04d}-12-31')
        params = dict(query=query, rows=page_size, offset=(page - 1) * page_size, filter=','.join(filters))
        time.sleep(0.25)
        response = httpx.get('https://api.crossref.org/works', params=params, timeout=30,
                             headers={'User-Agent': 'LazyMind paper-writing/1.0', 'Accept': 'application/json'})
        response.raise_for_status()
        payload = response.json()['message']
        items = []
        for work in payload.get('items', []):
            title = _plain_text(' '.join(work.get('title') or []))
            abstract = _plain_text(work.get('abstract'))
            doi = work.get('DOI')
            if not title or not abstract or not doi:
                continue
            authors = []
            for author in work.get('author', []):
                name = ' '.join(part for part in (author.get('given'), author.get('family')) if part)
                name = name or author.get('name', '')
                if name:
                    authors.append(name)
            date = work.get('published') or work.get('issued') or {}
            parts = (date.get('date-parts') or [[]])[0]
            items.append(_make_result(
                title=title, url='https://doi.org/' + doi, snippet=abstract, source=self.source_name,
                doi=doi, authors=authors, year=parts[0] if parts else None,
                venue=_plain_text(' '.join(work.get('container-title') or [])), abstract=abstract,
            ))
        return dict(items=items, total_pages=math.ceil(payload.get('total-results', 0) / page_size))

    def _fetch_content_result(self, item):
        metadata = item.get('metadata') or item.get('extra') or {}
        return _make_content_result(item, metadata.get('abstract', ''), content_type='abstract')
