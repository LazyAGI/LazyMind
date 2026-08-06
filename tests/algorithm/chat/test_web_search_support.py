import requests

from lazymind.chat.engine.tools.infra import web_search_support


def test_fetch_url_content_extracts_metadata_links_images_and_observable_truncation(monkeypatch):
    body = ''.join(f'<p>Paragraph {index} ' + ('x' * 80) + '</p>' for index in range(100))
    html = f'''<!doctype html>
    <html>
      <head>
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
    response._lazymind_raw_bytes = len(response.content)
    response._lazymind_response_truncated = False

    monkeypatch.setattr(web_search_support, 'validate_public_http_url', lambda url: url)
    monkeypatch.setattr(web_search_support, 'fetch_public_url', lambda *args, **kwargs: response)

    page = web_search_support.fetch_url_content('https://example.test/original')

    assert page['title'] == 'OG title'
    assert page['description'] == 'Page description'
    assert page['metadata']['canonical_url'] == 'https://example.test/canonical'
    assert page['metadata']['favicon_url'] == 'https://example.test/favicon.ico'
    assert page['metadata']['site_name'] == 'Example Site'
    assert page['metadata']['extraction_stats']['selected_root'] == 'article'
    assert page['links'] == [
        {'id': 1, 'text': 'Navigation', 'target_url': 'https://example.test/navigation'},
        {'id': 2, 'text': 'Child', 'target_url': 'https://example.test/child'},
    ]
    assert {item['url'] for item in page['metadata']['image_urls']} == {
        'https://example.test/cover.png',
        'https://example.test/body.png',
    }
    assert page['content_truncated'] is True
    assert 'Remove newsletter text' not in page['content']
    assert page['truncation_strategy'] == 'head'
    assert len(page['content']) == page['returned_content_chars'] == page['content_max_chars']
    assert page['content_chars'] > page['returned_content_chars']


def test_fetch_url_content_detects_utf8_when_http_defaults_to_latin1(monkeypatch):
    html = '''<html><head><title>并发编程指南</title></head>
    <body><article><p>协程适合高并发网络任务。</p></article></body></html>'''
    response = requests.Response()
    response.status_code = 200
    response.url = 'https://example.test/concurrency'
    response.headers['Content-Type'] = 'text/html'
    response.encoding = 'ISO-8859-1'
    response._content = html.encode('utf-8')

    monkeypatch.setattr(web_search_support, 'validate_public_http_url', lambda url: url)
    monkeypatch.setattr(web_search_support, 'fetch_public_url', lambda *args, **kwargs: response)

    page = web_search_support.fetch_url_content(response.url)

    assert page['title'] == '并发编程指南'
    assert page['content'] == '协程适合高并发网络任务。'
