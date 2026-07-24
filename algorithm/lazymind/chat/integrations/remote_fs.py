import base64
import os
from io import BytesIO, TextIOWrapper
from typing import Any, Dict, List, Optional
from urllib.parse import urlparse

import lazyllm
import requests

from lazyllm.tools.fs import LazyLLMFSBase
from lazymind.config import config as _cfg


class RemoteFS(LazyLLMFSBase):
    """A filesystem proxy for skill resources using the backend core remote-fs API."""

    def __init__(self, token: str = 'remote', base_url: str = '', timeout: float = 10, **kwargs):
        super().__init__(token=token, **kwargs)
        self.base_url = (base_url or str(_cfg['core_api_url'] or '').strip()).rstrip('/')
        self.timeout = timeout

    @staticmethod
    def _normalize_path(path: str) -> str:
        raw = str(path or '')
        if '://' in raw:
            parsed = urlparse(raw)
            raw = f'{parsed.netloc}{parsed.path}'
        return raw.strip('/')

    def _request(self, method: str, endpoint: str, **kwargs) -> Any:
        response = self._raw_request(method, endpoint, **kwargs)
        self._raise_for_status(response, endpoint)
        if not response.content:
            return {}
        try:
            payload = response.json()
        except ValueError as exc:
            raise RuntimeError(f'remote-fs {endpoint} returned invalid json') from exc
        return payload if payload is not None else {}

    def _raw_request(self, method: str, endpoint: str, **kwargs) -> requests.Response:
        agentic_config = lazyllm.globals.get('agentic_config') or {}
        user_id = agentic_config.get('user_id')
        task_id = agentic_config.get('session_id') or agentic_config.get('task_id')
        params = kwargs.pop('params', {})
        if user_id:
            params['user_id'] = user_id
        if task_id:
            params['task_id'] = task_id
        return requests.request(
            method,
            f'{self.base_url}/remote-fs/{endpoint}',
            params=params,
            timeout=self.timeout,
            **kwargs,
        )

    @staticmethod
    def _raise_for_status(response: requests.Response, endpoint: str) -> None:
        try:
            response.raise_for_status()
            return
        except requests.HTTPError as exc:
            message = RemoteFS._error_message(response)
            if message:
                raise RuntimeError(message) from exc
            raise

    @staticmethod
    def _error_message(response: requests.Response) -> str:
        try:
            payload = response.json()
        except ValueError:
            return (response.text or '').strip()
        if isinstance(payload, dict):
            return str(payload.get('message') or payload.get('error') or payload.get('code') or '').strip()
        return ''

    def _request_json(self, endpoint: str, **params: Any) -> Any:
        return self._request('GET', endpoint, params=params)

    @staticmethod
    def _qualify_name(name: str) -> str:
        normalized = RemoteFS._normalize_path(name)
        return f'remote://{normalized}' if normalized else 'remote://'

    @staticmethod
    def _package_root(path: str) -> str:
        normalized = RemoteFS._normalize_path(path)
        parts = [part for part in normalized.split('/') if part]
        if len(parts) >= 3 and parts[0] == 'skills':
            return '/'.join(parts[:3])
        if len(parts) >= 3 and parts[0] == 'plugins':
            return '/'.join(parts[:3])
        return normalized

    def _entry_remote_path(self, parent_path: str, entry: Dict[str, Any]) -> str:
        raw_path = str((entry or {}).get('path') or (entry or {}).get('name') or '').strip()
        normalized = self._normalize_path(raw_path)
        if not normalized:
            return ''
        if normalized == 'skills' or normalized.startswith('skills/'):
            return normalized
        package_root = self._package_root(parent_path)
        if package_root:
            return f'{package_root.rstrip("/")}/{normalized}'
        return normalized

    def _format_list_entry(self, parent_path: str, entry: Dict[str, Any]) -> Dict[str, Any]:
        formatted = dict(entry or {})
        remote_path = self._entry_remote_path(parent_path, formatted)
        formatted['path'] = remote_path
        formatted['name'] = self._qualify_name(remote_path)
        return formatted

    def ls(self, path: str, detail: bool = True, **kwargs) -> List[Any]:
        normalized_path = self._normalize_path(path)
        try:
            params = {'path': normalized_path}
            if kwargs.get('revision_id'):
                params['revision_id'] = kwargs['revision_id']
            data = self._request_json('list', **params)
        except RuntimeError as exc:
            if normalized_path == 'skills' and 'path does not exist' in str(exc):
                return []
            raise
        entries = [self._format_list_entry(normalized_path, entry) for entry in data.get('items', [])]
        if detail:
            return entries
        return [entry['name'] for entry in entries]

    def info(self, path: str, **kwargs) -> Dict[str, Any]:
        params = {'path': self._normalize_path(path)}
        if kwargs.get('revision_id'):
            params['revision_id'] = kwargs['revision_id']
        return self._request_json('info', **params)

    def exists(self, path: str, **kwargs) -> bool:
        params = {'path': self._normalize_path(path)}
        if kwargs.get('revision_id'):
            params['revision_id'] = kwargs['revision_id']
        data = self._request_json('exists', **params)
        return bool(data.get('exists'))

    def makedirs(self, path: str, exist_ok: bool = True) -> None:
        self.mkdir(path, create_parents=True)

    def mkdir(self, path: str, create_parents: bool = True, **kwargs) -> None:
        self._request(
            'POST',
            'dir',
            json={
                'path': self._normalize_path(path),
                'recursive': bool(create_parents),
            },
        )

    def rm(self, path: str, recursive: bool = False, maxdepth: Optional[int] = None) -> None:
        self._request(
            'DELETE',
            'path',
            params={
                'path': self._normalize_path(path),
                'recursive': str(bool(recursive)).lower(),
            },
        )

    def write(self, path: str, content: str, content_type: str = 'text/plain; charset=utf-8') -> None:
        self._request(
            'PUT',
            'content',
            params={'path': self._normalize_path(path)},
            data=content.encode('utf-8'),
            headers={'Content-Type': content_type},
        )

    def write_file(self, path: str, data: bytes, content_type: str = 'application/octet-stream') -> None:
        self._request(
            'PUT',
            'content',
            params={'path': self._normalize_path(path)},
            data=data,
            headers={'Content-Type': content_type},
        )

    def move(self, path1: str, path2: str, recursive: bool = False, **kwargs) -> None:
        self._request(
            'POST',
            'move',
            json={
                'from': self._normalize_path(path1),
                'to': self._normalize_path(path2),
            },
        )

    def trash(self, path: str) -> None:
        self._request(
            'POST',
            'trash',
            json={'path': self._normalize_path(path)},
        )

    def open(
        self,
        path: str,
        mode: str = 'rb',
        block_size: Optional[int] = None,
        cache_options=None,
        compression=None,
        **kwargs,
    ):
        return self._open(path, mode=mode, block_size=block_size, **kwargs)

    def _open(
        self,
        path: str,
        mode: str = 'rb',
        block_size: Optional[int] = None,
        **kwargs,
    ):
        is_write = any(flag in mode for flag in ('w', 'a', 'x', '+'))
        if is_write:
            encoding = kwargs.get('encoding') or 'utf-8'
            content_type = kwargs.get('content_type')
            if not content_type and 'b' not in mode:
                content_type = f'text/plain; charset={encoding}'
            buf = BytesIO()

            class _WriteBuffer:
                def __init__(self, _buf, _path, _encoding, _content_type, _fs):
                    self._buf = _buf
                    self._path = _path
                    self._encoding = _encoding
                    self._content_type = _content_type
                    self._fs = _fs

                def write(self, data):
                    original_len = len(data)
                    if isinstance(data, str):
                        data = data.encode(self._encoding)
                    self._buf.write(data)
                    return original_len

                def close(self):
                    self._fs.write_file(
                        self._path,
                        self._buf.getvalue(),
                        content_type=self._content_type or 'application/octet-stream',
                    )
                    self._buf.close()

                def __enter__(self):
                    return self

                def __exit__(self, *args):
                    self.close()
                    return False

            return _WriteBuffer(buf, self._normalize_path(path), encoding, content_type, self)

        response = self._raw_request(
            'GET',
            'content',
            params={
                'path': self._normalize_path(path),
                'encoding': 'raw',
                **({'revision_id': kwargs['revision_id']} if kwargs.get('revision_id') else {}),
            },
        )
        self._raise_for_status(response, 'content')
        body = BytesIO(response.content)
        if 'b' in mode:
            return body
        encoding = kwargs.get('encoding') or 'utf-8'
        return TextIOWrapper(body, encoding=encoding, errors=kwargs.get('errors'))

    def read_base64(self, path: str) -> bytes:
        data = self._request_json('content', path=self._normalize_path(path), encoding='base64')
        content = data.get('content') if isinstance(data, dict) else None
        if not isinstance(content, str):
            raise RuntimeError('remote-fs content base64 response missing content')
        return base64.b64decode(content)

    def materialize_dir(self, path: str, local_dir: str, **kwargs) -> Dict[str, Any]:
        normalized_root = self._normalize_path(path)
        revision_id = kwargs.get('revision_id')
        files: List[str] = []

        def walk(current: str) -> None:
            for entry in self.ls(current, detail=True, revision_id=revision_id):
                entry_name = str((entry or {}).get('name') or '').strip()
                if not entry_name:
                    continue
                remote_name = self._normalize_path(entry_name)
                if str((entry or {}).get('type') or 'file') in ('directory', 'dir'):
                    walk(remote_name)
                    continue
                prefix = normalized_root.rstrip('/') + '/'
                if remote_name.startswith(prefix):
                    rel_path = remote_name[len(prefix):]
                else:
                    rel_path = remote_name.rsplit('/', 1)[-1]
                rel_parts = [part for part in rel_path.replace('\\', '/').split('/') if part]
                if not rel_parts or any(part in ('.', '..') for part in rel_parts):
                    raise RuntimeError(f'remote-fs materialize got invalid relative path: {rel_path!r}')
                local_root = os.path.abspath(local_dir)
                destination = os.path.abspath(os.path.join(local_root, *rel_parts))
                if os.path.commonpath([local_root, destination]) != local_root:
                    raise RuntimeError(f'remote-fs materialize got invalid relative path: {rel_path!r}')
                os.makedirs(os.path.dirname(destination), exist_ok=True)
                with self.open(remote_name, 'rb', revision_id=revision_id) as src, open(destination, 'wb') as dst:
                    dst.write(src.read())
                files.append(rel_path)

        walk(normalized_root)
        return {
            'source_path': self._qualify_name(normalized_root),
            'local_dir': local_dir,
            'materialized': True,
            'file_count': len(files),
            'files': sorted(files),
        }
