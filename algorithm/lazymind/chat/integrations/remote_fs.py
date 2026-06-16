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
        session_id = lazyllm.globals['agentic_config'].get('session_id')
        params = kwargs.pop('params', {})
        if session_id:
            params['session_id'] = session_id
        response = requests.request(
            method,
            f'{self.base_url}/remote-fs/{endpoint}',
            params=params,
            timeout=self.timeout,
            **kwargs,
        )
        response.raise_for_status()
        payload = response.json()
        if payload.get('code') != 0:
            raise RuntimeError(payload.get('message') or f'remote-fs {endpoint} failed')
        return payload.get('data')

    def _request_json(self, endpoint: str, **params: Any) -> Any:
        return self._request('GET', endpoint, params=params)

    @staticmethod
    def _qualify_name(name: str) -> str:
        normalized = RemoteFS._normalize_path(name)
        return f'remote://{normalized}' if normalized else 'remote://'

    def _format_list_entry(self, entry: Dict[str, Any]) -> Dict[str, Any]:
        formatted = dict(entry or {})
        formatted['name'] = self._qualify_name(str(formatted.get('name') or ''))
        return formatted

    def ls(self, path: str, detail: bool = True, **kwargs) -> List[Any]:
        normalized_path = self._normalize_path(path)
        try:
            data = self._request_json('list', path=normalized_path, detail=str(bool(detail)).lower())
        except RuntimeError as exc:
            if normalized_path == 'skills' and 'path does not exist' in str(exc):
                return []
            raise
        if detail:
            return [self._format_list_entry(entry) for entry in data.get('entries', [])]
        return [self._qualify_name(name) for name in data.get('names', [])]

    def info(self, path: str, **kwargs) -> Dict[str, Any]:
        return self._request_json('info', path=self._normalize_path(path))

    def exists(self, path: str, **kwargs) -> bool:
        data = self._request_json('exists', path=self._normalize_path(path))
        return bool(data.get('exists'))

    def makedirs(self, path: str, exist_ok: bool = True) -> None:
        return

    def mkdir(self, path: str, create_parents: bool = True, **kwargs) -> None:
        return

    def rm(self, path: str, recursive: bool = False, maxdepth: Optional[int] = None) -> None:
        self._request(
            'DELETE',
            'path',
            params={
                'path': self._normalize_path(path),
                'recursive': str(bool(recursive)).lower(),
            },
        )

    def write(self, path: str, content: str) -> None:
        self._request(
            'PUT',
            'content',
            params={'path': self._normalize_path(path)},
            data=content.encode('utf-8'),
        )

    def write_file(self, path: str, data: bytes) -> None:
        self._request(
            'PUT',
            'content',
            params={'path': self._normalize_path(path)},
            data=data,
        )

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
            buf = BytesIO()

            class _WriteBuffer:
                def __init__(self, _buf, _path, _encoding, _fs):
                    self._buf = _buf
                    self._path = _path
                    self._encoding = _encoding
                    self._fs = _fs

                def write(self, data):
                    if isinstance(data, str):
                        data = data.encode(self._encoding)
                    self._buf.write(data)

                def close(self):
                    self._fs.write_file(self._path, self._buf.getvalue())
                    self._buf.close()

                def __enter__(self):
                    return self

                def __exit__(self, *args):
                    self.close()
                    return False

            return _WriteBuffer(buf, self._normalize_path(path), encoding, self)

        params = {'path': self._normalize_path(path)}
        session_id = lazyllm.globals['agentic_config'].get('session_id')
        if session_id:
            params['session_id'] = session_id
        response = requests.get(
            f'{self.base_url}/remote-fs/content',
            params=params,
            timeout=self.timeout,
        )
        response.raise_for_status()
        body = BytesIO(response.content)
        if 'b' in mode:
            return body
        encoding = kwargs.get('encoding') or 'utf-8'
        return TextIOWrapper(body, encoding=encoding)
