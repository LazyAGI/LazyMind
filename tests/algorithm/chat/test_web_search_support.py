import requests

from lazymind.chat.engine.tools.infra import web_search_support


def test_fetch_url_content_returns_basic_text_links_and_truncation(monkeypatch):
    body = ''.join(f'<p>Paragraph {index} ' + ('x' * 80) + '</p>' for index in range(100))
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
        'content_type', 'title', 'content', 'content_truncated', 'links',
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
