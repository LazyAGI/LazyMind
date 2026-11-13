from concurrent.futures import ThreadPoolExecutor
from threading import Barrier, Event, Lock, get_ident
from types import SimpleNamespace

import pytest
import requests

from lazymind.parsing.service import build_document


def _response(payload=None, content=b'zip'):
    return SimpleNamespace(json=lambda: payload, content=content, status_code=200,
                           raise_for_status=lambda: None)


def _reader_with_batch(monkeypatch, tmp_path, poll):
    source = tmp_path / 'report.pdf'
    source.write_bytes(b'fixture-pdf')
    monkeypatch.setitem(build_document._cfg._impl, 'ocr_cache_dir', str(tmp_path / 'images'))
    reader = build_document._build_pdf_reader()._build_reader('mineru', 'https://mineru.net')
    monkeypatch.setattr(reader, '_split_large_pdf', lambda path: [(path, 0)])
    monkeypatch.setattr(reader, '_get_token', lambda: 'fixture-token')
    monkeypatch.setattr(reader, '_resolve_dynamic_token', lambda: 'fixture-token')
    monkeypatch.setattr(reader, '_extract_content_from_zip',
                        lambda content, task_dir=None: ('[]', task_dir))
    monkeypatch.setattr('time.sleep', lambda _: None)
    calls = {'submit': 0, 'upload': 0, 'poll': 0, 'download': 0}

    def submit(method, url, **kwargs):
        assert method == 'POST'
        calls['submit'] += 1
        configured = reader._resolve_dynamic_token()
        key = configured[0] if isinstance(configured, (list, tuple)) else configured
        return _response({'code': 0, 'data': {
            'batch_id': 'batch-1',
            'file_urls': ['https://mineru.oss-cn-shanghai.aliyuncs.com/one'],
        }}), key

    def status(method, url, auth_key, **kwargs):
        assert method == 'GET' and url.endswith('/batch-1') and auth_key == 'fixture-token'
        calls['poll'] += 1
        return poll(calls['poll'])

    def upload(url, **kwargs):
        assert url == 'https://mineru.oss-cn-shanghai.aliyuncs.com/one'
        assert kwargs['allow_redirects'] is False
        calls['upload'] += 1
        return _response()

    def download(url, **kwargs):
        assert url == 'https://cdn-mineru.openxlab.org.cn/result.zip'
        assert kwargs['allow_redirects'] is False
        calls['download'] += 1
        return _response()

    monkeypatch.setattr(reader, '_submit_with_key', submit)
    monkeypatch.setattr(reader, '_request_with_pinned_auth', status)
    monkeypatch.setattr(requests, 'put', upload)
    monkeypatch.setattr(requests, 'get', download)
    return reader, source, calls


def _status(state='pending', *, code=0, message='ok'):
    return _response({'code': code, 'msg': message, 'data': {'extract_result': [
        {'state': state, 'full_zip_url': 'https://cdn-mineru.openxlab.org.cn/result.zip'},
    ]}})


def test_mineru_waits_past_120_pending_polls_without_submitting_again(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(
        monkeypatch, tmp_path, lambda poll: _status('done' if poll == 121 else 'pending'))

    assert reader._fetch_async(source)[0] == '[]'
    assert calls == {'submit': 1, 'upload': 1, 'poll': 121, 'download': 1}


def test_mineru_transient_poll_failure_keeps_the_uploaded_batch(monkeypatch, tmp_path):
    def poll(number):
        if number == 1:
            raise requests.ConnectionError('poll connection lost')
        return _status('done')

    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, poll)

    assert reader._fetch_async(source)[0] == '[]'
    assert calls == {'submit': 1, 'upload': 1, 'poll': 2, 'download': 1}


