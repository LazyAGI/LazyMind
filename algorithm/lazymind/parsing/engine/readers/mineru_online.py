"""Project-owned MinerU reader with bounded, single-batch result polling.

The upstream reader retries the entire submit/upload flow after a polling
timeout or a transient result-download failure. One slow PDF can therefore
create several remote jobs. Keep the batch identity after a successful upload
and retry only the request that failed.
"""

import hashlib
import json
import os
import re
import tempfile
import time
import uuid
import zipfile
from concurrent.futures import as_completed
from pathlib import Path
from urllib.parse import urlsplit

import requests
from filelock import FileLock, Timeout as FileLockTimeout

from lazyllm import LOG, globals as lazyllm_globals
from lazyllm.common import AllKeysExhaustedError, KeyAuthError, KeyPool
from lazyllm.common.threading import ThreadPoolExecutor
from lazyllm.tools.rag.readers.ocrReader.dynamic_pdf_reader import DynamicPDFReader as _DynamicPDFReader
from lazyllm.tools.rag.readers.ocrReader.mineru_pdf_reader import MineruPDFReader as _MineruPDFReader


_PENDING_STATES = frozenset({'waiting-file', 'uploading', 'pending', 'running', 'converting'})
_DEFAULT_WAIT_SECONDS = 3600
_POLL_INTERVAL_SECONDS = 3
_BATCH_MARKER_TTL_SECONDS = 24 * 3600


def _safe_message(value) -> str:
    return re.sub(r'https?://\S+', '<url>', str(value or '').strip())[:200]


def _retryable_transport(error: requests.RequestException) -> bool:
    if isinstance(error, (requests.Timeout, requests.ConnectionError)):
        return True
    if isinstance(error, requests.HTTPError):
        status = getattr(error.response, 'status_code', None)
        return status == 429 or isinstance(status, int) and status >= 500
    return False


