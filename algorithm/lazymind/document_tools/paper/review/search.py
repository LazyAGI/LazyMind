"""Parallel scholarly metadata retrieval, independent of Chat and model runtime."""
import copy
import hashlib
import html
import os
import re
import threading
import time
import unicodedata
import xml.etree.ElementTree as ET
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import unquote, urlsplit

import httpx


KEY_NAMES = {'sciverse': 'SCIVERSE_API_KEY', 'openalex': 'OPENALEX_API_KEY',
             'google_scholar': 'SERPAPI_API_KEY'}


def configured_keys():
    return {source: os.environ.get(name, '') for source, name in KEY_NAMES.items()}


def external_sources(api_keys):
    return ['crossref'] + [source for source in KEY_NAMES if api_keys.get(source)]


_ARXIV_GATE = threading.Lock()
_ARXIV_LAST = 0.0


class QuotaExceeded(RuntimeError):
    def __init__(self, source, result):
        super().__init__(source, result)
        self.source, self.result = source, result


def _text(value):
    return re.sub(r'\s+', ' ', html.unescape(re.sub(r'<[^>]+>', ' ', str(value or '')))).strip()


def _fingerprint(value):
    return ''.join(c for c in unicodedata.normalize('NFKC', value).casefold() if c.isalnum())


def _same_text(left, right):
    short, long = sorted((_fingerprint(p['title']) for p in (left, right)), key=len)
    abstract = _fingerprint(left.get('abstract', ''))[:300]
    return (len(short) >= 40 and len(long) - len(short) <= 12 and short in long
            and len(abstract) == 300 and abstract == _fingerprint(right.get('abstract', ''))[:300])


def identities(paper):
    metadata = paper.get('metadata') or {}
    title = re.sub(r'\.(?:pdf|docx?|md|txt)$', '', paper['title'], flags=re.I)
    keys = {('title', _fingerprint(title))}
    url = paper.get('url') or paper.get('paper_url') or ''
    parts = urlsplit(url)
    if parts.netloc:
        keys.add(('url', parts.netloc.casefold() + unquote(parts.path).rstrip('/')
                  + ('?' + parts.query if parts.query else '')))
    doi = unquote(str(paper.get('doi') or metadata.get('doi') or '')).casefold()
    doi = re.sub(r'^https?://(?:dx\.)?doi\.org/', '', doi).strip()
    if not doi:
        match = re.search(r'10\.\d{4,9}/[^\s?#]+', unquote(url), re.I)
        doi = match[0].rstrip('/').casefold() if match else ''
    arxiv = str(paper.get('arxiv_id') or metadata.get('arxiv_id') or '')
    for value in (url, doi):
        match = re.search(r'arxiv\.(?:org/(?:abs|pdf)/)?(\d{4}\.\d{4,5})(?:v\d+)?', value, re.I)
        if match:
            arxiv = match[1]
    if doi:
        keys.add(('doi', doi))
    if arxiv:
        keys.add(('arxiv', re.sub(r'v\d+$', '', arxiv.removesuffix('.pdf'))))
    if metadata.get('doc_id'):
        keys.add(('document', (metadata.get('kb_id') or paper.get('source', 'input'), str(metadata['doc_id']))))
    keys.update((kind, tuple(value) if isinstance(value, list) else value)
                for kind, value in paper.get('identities', []))
    return {key for key in keys if key[1]}


def same_paper(left, right):
    def authors(paper):
        keys = set()
        for name in paper.get('authors') or []:
            words = ' '.join(reversed(name.split(',', 1))).split()
            if words:
                keys.add((words[-1].casefold(), words[0][0].casefold()))
        return keys

    left_keys, right_keys = identities(left), identities(right)
    shared = left_keys & right_keys
    left_dois = {value for kind, value in left_keys if kind == 'doi'}
    right_dois = {value for kind, value in right_keys if kind == 'doi'}
    left_authors, right_authors = authors(left), authors(right)
    conflict = (left_dois and right_dois and left_dois.isdisjoint(right_dois)
                and left_authors and right_authors and left_authors.isdisjoint(right_authors))
    return bool((shared and (not conflict or any(kind != 'title' for kind, _ in shared)))
                or (left_authors & right_authors and _same_text(left, right)))