def test_mineru_transient_result_download_retries_same_batch(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))

    def download(url, **kwargs):
        calls['download'] += 1
        if calls['download'] == 1:
            raise requests.ReadTimeout('zip read stalled')
        return _response()

    monkeypatch.setattr(requests, 'get', download)

    assert reader._fetch_async(source)[0] == '[]'
    assert calls == {'submit': 1, 'upload': 1, 'poll': 1, 'download': 2}


def test_mineru_certificate_failure_does_not_poll_for_an_hour(monkeypatch, tmp_path):
    def poll(_):
        raise requests.exceptions.SSLError('untrusted certificate')

    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, poll)
    with pytest.raises(RuntimeError, match='status request failed.*SSLError'):
        reader._fetch_async(source)
    assert calls == {'submit': 1, 'upload': 1, 'poll': 1, 'download': 0}


def test_mineru_reader_verifies_api_and_artifact_tls(monkeypatch, tmp_path):
    reader, _, _ = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    seen = []

    def send(method, url, **kwargs):
        seen.append(kwargs)
        return SimpleNamespace(status_code=200, ok=True)

    monkeypatch.setattr(requests, 'request', send)
    reader._http_execute('GET', 'https://mineru.net/api/v4/extract-results/batch/test', verify=False)
    assert seen[0]['verify'] is True
    assert seen[0]['allow_redirects'] is False
    assert reader._online_request_kwargs() == {'verify': True}


def test_mineru_reports_api_error_instead_of_pretending_it_is_pending(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(
        monkeypatch, tmp_path, lambda _: _response({'code': 10017, 'msg': 'quota exhausted', 'data': None}))

    with pytest.raises(RuntimeError, match='10017.*quota exhausted'):
        reader._fetch_async(source)
    assert calls['submit'] == calls['upload'] == calls['poll'] == 1

    retry, source, retry_calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    assert retry._fetch_async(source)[0] == '[]'
    assert retry_calls == {'submit': 0, 'upload': 0, 'poll': 1, 'download': 1}


def test_mineru_reports_malformed_status_json_without_resubmitting(monkeypatch, tmp_path):
    broken = SimpleNamespace(json=lambda: (_ for _ in ()).throw(ValueError('invalid json')))
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: broken)

    with pytest.raises(RuntimeError, match='malformed batch batch-1 status response'):
        reader._fetch_async(source)
    assert calls['submit'] == calls['upload'] == calls['poll'] == 1


def test_mineru_rejects_untrusted_upload_target_before_sending_pdf(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))

    def bad_submit(method, url, **kwargs):
        calls['submit'] += 1
        return _response({'code': 0, 'data': {
            'batch_id': 'batch-1', 'file_urls': ['https://attacker.example/steal'],
        }}), reader._get_token()

    monkeypatch.setattr(reader, '_submit_with_key', bad_submit)
    with pytest.raises(RuntimeError, match='invalid batch upload target'):
        reader._fetch_async(source)
    assert calls['submit'] == 1
    assert calls['upload'] == calls['poll'] == calls['download'] == 0


def test_mineru_rejects_untrusted_result_archive(monkeypatch, tmp_path):
    def poll(_):
        return _response({'code': 0, 'data': {'extract_result': [
            {'state': 'done', 'full_zip_url': 'https://attacker.example/result.zip'},
        ]}})

    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, poll)
    with pytest.raises(RuntimeError, match='no result archive'):
        reader._fetch_async(source)
    assert calls == {'submit': 1, 'upload': 1, 'poll': 1, 'download': 0}


def test_mineru_rejects_upload_redirect_without_following_it(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))

    def redirected_upload(url, **kwargs):
        assert kwargs['allow_redirects'] is False
        calls['upload'] += 1
        return SimpleNamespace(status_code=307, raise_for_status=lambda: None)

    monkeypatch.setattr(requests, 'put', redirected_upload)
    with pytest.raises(RuntimeError, match='batch upload redirect'):
        reader._fetch_async(source)
    assert calls == {'submit': 1, 'upload': 1, 'poll': 0, 'download': 0}


