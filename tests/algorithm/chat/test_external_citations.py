import pytest

from lazymind.chat.service.utils.citations import (
    CITATION_REFS_KEY,
    register_external_search_result,
    reset_citation_state,
    upsert_external_source,
)


def _state():
    state = {}
    reset_citation_state(state)
    return state


def test_external_search_results_share_one_minimal_source():
    state = _state()
    first = register_external_search_result({
        'title': 'First title',
        'url': 'https://Example.test/article/',
        'snippet': 'First snippet',
        'source': 'google',
    }, state)
    duplicate = register_external_search_result({
        'title': 'Second title',
        'url': 'https://example.test/article#result',
        'snippet': 'Second snippet',
        'source': 'bing',
    }, state)

    assert first['citation_index'] == '1.1'
    assert duplicate['citation_index'] == '1.1'
    assert first['ref'] == duplicate['ref'] == '[[1.1]]'
    assert state[CITATION_REFS_KEY] == {
        '1.1': {
            'source_type': 'external',
            'title': 'First title',
            'url': 'https://Example.test/article/',
            'content': 'First snippet',
        },
    }


def test_fetch_supplements_search_source_and_registers_url_aliases():
    state = _state()
    search_item = register_external_search_result({
        'title': 'Search title',
        'url': 'https://example.test/article',
        'snippet': 'Search snippet',
    }, state)
    page = upsert_external_source({
        'url': 'https://example.test/article',
        'final_url': 'https://example.test/article/',
        'title': 'Fetched title',
        'content': 'Fetched page content',
    }, state)

    assert page['citation_index'] == search_item['citation_index'] == '1.1'
    assert page['ref'] == '[[1.1]]'
    assert state[CITATION_REFS_KEY]['1.1'] == {
        'source_type': 'external',
        'title': 'Fetched title',
        'url': 'https://example.test/article/',
        'content': 'Fetched page content',
    }
    duplicate = register_external_search_result({
        'title': 'Later search title',
        'url': 'https://example.test/article#search',
        'snippet': 'Later search snippet',
    }, state)
    assert duplicate['citation_index'] == '1.1'
    assert state[CITATION_REFS_KEY]['1.1']['content'] == 'Fetched page content'


@pytest.mark.parametrize(('first_item', 'duplicate_item', 'expected_url'), [
    (
        {
            'title': 'Paper',
            'url': '',
            'snippet': 'Abstract',
            'source': 'sciverse',
            'extra': {'doi': 'https://doi.org/10.1000/ABC', 'doc_id': 'doc-1'},
        },
        {
            'title': 'Paper from another endpoint',
            'url': 'https://doi.org/10.1000/abc',
            'snippet': 'Another abstract',
            'source': 'sciverse',
            'extra': {'doi': 'doi:10.1000/abc', 'doc_id': 'doc-1'},
        },
        'https://doi.org/10.1000/abc',
    ),
    (
        {
            'title': 'Provider-only document',
            'url': '',
            'snippet': 'Abstract',
            'source': 'sciverse',
            'extra': {'doc_id': 'doc-1'},
        },
        {
            'title': 'Same provider document',
            'url': '',
            'snippet': 'Other abstract',
            'source': 'sciverse',
            'extra': {'doc_id': 'doc-1'},
        },
        '',
    ),
])
def test_external_search_aliases_support_doi_and_provider_document_ids(
    first_item, duplicate_item, expected_url,
):
    state = _state()
    first = register_external_search_result(first_item, state)
    duplicate = register_external_search_result(duplicate_item, state)

    assert first['citation_index'] == duplicate['citation_index'] == '1.1'
    assert state[CITATION_REFS_KEY]['1.1']['url'] == expected_url