class LazyMindMineruPDFReader(_MineruPDFReader):
    """Keep an uploaded batch while waiting for the official MinerU API."""

    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        # LazyLLM derives the dynamic auth lookup from the concrete class
        # name. Preserve the configured `ocr_auth.mineru` contract.
        self._auth_source_key = 'mineru'

    def _get_active_pool(self):
        if not self._dynamic_auth:
            return super()._get_active_pool()
        configured = self._resolve_dynamic_token()
        if not isinstance(configured, (list, tuple)) or len(configured) < 2:
            with self._token_lock:
                pool_state = lazyllm_globals['key_pool_state']
                old_pool = pool_state.pop(self._credential_id, None)
                if old_pool is not None:
                    pool_state.pop(old_pool._id, None)
            return None
        # The upstream cache uses only the reader ID and can keep removed
        # credentials when request-scoped OCR configuration changes.
        keys = tuple(configured)
        with self._token_lock:
            pool_state = lazyllm_globals['key_pool_state']
            pool = pool_state.get(self._credential_id)
            if pool is None or tuple(pool._keys) != keys:
                if pool is not None:
                    pool_state.pop(pool._id, None)
                pool = KeyPool(list(keys), self._dynamic_key_policy)
                pool_state[self._credential_id] = pool
            return pool

    @staticmethod
    def _api_data(response, stage: str) -> dict:
        try:
            payload = response.json()
        except ValueError:
            raise RuntimeError(f'[MineruPDFReader] malformed {stage} response') from None
        if not isinstance(payload, dict):
            raise RuntimeError(f'[MineruPDFReader] malformed {stage} response')
        code = payload.get('code')
        if code != 0:
            raise RuntimeError(f'[MineruPDFReader] {stage} rejected (code={code}): '
                               f'{_safe_message(payload.get("msg"))}')
        data = payload.get('data')
        if not isinstance(data, dict):
            raise RuntimeError(f'[MineruPDFReader] malformed {stage} data')
        return data

    @staticmethod
    def _trusted_artifact_url(url) -> bool:
        if not isinstance(url, str):
            return False
        try:
            parsed = urlsplit(url)
            host = parsed.hostname
            if (parsed.scheme != 'https' or not host or parsed.username is not None
                    or parsed.password is not None or parsed.port is not None):
                return False
        except ValueError:
            return False
        return (host == 'mineru.net' or host.endswith('.mineru.net')
                or host in {'mineru.oss-cn-shanghai.aliyuncs.com',
                            'cdn-mineru.openxlab.org.cn'})

    @staticmethod
    def _check_artifact_response(response, stage: str) -> None:
        status = response.status_code
        if 300 <= status < 400:
            raise RuntimeError(f'[MineruPDFReader] {stage} redirect rejected (HTTP {status})')
        response.raise_for_status()
        if not 200 <= status < 300:
            raise RuntimeError(f'[MineruPDFReader] {stage} failed (HTTP {status})')

    @staticmethod
    def _request_before_upload(call, stage: str):
        # Retry submission or PUT only at its own stage. Once PUT succeeds,
        # polling and archive retries never repeat either request.
        for attempt in range(3):
            try:
                return call()
            except requests.RequestException as error:
                if not _retryable_transport(error) or attempt == 2:
                    status = getattr(getattr(error, 'response', None), 'status_code', None)
                    raise RuntimeError(f'[MineruPDFReader] {stage} failed '
                                       f'({status or type(error).__name__})') from None
                LOG.warning(f'[MineruPDFReader] {stage} transient failure '
                            f'({type(error).__name__}); retrying')
                time.sleep(2 ** (attempt + 1))

    def _submit_with_key(self, method: str, url: str, **kwargs):
        # Bind the receipt to the key used by this POST. Shared KeyPool state
        # can be overwritten by another split PDF submission before we read it.
        pool = self._get_active_pool()
        keys = pool.ordered_keys() if pool is not None else [self._get_token()]
        last_auth_error = None
        for key in keys:
            try:
                response = self._request_with_pinned_auth(method, url, key, **kwargs)
            except KeyAuthError as error:
                if pool is None:
                    raise
                pool.report_failure(key)
                last_auth_error = error
                continue
            if pool is not None:
                pool.report_success(key)
            return response, key
        raise AllKeysExhaustedError(f'{type(self).__name__}: all keys exhausted') from last_auth_error

    @staticmethod
    def _sleep_until_next_poll(deadline: float) -> None:
        remaining = deadline - time.monotonic()
        if remaining > 0:
            time.sleep(min(_POLL_INTERVAL_SECONDS, remaining))

    @staticmethod
    def _remaining_timeout(deadline: float, cap: float) -> float:
        return max(0.001, min(cap, deadline - time.monotonic()))

    @staticmethod
    def _source_digest(file_path: str) -> str:
        digest = hashlib.sha256()
        with open(file_path, 'rb') as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b''):
                digest.update(chunk)
        return digest.hexdigest()

    def _batch_marker_path(self, file_path: str, source_digest: str,
                           auth_key: str, model_version: str) -> Path:
        identity = '\0'.join((str(Path(file_path).resolve()), source_digest, model_version,
                              hashlib.sha256(auth_key.encode()).hexdigest()))
        name = hashlib.sha256(identity.encode()).hexdigest() + '.json'
        # The upstream reader clears image_cache_dir on construction. Use its
        # sibling so a document task can resume after a worker restart.
        return self._image_cache_dir.parent / '.mineru_batches' / name

    def _batch_lock_path(self, file_path: str, source_digest: str, model_version: str) -> Path:
        identity = '\0'.join((str(Path(file_path).resolve()), source_digest, model_version))
        name = hashlib.sha256(identity.encode()).hexdigest() + '.lock'
        return self._image_cache_dir.parent / '.mineru_batches' / name

    def _candidate_auth_keys(self) -> list[str]:
        # A new reader can choose another key from the same configured pool.
        # Dynamic pools may cache removed keys, so read the current config
        # directly and poll with the original key only if it is still present.
        if self._dynamic_auth:
            configured = self._resolve_dynamic_token()
            candidates = configured if isinstance(configured, (list, tuple)) else [configured]
        else:
            candidates = [self._get_token()]
            pool = self._get_active_pool()
            if pool is not None:
                candidates.extend(pool._keys)
        return list(dict.fromkeys(key for key in candidates if isinstance(key, str) and key))

    @staticmethod
    def _load_batch_marker(path: Path) -> str:
        try:
            marker = json.loads(path.read_text())
            batch_id = marker.get('batch_id')
            age = time.time() - float(marker.get('saved_at'))
            if (isinstance(batch_id, str) and re.fullmatch(r'[A-Za-z0-9_-]{1,128}', batch_id)
                    and 0 <= age < _BATCH_MARKER_TTL_SECONDS):
                return batch_id
        except (OSError, ValueError, TypeError, AttributeError):
            pass
        return ''

    @staticmethod
    def _save_batch_marker(path: Path, batch_id: str) -> None:
        temporary = None
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            fd, temporary = tempfile.mkstemp(prefix='.mineru-', dir=path.parent)
            with os.fdopen(fd, 'w') as output:
                json.dump({'batch_id': batch_id, 'saved_at': time.time()}, output)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, path)
        except OSError as error:
            LOG.warning(f'[MineruPDFReader] could not save batch resume marker '
                        f'({type(error).__name__})')
        finally:
            if temporary and os.path.exists(temporary):
                try:
                    os.unlink(temporary)
                except OSError:
                    pass

    @staticmethod
    def _clear_batch_marker(path: Path | None, batch_id: str) -> None:
        if path is None or LazyMindMineruPDFReader._load_batch_marker(path) != batch_id:
            return
        try:
            path.unlink()
        except OSError:
            pass

    def _fetch_async(self, file, use_cache: bool = True):
        file_str = str(file)
        splits = self._split_large_pdf(file_str)
        task_dir = self._image_cache_dir / str(uuid.uuid4())
        if len(splits) == 1:
            return self._fetch_async_by_upload(splits[0][0], task_dir=task_dir)

        results = {}
        with ThreadPoolExecutor(max_workers=min(len(splits), 5)) as executor:
            futures = {
                executor.submit(self._fetch_async_by_upload, sub_path, task_dir=task_dir): start_page
                for sub_path, start_page in splits
            }
            for future in as_completed(futures):
                results[futures[future]] = future.result()
        return self._merge_split_results(results)

    def _fetch_async_by_upload(self, file_path: str, task_dir: Path | None = None):
        fname = os.path.basename(file_path)
        model_version = self._online_model_version()
        source_digest = self._source_digest(file_path)
        cache_path = Path(file_path).parent / '.lazymind-artifacts' / 'mineru-cache-v1' / f'{source_digest}-{model_version}.zip'
        lock_path = self._batch_lock_path(file_path, source_digest, model_version)
        lock_path.parent.mkdir(parents=True, exist_ok=True)
        try:
            with FileLock(str(lock_path), timeout=self._timeout or _DEFAULT_WAIT_SECONDS):
                for marker_key in self._candidate_auth_keys():
                    marker_path = self._batch_marker_path(
                        file_path, source_digest, marker_key, model_version)
                    resumed_batch = self._load_batch_marker(marker_path)
                    if resumed_batch:
                        LOG.info(f'[MineruPDFReader] resuming batch {resumed_batch} for {fname}')
                        batch_id, auth_key = resumed_batch, marker_key
                        break
                else:
                    batch_id, auth_key, marker_path = self._submit_and_upload(
                        file_path, fname, source_digest, model_version)
        except FileLockTimeout:
            raise TimeoutError(f'[MineruPDFReader] waiting to submit {fname} exceeded '
                               f'{self._timeout or _DEFAULT_WAIT_SECONDS}s') from None
        # Release the cross-process lock before potentially hour-long polling.
        return self._wait_for_batch(batch_id, auth_key, task_dir, fname, marker_path, cache_path=cache_path)

    def _submit_and_upload(self, file_path: str, fname: str,
                           source_digest: str, model_version: str):
        payload = {'files': [{'name': fname}], 'model_version': model_version}
        submit, auth_key = self._request_before_upload(
            lambda: self._submit_with_key('POST', 'https://mineru.net/api/v4/file-urls/batch',
                                          json=payload, headers={'Content-Type': 'application/json'},
                                          timeout=min(self._timeout or 30, 30)),
            'batch submission',
        )
        submission = self._api_data(submit, 'batch submission')
        batch_id = str(submission.get('batch_id') or '')
        urls = submission.get('file_urls')
        if not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', batch_id) or not isinstance(urls, list) or not urls:
            raise RuntimeError('[MineruPDFReader] malformed batch submission receipt')
        upload_url = urls[0]
        if not self._trusted_artifact_url(upload_url):
            raise RuntimeError('[MineruPDFReader] invalid batch upload target')
        uploaded_at = time.time()

        def upload_once():
            with open(file_path, 'rb') as source:
                result = requests.put(upload_url, data=source,
                                      timeout=min(self._timeout or 300, 300),
                                      allow_redirects=False,
                                      **self._online_request_kwargs())
                self._check_artifact_response(result, 'batch upload')
                return result

        self._request_before_upload(upload_once, 'batch upload')
        LOG.info(f'[BENCHMARK] file={fname} phase=upload elapsed={time.time() - uploaded_at:.3f}s')
        marker_path = (self._batch_marker_path(file_path, source_digest, auth_key, model_version)
                       if auth_key else None)
        if marker_path is not None:
            self._save_batch_marker(marker_path, batch_id)
        return batch_id, auth_key, marker_path

    def _wait_for_batch(self, batch_id: str, auth_key: str, task_dir: Path | None, fname: str,
                        marker_path: Path | None = None, cache_path: Path | None = None):
        wait_started = time.monotonic()
        deadline = wait_started + (self._timeout or _DEFAULT_WAIT_SECONDS)
        status_url = f'https://mineru.net/api/v4/extract-results/batch/{batch_id}'
        last_state = 'no-result'
        transient_failures = 0
        while time.monotonic() < deadline:
            try:
                result = self._request_with_pinned_auth(
                    'GET', status_url, auth_key,
                    timeout=self._remaining_timeout(deadline, 30),
                )
            except requests.RequestException as error:
                if not _retryable_transport(error):
                    status = getattr(getattr(error, 'response', None), 'status_code', None)
                    raise RuntimeError(f'[MineruPDFReader] batch {batch_id} status request failed '
                                       f'({status or type(error).__name__})') from None
                transient_failures += 1
                if transient_failures == 1 or transient_failures % 10 == 0:
                    LOG.warning(f'[MineruPDFReader] batch {batch_id} status request transient failure '
                                f'({type(error).__name__}); still waiting')
                self._sleep_until_next_poll(deadline)
                continue

            data = self._api_data(result, f'batch {batch_id} status')
            entries = data.get('extract_result') or []
            if not isinstance(entries, list):
                raise RuntimeError(f'[MineruPDFReader] malformed batch {batch_id} results')
            if entries:
                entry = entries[0]
                if not isinstance(entry, dict):
                    raise RuntimeError(f'[MineruPDFReader] malformed batch {batch_id} result item')
                last_state = str(entry.get('state') or 'unknown')
                if last_state == 'done':
                    result_url = entry.get('full_zip_url')
                    if not self._trusted_artifact_url(result_url):
                        raise RuntimeError(f'[MineruPDFReader] batch {batch_id} has no result archive')
                    archive = self._download_result(result_url, batch_id, deadline)
                    LOG.info(f'[BENCHMARK] file={fname} phase=wait '
                             f'elapsed={time.monotonic() - wait_started:.3f}s')
                    try:
                        extracted = self._extract_content_from_zip(archive, task_dir=task_dir)
                    except (zipfile.BadZipFile, ValueError, TypeError, KeyError):
                        self._clear_batch_marker(marker_path, batch_id)
                        raise
                    # Keep the completed receipt for the marker TTL. Another
                    # split part or concurrent parse may still need this batch.
                    if cache_path is not None and len(archive) <= 256 * 1024 * 1024:
                        try:
                            cache_path.parent.mkdir(parents=True, exist_ok=True)
                            with tempfile.NamedTemporaryFile(dir=cache_path.parent, delete=False) as cached:
                                cached.write(archive)
                            os.replace(cached.name, cache_path)
                        except OSError:
                            LOG.warning('[MineruPDFReader] could not retain shared parsing archive')
                    return extracted
                if last_state == 'failed':
                    self._clear_batch_marker(marker_path, batch_id)
                    raise RuntimeError(f'[MineruPDFReader] batch {batch_id} failed: '
                                       f'{_safe_message(entry.get("err_msg")) or "unknown error"}')
                if last_state not in _PENDING_STATES:
                    raise RuntimeError(f'[MineruPDFReader] batch {batch_id} returned unknown state '
                                       f'{_safe_message(last_state)}')
            self._sleep_until_next_poll(deadline)
        elapsed = time.monotonic() - wait_started
        raise TimeoutError(f'[MineruPDFReader] batch {batch_id} remained {last_state} '
                           f'after {elapsed:.0f}s of result polling')

    def _download_result(self, result_url: str, batch_id: str, deadline: float) -> bytes:
        while time.monotonic() < deadline:
            try:
                response = requests.get(result_url, timeout=self._remaining_timeout(deadline, 120),
                                        allow_redirects=False,
                                        **self._online_request_kwargs())
                self._check_artifact_response(response, 'archive download')
                return response.content
            except requests.RequestException as error:
                if not _retryable_transport(error):
                    status = getattr(getattr(error, 'response', None), 'status_code', None)
                    raise RuntimeError(f'[MineruPDFReader] batch {batch_id} archive download failed '
                                       f'({status or type(error).__name__})') from None
                LOG.warning(f'[MineruPDFReader] batch {batch_id} archive download transient failure '
                            f'({type(error).__name__}); retrying')
                self._sleep_until_next_poll(deadline)
        raise TimeoutError(f'[MineruPDFReader] batch {batch_id} archive download exceeded wait budget')


class LazyMindDynamicPDFReader(_DynamicPDFReader):
    """Use the bounded MinerU reader for LazyMind knowledge-base imports."""

    def _build_reader(self, reader_type: str, ocr_url: str):
        if reader_type != 'mineru':
            return super()._build_reader(reader_type, ocr_url)
        kwargs = {'url': ocr_url, 'dynamic_auth': True,
                  'timeout': self._timeout, 'post_func': self._post_func}
        if self._image_cache_dir:
            kwargs['image_cache_dir'] = self._image_cache_dir
        return LazyMindMineruPDFReader(**kwargs)