def test_mineru_rejects_archive_redirect_without_following_it(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))

    def redirected_download(url, **kwargs):
        assert kwargs['allow_redirects'] is False
        calls['download'] += 1
        return SimpleNamespace(status_code=302, raise_for_status=lambda: None)

    monkeypatch.setattr(requests, 'get', redirected_download)
    with pytest.raises(RuntimeError, match='archive download redirect'):
        reader._fetch_async(source)
    assert calls == {'submit': 1, 'upload': 1, 'poll': 1, 'download': 1}


def test_mineru_malformed_archive_allows_new_batch_on_retry(monkeypatch, tmp_path):
    first, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))

    def malformed_archive(content, task_dir=None):
        raise ValueError('No *_content_list.json found in zip')

    monkeypatch.setattr(first, '_extract_content_from_zip', malformed_archive)
    with pytest.raises(ValueError, match='No .*content_list'):
        first._fetch_async(source)
    assert calls['submit'] == calls['upload'] == 1

    retry, source, retry_calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    assert retry._fetch_async(source)[0] == '[]'
    assert retry_calls == {'submit': 1, 'upload': 1, 'poll': 1, 'download': 1}


def test_mineru_wait_budget_is_bounded_and_reports_the_pending_batch(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('running'))
    reader._timeout = 8
    clock = [0.0]
    monkeypatch.setattr('time.monotonic', lambda: clock[0])
    monkeypatch.setattr('time.sleep', lambda seconds: clock.__setitem__(0, clock[0] + seconds))

    with pytest.raises(TimeoutError, match='batch-1.*running'):
        reader._fetch_async(source)
    assert calls['submit'] == calls['upload'] == 1
    assert calls['poll'] <= 4


def test_mineru_retry_resumes_uploaded_batch_after_worker_restart(monkeypatch, tmp_path):
    first, source, first_calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('pending'))
    first._timeout = 8
    clock = [0.0]
    monkeypatch.setattr('time.monotonic', lambda: clock[0])
    monkeypatch.setattr('time.sleep', lambda seconds: clock.__setitem__(0, clock[0] + seconds))
    with pytest.raises(TimeoutError):
        first._fetch_async(source)
    assert first_calls['submit'] == first_calls['upload'] == 1

    second, source, second_calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    assert second._fetch_async(source)[0] == '[]'
    assert second_calls == {'submit': 0, 'upload': 0, 'poll': 1, 'download': 1}

    current_time = __import__('time').time()
    monkeypatch.setattr('time.time', lambda: current_time + 24 * 3600 + 1)
    later, source, later_calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    assert later._fetch_async(source)[0] == '[]'
    assert later_calls['submit'] == later_calls['upload'] == 1


def test_mineru_completed_batch_is_reused_on_next_parse(monkeypatch, tmp_path):
    first, source, first_calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    assert first._fetch_async(source)[0] == '[]'
    assert first_calls['submit'] == first_calls['upload'] == 1

    second, source, second_calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    assert second._fetch_async(source)[0] == '[]'
    assert second_calls == {'submit': 0, 'upload': 0, 'poll': 1, 'download': 1}


def test_mineru_split_retry_reuses_a_completed_part(monkeypatch, tmp_path):
    part_a, part_b = tmp_path / 'part-a.pdf', tmp_path / 'part-b.pdf'
    part_a.write_bytes(b'a')
    part_b.write_bytes(b'b')
    splits = [(str(part_a), 0), (str(part_b), 1)]
    first, source, first_calls = _reader_with_batch(
        monkeypatch, tmp_path, lambda _: _status('done'))
    monkeypatch.setattr(first, '_split_large_pdf', lambda _: splits)
    original_wait = first._wait_for_batch

    def wait(batch_id, auth_key, task_dir, fname, marker_path=None, **kwargs):
        if fname == 'part-b.pdf':
            raise TimeoutError('part B still pending')
        return original_wait(batch_id, auth_key, task_dir, fname, marker_path, **kwargs)

    monkeypatch.setattr(first, '_wait_for_batch', wait)
    with pytest.raises(TimeoutError, match='part B'):
        first._fetch_async(source)
    assert first_calls['submit'] == first_calls['upload'] == 2

    retry, source, retry_calls = _reader_with_batch(
        monkeypatch, tmp_path, lambda _: _status('done'))
    monkeypatch.setattr(retry, '_split_large_pdf', lambda _: splits)
    assert retry._fetch_async(source)[0] == '[]'
    assert retry_calls == {'submit': 0, 'upload': 0, 'poll': 2, 'download': 2}


