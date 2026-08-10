from lazymind.chat.service.utils.citations import (
    CITATION_REFS_KEY,
    normalize_external_url,
    register_external_search_result,
    reset_citation_state,
    upsert_external_source,
)


def _state():
    state = {}
    reset_citation_state(state)
    return state


def test_normalize_external_url_for_source_matching():
    assert normalize_external_url('HTTPS://Example.COM:443/path/#fragment') == 'https://example.com/path'
    assert normalize_external_url('http://example.com:80/') == 'http://example.com'
    assert normalize_external_url('https://example.com/path/?a=1#part') == 'https://example.com/path?a=1'
    assert normalize_external_url(
        'https://example.com/path?id=1&utm_source=search&fbclid=tracking'
    ) == 'https://example.com/path?id=1'
    assert normalize_external_url('file:///tmp/page.html') == ''


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
        'final_url': 'https://www.example.test/article/',
        'title': 'Fetched title',
        'content': 'Fetched page content',
        'metadata': {'canonical_url': 'https://example.test/canonical'},
    }, state)

    assert page['citation_index'] == search_item['citation_index'] == '1.1'
    assert page['ref'] == '[[1.1]]'
    assert state[CITATION_REFS_KEY]['1.1'] == {
        'source_type': 'external',
        'title': 'Fetched title',
        'url': 'https://example.test/canonical',
        'content': 'Fetched page content',
    }
    duplicate = register_external_search_result({
        'title': 'Later search title',
        'url': 'https://example.test/canonical#search',
        'snippet': 'Later search snippet',
    }, state)
    assert duplicate['citation_index'] == '1.1'
    assert state[CITATION_REFS_KEY]['1.1']['content'] == 'Fetched page content'


def test_external_search_aliases_deduplicate_provider_identifiers():
    state = _state()
    first = register_external_search_result({
        'title': 'Paper',
        'url': '',
        'snippet': 'Abstract',
        'source': 'sciverse',
        'extra': {'doi': 'https://doi.org/10.1000/ABC', 'doc_id': 'doc-1'},
    }, state)
    duplicate = register_external_search_result({
        'title': 'Paper from another endpoint',
        'url': 'https://doi.org/10.1000/abc',
        'snippet': 'Another abstract',
        'source': 'sciverse',
        'extra': {'doi': 'doi:10.1000/abc', 'doc_id': 'doc-1'},
    }, state)

    assert first['citation_index'] == duplicate['citation_index'] == '1.1'
    assert state[CITATION_REFS_KEY]['1.1']['url'] == 'https://doi.org/10.1000/abc'


def test_external_search_result_with_provider_document_id_is_registered_without_url():
    state = _state()
    item = register_external_search_result({
        'title': 'Provider-only document',
        'url': '',
        'snippet': 'Abstract',
        'source': 'sciverse',
        'extra': {'doc_id': 'doc-1'},
    }, state)

    assert item['ref'] == '[[1.1]]'
    assert state[CITATION_REFS_KEY]['1.1'] == {
        'source_type': 'external',
        'title': 'Provider-only document',
        'url': '',
        'content': 'Abstract',
    }
