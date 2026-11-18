"""Project-owned MinerU reader with bounded, single-batch result polling.

The upstream reader retries the entire submit/upload flow after a polling
timeout or a transient result-download failure. One slow PDF can therefore
create several remote jobs. Keep the batch identity after a successful upload
and retry only the request that failed.
"""

import hashlib
import io
import json
import ntpath
import os
import re
import tempfile
import time
import uuid
import zipfile
from concurrent.futures import as_completed
from pathlib import Path, PurePosixPath
from urllib.parse import urlsplit

import requests
from filelock import FileLock, Timeout as FileLockTimeout

from lazyllm import LOG, config as lazyllm_config, globals as lazyllm_globals
from lazyllm.common import AllKeysExhaustedError, KeyAuthError, KeyPool
from lazyllm.common.threading import ThreadPoolExecutor
from lazyllm.tools.rag.readers.ocrReader.dynamic_pdf_reader import DynamicPDFReader as _DynamicPDFReader
from lazyllm.tools.rag.readers.ocrReader.mineru_pdf_reader import MineruPDFReader as _MineruPDFReader
from lazymind.common.mineru_transport import official_api_request
from lazymind.config import config as project_config


_PENDING_STATES = frozenset({'waiting-file', 'uploading', 'pending', 'running', 'converting'})
_DEFAULT_WAIT_SECONDS = 3600
_POLL_INTERVAL_SECONDS = 3
_BATCH_MARKER_TTL_SECONDS = 24 * 3600


def _safe_message(value) -> str:
    return re.sub(r'https?://\S+', '<url>', str(value or '').strip())[:200]


def _retryable_transport(error: requests.RequestException) -> bool:
    if isinstance(error, requests.exceptions.SSLError):
        return False
    if isinstance(error, (requests.Timeout, requests.ConnectionError)):
        return True
    if isinstance(error, requests.HTTPError):
        status = getattr(error.response, 'status_code', None)
        return status == 429 or isinstance(status, int) and status >= 500
    return False