def test_mineru_parallel_attempts_submit_the_same_pdf_once(monkeypatch, tmp_path):
    original_sleep = __import__('time').sleep
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    monkeypatch.setattr('time.sleep', original_sleep)
    first_upload_entered = Event()
    second_digest_done = Event()
    upload_release = Event()
    counter_lock = Lock()
    digest_calls = [0]
    original_digest = reader._source_digest

    def digest(path):
        result = original_digest(path)
        with counter_lock:
            digest_calls[0] += 1
            if digest_calls[0] == 2:
                second_digest_done.set()
        return result

    def upload(url, **kwargs):
        with counter_lock:
            calls['upload'] += 1
        first_upload_entered.set()
        assert upload_release.wait(5)
        return _response()

    monkeypatch.setattr(reader, '_source_digest', digest)
    monkeypatch.setattr(requests, 'put', upload)
    with ThreadPoolExecutor(max_workers=2) as executor:
        first = executor.submit(reader._fetch_async, source)
        assert first_upload_entered.wait(5)
        second = executor.submit(reader._fetch_async, source)
        assert second_digest_done.wait(5)
        upload_release.set()
        assert first.result(timeout=5)[0] == '[]'
        assert second.result(timeout=5)[0] == '[]'
    assert calls['submit'] == calls['upload'] == 1


def test_mineru_retry_finds_the_successful_key_after_pool_rotation(monkeypatch, tmp_path):
    first, source, first_calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('pending'))
    first._timeout = 8
    clock = [0.0]
    monkeypatch.setattr('time.monotonic', lambda: clock[0])
    monkeypatch.setattr('time.sleep', lambda seconds: clock.__setitem__(0, clock[0] + seconds))
    with pytest.raises(TimeoutError):
        first._fetch_async(source)
    assert first_calls['submit'] == 1

    second, source, second_calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    monkeypatch.setattr(second, '_get_token', lambda: 'other-key')
    monkeypatch.setattr(second, '_resolve_dynamic_token', lambda: ['other-key', 'fixture-token'])
    assert second._fetch_async(source)[0] == '[]'
    assert second_calls == {'submit': 0, 'upload': 0, 'poll': 1, 'download': 1}


def test_mineru_retry_does_not_resume_with_a_different_credential(monkeypatch, tmp_path):
    first, source, _ = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('pending'))
    first._timeout = 8
    clock = [0.0]
    monkeypatch.setattr('time.monotonic', lambda: clock[0])
    monkeypatch.setattr('time.sleep', lambda seconds: clock.__setitem__(0, clock[0] + seconds))
    with pytest.raises(TimeoutError):
        first._fetch_async(source)

    second, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    monkeypatch.setattr(second, '_get_token', lambda: 'different-account-key')
    monkeypatch.setattr(second, '_resolve_dynamic_token', lambda: 'different-account-key')

    def status(method, url, auth_key, **kwargs):
        assert auth_key == 'different-account-key'
        calls['poll'] += 1
        return _status('done')

    monkeypatch.setattr(second, '_request_with_pinned_auth', status)
    assert second._fetch_async(source)[0] == '[]'
    assert calls == {'submit': 1, 'upload': 1, 'poll': 1, 'download': 1}


