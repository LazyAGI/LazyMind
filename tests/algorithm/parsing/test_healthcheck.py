import pytest

import lazymind.parsing.service.healthcheck as healthcheck


class _UrlopenResponse:
    def __init__(self, status=200, body=b'{}'):
        self.status = status
        self._body = body

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return None

    def read(self):
        return self._body


def test_get_bytes_raises_for_bad_status(monkeypatch):
    monkeypatch.setattr(
        healthcheck.urllib.request,
        'urlopen',
        lambda url, timeout: _UrlopenResponse(status=500),
    )

    with pytest.raises(RuntimeError, match='HTTP 500'):
        healthcheck._get_bytes('http://service.test/health')


def test_main_checks_local_docs_and_processor_registration(monkeypatch):
    seen_urls = []

    def fake_get_bytes(url):
        seen_urls.append(url)
        if url.endswith('/algo/list'):
            return b'{"data":[{"algo_id":"general_algo"}]}'
        return b''

    monkeypatch.setenv('LAZYMIND_ALGO_SERVER_PORT', '18000')
    monkeypatch.setenv('LAZYMIND_DOCUMENT_PROCESSOR_URL', 'http://processor.test/')
    monkeypatch.setattr(healthcheck, '_get_bytes', fake_get_bytes)

    assert healthcheck.main() == 0
    assert seen_urls == [
        'http://127.0.0.1:18000/docs',
        'http://processor.test/algo/list',
    ]


def test_main_uses_document_server_port_fallback(monkeypatch):
    seen_urls = []

    def fake_get_bytes(url):
        seen_urls.append(url)
        return b'{"data":[{"algo_id":"general_algo"}]}'

    monkeypatch.delenv('LAZYMIND_ALGO_SERVER_PORT', raising=False)
    monkeypatch.setenv('LAZYMIND_DOCUMENT_SERVER_PORT', '18001')
    monkeypatch.setattr(healthcheck, '_get_bytes', fake_get_bytes)

    assert healthcheck.main() == 0
    assert seen_urls[0] == 'http://127.0.0.1:18001/docs'


def test_main_raises_when_algo_is_not_registered(monkeypatch):
    monkeypatch.setattr(healthcheck, '_get_bytes', lambda url: b'{"data":[]}')

    with pytest.raises(RuntimeError, match='algo_id not registered yet'):
        healthcheck.main()
