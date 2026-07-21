from __future__ import annotations

from typing import Any, Optional

from lazymind.chat.integrations.remote_fs import RemoteFS
from lazymind.review.memory_review.paths import (
    LEGACY_MEMORY_PATH,
    LEGACY_USER_PREFERENCE_PATH,
    PREFERENCE_PATH,
    PROFILE_PATH,
    SOUL_PATH,
)
from lazymind.review.memory_review.store import MemoryStore

# Legacy target names kept for memory_reader / review callers.
MEMORY_TARGET_PATHS = {
    'memory': LEGACY_MEMORY_PATH,
    'user_preference': LEGACY_USER_PREFERENCE_PATH,
}

MEMORY_TREE_PATHS = {
    'soul': SOUL_PATH,
    'profile': PROFILE_PATH,
    'preference': PREFERENCE_PATH,
}


class MemoryRemoteStore:
    """RemoteFS access layer for legacy targets and structured memory paths.

    Legacy ``read`` / ``write`` still target ``memory/memory.md`` and
    ``memory/user.md`` so existing tools keep working until they migrate.
    Prefer ``MemoryStore`` or the ``*_path`` helpers for the structured tree.
    """

    def __init__(self, fs: Optional[RemoteFS] = None):
        self.fs = fs or RemoteFS()
        self.store = MemoryStore(self.fs)

    def read(self, target: str) -> str:
        path = MEMORY_TARGET_PATHS[target]
        with self.fs.open(path, 'r', encoding='utf-8', errors='replace') as fh:
            return fh.read()

    def write(self, target: str, content: str) -> None:
        self.fs.write(MEMORY_TARGET_PATHS[target], content)

    def read_path(self, path: str) -> str:
        return self.store.read(path)

    def write_path(self, path: str, content: str) -> None:
        self.store.write(path, content)

    def exists_path(self, path: str) -> bool:
        return self.store.exists(path)

    def list_dir(self, path: str) -> list[dict[str, Any]]:
        return self.store.list_dir(path)
