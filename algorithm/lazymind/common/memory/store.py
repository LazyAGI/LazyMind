from __future__ import annotations

import re
from typing import Any, Optional

from lazymind.common.integrations.remote_fs import RemoteFS
from .exceptions import MemoryPartialApplyError
from .field_contract import validate_memory_transition
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
    PreferenceItem,
    validate_preference_index,
    validate_reference_content,
)
from .validation.common import parse_yaml_mapping
from .validation.document import (
    is_internal_memory_path,
    split_stored_memory_content,
    validate_stored_memory_content,
)
from .editors.document import apply_memory_operations

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
        return self._read_versioned(
            SOUL_PATH,
            label='soul',
        )

    def read_profile(self) -> str:
        return self._read_versioned(
            PROFILE_PATH,
            label='profile',
        )

    def read_preference(self) -> str:
        return self.read(PREFERENCE_PATH)

    def _read_versioned(self, path: str, *, label: str) -> str:
        raw = self.read(path)
        try:
            _, visible = split_stored_memory_content(raw, label=label)
        except ValueError as exc:
            raise ValueError(str(exc)) from exc
        return visible

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

    def apply_soul_operations(
        self,
        operations: list[dict[str, Any]],
    ) -> dict[str, Any]:
        return self._apply_memory_operations(
            SOUL_PATH,
            label='soul',
            operations=operations,
            apply=apply_memory_operations,
        )

    def apply_profile_operations(
        self,
        operations: list[dict[str, Any]],
    ) -> dict[str, Any]:
        return self._apply_memory_operations(
            PROFILE_PATH,
            label='profile',
            operations=operations,
            apply=apply_memory_operations,
        )

    def _apply_memory_operations(
        self,
        path: str,
        *,
        label: str,
        operations: list[dict[str, Any]],
        apply,
    ) -> dict[str, Any]:
        loaded = self.read(path)
        edited = apply(loaded, operations, label=label)
        stored_content = edited.get('stored_content')
        if not isinstance(stored_content, str) or not stored_content.strip():
            raise ValueError(f'{label} editor did not produce valid stored content.')
        _, before_visible = split_stored_memory_content(loaded, label=label)
        _, after_visible = split_stored_memory_content(stored_content, label=label)
        before_stored = parse_yaml_mapping(loaded)
        after_stored = parse_yaml_mapping(stored_content)
        before_metadata = {
            key: value
            for key, value in before_stored.items()
            if is_internal_memory_path(key)
        }
        after_metadata = {
            key: value
            for key, value in after_stored.items()
            if is_internal_memory_path(key)
        }
        if before_metadata != after_metadata:
            raise ValueError(f'{label} internal metadata cannot be changed.')
        validate_memory_transition(
            parse_yaml_mapping(before_visible),
            parse_yaml_mapping(after_visible),
            label=label,
        )

        self.write(path, stored_content)
        return {
            'content': edited['content'],
            'operations': list(edited.get('operations') or operations),
        }

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
    ) -> PreferenceItem:
        from .editors.preference import add_preference_entry

        loaded = self.read(PREFERENCE_PATH)
        edited = add_preference_entry(
            loaded,
            name=name,
            summary=summary,
            scenario=scenario,
            details=details,
            reason=reason,
            source_kind=source_kind,
            conversation_id=conversation_id,
        )
        reference_name = edited['reference_name']
        self.write(
            build_reference_path(reference_name),
            edited['reference_content'],
        )
        try:
            self.write(PREFERENCE_PATH, edited['content'])
        except Exception as write_exc:
            try:
                self.delete_reference(reference_name)
            except Exception as cleanup_exc:
                raise MemoryPartialApplyError(
                    (
                        f'preference add partially applied: reference '
                        f'{reference_name!r} was created but the index write failed; '
                        f'cleanup also failed: {cleanup_exc}'
                    ),
                    operation='add',
                    applied=['reference'],
                    failed=['preference_index', 'reference_cleanup'],
                    item=edited['item'],
                ) from write_exc
            raise
        return edited['item']

    def remove_preference_with_reference(self, name: str) -> PreferenceItem:
        from .editors.preference import delete_preference_entry, reference_name_from_item

        loaded = self.read(PREFERENCE_PATH)
        edited = delete_preference_entry(loaded, name=name)
        self.write(PREFERENCE_PATH, edited['content'])
        reference_name = reference_name_from_item(edited['item'])
        try:
            self.delete_reference(reference_name)
        except Exception as exc:
            raise MemoryPartialApplyError(
                (
                    f'preference delete partially applied: index entry '
                    f"{edited['item'].name!r} was removed but reference "
                    f'{reference_name!r} could not be deleted: {exc}'
                ),
                operation='delete',
                applied=['preference_index'],
                failed=['reference'],
                item=edited['item'],
            ) from exc
        return edited['item']

    def _require_file_path(self, path: str) -> str:
        normalized = normalize_memory_path(path)
        if is_fixed_memory_file(normalized) or is_reference_path(normalized):
            return normalized
        raise ValueError(f'unsupported memory file path: {path!r}')

    def _validate(self, path: str, content: str) -> None:
        if path == SOUL_PATH:
            error = validate_stored_memory_content(content, label='soul')
        elif path == PROFILE_PATH:
            error = validate_stored_memory_content(content, label='profile')
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
