from __future__ import annotations

from typing import Optional

from lazymind.common.integrations.remote_fs import RemoteFS


MEMORY_TARGET_PATHS = {
    'memory': 'memory/memory.md',
    'user_preference': 'memory/user.md',
}


class MemoryRemoteStore:
    """Read-only RemoteFS access to persistent memory documents."""

    def __init__(self, fs: Optional[RemoteFS] = None):
        self.fs = fs or RemoteFS()

    def read(self, target: str) -> str:
        path = MEMORY_TARGET_PATHS[target]
        with self.fs.open(path, 'r', encoding='utf-8', errors='replace') as handle:
            return handle.read()
