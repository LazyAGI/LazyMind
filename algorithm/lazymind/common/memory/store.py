from __future__ import annotations

import re
from typing import Any, Optional

from lazymind.common.integrations.remote_fs import RemoteFS
from lazymind.config import config as _cfg

from .paths import (
    AGENTS_ROOT,
    PREFERENCE_PATH,
    PROFILE_PATH,
    REFERENCE_ROOT,
    SOUL_PATH,
    USERS_ROOT,
    build_reference_path,
    is_fixed_memory_file,
    is_reference_path,
    normalize_memory_path,
    split_reference_ref,
)
from .validation import (
    parse_preference_items,
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
                content = fh.read()
        except Exception as exc:
            if self._is_not_found(exc):
                raise FileNotFoundError(f'memory path not found: {normalized}') from exc
            raise RuntimeError(f'failed to read {normalized}: {exc}') from exc
        self._validate(normalized, content)
        return content

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
        except ValueError:
            raise
        except Exception as exc:
            raise RuntimeError(f'failed to write {normalized}: {exc}') from exc

    def read_soul(self) -> str:
        return self.read(SOUL_PATH)

    def read_profile(self) -> str:
        return self.read(PROFILE_PATH)

    def read_preference(self) -> str:
        return self.read(PREFERENCE_PATH)

    def read_reference(self, ref: str) -> str:
        path, anchor = split_reference_ref(ref)
        content = self.read(path)
        if not anchor:
            return content
        return self._extract_anchor_section(content, anchor)

    def delete_reference(self, name: str) -> None:
        path = build_reference_path(name)
        if not is_reference_path(path):
            raise ValueError(f'invalid reference name: {name!r}')
        try:
            if hasattr(self.fs, 'rm'):
                self.fs.rm(path)
            elif hasattr(self.fs, 'delete'):
                self.fs.delete(path)
            else:
                raise RuntimeError('remote filesystem does not support delete')
        except ValueError:
            raise
        except Exception as exc:
            if self._is_not_found(exc):
                return
            raise RuntimeError(f'failed to delete {path}: {exc}') from exc

    def apply_soul_fields(self, changes: dict[str, str]) -> dict[str, Any]:
        from .result import memory_ok

        loaded = self._read_document(SOUL_PATH, label='soul')
        if not loaded.get('ok'):
            return loaded
        from .editors.soul import set_soul_fields

        edited = set_soul_fields(loaded['content'], changes)
        if not edited.get('ok'):
            return edited
        written = self._write_document(SOUL_PATH, edited['content'])
        if not written.get('ok'):
            return written
        return memory_ok(
            content=edited['content'],
            fields=list(edited.get('fields') or changes),
        )

    def apply_profile_fields(self, changes: dict[str, str]) -> dict[str, Any]:
        from .result import memory_ok

        loaded = self._read_document(PROFILE_PATH, label='profile')
        if not loaded.get('ok'):
            return loaded
        from .editors.profile import set_profile_fields

        edited = set_profile_fields(loaded['content'], changes)
        if not edited.get('ok'):
            return edited
        written = self._write_document(PROFILE_PATH, edited['content'])
        if not written.get('ok'):
            return written
        return memory_ok(
            content=edited['content'],
            fields=list(edited.get('fields') or changes),
        )

    def add_preference_with_reference(
        self,
        *,
        name: str,
        summary: str,
        scenario: str,
        details: str,
        reason: str,
        source_kind: str,
        conversation_id: str,
    ) -> dict[str, Any]:
        from .editors.preference import add_preference_entry
        from .result import memory_ok

        loaded = self._read_document(PREFERENCE_PATH, label='preference')
        if not loaded.get('ok'):
            return loaded
        current_items = len(parse_preference_items(loaded['content']))
        max_items = int(_cfg['preference_index_max_items'])
        if current_items >= max_items:
            from .result import memory_err

            attempted_items = current_items + 1
            return memory_err(
                (
                    'preference index capacity exceeded: '
                    f'used_items={attempted_items} max_items={max_items}'
                ),
                type='capacity_exceeded',
                used_items=attempted_items,
                max_items=max_items,
            )
        edited = add_preference_entry(
            loaded['content'],
            name=name,
            summary=summary,
            scenario=scenario,
            details=details,
            reason=reason,
            source_kind=source_kind,
            conversation_id=conversation_id,
        )
        if not edited.get('ok'):
            return edited
        reference_name = edited['reference_name']
        written_ref = self._write_document(
            build_reference_path(reference_name),
            edited['reference_content'],
        )
        if not written_ref.get('ok'):
            return written_ref
        written_pref = self._write_document(PREFERENCE_PATH, edited['content'])
        if not written_pref.get('ok'):
            try:
                self.delete_reference(reference_name)
            except Exception as cleanup_exc:
                from .result import memory_err

                return memory_err(
                    (
                        f"preference add partially applied: reference "
                        f"{reference_name!r} was created but the index write failed; "
                        f'cleanup also failed: {cleanup_exc}'
                    ),
                    type='partial',
                    operation='add',
                    applied=['reference'],
                    failed=['preference_index'],
                    item=edited['item'],
                )
            return written_pref
        return memory_ok(item=edited['item'])

    def remove_preference_with_reference(self, name: str) -> dict[str, Any]:
        from .editors.preference import delete_preference_entry, reference_name_from_item
        from .result import memory_ok

        loaded = self._read_document(PREFERENCE_PATH, label='preference')
        if not loaded.get('ok'):
            return loaded
        edited = delete_preference_entry(loaded['content'], name=name)
        if not edited.get('ok'):
            return edited
        written = self._write_document(PREFERENCE_PATH, edited['content'])
        if not written.get('ok'):
            return written
        reference_name = reference_name_from_item(edited['item'])
        try:
            self.delete_reference(reference_name)
        except Exception as exc:
            from .result import memory_err

            return memory_err(
                (
                    f"preference delete partially applied: index entry "
                    f"{edited['item'].name!r} was removed but reference "
                    f'{reference_name!r} could not be deleted: {exc}'
                ),
                type='partial',
                operation='delete',
                applied=['preference_index'],
                failed=['reference'],
                item=edited['item'],
            )
        return memory_ok(item=edited['item'])

    def _read_document(self, path: str, *, label: str) -> dict[str, Any]:
        from .result import memory_err, memory_ok

        try:
            return memory_ok(content=self.read(path))
        except FileNotFoundError:
            return memory_err(f'{label} document not found.', type='not_found')
        except ValueError as exc:
            return memory_err(str(exc), type='validation')
        except RuntimeError as exc:
            return memory_err(str(exc), type='store')

    def _write_document(self, path: str, content: str) -> dict[str, Any]:
        from .result import memory_err, memory_ok

        try:
            self.write(path, content)
            return memory_ok()
        except ValueError as exc:
            return memory_err(str(exc), type='validation')
        except RuntimeError as exc:
            return memory_err(str(exc).strip(), type='store')
        except Exception as exc:
            return memory_err(f'failed to write {path}: {exc}', type='store')

    def _require_file_path(self, path: str) -> str:
        normalized = normalize_memory_path(path)
        if is_fixed_memory_file(normalized) or is_reference_path(normalized):
            return normalized
        raise ValueError(f'unsupported memory file path: {path!r}')

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
            raise ValueError(error)

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
        if isinstance(exc, FileNotFoundError):
            return True
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
