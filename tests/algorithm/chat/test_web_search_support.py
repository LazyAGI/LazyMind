import requests
import pytest

from lazymind.chat.engine.tools.infra import web_search_support


def test_fetch_url_content_returns_basic_text_links_and_truncation(monkeypatch):
    body = ''.join(f'<p>Paragraph {index} ' + ('x' * 80) + '</p>' for index in range(300))
    html = f'''<!doctype html>
    <html>
      <head>
        <title>Document title</title>
        <meta property="og:title" content="OG title">
        <meta name="description" content="Page description">
        <meta property="og:site_name" content="Example Site">
        <meta property="og:image" content="/cover.png">
        <link rel="canonical" href="/canonical">
        <link rel="icon" href="/favicon.ico">
      </head>
      <body>
        <nav><a href="/navigation">Navigation</a></nav>
        <article>
          <h1>Article heading</h1>
          <div class="newsletter"><p>Remove newsletter text</p></div>
          <script>ignore script text</script>
          {body}
          <a href="/child#section">Child</a>
          <a href="https://user:secret@example.test/private">Credential link</a>
          <img src="/body.png" alt="Body image">
        </article>
      </body>
    </html>'''
    response = requests.Response()
    response.status_code = 200
    response.url = 'https://example.test/final'
    response.headers['Content-Type'] = 'text/html; charset=utf-8'
    response.encoding = 'utf-8'
    response._content = html.encode()
    response._lazymind_response_truncated = False

    monkeypatch.setattr(web_search_support, 'validate_public_http_url', lambda url: url)
    monkeypatch.setattr(web_search_support, 'fetch_public_url', lambda *args, **kwargs: response)

    page = web_search_support.fetch_url_content('https://example.test/original')

    assert page['title'] == 'Document title'
    assert page['links'] == [
        {'text': 'Navigation', 'target_url': 'https://example.test/navigation'},
        {'text': 'Child', 'target_url': 'https://example.test/child'},
    ]
    assert page['content_truncated'] is True
    assert 'Remove newsletter text' in page['content']
    assert 'ignore script text' not in page['content']
    assert set(page) == {
        'status', 'source_status', 'url', 'final_url', 'status_code',
        'content_type', 'title', 'content', 'content_truncated', 'content_read', 'links',
    }


def test_fetch_url_content_rejects_binary_resources(monkeypatch):
    response = requests.Response()
    response.status_code = 200
    response.url = 'https://example.test/image.jpg'
    response.headers['Content-Type'] = 'image/jpeg'
    response._content = b'\xff\xd8\xff\xe0binary'
    response._lazymind_response_truncated = False

    monkeypatch.setattr(web_search_support, 'validate_public_http_url', lambda url: url)
    monkeypatch.setattr(web_search_support, 'fetch_public_url', lambda *args, **kwargs: response)

    try:
        web_search_support.fetch_url_content(response.url)
    except ValueError as exc:
        assert str(exc) == 'unsupported url content type: image/jpeg'
    else:
        raise AssertionError('binary resources must not be decoded as page text')


def test_fetch_url_content_ingests_pdf(monkeypatch):
    response = requests.Response()
    response.status_code = 200
    response.url = 'https://example.test/paper.pdf'
    response.headers['Content-Type'] = 'application/pdf'
    response._content = b'%PDF-1.4 fake'
    response._lazymind_response_truncated = False

    monkeypatch.setattr(web_search_support, 'validate_public_http_url', lambda url: url)
    monkeypatch.setattr(web_search_support, 'fetch_public_url', lambda *args, **kwargs: response)
    monkeypatch.setattr(
        web_search_support,
        '_ingest_fetched_pdf',
        lambda *args, **kwargs: {
            'status': 'ok',
            'source_status': 'pdf_ingested',
            'url': 'https://example.test/paper.pdf',
            'final_url': 'https://example.test/paper.pdf',
            'status_code': 200,
            'content_type': 'application/pdf',
            'title': 'paper.pdf',
            'content': '',
            'content_truncated': False,
            'links': [],
            'file_id': 'fr_abc123abc123',
            'display_name': 'paper.pdf',
            'pages': 12,
            'parse_status': 'ready',
            'parse_error': None,
        },
    )

    page = web_search_support.fetch_url_content('https://example.test/paper.pdf', offset=4000, limit=1)
    assert page['source_status'] == 'pdf_ingested'
    assert page['file_id'] == 'fr_abc123abc123'
    assert page['content'] == ''


def test_page_content_continuation_has_no_gaps_and_does_not_hide_download_cap():
    text = '中文abcdef' * 1000
    offset = 0
    pages = []
    while True:
        result = web_search_support._page_content(text, 700, offset, False)
        pages.append(result['content'])
        read = result['content_read']
        if not read['more']:
            break
        offset = read['next_offset']
    assert ''.join(pages) == text
    capped = web_search_support._page_content(text, len(text), 0, True)
    assert capped['content_truncated'] is True
    assert 'more' not in capped['content_read']
    assert 'next_offset' not in capped['content_read']


