# Copyright (c) 2026 LazyAGI. All rights reserved.
"""Read-only tools for working with local files.

Provides local directory listing, filename search, text search, file reading,
and file metadata lookup for local files made available to the current request.

Backend: prefers ripgrep_ (``rg``) for ``grep`` and ``glob`` when available;
falls back to Python stdlib otherwise.  The two backends differ in edge-case
behaviour (hidden files, .gitignore, binary-file detection, regex dialect) —
rg is the primary path and Python is a best-effort fallback.

.. _ripgrep: https://github.com/BurntSushi/ripgrep
"""
from __future__ import annotations

import datetime
from dataclasses import dataclass
import fnmatch
import glob as _glob
import json
import os
import re
import shutil
import subprocess
from typing import Any, Dict, List, Optional

import lazyllm

from lazymind.chat.engine.tools.infra import tool_error, tool_success

_RG_BINARY = shutil.which('rg') or ''
_RG_TIMEOUT = 30


@dataclass(frozen=True)
class LocalFSScope:
    source_id: str
    roots: tuple[str, ...]
    file_extensions: frozenset[str]


class LocalFSToolGroup:
    """Read-only tools for listing, searching, and reading local files.

    The tools can access only the local files and directories made available
    for the current request.
    """

    __public_apis__ = ['ls', 'glob', 'grep', 'read', 'info']

    def _get_scopes(self) -> List[LocalFSScope]:
        config = lazyllm.globals.get('agentic_config') or {}
        sources = config.get('local_fs_sources') or []
        if not isinstance(sources, list):
            return []

        scopes: List[LocalFSScope] = []
        for source in sources:
            if not isinstance(source, dict):
                continue
            source_id = source.get('source_id')
            paths = source.get('paths')
            file_extensions = source.get('file_extensions')
            if not isinstance(source_id, str) or not isinstance(paths, list) or not isinstance(file_extensions, list):
                continue
            roots = tuple(path for path in paths if isinstance(path, str) and path.strip())
            extensions = frozenset(ext for ext in file_extensions if isinstance(ext, str) and ext.strip())
            if roots and extensions:
                scopes.append(LocalFSScope(source_id=source_id, roots=roots, file_extensions=extensions))
        return scopes

    def __key_source__(self) -> Any:
        return self._get_scopes()

    def _resolve_with_scope(self, target: str) -> tuple[str, LocalFSScope]:
        """Resolve *target* to an absolute path within a configured source.

        Raises:
            PermissionError: if *target* is outside the allowed set.
        """
        scopes = self._get_scopes()
        if not scopes:
            raise PermissionError('No local filesystem paths are configured')
        target = os.path.realpath(target)
        for scope in scopes:
            for root in scope.roots:
                base = os.path.realpath(root)
                try:
                    if os.path.commonpath([base, target]) == base:
                        return target, scope
                except ValueError:
                    continue
        roots = [root for scope in scopes for root in scope.roots]
        raise PermissionError(f'Path {target} is not within allowed paths: {roots}')

    def _resolve_dir(self, path: str) -> tuple[str, LocalFSScope]:
        resolved, scope = self._resolve_with_scope(path)
        if not os.path.isdir(resolved):
            raise ValueError(f'Path is not a directory: {path}')
        return resolved, scope

    def _iter_roots(self, path: Optional[str]) -> list[tuple[str, LocalFSScope]]:
        scopes = self._get_scopes()
        if not scopes:
            raise PermissionError('No local filesystem paths are configured')
        if path is None or str(path).strip() in ('', '.'):
            roots: list[tuple[str, LocalFSScope]] = []
            for scope in scopes:
                for root in scope.roots:
                    resolved = os.path.realpath(root)
                    if os.path.isdir(resolved):
                        roots.append((resolved, scope))
            return roots
        return [self._resolve_dir(str(path))]

    @staticmethod
    def _file_extension(path: str) -> str:
        return os.path.splitext(path)[1].lower().lstrip('.')

    def _is_visible_file(self, scope: LocalFSScope, path: str) -> bool:
        return self._file_extension(path) in scope.file_extensions

    def _ensure_visible_file(self, scope: LocalFSScope, path: str) -> None:
        if not self._is_visible_file(scope, path):
            raise PermissionError(f'File extension is not allowed: {path}')

    def _resolve_visible_file(self, path: str) -> Optional[tuple[str, LocalFSScope]]:
        try:
            resolved, scope = self._resolve_with_scope(path)
            if os.path.isfile(resolved) and self._is_visible_file(scope, resolved):
                return resolved, scope
        except OSError:
            return None
        return None

    def _resolve_visible_file_for_scope(self, path: str, scope: LocalFSScope) -> Optional[str]:
        visible = self._resolve_visible_file(path)
        if not visible:
            return None
        resolved, resolved_scope = visible
        if resolved_scope != scope:
            return None
        return resolved

    def _entry(self, path: str, scope: LocalFSScope) -> Dict[str, Any]:
        st = os.stat(path)
        return {
            'name': os.path.basename(path),
            'path': path,
            'type': 'directory' if os.path.isdir(path) else 'file',
            'source_id': scope.source_id,
            'size': st.st_size,
            'mtime': datetime.datetime.fromtimestamp(st.st_mtime).isoformat(),
        }

    @staticmethod
    def _has_rg() -> bool:
        return bool(_RG_BINARY)

    @staticmethod
    def _run_rg(args: List[str], cwd: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [_RG_BINARY] + args,
            capture_output=True, text=True, timeout=_RG_TIMEOUT, cwd=cwd,
        )

    def ls(self, path: Optional[str] = None, max_entries: int = 200) -> Dict[str, Any]:
        """List available local directories or one directory level.

        Args:
            path: Directory path. When omitted, lists available local root directories.
            max_entries: Maximum entries to return, default 200.

        Returns:
            A directory listing with entry paths, types, sizes, update times,
            and pagination metadata.
        """
        entries: List[Dict[str, Any]] = []
        limit = max(1, max_entries)

        if path is None or str(path).strip() in ('', '.'):
            for root, scope in self._iter_roots(None):
                entries.append(self._entry(root, scope))
                if len(entries) >= limit:
                    break
            return tool_success('ls', {
                'path': None,
                'entry_count': len(entries),
                'truncated': len(entries) >= limit,
                'max_entries': limit,
                'entries': entries,
            })

        safe_dir, scope = self._resolve_dir(str(path))
        with os.scandir(safe_dir) as iterator:
            for entry in sorted(iterator, key=lambda item: item.name):
                try:
                    entry_path, entry_scope = self._resolve_with_scope(entry.path)
                    if entry.is_dir(follow_symlinks=True):
                        entries.append(self._entry(entry_path, entry_scope))
                    elif entry.is_file(follow_symlinks=True) and self._is_visible_file(entry_scope, entry_path):
                        entries.append(self._entry(entry_path, entry_scope))
                except OSError:
                    continue
                if len(entries) >= limit:
                    break

        return tool_success('ls', {
            'path': safe_dir,
            'source_id': scope.source_id,
            'entry_count': len(entries),
            'truncated': len(entries) >= limit,
            'max_entries': limit,
            'entries': entries,
        })

    def glob(self, pattern: str, path: Optional[str] = None) -> Dict[str, Any]:
        """Find local files whose names match a glob pattern.

        Args:
            pattern: Glob pattern, e.g. ``**/*.pdf`` or ``*.csv``.
            path: Optional directory returned by ls. Omit path to search all
                available local directories; do not pass a shared parent directory.

        Returns:
            A list of matching local file paths.
        """
        matches: List[str] = []
        for safe_dir, scope in self._iter_roots(path):
            if self._has_rg():
                proc = self._run_rg(['--files', '--no-ignore', '--hidden', '--glob', pattern], cwd=safe_dir)
                if proc.returncode > 1:
                    return tool_error('glob', f'ripgrep glob failed: {proc.stderr.strip() or "unknown error"}')
                raw = [os.path.join(safe_dir, p) for p in proc.stdout.splitlines() if p.strip()]
            else:
                py_pattern = pattern if '**' in pattern else f'**/{pattern}'
                raw = [os.path.join(safe_dir, p) for p in _glob.glob(py_pattern, root_dir=safe_dir, recursive=True)]
            for fpath in raw:
                resolved = self._resolve_visible_file_for_scope(fpath, scope)
                if resolved:
                    matches.append(resolved)
        matches.sort()
        return tool_success('glob', {
            'pattern': pattern,
            'path': path,
            'match_count': len(matches),
            'matches': matches[:200],
        })

    def grep(
        self,
        pattern: str,
        path: Optional[str] = None,
        glob: str = '*',
        max_results: int = 50,
    ) -> Dict[str, Any]:
        """Search text within available local files.

        Args:
            pattern: Regex search pattern.
            path: Optional directory returned by ls. Omit path to search all
                available local directories; do not pass a shared parent directory.
            glob: Filename filter (only search matching files), default ``*``.
            max_results: Maximum results to return, default 50.

        Returns:
            Matching lines with file path, line number, and text snippet.
        """
        matches: List[Dict[str, Any]] = []
        for safe_dir, scope in self._iter_roots(path):
            if self._has_rg():
                result = self._grep_rg(pattern, safe_dir, scope, glob, max_results - len(matches))
            else:
                result = self._grep_py(pattern, safe_dir, scope, glob, max_results - len(matches))
            if not result.get('success'):
                return result
            matches.extend(result.get('result', {}).get('matches', []))
            if len(matches) >= max_results:
                break
        return tool_success('grep', {
            'pattern': pattern,
            'path': path,
            'match_count': len(matches),
            'matches': matches,
        })

    def _grep_rg(
        self, pattern: str, safe_dir: str, scope: LocalFSScope, glob_filter: str, max_results: int,
    ) -> Dict[str, Any]:
        if max_results <= 0:
            return tool_success('grep', {'matches': []})
        args = ['--json', '--no-heading', '--no-ignore', '--hidden', '-g', glob_filter, '--', pattern]
        try:
            proc = self._run_rg(args, cwd=safe_dir)
        except subprocess.TimeoutExpired:
            return tool_error('grep', f'Search timed out (>{_RG_TIMEOUT}s)', error_type='Timeout')

        if proc.returncode > 1:
            return tool_error('grep', f'ripgrep search failed: {proc.stderr.strip() or "unknown error"}')

        matches: List[Dict[str, Any]] = []
        for line in proc.stdout.splitlines():
            if not line.strip():
                continue
            try:
                entry = json.loads(line)
            except json.JSONDecodeError:
                continue
            if entry.get('type') != 'match':
                continue
            data = entry.get('data', {})
            fpath = os.path.join(safe_dir, data.get('path', {}).get('text', ''))
            resolved = self._resolve_visible_file_for_scope(fpath, scope)
            if not resolved:
                continue
            content = data.get('lines', {}).get('text', '').rstrip()
            lineno = data.get('line_number', 0)
            matches.append({
                'file': resolved,
                'source_id': scope.source_id,
                'line': lineno,
                'content': content[:500],
            })
            if len(matches) >= max_results:
                break

        return tool_success('grep', {
            'pattern': pattern,
            'path': safe_dir,
            'match_count': len(matches),
            'matches': matches,
        })

    def _grep_py(
        self, pattern: str, safe_dir: str, scope: LocalFSScope, glob_filter: str, max_results: int,
    ) -> Dict[str, Any]:
        if max_results <= 0:
            return tool_success('grep', {'matches': []})
        try:
            regex = re.compile(pattern)
        except re.error as exc:
            return tool_error('grep', f'Invalid regex: {exc}')

        matches: List[Dict[str, Any]] = []
        for root, _dirs, files in os.walk(safe_dir):
            for fn in files:
                if not fnmatch.fnmatch(fn, glob_filter):
                    continue
                fpath = os.path.join(root, fn)
                resolved = self._resolve_visible_file_for_scope(fpath, scope)
                if not resolved:
                    continue
                try:
                    with open(resolved, 'r', encoding='utf-8', errors='replace') as fh:
                        for lineno, line in enumerate(fh, 1):
                            if regex.search(line):
                                matches.append({
                                    'file': resolved,
                                    'source_id': scope.source_id,
                                    'line': lineno,
                                    'content': line.rstrip()[:500],
                                })
                                if len(matches) >= max_results:
                                    break
                except OSError:
                    continue
                if len(matches) >= max_results:
                    break
            if len(matches) >= max_results:
                break

        return tool_success('grep', {
            'pattern': pattern,
            'path': safe_dir,
            'match_count': len(matches),
            'matches': matches,
        })

    def read(
        self,
        filepath: str,
        start_line: int = 0,
        max_lines: int = 500,
    ) -> Dict[str, Any]:
        """Read text content from an available local file.

        Args:
            filepath: Local file path to read.
            start_line: Starting line number (0-based), default 0.
            max_lines: Maximum lines to read, default 500.

        Returns:
            File content plus line range and total line count metadata.
        """
        safe_path, scope = self._resolve_with_scope(filepath)
        if not os.path.isfile(safe_path):
            return tool_error('read', f'File not found: {filepath}')
        self._ensure_visible_file(scope, safe_path)

        try:
            with open(safe_path, 'r', encoding='utf-8', errors='replace') as fh:
                chunk: List[str] = []
                total = 0
                for index, line in enumerate(fh):
                    total += 1
                    if start_line <= index < start_line + max_lines:
                        chunk.append(line)
        except OSError as exc:
            return tool_error('read', f'Cannot read file: {exc}')

        return tool_success('read', {
            'filepath': safe_path,
            'source_id': scope.source_id,
            'total_lines': total,
            'start_line': start_line,
            'end_line': start_line + len(chunk),
            'content': ''.join(chunk),
        })

    def info(self, path: Optional[str] = None) -> Dict[str, Any]:
        """Get metadata for an available local file or directory.

        Args:
            path: Local file or directory path. When omitted, returns metadata for
                available local root directories.

        Returns:
            File or directory metadata such as path, type, size, and update time.
        """
        if path is None or str(path).strip() in ('', '.'):
            entries = [self._entry(root, scope) for root, scope in self._iter_roots(None)]
            return tool_success('info', {'path': None, 'entries': entries})
        else:
            safe_path, scope = self._resolve_with_scope(str(path))
            if os.path.isfile(safe_path):
                self._ensure_visible_file(scope, safe_path)

        try:
            st = os.stat(safe_path)
        except OSError as exc:
            return tool_error('info', f'Cannot get file info: {exc}')

        return tool_success('info', {
            'path': safe_path,
            'type': 'directory' if os.path.isdir(safe_path) else 'file',
            'source_id': scope.source_id,
            'size': st.st_size,
            'mtime': datetime.datetime.fromtimestamp(st.st_mtime).isoformat(),
        })
