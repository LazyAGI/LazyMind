from __future__ import annotations

import re
from typing import Any, Optional

from lazymind.chat.integrations.remote_fs import RemoteFS

from .defaults import default_preference_md, default_profile_md, default_soul_md
from .errors import MemoryNotFoundError, MemoryPathError, MemoryStoreError, MemoryValidationError
from .paths import (
    AGENTS_ROOT,
    MEMORY_ROOT,
    PREFERENCE_PATH,
    PROFILE_PATH,
    REFERENCE_ROOT,
    SOUL_PATH,
    USERS_ROOT,
    build_reference_path,
    is_fixed_memory_file,
    is_memory_path,
    is_reference_path,
    normalize_memory_path,
    split_reference_ref,
)
from .schema import (
    validate_preference_index,
    validate_profile_content,
    validate_reference_content,
    validate_soul_content,
)

_ANCHOR_HEADING_RE = re.compile(r'(?m)^(#{1,6})\s+(?P<title>.+?)\s*$')


class MemoryStore:
    """RemoteFS-backed store for soul / profile / preference / references."""

    def __init__(self, fs: Optional[RemoteFS] = None):
        self.fs = fs or RemoteFS()

    def read(self, path: str) -> str:
        normalized = self._require_file_path(path)
        try:
            with self.fs.open(normalized, 'r', encoding='utf-8', errors='replace') as fh:
                return fh.read()
        except Exception as exc:
            if self._is_not_found(exc):
                raise MemoryNotFoundError(f'memory path not found: {normalized}') from exc
            raise MemoryStoreError(f'failed to read {normalized}: {exc}') from exc

    def write(self, path: str, content: str, *, validate: bool = True) -> None:
        normalized = self._require_file_path(path)
        text = content if isinstance(content, str) else str(content)
        if validate:
            self._validate(normalized, text)
        try:
            parent = normalized.rsplit('/', 1)[0]
            if parent in {AGENTS_ROOT, USERS_ROOT, REFERENCE_ROOT}:
                self._ensure_dir(parent)
            self.fs.write(normalized, text)
        except MemoryValidationError:
            raise
        except Exception as exc:
            raise MemoryStoreError(f'failed to write {normalized}: {exc}') from exc

    def exists(self, path: str) -> bool:
        normalized = normalize_memory_path(path)
        if not is_memory_path(normalized):
            raise MemoryPathError(f'unsupported memory path: {path!r}')
        try:
            return bool(self.fs.exists(normalized))
        except Exception as exc:
            raise MemoryStoreError(f'failed to check exists for {normalized}: {exc}') from exc

    def list_dir(self, path: str) -> list[dict[str, Any]]:
        normalized = normalize_memory_path(path)
        if normalized not in {MEMORY_ROOT, AGENTS_ROOT, USERS_ROOT, REFERENCE_ROOT}:
            raise MemoryPathError(f'unsupported memory directory: {path!r}')
        try:
            if not self.fs.exists(normalized):
                return []
            entries = self.fs.ls(normalized, detail=True)
        except Exception as exc:
            if self._is_not_found(exc):
                return []
            raise MemoryStoreError(f'failed to list {normalized}: {exc}') from exc

        items: list[dict[str, Any]] = []
        for entry in entries or []:
            raw_path = str((entry or {}).get('path') or (entry or {}).get('name') or '').strip()
            entry_path = normalize_memory_path(raw_path)
            if not entry_path:
                continue
            entry_type = str((entry or {}).get('type') or 'file').strip()
            items.append({
                'name': entry_path.rsplit('/', 1)[-1],
                'path': entry_path,
                'type': 'dir' if entry_type in ('directory', 'dir') else 'file',
                'size': (entry or {}).get('size'),
            })
        return sorted(items, key=lambda item: (item['type'] != 'dir', item['name']))

    def read_soul(self) -> str:
        return self._read_or_default(SOUL_PATH, default_soul_md())

    def read_profile(self) -> str:
        return self._read_or_default(PROFILE_PATH, default_profile_md())

    def read_preference(self) -> str:
        return self._read_or_default(PREFERENCE_PATH, default_preference_md())

    def read_reference(self, ref: str) -> str:
        path, anchor = split_reference_ref(ref)
        content = self.read(path)
        if not anchor:
            return content
        return self._extract_anchor_section(content, anchor)

    def write_soul(self, content: str) -> None:
        self.write(SOUL_PATH, content)

    def write_profile(self, content: str) -> None:
        self.write(PROFILE_PATH, content)

    def write_preference(self, content: str) -> None:
        self.write(PREFERENCE_PATH, content)

    def write_reference(self, name: str, content: str) -> None:
        self.write(build_reference_path(name), content)

    def list_references(self) -> list[dict[str, Any]]:
        return [item for item in self.list_dir(REFERENCE_ROOT) if item.get('type') == 'file']

    def ensure_defaults(self) -> None:
        for path, factory in (
            (SOUL_PATH, default_soul_md),
            (PROFILE_PATH, default_profile_md),
            (PREFERENCE_PATH, default_preference_md),
        ):
            if not self.exists(path):
                self.write(path, factory(), validate=True)
        self._ensure_dir(REFERENCE_ROOT)

    def _read_or_default(self, path: str, default: str) -> str:
        try:
            return self.read(path)
        except MemoryNotFoundError:
            return default

    def _require_file_path(self, path: str) -> str:
        normalized = normalize_memory_path(path)
        if is_fixed_memory_file(normalized) or is_reference_path(normalized):
            return normalized
        raise MemoryPathError(f'unsupported memory file path: {path!r}')

    def _validate(self, path: str, content: str) -> None:
        if path == SOUL_PATH:
            error = validate_soul_content(content)
        elif path == PROFILE_PATH:
            error = validate_profile_content(content)
        elif path == PREFERENCE_PATH:
            error = validate_preference_index(content)
        elif is_reference_path(path):
            error = validate_reference_content(content)
        else:
            error = f'unsupported memory file path: {path!r}'
        if error:
            raise MemoryValidationError(error)

    def _ensure_dir(self, path: str) -> None:
        try:
            if hasattr(self.fs, 'makedirs'):
                self.fs.makedirs(path, exist_ok=True)
            elif hasattr(self.fs, 'mkdir'):
                self.fs.mkdir(path, create_parents=True)
        except Exception:
            # Backend may create parents implicitly on write.
            return

    @staticmethod
    def _is_not_found(exc: Exception) -> bool:
        message = str(exc).lower()
        return 'not found' in message or '404' in message or 'does not exist' in message

    @staticmethod
    def _extract_anchor_section(content: str, anchor: str) -> str:
        needle = str(anchor or '').strip().lower().replace('_', '-')
        if not needle:
            return content
        lines = (content or '').splitlines()
        start = None
        start_level = None
        for idx, line in enumerate(lines):
            match = _ANCHOR_HEADING_RE.match(line)
            if not match:
                continue
            title = match.group('title').strip()
            slug = re.sub(r'[^a-z0-9]+', '-', title.lower()).strip('-')
            if slug == needle or title.lower() == needle:
                start = idx
                start_level = len(match.group(1))
                break
        if start is None:
            # Fall back to full file when the anchor is absent.
            return content
        end = len(lines)
        for idx in range(start + 1, len(lines)):
            match = _ANCHOR_HEADING_RE.match(lines[idx])
            if match and len(match.group(1)) <= (start_level or 1):
                end = idx
                break
        return '\n'.join(lines[start:end]).strip() + '\n'