def test_mineru_retry_does_not_use_a_removed_key_from_cached_pool(monkeypatch, tmp_path):
    first, source, _ = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('pending'))
    first._timeout = 8
    clock = [0.0]
    monkeypatch.setattr('time.monotonic', lambda: clock[0])
    monkeypatch.setattr('time.sleep', lambda seconds: clock.__setitem__(0, clock[0] + seconds))
    with pytest.raises(TimeoutError):
        first._fetch_async(source)

    second, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    monkeypatch.setattr(second, '_get_token', lambda: 'fixture-token')
    monkeypatch.setattr(second, '_resolve_dynamic_token', lambda: 'replacement-key')

    def status(method, url, auth_key, **kwargs):
        assert auth_key == 'replacement-key'
        calls['poll'] += 1
        return _status('done')

    monkeypatch.setattr(second, '_request_with_pinned_auth', status)
    assert second._fetch_async(source)[0] == '[]'
    assert calls == {'submit': 1, 'upload': 1, 'poll': 1, 'download': 1}


def test_mineru_reused_reader_refreshes_changed_dynamic_key_pool(monkeypatch, tmp_path):
    reader, _, _ = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    monkeypatch.delattr(reader, '_get_token')
    configured = [['first-a', 'first-b']]
    headers = []
    monkeypatch.setattr(reader, '_resolve_dynamic_token', lambda: configured[0])

    def request(method, url, **kwargs):
        headers.append(kwargs['headers']['Authorization'])
        return SimpleNamespace(ok=True, status_code=200)

    monkeypatch.setattr(requests, 'request', request)
    reader._request('POST', 'https://mineru.net/api/v4/file-urls/batch')
    first_pool = reader._get_active_pool()
    configured[0] = ['second-a', 'second-b']
    reader._request('POST', 'https://mineru.net/api/v4/file-urls/batch')
    second_pool = reader._get_active_pool()
    assert headers[0] in {'Bearer first-a', 'Bearer first-b'}
    assert headers[1] in {'Bearer second-a', 'Bearer second-b'}
    assert second_pool is not first_pool
    configured[0] = ['only-key']
    assert reader._get_active_pool() is None
    from lazyllm import globals as lazyllm_globals
    state = lazyllm_globals['key_pool_state']
    assert reader._credential_id not in state
    assert first_pool._id not in state
    assert second_pool._id not in state


