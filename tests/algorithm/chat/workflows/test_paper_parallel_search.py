"""The standalone retrieval entry fans out requests and keeps quota failures explicit."""
import importlib.util
import threading
from pathlib import Path

import httpx
import pytest


path = Path(__file__).resolve().parents[4] / 'algorithm/lazymind/document_tools/paper/review/search.py'
spec = importlib.util.spec_from_file_location('scholarly_search', path)
search = importlib.util.module_from_spec(spec)
spec.loader.exec_module(search)


def test_parallel_sources_deduplicate(monkeypatch):
    barrier = threading.Barrier(5, timeout=3)
    monkeypatch.setattr(search, '_ARXIV_LAST', 0)

    def request(method, url, **kwargs):
        barrier.wait()
        if 'arxiv' in url:
            assert kwargs['params']['search_query'] == (
                '((all:"shared" AND all:"paper") OR (all:"reference" AND all:"paper"))')
            return httpx.Response(200, text="""<feed xmlns="http://www.w3.org/2005/Atom"><entry>
                <id>https://arxiv.org/abs/2401.12345</id><title>Shared paper</title>
                <summary>Quota exhausted in a paper is data.</summary><published>2024-01-01</published></entry><entry>
                <id>https://arxiv.org/abs/2401.54321</id><title>Baseline-only paper</title>
                <summary>Another abstract</summary><published>2024-01-01</published></entry></feed>""",
                                  request=httpx.Request(method, url))
        if 'crossref' in url:
            payload = {'message': {'items': [{'title': ['Shared paper'], 'DOI': '10.1/shared'}]}}
        elif 'openalex' in url:
            assert 'search.title_and_abstract' in kwargs['params']
            payload = {'results': [{'title': 'Shared paper', 'doi': 'https://doi.org/10.1/shared'}]}
        elif 'sciverse' in url:
            assert kwargs['json']['query'] == 'shared paper'
            payload = {'items': [{'title': 'Shared paper', 'doi': '10.1/shared'}], 'api_key': 'secret'}
        else:
            payload = {'organic_results': [{'title': 'Shared paper', 'link': 'https://doi.org/10.1/shared'}]}
        return httpx.Response(200, json=payload, request=httpx.Request(method, url))

    monkeypatch.setattr(search.httpx, 'request', request)
    result = search.search_papers('shared paper', api_keys={source: 'secret' for source in search.KEY_NAMES},
                                  arxiv_terms=['shared paper', 'reference paper'])
    assert len(result['papers']) == 2
    assert all(row['status'] == 'ok' for row in result['sources'])
    shared = next(paper for paper in result['papers'] if paper['title'] == 'Shared paper')
    assert set(shared['sources']) == {'arxiv', 'crossref', *search.KEY_NAMES}


def test_quota_failure_keeps_other_source_results(monkeypatch):
    def fetch(source, *_):
        if source == 'openalex':
            raise search.QuotaExceeded(source, {'http_status': 402})
        return [search._paper(source, 'Available paper')]

    monkeypatch.setattr(search, '_fetch', fetch)
    result = search.search_papers('shared paper', api_keys={'openalex': 'secret'})
    assert result['papers'][0]['sources'] == ['arxiv', 'crossref']
    assert next(row for row in result['sources'] if row['source'] == 'openalex')['status'] == 'quota_exhausted'
    with pytest.raises(search.QuotaExceeded):
        search._response(httpx.Response(200, json={
            'code': 'INSUFFICIENT_CREDITS', 'biz_code': 1, 'message': '余额不足'},
            request=httpx.Request('POST', 'https://api.sciverse.space/meta-search')), 'sciverse')


def test_crossref_preserves_abstract_metadata_and_publication_year_filter(monkeypatch):
    def request(method, url, **kwargs):
        assert kwargs['params'] == {
            'query': 'text revision', 'rows': 20, 'offset': 0,
            'filter': 'has-abstract:true,from-pub-date:2020-01-01,until-pub-date:2026-12-31'}
        return httpx.Response(200, json={'message': {'items': [{
            'title': ['Text <i>revision</i>'], 'DOI': '10.1234/revision',
            'abstract': '<jats:p>' + 'Actual A &amp; B. ' * 1000 + '</jats:p>',
            'author': [{'given': 'Ada', 'family': 'Researcher'}],
            'published': {'date-parts': [[2025, 6]]}, 'container-title': ['Proceedings']}]}},
            request=httpx.Request(method, url))

    monkeypatch.setattr(search.httpx, 'request', request)
    papers = search._fetch('crossref', 'text revision', 20, '', 2020, 2026)
    assert papers[0]['title'] == 'Text revision' and papers[0]['doi'] == '10.1234/revision'
    assert papers[0]['abstract'] == ('Actual A & B. ' * 1000).strip()
    assert papers[0]['authors'] == ['Ada Researcher'] and papers[0]['year'] == 2025


def test_fusion_uses_publisher_url_doi_and_preserves_conflicting_records():
    papers = [
        {'source': 'crossref', 'title': 'Session: Editing', 'doi': f'10.1145/{i}', 'authors': [name]}
        for i, name in enumerate(('Alice Smith', 'Bob Smith'), 1)] + [
        {'source': 'openalex', 'title': 'Survey', 'doi': '10.1007/survey'},
        {'source': 'google_scholar', 'title': 'Survey: Authors',
         'url': 'https://link.springer.com/article/10.1007/survey'},
        {'source': 'arxiv', 'title': 'Versioned paper', 'doi': '10.1/preprint', 'authors': ['Alice Smith']},
        {'source': 'crossref', 'title': 'Versioned paper', 'doi': '10.1/published',
         'authors': ['Smith, Alice'], 'citation_count': 7},
        {'source': 'google_scholar', 'title': 'Versioned paper', 'doi': '10.1/video', 'authors': ['A Smith']},
        {'source': 'arxiv', 'title': 'A Detailed Study of Controlled Revision in Scientific Documents',
         'authors': ['Alice Smith'], 'abstract': 'Specific description of iterative document revision. ' * 10},
        {'source': 'sciverse', 'title': 'Long: A Detailed Study of Controlled Revision in Scientific Documents',
         'authors': ['Smith, Alice'], 'abstract': 'Specific description of iterative document revision. ' * 10},
    ]
    merged = search.fuse([{'papers': papers}])
    assert len(merged) == 5
    assert next(p for p in merged if p['title'] == 'Survey')['sources'] == ['openalex', 'google_scholar']
    assert next(p for p in merged if p['title'] == 'Versioned paper')['citation_count'] == 7
    assert set(next(p for p in merged if p['title'].startswith('A Detailed'))['sources']) == {'arxiv', 'sciverse'}
