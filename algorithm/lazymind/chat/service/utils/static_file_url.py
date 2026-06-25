import hashlib
import hmac
import os
import time
from pathlib import Path
from urllib.parse import quote, unquote

from lazymind.config import config as _cfg


def _upload_root() -> str:
    for key in ('shared_upload_dir', 'upload_dir'):
        try:
            value = (_cfg[key] or '').strip()
        except (KeyError, TypeError):
            value = ''
        if value:
            return str(Path(value).resolve())
    env = (os.environ.get('LAZYMIND_UPLOAD_ROOT') or os.environ.get('LAZYMIND_SHARED_UPLOAD_DIR') or '').strip()
    if env:
        return str(Path(env).resolve())
    return '/var/lib/lazymind/uploads'


def _sign_secret() -> str:
    return (os.environ.get('LAZYMIND_FILE_URL_SIGN_SECRET') or 'lazymind-file-url-secret').strip()


def _expire_seconds() -> int:
    raw = (os.environ.get('LAZYMIND_FILE_URL_EXPIRE_SECONDS') or '3600').strip()
    try:
        value = int(raw)
    except ValueError:
        value = 3600
    return value if value > 0 else 3600


def file_relative_path(full_path: str) -> str:
    path = (full_path or '').strip()
    if not path:
        return ''
    try:
        clean_path = Path(path).resolve()
    except (ValueError, OSError):
        return ''
    root = Path(_upload_root()).resolve()
    try:
        rel = clean_path.relative_to(root)
    except ValueError:
        return ''
    rel_str = rel.as_posix()
    if rel_str in ('.', '..') or rel_str.startswith('../'):
        return ''
    return rel_str


def encode_static_file_path(rel: str) -> str:
    return '/'.join(quote(part, safe='') for part in rel.split('/'))


def sign_static_file(rel: str, expires: int) -> str:
    mac = hmac.new(_sign_secret().encode('utf-8'), digestmod=hashlib.sha256)
    mac.update(rel.encode('utf-8'))
    mac.update(b'\n')
    mac.update(str(expires).encode('utf-8'))
    return mac.hexdigest()


def static_file_url_from_full_path(full_path: str) -> str:
    rel = file_relative_path(full_path)
    if not rel:
        return ''
    expires = int(time.time()) + _expire_seconds()
    sig = sign_static_file(rel, expires)
    return f'/static-files/{encode_static_file_path(rel)}?expires={expires}&sig={sig}'


def basename_from_path(path: str) -> str:
    without_query = (path or '').split('?')[0]
    parts = without_query.split('/')
    return unquote(parts[-1] if parts else without_query)


def local_path_from_static_file_url(path_or_url: str) -> str:
    raw = (path_or_url or '').strip()
    if not raw:
        return ''
    root = Path(_upload_root()).resolve()
    if raw.startswith('/static-files/'):
        path_only = raw.split('?', 1)[0]
        rel_encoded = path_only[len('/static-files/'):].lstrip('/')
        if not rel_encoded:
            return ''
        rel = '/'.join(unquote(part) for part in rel_encoded.split('/'))
        return str((root / rel).resolve())
    marker = '/var/lib/lazymind/uploads/'
    if raw.startswith(marker):
        rel = raw.split('?', 1)[0][len(marker):].lstrip('/')
        return str((root / rel).resolve())
    try:
        resolved_root = str(root)
        if raw.startswith(resolved_root):
            return str(Path(raw.split('?', 1)[0]).resolve())
    except OSError:
        pass
    if raw.startswith('http://') or raw.startswith('https://'):
        idx = raw.find(marker)
        if idx >= 0:
            rel = raw[idx:].split('?', 1)[0][len(marker):].lstrip('/')
            return str((root / rel).resolve())
        idx = raw.find(str(root))
        if idx >= 0:
            return str(Path(raw[idx:].split('?', 1)[0]).resolve())
    if not raw.startswith(('http://', 'https://', '/static-files/')):
        candidate = (root / raw.lstrip('/')).resolve()
        if candidate.is_file():
            return str(candidate)
    return ''


def resolve_local_image_path(path_or_url: str) -> str:
    raw = (path_or_url or '').strip()
    if not raw:
        return ''
    local = local_path_from_static_file_url(raw)
    if local:
        return local
    path = Path(raw.split('?', 1)[0])
    if path.is_file():
        return str(path.resolve())
    return raw


def static_file_url_from_any(path: str) -> str:
    raw = (path or '').strip()
    if not raw:
        return ''
    if raw.startswith('/static-files/'):
        return raw
    if raw.startswith('http://') or raw.startswith('https://'):
        marker = '/var/lib/lazymind/uploads/'
        idx = raw.find(marker)
        if idx >= 0:
            return static_file_url_from_full_path(local_path_from_static_file_url(raw))
        return ''
    if raw.startswith('/var/lib/lazymind/uploads/'):
        return static_file_url_from_full_path(local_path_from_static_file_url(raw))
    joined = os.path.join(_upload_root(), raw.lstrip('/'))
    return static_file_url_from_full_path(joined)
