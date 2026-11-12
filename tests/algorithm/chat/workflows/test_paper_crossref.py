"""Public scholarly discovery preserves actual metadata, abstracts and source boundaries."""
import copy
from unittest.mock import Mock

import httpx
import pytest

from lazymind.document_tools.paper.review import crossref, retrieval


@pytest.fixture
def works(monkeypatch):
    payload = {'total-results': 7, 'items': [dict(
        DOI='10.1234/example', title=['Collaborative <i>writing</i>'],
        abstract='<jats:p>' + 'Actual abstract with A &amp; B. ' * 1000 + '</jats:p>',
        author=[{'given': 'Ada', 'family': 'Researcher'}, {'name': 'Study Group'}],
        published={'date-parts': [[2025, 6]]}, **{'container-title': ['Writing Proceedings']},
    )]}
    request = Mock(side_effect=lambda url, **kwargs: httpx.Response(
        200, json={'message': payload}, request=httpx.Request('GET', url)))
    monkeypatch.setattr(crossref.httpx, 'get', request)
    monkeypatch.setattr(crossref.time, 'sleep', Mock())
    return payload, request


def test_crossref_search_binds_metadata_and_full_abstract_without_another_request(works):
    payload, request = works
    original = copy.deepcopy(payload)
    provider = crossref.CrossrefSearch()
    warnings = []
    papers, more = retrieval.search_sciverse(provider, {'collaborative writing': 3}, 2020, 2026, warnings, page=2)
    assert warnings == [] and more == {'collaborative writing': 3}
    assert len(papers) == 1 and papers[0]['source'] == 'crossref'
    assert papers[0]['title'] == 'Collaborative writing'
    assert papers[0]['doi'] == '10.1234/example'
    assert papers[0]['authors'] == ['Ada Researcher', 'Study Group']
    assert papers[0]['year'] == 2025 and papers[0]['venue'] == 'Writing Proceedings'
    result = retrieval.read_sciverse(provider, papers[0], warnings)
    assert result['text'] == ('Actual abstract with A & B. ' * 1000).strip()
    assert result['reading']['content_type'] == 'abstract'
    assert result['reading']['truncated'] is False
    assert payload == original
    assert request.call_count == 1
    assert request.call_args.kwargs['params'] == dict(
        query='collaborative writing', rows=3, offset=3,
        filter='has-abstract:true,from-pub-date:2020-01-01,until-pub-date:2026-12-31',
    )


@pytest.mark.parametrize('missing', ['DOI', 'title', 'abstract'])
def test_crossref_does_not_admit_incomplete_or_unreadable_records(works, missing):
    payload, _ = works
    payload['items'][0].pop(missing)
    papers, _ = retrieval.search_sciverse(crossref.CrossrefSearch(), {'writing': 3}, None, None, [])
    assert papers == []


def test_crossref_http_failure_cannot_create_scholarly_evidence(monkeypatch):
    monkeypatch.setattr(crossref.time, 'sleep', Mock())
    monkeypatch.setattr(crossref.httpx, 'get', Mock(return_value=httpx.Response(
        503, request=httpx.Request('GET', 'https://api.crossref.org/works'))))
    warnings = []
    papers, more = retrieval.search_sciverse(crossref.CrossrefSearch(), {'writing': 3}, None, None, warnings)
    assert papers == [] and more == {}
    assert len(warnings) == 1 and 'Crossref' in warnings[0] and 'HTTPStatusError' in warnings[0]


def test_default_provider_uses_crossref_when_sciverse_is_unconfigured(monkeypatch):
    from lazyllm.tools.tools import search
    configured = Mock()
    monkeypatch.setattr(search, 'SciverseSearch', lambda: configured)
    assert retrieval.sciverse_provider() is configured
    configured.inject_auth_header.side_effect = ValueError('credential is not configured')
    assert retrieval.sciverse_provider().source_name == 'crossref'
