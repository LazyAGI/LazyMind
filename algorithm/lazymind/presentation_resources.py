"""Versioned PPT libraries hosted on ModelScope and Hugging Face.

Only disposable runtime caches are local. Downloads are single-flight, bounded,
checksummed and published atomically; a failed mirror never exposes partial data.
"""
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import logging
import os
from pathlib import Path, PurePosixPath
import shutil
import stat
import tempfile
import threading

import httpx
from filelock import FileLock

LOG = logging.getLogger(__name__)
RELEASES = json.loads(Path(__file__).with_name('presentation_resources.json').read_text())
MIRRORS = (
    'https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/',
    'https://huggingface.co/datasets/LazyAGI/LazyMind/resolve/main/',
)
_EXECUTOR = ThreadPoolExecutor(max_workers=2, thread_name_prefix='ppt-resources')
_GUARD = threading.Lock()
_JOBS = {}


def cache_root():
    return Path(os.environ.get('LAZYMIND_PPT_RESOURCE_CACHE')
                or Path.home() / '.cache/lazymind/presentation-resources').expanduser().resolve()


def cached_resource(kind):
    release = RELEASES[kind]
    directory = cache_root() / release['sha256']
    root = directory / release['root']
    marker = directory / '.complete'
    try:
        if root.is_dir() and marker.read_text() == release['sha256']:
            return root
    except OSError:
        pass
    return None


def _extract(archive, directory):
    import zipfile
    with zipfile.ZipFile(archive) as bundle:
        entries = bundle.infolist()
        if len(entries) > 10000 or sum(e.file_size for e in entries) > 1024 ** 3:
            raise ValueError('PPT library archive exceeds extraction limits')
        for entry in entries:
            path = PurePosixPath(entry.filename)
            if (path.is_absolute() or '..' in path.parts or '\\' in entry.filename
                    or ':' in entry.filename or stat.S_ISLNK(entry.external_attr >> 16)):
                raise ValueError('Unsafe PPT library archive path')
        bundle.extractall(directory)


def ensure_resource(kind):
    release = RELEASES[kind]
    ready = cached_resource(kind)
    if ready:
        return ready
    base = cache_root()
    base.mkdir(parents=True, exist_ok=True)
    with FileLock(str(base / (release['sha256'] + '.lock')), timeout=600):
        ready = cached_resource(kind)
        if ready:
            return ready
        failures = []
        for mirror in MIRRORS:
            try:
                with tempfile.TemporaryDirectory(prefix='.download-', dir=base) as tmp:
                    staging = Path(tmp)
                    archive = staging / 'bundle.zip'
                    digest = hashlib.sha256()
                    size = 0
                    with httpx.Client(timeout=httpx.Timeout(45, connect=10), follow_redirects=True) as client:
                        with client.stream('GET', mirror + release['path']) as response:
                            response.raise_for_status()
                            with archive.open('wb') as stream:
                                for chunk in response.iter_bytes():
                                    size += len(chunk)
                                    if size > release['bytes']:
                                        raise ValueError('PPT library download exceeds expected size')
                                    digest.update(chunk)
                                    stream.write(chunk)
                    if size != release['bytes'] or digest.hexdigest() != release['sha256']:
                        raise ValueError('PPT library checksum/size mismatch')
                    unpacked = staging / 'unpacked'
                    _extract(archive, unpacked)
                    root = unpacked / release['root']
                    required = 'manifest.json' if kind == 'fonts' else 'catalog.json'
                    json.loads((root / required).read_text(encoding='utf-8'))
                    (unpacked / '.complete').write_text(release['sha256'])
                    target = base / release['sha256']
                    if target.exists():
                        shutil.rmtree(target)
                    unpacked.replace(target)
                    LOG.info('PPT resource ready: %s (%s)', kind, release['sha256'][:12])
                    return target / release['root']
            except Exception as exc:
                # Do not log redirect URLs, which can contain signed credentials.
                reason = f'{type(exc).__name__}'
                if isinstance(exc, httpx.HTTPStatusError):
                    reason += f' HTTP {exc.response.status_code}'
                elif isinstance(exc, ValueError):
                    reason += f': {exc}'
                failures.append(reason)
                LOG.warning('PPT resource %s mirror failed: %s: %s', kind, mirror, reason)
        raise RuntimeError(f'PPT {kind} library download failed on both mirrors: ' + '; '.join(failures))


def _background(kind):
    try:
        return ensure_resource(kind)
    except Exception:
        LOG.exception('PPT resource prefetch failed: %s; next use will retry', kind)
        return None


def prefetch():
    """Start both libraries without blocking workflow startup; retry failed runs."""
    with _GUARD:
        for kind in RELEASES:
            key = (str(cache_root()), kind)
            if cached_resource(kind):
                continue
            job = _JOBS.get(key)
            if job is None or job.done():
                _JOBS[key] = _EXECUTOR.submit(_background, kind)


def font_families():
    return RELEASES['fonts']['families']