def _authors(values):
    if isinstance(values, str):
        return [values] if values.strip() else []
    return [name for item in values or [] if (name := (
        item if isinstance(item, str) else item.get('name') or item.get('display_name')
        or ' '.join(filter(None, (item.get('given'), item.get('family'))))))]


def _paper(source, title, url='', abstract='', **metadata):
    return {'source': source, 'title': _text(title), 'url': url or '', 'abstract': _text(abstract), **metadata}


def _response(response, source):
    if source == 'arxiv':
        response.raise_for_status()
        return response.text
    try:
        raw = response.json()
    except ValueError:
        raw = response.text
    base = (raw.get('base_resp') or {}) if isinstance(raw, dict) else {}
    message = raw.get('message') if isinstance(raw, dict) and isinstance(raw.get('message'), str) else None
    error = (raw.get('error') or message or raw.get('detail')
             or base.get('status_msg')) if isinstance(raw, dict) else raw
    quota = re.search(r'quota.?exceed|quota.?exhaust|insufficient.?credit|insufficient.?balance|'
                      r'out of (?:searches|credits)|run out of (?:searches|credits)|'
                      r'(?:daily|monthly) (?:budget|quota|limit).*(?:exceed|reach)|'
                      r'(?:exceed|reach).*(?:daily|monthly) (?:budget|quota|limit)|'
                      r'余额不足|积分不足|额度.{0,8}(?:不足|用尽|耗尽)',
                      str((error, raw.get('code') if isinstance(raw, dict) else None)), re.I)
    if response.status_code == 402 or quota or base.get('status_code') in {1008, 2053, 2056, 2067}:
        raise QuotaExceeded(source, {'http_status': response.status_code, 'error': error})
    response.raise_for_status()
    business_error = source == 'sciverse' and isinstance(raw, dict) and (
        raw.get('biz_code', 0) or raw.get('code', 'SUCCESS') not in {None, 'SUCCESS', 0, 200})
    if isinstance(raw, dict) and (raw.get('error') or base.get('status_code', 0) or business_error):
        raise ValueError(str(error))
    return raw


def arxiv_query(terms):
    groups = []
    for term in terms:
        words = [word for word in re.findall(r'[\w-]+', term)
                 if word.lower() not in {'a', 'an', 'the', 'of', 'for', 'to', 'and', 'with'}]
        if not words:
            raise ValueError('arXiv keyword groups must contain searchable terms')
        groups.append('(' + ' AND '.join(f'all:"{word}"' for word in words) + ')')
    return ' OR '.join(groups)


