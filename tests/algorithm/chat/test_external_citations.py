from lazymind.chat.service.utils.citations import (
    CITATION_REFS_KEY,
    EXTERNAL_SOURCE_KEY_MAP_KEY,
    normalize_external_url,
    register_citation_item,
    register_external_search_result,
    reset_citation_state,
    rewrite_citations,
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
    assert state[EXTERNAL_SOURCE_KEY_MAP_KEY] == {'url:https://example.test/article': '1.1'}


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
    assert page['source_action'] == 'supplemented_source'
    assert state[CITATION_REFS_KEY]['1.1'] == {
        'source_type': 'external',
        'title': 'Fetched title',
        'url': 'https://example.test/canonical',
        'content': 'Fetched page content',
    }
    assert state[EXTERNAL_SOURCE_KEY_MAP_KEY] == {
        'url:https://example.test/article': '1.1',
        'url:https://www.example.test/article': '1.1',
        'url:https://example.test/canonical': '1.1',
    }

    duplicate = register_external_search_result({
        'title': 'Later search title',
        'url': 'https://example.test/canonical#search',
        'snippet': 'Later search snippet',
    }, state)
    assert duplicate['citation_index'] == '1.1'
    assert state[CITATION_REFS_KEY]['1.1']['content'] == 'Fetched page content'


def test_fetch_keeps_image_and_favicon_source_mapping():
    state = _state()
    page = upsert_external_source({
        'url': 'https://example.test/article',
        'title': 'Article',
        'content': 'Content',
        'metadata': {
            'favicon_url': 'https://example.test/favicon.ico',
            'image_urls': [
                {'url': 'https://example.test/cover.png'},
                {'url': 'https://example.test/cover.png'},
            ],
        },
    }, state)

    source = state[CITATION_REFS_KEY][page['citation_index']]
    assert source['image_urls'] == ['https://example.test/cover.png']
    assert source['favicon_url'] == 'https://example.test/favicon.ico'


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
    assert state[EXTERNAL_SOURCE_KEY_MAP_KEY]['doi:10.1000/abc'] == '1.1'
    assert state[EXTERNAL_SOURCE_KEY_MAP_KEY]['provider_doc:sciverse:doc-1'] == '1.1'


def test_external_search_result_without_openable_url_is_not_registered():
    state = _state()
    item = register_external_search_result({
        'title': 'Provider-only document',
        'url': '',
        'snippet': 'Abstract',
        'source': 'sciverse',
        'extra': {'doc_id': 'doc-1'},
    }, state)

    assert 'ref' not in item
    assert state[CITATION_REFS_KEY] == {}


def test_direct_fetch_requires_content_and_does_not_replace_values_with_empty_fields():
    state = _state()
    rejected = upsert_external_source({
        'url': 'https://example.test/empty',
        'title': 'Empty page',
        'content': '',
    }, state)
    assert 'citation_index' not in rejected
    assert state[CITATION_REFS_KEY] == {}

    page = upsert_external_source({
        'url': 'https://example.test/direct',
        'title': 'Direct page',
        'content': 'Direct content',
    }, state)
    updated = upsert_external_source({
        'url': 'https://example.test/direct#section',
        'title': '',
        'content': 'Updated content',
    }, state)

    assert page['citation_index'] == updated['citation_index'] == '1.1'
    assert state[CITATION_REFS_KEY]['1.1']['title'] == 'Direct page'
    assert state[CITATION_REFS_KEY]['1.1']['content'] == 'Updated content'


def test_external_and_kb_sources_share_document_numbering():
    state = _state()
    external = register_external_search_result({
        'title': 'External',
        'url': 'https://example.test',
        'snippet': 'External evidence',
    }, state)
    kb = register_citation_item({
        'uid': 'node-1',
        'text': 'KB evidence',
        'docid': 'doc-1',
        'kb_id': 'kb-1',
        'group': 'block',
        'number': 1,
        'metadata': {'file_name': 'doc.md'},
    }, state)

    assert external['citation_index'] == '1.1'
    assert kb['citation_index'] == '2.1'
    assert state[CITATION_REFS_KEY]['2.1']['source_type'] == 'knowledge_base'


def test_external_source_rewrites_with_its_title_and_reset_isolates_requests():
    state = _state()
    item = register_external_search_result({
        'title': 'External title',
        'url': 'https://example.test',
        'snippet': 'External evidence',
    }, state)

    text, sources = rewrite_citations(f"Evidence {item['ref']}", state)
    assert text == 'Evidence [1](#source-1.1 "External title")'
    assert sources[0]['source_type'] == 'external'
    assert sources[0]['index'] == '1.1'

    reset_citation_state(state)
    assert state[CITATION_REFS_KEY] == {}
    assert state[EXTERNAL_SOURCE_KEY_MAP_KEY] == {}
