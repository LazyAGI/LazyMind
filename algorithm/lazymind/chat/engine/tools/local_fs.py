# Copyright (c) 2026 LazyAGI. All rights reserved.
"""Read-only local filesystem tool group.

Activated only when ``tool_config`` includes a ``localfs_paths`` whitelist.
All operations are constrained to the whitelisted paths.

Backend: prefers ripgrep_ (``rg``) for ``grep`` and ``glob`` when available;
falls back to Python stdlib otherwise.  The two backends differ in edge-case
behaviour (hidden files, .gitignore, binary-file detection, regex dialect) —
rg is the primary path and Python is a best-effort fallback.

.. _ripgrep: https://github.com/BurntSushi/ripgrep
"""
from __future__ import annotations

import datetime
import fnmatch
import glob as _glob
import json
import os
import re
import shutil
import subprocess
from typing import Any, Dict, List, Optional

import lazyllm

from lazymind.chat.engine.tools.infra import handle_tool_errors, tool_error, tool_success

_RG_BINARY = shutil.which('rg') or ''
_RG_TIMEOUT = 30


class LocalFSToolGroup:
    """Read-only local filesystem tools.

    Activated when ``tool_config`` contains ``{"localfs_paths": ["/path1", ...]}``.
    The whitelist is stored in ``agentic_config['localfs_paths']`` and every
    operation validates the target path against it.
    """

    __public_apis__ = ['glob', 'grep', 'read', 'info']

    def _get_allowed_paths(self) -> List[str]:
        config = lazyllm.globals.get('agentic_config') or {}
        paths = config.get('localfs_paths') or []
        return [paths] if isinstance(paths, str) else paths

    def __key_source__(self) -> Any:
        return self._get_allowed_paths()

    def _resolve(self, target: str) -> str:
        """Resolve *target* to an absolute path within the whitelist.

        Raises:
            PermissionError: if *target* is outside the allowed set.
        """
        allowed = self._get_allowed_paths()
        if not allowed:
            raise PermissionError('No local filesystem paths are configured')
        target = os.path.realpath(target)
        for base in allowed:
            base = os.path.realpath(base)
            try:
                if os.path.commonpath([base, target]) == base:
                    return target
            except ValueError:
                continue
        raise PermissionError(f'Path {target} is not within allowed paths: {allowed}')

    def _resolve_dir(self, path: Optional[str]) -> str:
        """Resolve *path* to a directory within the whitelist.

        When *path* is ``None`` or ``"."``, falls back to the first allowed path
        that is a directory (or the first allowed path).
        """
        allowed = self._get_allowed_paths()
        if not allowed:
            raise PermissionError('No local filesystem paths are configured')
        if path is None or str(path).strip() in ('', '.'):
            for base in allowed:
                abs_base = os.path.realpath(base)
                if os.path.isdir(abs_base):
                    return abs_base
            return os.path.realpath(allowed[0])
        resolved = self._resolve(str(path))
        if not os.path.isdir(resolved):
            raise ValueError(f'Path is not a directory: {path}')
        return resolved

    @staticmethod
    def _has_rg() -> bool:
        return bool(_RG_BINARY)

    @staticmethod
    def _run_rg(args: List[str], cwd: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [_RG_BINARY] + args,
            capture_output=True, text=True, timeout=_RG_TIMEOUT, cwd=cwd,
        )

    @handle_tool_errors
    def glob(self, pattern: str, path: Optional[str] = None) -> Dict[str, Any]:
        """Match files by glob pattern within allowed paths.

        Args:
            pattern: Glob pattern, e.g. ``**/*.py``, ``*.md``.
            path: Search root directory; defaults to the first allowed path.

        Returns:
            dict with ``pattern``, ``path``, ``match_count``, ``matches``.
        """
        safe_dir = self._resolve_dir(path)
        if self._has_rg():
            proc = self._run_rg(['--files', '--glob', pattern], cwd=safe_dir)
            if proc.returncode > 1:
                return tool_error('glob', f'ripgrep glob failed: {proc.stderr.strip() or "unknown error"}')
            raw = [os.path.join(safe_dir, p) for p in proc.stdout.splitlines() if p.strip()]
        else:
            py_pattern = pattern if '**' in pattern else f'**/{pattern}'
            raw = _glob.glob(py_pattern, root_dir=safe_dir, recursive=True)
        raw.sort()
        return tool_success('glob', {
            'pattern': pattern,
            'path': safe_dir,
            'match_count': len(raw),
            'matches': raw[:200],
        })

    @handle_tool_errors
    def grep(
        self,
        pattern: str,
        path: Optional[str] = None,
        glob: str = '*',
        max_results: int = 50,
    ) -> Dict[str, Any]:
        """Recursively search file contents within allowed paths.

        Args:
            pattern: Regex search pattern (ripgrep dialect by default; Python
                fallback also supported).
            path: Search root directory; defaults to the first allowed path.
            glob: Filename filter (only search matching files), default ``*``.
            max_results: Maximum results to return, default 50.

        Returns:
            dict with ``pattern``, ``path``, ``match_count``, ``matches``.
        """
        safe_dir = self._resolve_dir(path)
        if self._has_rg():
            return self._grep_rg(pattern, safe_dir, glob, max_results)
        return self._grep_py(pattern, safe_dir, glob, max_results)

    def _grep_rg(
        self, pattern: str, safe_dir: str, glob_filter: str, max_results: int,
    ) -> Dict[str, Any]:
        args = ['--json', '--no-heading', '-g', glob_filter, '--', pattern]
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
            content = data.get('lines', {}).get('text', '').rstrip()
            lineno = data.get('line_number', 0)
            matches.append({
                'file': fpath,
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
        self, pattern: str, safe_dir: str, glob_filter: str, max_results: int,
    ) -> Dict[str, Any]:
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
                try:
                    with open(fpath, 'r', encoding='utf-8', errors='replace') as fh:
                        for lineno, line in enumerate(fh, 1):
                            if regex.search(line):
                                matches.append({
                                    'file': fpath,
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

    @handle_tool_errors
    def read(
        self,
        filepath: str,
        start_line: int = 0,
        max_lines: int = 500,
    ) -> Dict[str, Any]:
        """Read text file contents within allowed paths.

        Args:
            filepath: File path (must be within the whitelist).
            start_line: Starting line number (0-based), default 0.
            max_lines: Maximum lines to read, default 500.

        Returns:
            dict with ``filepath``, ``total_lines``, ``start_line``,
            ``end_line``, ``content``.
        """
        safe_path = self._resolve(filepath)
        if not os.path.isfile(safe_path):
            return tool_error('read', f'File not found: {filepath}')

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
            'total_lines': total,
            'start_line': start_line,
            'end_line': start_line + len(chunk),
            'content': ''.join(chunk),
        })

    @handle_tool_errors
    def info(self, path: Optional[str] = None) -> Dict[str, Any]:
        """Get metadata for a file or directory.

        Args:
            path: Path (must be within the whitelist); defaults to the first
                allowed path.

        Returns:
            dict with ``path``, ``type``, ``size``, ``mtime``.
        """
        if path is None or str(path).strip() in ('', '.'):
            allowed = self._get_allowed_paths()
            if not allowed:
                raise PermissionError('No local filesystem paths are configured')
            safe_path = os.path.realpath(allowed[0])
        else:
            safe_path = self._resolve(str(path))

        try:
            st = os.stat(safe_path)
        except OSError as exc:
            return tool_error('info', f'Cannot get file info: {exc}')

        return tool_success('info', {
            'path': safe_path,
            'type': 'directory' if os.path.isdir(safe_path) else 'file',
            'size': st.st_size,
            'mtime': datetime.datetime.fromtimestamp(st.st_mtime).isoformat(),
        })