def _fetch(source, query, limit, key, year_from=None, year_to=None):
    global _ARXIV_LAST
    headers = {'User-Agent': 'LazyMind scholarly-retrieval/1.0'}
    method, payload = 'GET', None
    if source == 'arxiv':
        with _ARXIV_GATE:
            time.sleep(max(0, 3 - (time.monotonic() - _ARXIV_LAST)))
            _ARXIV_LAST = time.monotonic()
        url = 'https://export.arxiv.org/api/query'
        params = {'search_query': '(' + query + ')', 'max_results': limit, 'start': 0,
                  'sortBy': 'relevance', 'sortOrder': 'descending'}
    elif source == 'crossref':
        url = 'https://api.crossref.org/works'
        params = {'query': query, 'rows': limit, 'offset': 0,
                  'filter': 'has-abstract:true'}
    elif source == 'openalex':
        url = 'https://api.openalex.org/works'
        params = {'search.title_and_abstract': query, 'per_page': limit, 'api_key': key}
    elif source == 'sciverse':
        url, method = 'https://api.sciverse.space/meta-search', 'POST'
        headers['Authorization'] = 'Bearer ' + key
        params = None
        payload = {'query': query, 'page': 1, 'page_size': limit, 'fields': [
            'title', 'doi', 'doc_id', 'abstract', 'author',
            'publication_published_year', 'publication_venue_name_unified']}
    elif source == 'google_scholar':
        url = 'https://serpapi.com/search.json'
        params = {'engine': 'google_scholar', 'q': query, 'num': limit, 'api_key': key,
                  'hl': 'en', 'as_vis': 1}
    else:
        raise ValueError('Unsupported scholarly source: ' + source)
    if year_from is not None or year_to is not None:
        if source == 'arxiv':
            params['search_query'] += ' AND submittedDate:[' + (
                f'{year_from}01010000' if year_from else '*') + ' TO ' + (
                f'{year_to}12312359' if year_to else '*') + ']'
        elif source == 'crossref':
            for name, value, suffix in [('from-pub-date', year_from, '01-01'),
                                        ('until-pub-date', year_to, '12-31')]:
                if value is not None:
                    params['filter'] += f',{name}:{value:04d}-{suffix}'
        elif source == 'openalex':
            params['filter'] = ','.join(f'{name}:{year}-{suffix}' for name, year, suffix in [
                ('from_publication_date', year_from, '01-01'), ('to_publication_date', year_to, '12-31')]
                if year is not None)
        elif source == 'sciverse':
            payload['filters'] = [{'field': 'publication_published_year', 'operator': operator, 'value': year}
                                  for operator, year in [('FILTER_OP_GTE', year_from), ('FILTER_OP_LTE', year_to)]
                                  if year is not None]
        else:
            params.update({name: year for name, year in [('as_ylo', year_from), ('as_yhi', year_to)]
                           if year is not None})
    response = httpx.request(method, url, params=params, json=payload, headers=headers,
                             timeout=40, follow_redirects=True)
    raw = _response(response, source)
    items = []
    if source == 'arxiv':
        ns = {'a': 'http://www.w3.org/2005/Atom', 'x': 'http://arxiv.org/schemas/atom'}
        root = ET.fromstring(raw)
        if root.tag != '{http://www.w3.org/2005/Atom}feed':
            raise ValueError('arXiv returned a non-Atom response')
        for row in root.findall('a:entry', ns):
            url = row.findtext('a:id', '', ns)
            if '/api/errors' in url:
                raise ValueError(row.findtext('a:summary', '', ns))
            items.append(_paper(source, row.findtext('a:title', '', ns), url,
                                row.findtext('a:summary', '', ns),
                                arxiv_id=url.rsplit('/', 1)[-1], doi=row.findtext('x:doi', '', ns),
                                authors=[a.findtext('a:name', '', ns) for a in row.findall('a:author', ns)],
                                year=int(row.findtext('a:published', '0000', ns)[:4])))
    elif source == 'crossref':
        for row in raw['message']['items']:
            date = row.get('published') or row.get('issued') or {}
            year = ((date.get('date-parts') or [[]])[0] or [None])[0]
            items.append(_paper(source, ' '.join(row.get('title') or []),
                                'https://doi.org/' + row.get('DOI', ''), row.get('abstract'),
                                doi=row.get('DOI', ''), authors=_authors(row.get('author')),
                                year=year, venue='; '.join(row.get('container-title') or []),
                                publication_type=row.get('type'), citation_count=row.get('is-referenced-by-count')))
    elif source == 'openalex':
        for row in raw['results']:
            inverted = row.get('abstract_inverted_index') or {}
            positions = {i: word for word, indices in inverted.items() for i in indices}
            location = row.get('primary_location') or {}
            items.append(_paper(source, row.get('title'), location.get('landing_page_url') or row.get('doi')
                                or row.get('id'), ' '.join(positions[i] for i in sorted(positions)),
                                doi=row.get('doi') or '', authors=_authors([
                                    a.get('author', {}) for a in row.get('authorships', [])]),
                                year=row.get('publication_year'),
                                venue=(location.get('source') or {}).get('display_name'),
                                openalex_id=row.get('id'), citation_count=row.get('cited_by_count')))
    elif source == 'sciverse':
        data = raw.get('data') if isinstance(raw.get('data'), dict) else raw
        for row in data.get('hits') or data.get('results') or data.get('items') or []:
            row = row.get('_source', row)
            doi = row.get('doi') or row.get('publication_doi') or ''
            items.append(_paper(source, row.get('title') or row.get('paper_title'),
                                row.get('url') or row.get('paper_url')
                                or ('https://doi.org/' + doi if doi else ''),
                                row.get('abstract') or row.get('summary'), doi=doi,
                                authors=_authors(row.get('author') or row.get('authors')),
                                year=row.get('publication_published_year') or row.get('year'),
                                doc_id=row.get('doc_id') or row.get('id'),
                                venue=row.get('publication_venue_name_unified')))
    else:
        for row in raw.get('organic_results', []):
            publication = row.get('publication_info') or {}
            year = re.search(r'\b(?:19|20)\d{2}\b', publication.get('summary', ''))
            url = row.get('link') or ''
            items.append(_paper(source, row.get('title'), url, row.get('snippet'),
                                doi=unquote(url.split('doi.org/', 1)[1]) if 'doi.org/' in url else '',
                                authors=_authors(publication.get('authors')),
                                year=int(year[0]) if year else None, scholar_id=row.get('result_id'),
                                publication_summary=publication.get('summary'), citation_count=(
                                    (row.get('inline_links') or {}).get('cited_by') or {}).get('total')))
    return [item for item in items if item['title']][:limit]