class LazyMindMineruPDFReader(_MineruPDFReader):
    """Keep an uploaded batch while waiting for the official MinerU API."""

    def __init__(self, **kwargs):
        cache_dir = kwargs.pop('image_cache_dir', None) or Path(lazyllm_config['home']) / 'mineru_cache'
        # The upstream base constructor clears this directory. It contains image
        # paths retained by earlier documents and may be in use by other workers.
        # Bypass that cleanup; each parse already extracts into a unique task dir.
        super().__init__(image_cache_dir=None, **kwargs)
        self._image_cache_dir = Path(cache_dir)
        self._image_cache_dir.mkdir(parents=True, exist_ok=True)
        # LazyLLM derives the dynamic auth lookup from the concrete class
        # name. Preserve the configured `ocr_auth.mineru` contract.
        self._auth_source_key = 'mineru'

    def _online_model_version(self) -> str:
        return 'vlm'

    @property
    def appendix_hash_key(self):
        return super().appendix_hash_key + '|shared-vlm-v1'

    def _load_data(self, file, extra_info=None, use_cache=True, **kwargs):
        if self._offline_mode:
            return super()._load_data(file, extra_info, use_cache, **kwargs)
        # Parse the exact original bytes read by Go; in-place normalization
        # changes the content identity and places coordinates on different pages.
        path = Path(file)
        response, task_dir = self._fetch_async(path, use_cache)
        info = dict(extra_info or {})
        if task_dir is not None:
            info['image_cache_dir'] = str(task_dir)
        return self._build_nodes_from_response(response, path, info)

    def _http_execute(self, method: str, url: str, **kwargs):
        return official_api_request(super()._http_execute, method, url, **kwargs)

    def _online_request_kwargs(self):
        # Signed storage URLs must also validate their own host and trust chain.
        return {'verify': True}

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

    @staticmethod
    def _archive_cache_path(file_path: str, source_digest: str, model_version: str,
                            legacy: bool = False) -> Path:
        directory = Path(file_path).parent
        root = Path(project_config['shared_upload_dir'])
        if not legacy and directory.is_relative_to(root):
            directory = root
        path = directory / '.lazymind-artifacts' / 'mineru-cache-v1' / f'{source_digest}-{model_version}.zip'
        if os.name == 'nt':
            # The shared sidecar exceeds MAX_PATH even for ordinary desktop
            # uploads. Use Win32's extended namespace without relocating it:
            # Go and Python must still lock and publish the same cache file.
            absolute = ntpath.abspath(str(path))
            if not absolute.startswith('\\\\?\\'):
                absolute = ('\\\\?\\UNC\\' + absolute[2:] if absolute.startswith('\\\\')
                            else '\\\\?\\' + absolute)
            return Path(absolute)
        return path

    def _batch_lock_path(self, file_path: str, source_digest: str, model_version: str) -> Path:
        # Go uses flock on this exact persistent sidecar; never unlink a lock file.
        return Path(str(self._archive_cache_path(file_path, source_digest, model_version)) + '.lock')

    @staticmethod
    def _archive_content(archive: bytes, model_version: str) -> list:
        if len(archive) > 256 * 1024 * 1024:
            raise ValueError('MinerU archive exceeds size limit')
        with zipfile.ZipFile(io.BytesIO(archive)) as zipped:
            members = zipped.infolist()
            if any(PurePosixPath(m.filename).is_absolute() or '..' in PurePosixPath(m.filename).parts
                   or '\\' in m.filename for m in members):
                raise ValueError('unsafe MinerU archive member')
            lists = [m for m in members if m.filename.endswith('_content_list.json')]
            if len(lists) != 1 or lists[0].file_size > 32 * 1024 * 1024:
                raise ValueError('invalid MinerU content list')
            if sum(m.file_size for m in members) > 256 * 1024 * 1024:
                raise ValueError('MinerU archive exceeds extraction limit')
            content = json.loads(zipped.read(lists[0]))
            if not isinstance(content, list) or not content:
                raise ValueError('MinerU result contains no structured blocks')
            for item in content:
                if (not isinstance(item, dict) or not isinstance(item.get('type'), str)
                        or not item['type'].strip() or type(item.get('page_idx')) is not int
                        or not 0 <= item['page_idx'] <= 100000):
                    raise ValueError('invalid MinerU structure')
                if model_version == 'pipeline':
                    bbox = item.get('bbox')
                    if (not isinstance(bbox, list) or len(bbox) != 4
                            or not all(type(v) in (int, float) and 0 <= v <= 1000 for v in bbox)
                            or not (bbox[0] < bbox[2] and bbox[1] < bbox[3])):
                        raise ValueError('MinerU pipeline result lacks page geometry')
            return content

    @staticmethod
    def _save_archive(cache_path: Path, archive: bytes):
        cache_path.parent.mkdir(parents=True, exist_ok=True)
        fd, temp_path = tempfile.mkstemp(prefix='.archive-', dir=cache_path.parent)
        try:
            with os.fdopen(fd, 'wb') as output:
                output.write(archive)
            os.replace(temp_path, cache_path)
        finally:
            if os.path.exists(temp_path):
                os.unlink(temp_path)

    def _read_shared_archive(self, file_path: str, digest: str, task_dir: Path, *, promote=False):
        for model in ('vlm', 'pipeline'):
            target = self._archive_cache_path(file_path, digest, model)
            legacy = self._archive_cache_path(file_path, digest, model, legacy=True)
            for candidate in dict.fromkeys((target, legacy)):
                try:
                    with candidate.open('rb') as cached:
                        archive = cached.read(256 * 1024 * 1024 + 1)
                    self._archive_content(archive, model)
                    extracted = self._extract_content_from_zip(archive, task_dir=task_dir)
                except (OSError, zipfile.BadZipFile, ValueError, TypeError, KeyError,
                        AttributeError, IndexError, RuntimeError):
                    continue
                if promote and candidate == legacy and target != legacy:
                    try:
                        self._save_archive(target, archive)
                    except OSError:
                        LOG.warning('[MineruPDFReader] could not promote shared parsing archive')
                return extracted
        return None

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
        task_dir = self._image_cache_dir / str(uuid.uuid4())
        digest = self._source_digest(file_str)
        model = self._online_model_version()
        lock_path = self._batch_lock_path(file_str, digest, model)
        lock_path.parent.mkdir(parents=True, exist_ok=True)
        try:
            with FileLock(str(lock_path), timeout=self._timeout or _DEFAULT_WAIT_SECONDS):
                if use_cache:
                    cached = self._read_shared_archive(file_str, digest, task_dir, promote=True)
                    if cached is not None:
                        return cached
                splits = self._split_large_pdf(file_str)
                if len(splits) == 1:
                    return self._fetch_locked(splits[0][0], digest, task_dir, use_cache)

                # Part lock keys have distinct source hashes. Retain successful
                # parts for retry but publish the original only after every part.
                with ThreadPoolExecutor(max_workers=min(len(splits), 5)) as executor:
                    futures = {
                        executor.submit(self._fetch_async_by_upload, sub_path,
                                        task_dir=task_dir / str(start_page), use_cache=use_cache): start_page
                        for sub_path, start_page in splits
                    }
                    results = {futures[f]: f.result() for f in as_completed(futures)}
                return self._publish_split_archive(file_str, digest, task_dir, results)
        except FileLockTimeout:
            raise TimeoutError(f'[MineruPDFReader] waiting to submit {Path(file_str).name} exceeded '
                               f'{self._timeout or _DEFAULT_WAIT_SECONDS}s') from None

    def _fetch_async_by_upload(self, file_path: str, task_dir: Path | None = None, use_cache: bool = True):
        source_digest = self._source_digest(file_path)
        lock_path = self._batch_lock_path(file_path, source_digest, self._online_model_version())
        lock_path.parent.mkdir(parents=True, exist_ok=True)
        try:
            with FileLock(str(lock_path), timeout=self._timeout or _DEFAULT_WAIT_SECONDS):
                if use_cache:
                    # A split VLM parse cannot mix legacy and VLM parts and
                    # mislabel the provenance of the aggregate.
                    path = self._archive_cache_path(file_path, source_digest, 'vlm')
                    try:
                        with path.open('rb') as cached:
                            raw = cached.read(256 * 1024 * 1024 + 1)
                        self._archive_content(raw, 'vlm')
                        return self._extract_content_from_zip(raw, task_dir=task_dir)
                    except (OSError, zipfile.BadZipFile, ValueError, TypeError, KeyError, AttributeError):
                        pass
                return self._fetch_locked(file_path, source_digest, task_dir, use_cache)
        except FileLockTimeout:
            raise TimeoutError(f'[MineruPDFReader] waiting to submit {Path(file_path).name} exceeded '
                               f'{self._timeout or _DEFAULT_WAIT_SECONDS}s') from None

    def _fetch_locked(self, file_path: str, digest: str, task_dir: Path, use_cache: bool):
        fname = os.path.basename(file_path)
        model = self._online_model_version()
        cache_path = self._archive_cache_path(file_path, digest, model)
        for marker_key in self._candidate_auth_keys() if use_cache else []:
            marker_path = self._batch_marker_path(file_path, digest, marker_key, model)
            resumed_batch = self._load_batch_marker(marker_path)
            if resumed_batch:
                LOG.info(f'[MineruPDFReader] resuming batch {resumed_batch} for {fname}')
                batch_id, auth_key = resumed_batch, marker_key
                break
        else:
            batch_id, auth_key, marker_path = self._submit_and_upload(file_path, fname, digest, model)
        return self._wait_for_batch(batch_id, auth_key, task_dir, fname, marker_path, cache_path=cache_path)

    def _publish_split_archive(self, file_path, digest, task_dir, results):
        content = []
        archive = io.BytesIO()
        model = self._online_model_version()
        with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as output:
            for start_page, (response, directory) in sorted(results.items()):
                items = json.loads(response)
                for item in items:
                    item['page_idx'] += start_page
                    for key in ('img_path', 'image_path'):
                        if isinstance(item.get(key), str) and item[key]:
                            item[key] = f'{start_page}/{item[key]}'
                content.extend(items)
                for asset in directory.rglob('*'):
                    relative = asset.relative_to(directory)
                    if asset.is_file() and 'images' in relative.parts:
                        output.write(asset, f'{start_page}/{relative.as_posix()}')
            output.writestr('paper_content_list.json', json.dumps(content))
        if content:
            raw = archive.getvalue()
            self._archive_content(raw, model)
            self._save_archive(self._archive_cache_path(file_path, digest, model), raw)
        return json.dumps(content), task_dir

    def _submit_and_upload(self, file_path: str, fname: str,
                           source_digest: str, model_version: str):
        payload = {'files': [{'name': fname}], 'model_version': model_version, 'enable_formula': True}
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
                        self._archive_content(archive, self._online_model_version())
                        extracted = self._extract_content_from_zip(archive, task_dir=task_dir)
                    except (zipfile.BadZipFile, ValueError, TypeError, KeyError):
                        self._clear_batch_marker(marker_path, batch_id)
                        raise
                    if cache_path is not None:
                        try:
                            self._save_archive(cache_path, archive)
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

    @property
    def appendix_hash_key(self):
        return super().appendix_hash_key + '|shared-vlm-v1'

    def _load_data(self, file, extra_info=None, **kwargs):
        ocr_type, url = self._resolve_route(extra_info)
        reader_type, url = self._reader_cache_key(ocr_type, url, file)
        if reader_type == 'mineru':
            reader = self._get_reader(reader_type, url)
            if not reader._offline_mode:
                return reader.forward(file, extra_info=extra_info, **kwargs)
        return super()._load_data(file, extra_info=extra_info, **kwargs)

    def _build_reader(self, reader_type: str, ocr_url: str):
        if reader_type != 'mineru':
            return super()._build_reader(reader_type, ocr_url)
        kwargs = {'url': ocr_url, 'dynamic_auth': True,
                  'timeout': self._timeout, 'post_func': self._post_func}
        if self._image_cache_dir:
            kwargs['image_cache_dir'] = self._image_cache_dir
        return LazyMindMineruPDFReader(**kwargs)
