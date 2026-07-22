from __future__ import annotations

from typing import Any, Optional

from lazymind.common.integrations.remote_fs import RemoteFS

from .store import MemoryStore


class MemoryRemoteStore:
    """Thin RemoteFS facade over structured ``MemoryStore``."""

    def __init__(self, fs: Optional[RemoteFS] = None):
        self.fs = fs or RemoteFS()
        self.store = MemoryStore(self.fs)

    def read_path(self, path: str) -> str:
        return self.store.read(path)

    def write_path(self, path: str, content: str) -> None:
        self.store.write(path, content)

    def exists_path(self, path: str) -> bool:
        return self.store.exists(path)

    def list_dir(self, path: str) -> list[dict[str, Any]]:
        return self.store.list_dir(path)