def fuse(runs):
    """Merge duplicate works while retaining their metadata and source identities."""
    groups = []
    for run in runs:
        for paper in run.get('papers', []):
            matches = [group for group in groups if any(same_paper(paper, member) for member in group)]
            group = matches[0] if matches else []
            if not matches:
                groups.append(group)
            for other in matches[1:]:
                group.extend(other)
                groups.remove(other)
            group.append(paper)
    papers = []
    for members in groups:
        keys = set().union(*(identities(p) for p in members))
        paper = copy.deepcopy(max(members, key=lambda p: len(p.get('abstract', ''))))
        paper['citation_count'] = max((p['citation_count'] for p in members
                                       if isinstance(p.get('citation_count'), (int, float))), default=None)
        paper.update(id='paper-' + hashlib.sha256(repr(sorted(keys)).encode()).hexdigest()[:16],
                     sources=list(dict.fromkeys(p['source'] for p in members)),
                     doc_id=next((p['doc_id'] for p in members if p.get('doc_id')), None),
                     identities=[list(key) for key in sorted(keys)])
        papers.append(paper)
    return papers


def search_papers(query, *, limit=20, api_keys=None, arxiv_terms=None, year_from=None, year_to=None):
    api_keys = api_keys or {}
    sources = ['arxiv', *external_sources(api_keys)]
    if not query.strip() or not 1 <= limit <= 20:
        raise ValueError('A query and between 1 and 20 candidates per source are required')

    def run(source):
        source_query = arxiv_query(arxiv_terms or [query]) if source == 'arxiv' else query
        for attempt in range(3):
            try:
                papers = _fetch(source, source_query, limit, api_keys.get(source, ''), year_from, year_to)
                return {'source': source, 'status': 'ok', 'papers': papers}
            except QuotaExceeded:
                return {'source': source, 'status': 'quota_exhausted', 'papers': []}
            except (httpx.HTTPError, ValueError, KeyError, ET.ParseError) as error:
                status = error.response.status_code if isinstance(error, httpx.HTTPStatusError) else None
                if status is not None and status < 500 and status != 429:
                    break
                if attempt < 2:
                    time.sleep(2 ** attempt)
        return {'source': source, 'status': 'failed', 'papers': []}

    with ThreadPoolExecutor(max_workers=len(sources)) as executor:
        runs = list(executor.map(run, sources))
    return {'sources': runs, 'papers': fuse(runs)}


def validate_arxiv_terms(terms):
    if not isinstance(terms, list) or not 1 <= len(terms) <= 3 or any(
            not isinstance(t, str) or not t.strip() or len(t.split()) > 8 for t in terms):
        raise ValueError('arxiv_terms must contain 1–3 English keyword groups of at most 8 words each')
    arxiv_query(terms)
    return terms


def validate_query(query):
    if not isinstance(query, str) or not query.strip() or len(query.split()) > 6 or len(query) > 160:
        raise ValueError('external_query must be one concise English task query of at most 6 words')
    return query
