import importlib
import sys
import types
import urllib.error

import pytest


class _Response:
    def __init__(self, payload=None, status_code=200):
        self._payload = payload or {}
        self.status = status_code

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return False

    def raise_for_status(self):
        return None

    def json(self):
        return self._payload


def _import_parsing_runtime(monkeypatch):
    build_document = types.ModuleType('lazymind.parsing.service.build_document')
    build_document.ALGO_ID = 'general_algo'
    build_document.get_algo_server_port = lambda: 18000
    build_document.build_document = lambda: None
    build_document.reset_document = lambda: None
    build_document.drop_lazyllm_tables = lambda: None
    build_document.reset_stores = lambda: None
    monkeypatch.setitem(sys.modules, 'lazymind.parsing.service.build_document', build_document)
    sys.modules.pop('lazymind.parsing.app', None)
    return importlib.import_module('lazymind.parsing.app')


def test_wait_for_http_ok_returns_on_success(monkeypatch):
    parsing_runtime = _import_parsing_runtime(monkeypatch)
    seen_urls = []

    def fake_urlopen(url, timeout):
        seen_urls.append((url, timeout))
        return _Response(status_code=204)

    monkeypatch.setattr(parsing_runtime.urllib.request, 'urlopen', fake_urlopen)

    parsing_runtime._wait_for_http_ok('http://service.test/health', 'service', timeout=1, interval=0.1)

    assert seen_urls == [('http://service.test/health', 3)]


def test_wait_for_http_ok_retries_then_returns(monkeypatch):
    parsing_runtime = _import_parsing_runtime(monkeypatch)
    responses = iter([
        urllib.error.URLError('down'),
        _Response(status_code=200),
    ])
    sleeps = []

    def fake_urlopen(url, timeout):
        response = next(responses)
        if isinstance(response, Exception):
            raise response
        return response

    monkeypatch.setattr(parsing_runtime.urllib.request, 'urlopen', fake_urlopen)
    monkeypatch.setattr(parsing_runtime.time, 'sleep', lambda interval: sleeps.append(interval))

    parsing_runtime._wait_for_http_ok('http://service.test/health', 'service', timeout=5, interval=0.25)

    assert sleeps == [0.25]


def test_wait_for_http_ok_raises_after_timeout(monkeypatch):
    parsing_runtime = _import_parsing_runtime(monkeypatch)
    times = iter([0, 2])
    sleeps = []
    monkeypatch.setattr(parsing_runtime.time, 'time', lambda: next(times))
    monkeypatch.setattr(parsing_runtime.time, 'sleep', lambda interval: sleeps.append(interval))
    monkeypatch.setattr(
        parsing_runtime.urllib.request,
        'urlopen',
        lambda url, timeout: (_ for _ in ()).throw(urllib.error.URLError('down')),
    )

    with pytest.raises(RuntimeError, match='timed out waiting for service'):
        parsing_runtime._wait_for_http_ok('http://service.test/health', 'service', timeout=1, interval=0.5)

    assert sleeps == []


def test_wait_for_algorithm_registration_retries_until_algo_is_present(monkeypatch):
    parsing_runtime = _import_parsing_runtime(monkeypatch)
    responses = iter([
        _Response({'data': [{'algo_id': 'other'}]}),
        _Response({'data': [{'algo_id': 'general_algo'}]}),
    ])
    sleeps = []
    monkeypatch.setattr(parsing_runtime.requests, 'get', lambda url, timeout: next(responses))
    monkeypatch.setattr(parsing_runtime.time, 'sleep', lambda interval: sleeps.append(interval))

    parsing_runtime._wait_for_algorithm_registration(
        'http://processor.test/',
        'general_algo',
        timeout=5,
        interval=0.25,
    )

    assert sleeps == [0.25]


def test_wait_for_algorithm_registration_raises_after_timeout(monkeypatch):
    parsing_runtime = _import_parsing_runtime(monkeypatch)
    times = iter([0, 2])
    monkeypatch.setattr(parsing_runtime.time, 'time', lambda: next(times))
    monkeypatch.setattr(parsing_runtime.time, 'sleep', lambda interval: None)
    monkeypatch.setattr(parsing_runtime.requests, 'get', lambda url, timeout: _Response({'data': []}))

    with pytest.raises(RuntimeError, match='timed out waiting for algorithm registration'):
        parsing_runtime._wait_for_algorithm_registration(
            'http://processor.test',
            'general_algo',
            timeout=1,
            interval=0.25,
        )


def test_main_waits_starts_docs_and_exits_on_keyboard_interrupt(monkeypatch):
    parsing_runtime = _import_parsing_runtime(monkeypatch)
    calls = []

    class FakeDocs:
        def start(self):
            calls.append(('docs.start',))

    monkeypatch.setenv('LAZYMIND_DOCUMENT_PROCESSOR_URL', 'http://processor.test/')
    monkeypatch.setenv('LAZYMIND_STARTUP_RETRY_INTERVAL', '0.5')
    monkeypatch.setenv('LAZYMIND_STARTUP_TIMEOUT', '3')
    monkeypatch.setattr(parsing_runtime, 'build_document', lambda: FakeDocs())
    monkeypatch.setattr(parsing_runtime, 'get_algo_server_port', lambda: 18000)
    monkeypatch.setattr(
        parsing_runtime,
        '_wait_for_http_ok',
        lambda url, label, timeout, interval: calls.append(('http', url, label, timeout, interval)),
    )
    monkeypatch.setattr(
        parsing_runtime,
        '_wait_for_algorithm_registration',
        lambda processor_url, algo_id, timeout, interval: calls.append(
            ('registration', processor_url, algo_id, timeout, interval)
        ),
    )
    monkeypatch.setattr(
        parsing_runtime.time,
        'sleep',
        lambda seconds: (_ for _ in ()).throw(KeyboardInterrupt),
    )

    parsing_runtime.main()

    assert calls == [
        ('http', 'http://processor.test/health', 'DocumentProcessor', 3.0, 0.5),
        ('docs.start',),
        ('http', 'http://127.0.0.1:18000/docs', 'lazyllm-algo local service', 3.0, 0.5),
        ('registration', 'http://processor.test', 'general_algo', 3.0, 0.5),
    ]