@pytest.mark.parametrize('kwargs', [
    {'offset': -1}, {'offset': True}, {'offset': None}, {'offset': 1.5},
    {'limit': 0}, {'limit': -1}, {'limit': '1'}, {'limit': 1.5},
    {'offset': '0'}, {'limit': True},
])
def test_invalid_url_window_fails_before_network(monkeypatch, kwargs):
    monkeypatch.setattr(web_search_support, 'validate_public_http_url', lambda url: pytest.fail('network'))
    with pytest.raises(ValueError):
        web_search_support.fetch_url_content('https://example.test', **kwargs)


def test_url_fetch_character_limit_and_continuation(monkeypatch):
    text = '正文🙂' * 20000
    response = requests.Response()
    response.status_code = 200
    response.url = 'https://example.test/text'
    response.headers['Content-Type'] = 'text/plain; charset=utf-8'
    response.encoding = 'utf-8'
    response._content = text.encode()
    monkeypatch.setattr(web_search_support, 'validate_public_http_url', lambda url: url)
    monkeypatch.setattr(web_search_support, 'fetch_public_url', lambda *a, **k: response)
    first = web_search_support.fetch_url_content(response.url, limit=100000)
    assert len(first['content']) == 4000
    offset = first['content_read']['next_offset']
    second = web_search_support.fetch_url_content(response.url, offset=offset, limit=13)
    assert second['content'] == text[offset:offset + 13]


@pytest.mark.parametrize('media_type', ['text/plain', 'text/html'])
@pytest.mark.parametrize('configured,limit,expected', [
    (4000, None, 4000), (900, None, 900), (900, 100000, 900), (900, 13, 13),
    (1, None, 200), ('invalid', None, 4000),
])
def test_configured_page_length_and_refetch(monkeypatch, media_type, configured, limit, expected):
    text = '正文🙂abcdef' * 501
    response = requests.Response()
    response.status_code = 200
    response.url = 'https://example.test/page'
    response.headers['Content-Type'] = media_type
    response.encoding = 'utf-8'
    response._content = (f'<html><body><p>{text}</p></body></html>' if media_type == 'text/html' else text).encode()
    monkeypatch.setattr(web_search_support, '_cfg', {'url_fetch_max_length': configured, 'web_search_timeout': 10})
    monkeypatch.setattr(web_search_support, 'validate_public_http_url', lambda url: url)
    calls = []

    def fetch(*args, **kwargs):
        calls.append(args[1])
        return response

    monkeypatch.setattr(web_search_support, 'fetch_public_url', fetch)
    pages = []
    offset = 0
    while True:
        page = web_search_support.fetch_url_content(response.url, offset=offset, limit=limit)
        read = page['content_read']
        assert read['limit'] == expected
        assert page['content'] == text[offset:offset + expected]
        pages.append(page['content'])
        if not read['more']:
            break
        offset = read['next_offset']
    assert ''.join(pages) == text
    assert len(calls) == len(pages) > 1
    for offset in (len(text), len(text) + 10):
        page = web_search_support.fetch_url_content(response.url, offset=offset, limit=limit)
        assert page['content'] == ''
        assert page['content_read']['more'] is False
    response._content = b''
    assert web_search_support.fetch_url_content(response.url)['content'] == ''


def test_download_truncation_allows_only_available_pages(monkeypatch):
    response = requests.Response()
    response.status_code = 200
    response.url = 'https://example.test/page'
    response.headers['Content-Type'] = 'text/plain'
    response.encoding = 'utf-8'
    response._content = ('文' * 5000).encode()
    response._lazymind_response_truncated = True
    monkeypatch.setattr(web_search_support, '_cfg', {'url_fetch_max_length': 4000, 'web_search_timeout': 10})
    monkeypatch.setattr(web_search_support, 'validate_public_http_url', lambda url: url)
    monkeypatch.setattr(web_search_support, 'fetch_public_url', lambda *args, **kwargs: response)
    first = web_search_support.fetch_url_content(response.url)
    assert first['content_read']['more'] is True
    last = web_search_support.fetch_url_content(response.url, offset=first['content_read']['next_offset'])
    assert len(last['content']) == 1000
    assert last['content_truncated'] is True
    assert last['content_read']['response_truncated'] is True
    assert 'more' not in last['content_read'] and 'next_offset' not in last['content_read']


@pytest.mark.parametrize('url', ['http://127.0.0.1/private', 'https://user:secret@example.test/private'])
def test_paginated_fetch_preserves_url_safety(monkeypatch, url):
    monkeypatch.setattr(web_search_support.socket, 'getaddrinfo',
                        lambda *a, **k: [(None, None, None, None, ('127.0.0.1', 80))])
    monkeypatch.setattr(web_search_support, 'fetch_public_url', lambda *a, **k: pytest.fail('unsafe fetch'))
    with pytest.raises(ValueError):
        web_search_support.fetch_url_content(url, offset=4000, limit=100)