def test_mineru_parallel_split_batches_poll_with_their_own_submit_keys(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    monkeypatch.delattr(reader, '_submit_with_key')
    monkeypatch.delattr(reader, '_get_token')
    part_a, part_b = tmp_path / 'part-a.pdf', tmp_path / 'part-b.pdf'
    part_a.write_bytes(b'a')
    part_b.write_bytes(b'b')
    monkeypatch.setattr(reader, '_split_large_pdf',
                        lambda _: [(str(part_a), 0), (str(part_b), 1)])
    monkeypatch.setattr(reader, '_resolve_dynamic_token', lambda: ['key-a', 'key-b'])
    post_barrier = Barrier(2)
    old_auth_read_barrier = Barrier(2)
    assigned = {}
    assignment_lock = Lock()
    status_keys = []

    class Pool:
        def ordered_keys(self):
            with assignment_lock:
                return [assigned.setdefault(get_ident(), ('key-a', 'key-b')[len(assigned)])]

        def report_success(self, key):
            self.last_success = key

        def report_failure(self, key):
            raise AssertionError('unexpected auth failure')

    pool = Pool()
    monkeypatch.setattr(reader, '_get_active_pool', lambda: pool)

    def shared_last_success():
        old_auth_read_barrier.wait(5)
        return pool.last_success

    monkeypatch.setattr(reader, '_auth_key_after_successful_request', shared_last_success)

    def receipt(key):
        return _response({'code': 0, 'data': {
            'batch_id': f'batch-{key}',
            'file_urls': ['https://mineru.oss-cn-shanghai.aliyuncs.com/one'],
        }})

    def execute(method, url, **kwargs):
        key = kwargs['headers']['Authorization'].removeprefix('Bearer ')
        post_barrier.wait(5)
        calls['submit'] += 1
        return receipt(key)

    monkeypatch.setattr(reader, '_http_execute', execute)

    def pinned(method, url, auth_key, **kwargs):
        if method == 'POST':
            post_barrier.wait(5)
            calls['submit'] += 1
            return receipt(auth_key)
        assert method == 'GET'
        assert url.endswith(f'/batch-{auth_key}')
        status_keys.append(auth_key)
        calls['poll'] += 1
        return _status('done')

    monkeypatch.setattr(reader, '_request_with_pinned_auth', pinned)
    assert reader._fetch_async(source)[0] == '[]'
    assert set(status_keys) == {'key-a', 'key-b'}
    assert calls == {'submit': 2, 'upload': 2, 'poll': 2, 'download': 2}


def test_mineru_reports_terminal_failure_without_resubmitting(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _response({
        'code': 0, 'data': {'extract_result': [{'state': 'failed', 'err_msg': 'unsupported pdf'}]},
    }))

    with pytest.raises(RuntimeError, match='unsupported pdf'):
        reader._fetch_async(source)
    assert calls['submit'] == calls['upload'] == calls['poll'] == 1


def test_mineru_retains_archive_for_reading_ir(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    reader._fetch_async(source)
    digest = reader._source_digest(str(source))
    cache = source.parent / '.lazymind-artifacts' / 'mineru-cache-v1' / f'{digest}-{reader._online_model_version()}.zip'
    assert cache.is_file()
    assert cache.read_bytes()


def test_reader_initialization_preserves_retained_images_and_other_readers(tmp_path):
    from lazymind.parsing.engine.readers.mineru_online import LazyMindMineruPDFReader
    cache = tmp_path / 'images'
    old = cache / 'existing-document' / 'figure.jpg'
    old.parent.mkdir(parents=True)
    old.write_bytes(b'retained-figure')
    first = LazyMindMineruPDFReader(image_cache_dir=cache, api_key='fixture')
    assert old.read_bytes() == b'retained-figure'
    active = first._image_cache_dir / 'active-document' / 'figure.jpg'
    active.parent.mkdir(parents=True)
    active.write_bytes(b'active-figure')
    LazyMindMineruPDFReader(image_cache_dir=cache, api_key='fixture')
    assert old.read_bytes() == b'retained-figure'
    assert active.read_bytes() == b'active-figure'


def _shared_fixture(reader, source, content=None):
    import io
    import zipfile
    data = io.BytesIO()
    with zipfile.ZipFile(data, 'w') as archive:
        archive.writestr('paper_content_list.json', content or '[{"type":"text","text":"cached","page_idx":0}]')
    cache = (source.parent / '.lazymind-artifacts' / 'mineru-cache-v1'
             / f'{reader._source_digest(source)}-{reader._online_model_version()}.zip')
    cache.parent.mkdir(parents=True, exist_ok=True)
    cache.write_bytes(data.getvalue())
    return cache


def test_shared_archive_reused_before_split_or_remote_request(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    _shared_fixture(reader, source)
    monkeypatch.setattr(reader, '_split_large_pdf', lambda _: pytest.fail('cached original must bypass splitting'))
    assert reader._fetch_async(source)[0] == '[]'
    assert calls == {'submit': 0, 'upload': 0, 'poll': 0, 'download': 0}


@pytest.mark.parametrize('content', [b'broken', b'[]'])
def test_invalid_shared_archive_falls_back_to_remote(monkeypatch, tmp_path, content):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    _shared_fixture(reader, source).write_bytes(content)
    assert reader._fetch_async(source)[0] == '[]'
    assert calls['upload'] == 1


def test_shared_cache_bypass_forces_fresh_submission(monkeypatch, tmp_path):
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    reader._fetch_async(source)
    _shared_fixture(reader, source)
    reader._fetch_async(source, use_cache=False)
    assert calls['upload'] == 2


@pytest.mark.parametrize('split', [False, True])
def test_shared_cache_rechecked_after_waiting_for_flock(monkeypatch, tmp_path, split):
    import subprocess
    import sys
    import time
    if sys.platform == 'win32':
        pytest.skip('Unix flock interoperability')
    real_sleep = time.sleep
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    monkeypatch.setattr(time, 'sleep', real_sleep)
    if split:
        parts = [tmp_path / 'part-a.pdf', tmp_path / 'part-b.pdf']
        for index, part in enumerate(parts):
            part.write_bytes(f'part-{index}'.encode())
        monkeypatch.setattr(reader, '_split_large_pdf', lambda _: [(str(part), i) for i, part in enumerate(parts)])
    cache = _shared_fixture(reader, source)
    raw = cache.read_bytes()
    cache.unlink()
    lock = str(cache) + '.lock'
    script = """import fcntl, pathlib, sys
with open(sys.argv[1], 'w') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    print('locked', flush=True)
    sys.stdin.buffer.read(1)
    pathlib.Path(sys.argv[2]).write_bytes(bytes.fromhex(sys.argv[3]))
"""
    with subprocess.Popen([sys.executable, '-c', script, lock, str(cache), raw.hex()],
                          stdin=subprocess.PIPE, stdout=subprocess.PIPE) as process:
        assert process.stdout.readline() == b'locked\n'
        with ThreadPoolExecutor(max_workers=1) as executor:
            waiting = executor.submit(reader._fetch_async, str(source))
            real_sleep(0.1)
            assert not waiting.done()
            process.stdin.write(b'x')
            process.stdin.flush()
            assert waiting.result(timeout=5)[0] == '[]'
        assert process.wait(timeout=5) == 0
    assert calls['upload'] == calls['poll'] == 0


def test_shared_archive_real_extraction_retains_images(monkeypatch, tmp_path):
    import zipfile
    from lazymind.parsing.engine.readers.mineru_online import LazyMindMineruPDFReader
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    cache = _shared_fixture(reader, source)
    with zipfile.ZipFile(cache, 'a') as zipped:
        zipped.writestr('images/figure.png', b'fixture-image')
    monkeypatch.setattr(reader, '_extract_content_from_zip',
                        LazyMindMineruPDFReader._extract_content_from_zip.__get__(reader))
    content, extracted = reader._fetch_async(source)
    assert 'cached' in content
    assert (extracted / 'images/figure.png').read_bytes() == b'fixture-image'
    assert calls['submit'] == 0


def test_shared_lock_wait_times_out_without_upload(monkeypatch, tmp_path):
    import time
    from filelock import FileLock
    real_sleep = time.sleep
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    monkeypatch.setattr(time, 'sleep', real_sleep)
    reader._timeout = 0.05
    cache = _shared_fixture(reader, source)
    cache.unlink()
    with FileLock(str(cache) + '.lock'):
        with pytest.raises(TimeoutError, match='waiting to submit'):
            reader._fetch_async(source)
    assert calls['submit'] == 0


def test_shared_archive_malformed_layout_falls_back(monkeypatch, tmp_path):
    import zipfile
    from lazymind.parsing.engine.readers.mineru_online import LazyMindMineruPDFReader
    reader, source, calls = _reader_with_batch(monkeypatch, tmp_path, lambda _: _status('done'))
    cache = _shared_fixture(reader, source)
    with zipfile.ZipFile(cache, 'a') as zipped:
        zipped.writestr('layout.json', '[]')
    real_extract = LazyMindMineruPDFReader._extract_content_from_zip.__get__(reader)
    monkeypatch.setattr(reader, '_extract_content_from_zip',
                        lambda data, task_dir=None: ('[]', task_dir) if data == b'zip'
                        else real_extract(data, task_dir=task_dir))
    assert reader._fetch_async(source)[0] == '[]'
    assert calls['upload'] == 1
