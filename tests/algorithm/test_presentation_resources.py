"""Cloud PPT libraries: mirror recovery, integrity, safe extraction and single flight."""
from concurrent.futures import ThreadPoolExecutor
import hashlib
import io
import json
import threading
import zipfile

import httpx
import pytest

from lazymind import presentation_resources as resources


@pytest.fixture
def bundle(tmp_path, monkeypatch):
    data = io.BytesIO()
    with zipfile.ZipFile(data, 'w') as archive:
        archive.writestr('presentation-assets/catalog.json', json.dumps({'assets': [], 'sources': {}}))
    raw = data.getvalue()
    release = {'path': 'Ppt-assets/test.zip', 'root': 'presentation-assets',
               'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()}
    monkeypatch.setenv('LAZYMIND_PPT_RESOURCE_CACHE', str(tmp_path / 'cache'))
    monkeypatch.setattr(resources, 'RELEASES', {'assets': release})
    return raw


def transport(monkeypatch, handler):
    client = httpx.Client
    monkeypatch.setattr(resources.httpx, 'Client', lambda **kw: client(transport=httpx.MockTransport(handler), **kw))


def test_bad_mirror_recovers_and_completed_cache_never_downloads_again(bundle, monkeypatch):
    calls = []

    def handler(request):
        calls.append(str(request.url))
        return httpx.Response(200, content=b'bad' if len(calls) == 1 else bundle)

    transport(monkeypatch, handler)
    root = resources.ensure_resource('assets')
    assert json.loads((root / 'catalog.json').read_text())['assets'] == []
    assert len(calls) == 2
    assert resources.ensure_resource('assets') == root
    assert len(calls) == 2
    assert not list(resources.cache_root().glob('.download-*'))


def test_failed_download_leaves_no_ready_cache_and_can_retry(bundle, monkeypatch):
    good = False

    def handler(request):
        return httpx.Response(200, content=bundle) if good else httpx.Response(502)

    transport(monkeypatch, handler)
    with pytest.raises(RuntimeError, match='HTTP 502'):
        resources.ensure_resource('assets')
    assert resources.cached_resource('assets') is None
    good = True
    assert resources.ensure_resource('assets').is_dir()


def test_concurrent_consumers_download_once(bundle, monkeypatch):
    calls = []
    transport(monkeypatch, lambda request: calls.append(request) or httpx.Response(200, content=bundle))
    with ThreadPoolExecutor(max_workers=4) as pool:
        paths = list(pool.map(lambda _: resources.ensure_resource('assets'), range(4)))
    assert len(set(paths)) == 1
    assert len(calls) == 1


@pytest.mark.parametrize('name', ['../escape', '/absolute', 'C:/escape', 'a\\escape'])
def test_rejects_archive_escape(tmp_path, name):
    archive = tmp_path / 'bad.zip'
    with zipfile.ZipFile(archive, 'w') as stream:
        entry = zipfile.ZipInfo('placeholder')
        # ZipInfo's constructor normalizes backslashes on Windows. Preserve the
        # hostile archive name so the reader, not the writer, validates it.
        entry.filename = name
        stream.writestr(entry, 'bad')
    with pytest.raises(ValueError, match='Unsafe'):
        resources._extract(archive, tmp_path / 'out')
    assert not (tmp_path / 'escape').exists()


def test_prefetch_returns_while_download_is_running_and_deduplicates(bundle, monkeypatch):
    entered, release = threading.Event(), threading.Event()
    calls = []

    def handler(request):
        calls.append(request)
        entered.set()
        assert release.wait(5)
        return httpx.Response(200, content=bundle)

    transport(monkeypatch, handler)
    monkeypatch.setattr(resources, '_JOBS', {})
    with ThreadPoolExecutor(max_workers=2) as executor:
        monkeypatch.setattr(resources, '_EXECUTOR', executor)
        _check_prefetch(entered, release, resources, calls)


def _check_prefetch(entered, release, resources, calls):
    try:
        resources.prefetch()
        assert entered.wait(5)
        resources.prefetch()
        assert resources.cached_resource('assets') is None
    finally:
        release.set()
        for job in resources._JOBS.values():
            job.result(timeout=5)
    assert len(calls) == 1
