# Copyright (c) 2026 LazyAGI. All rights reserved.
"""Tools for reading, searching, and safely editing local text files.

Provides local directory listing, filename search, text search, file reading,
and file metadata lookup for local files made available to the current request.

Backend: prefers ripgrep_ (``rg``) for ``grep`` and ``glob`` when available;
falls back to Python stdlib otherwise.  The two backends differ in edge-case
behaviour (hidden files, .gitignore, binary-file detection, regex dialect) —
rg is the primary path and Python is a best-effort fallback.

.. _ripgrep: https://github.com/BurntSushi/ripgrep
"""
from __future__ import annotations

import copy
import datetime
from dataclasses import dataclass
import fnmatch
import glob as _glob
import json
import os
import re
import shutil
import subprocess
import time
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.text_edit import replace_exact_text_file
from lazymind.chat.engine.tools.infra.core_api_client import get_core_api, post_core_api

_RG_BINARY = shutil.which('rg') or ''
_RG_TIMEOUT = 30


@dataclass(frozen=True)
class LocalFSScope:
    source_id: str
    roots: tuple[str, ...]
    file_extensions: frozenset[str]


class LocalFileToolkit:
    """Tools for listing, searching, reading, and safely editing local text files.

    The tools can access only the local files and directories made available
    for the current request.
    """

    __public_apis__ = ['ls', 'glob', 'grep', 'read', 'string_replace', 'create', 'overwrite', 'append', 'delete', 'mkdir', 'info']

    def _get_scopes(self) -> List[LocalFSScope]:
        if hasattr(self, '_scope_override'):
            return self._scope_override
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

    @staticmethod
    def _workspace_context() -> Optional[Dict[str, Any]]:
        config = lazyllm.globals.get('agentic_config') or {}
        context = config.get('_core_workspace_context') or config.get('workspace_context')
        if not isinstance(context, dict) or not context.get('workspace_id'):
            parent = config.get('parent_agentic_config')
            context = parent.get('_core_workspace_context') if isinstance(parent, dict) else None
        workspace_id = str(context.get('workspace_id') or '').strip() if isinstance(context, dict) else ''
        if not workspace_id:
            for source in config.get('local_fs_sources') or []:
                source_id = str(source.get('source_id') or '').strip() if isinstance(source, dict) else ''
                if source_id.startswith('local-workspace:'):
                    workspace_id = source_id.removeprefix('local-workspace:').strip()
                    break
        user_id = str(config.get('user_id') or '').strip()
        conversation_id = str(config.get('conversation_id') or '').strip()
        if not workspace_id or not user_id or not conversation_id:
            return None
        return {
            'workspace_id': workspace_id,
            'permission_mode': str(context.get('permission_mode') or '').strip() if isinstance(context, dict) else '',
            'permission_version': context.get('permission_version') if isinstance(context, dict) else None,
            'user_id': user_id,
            'conversation_id': conversation_id,
        }

    def _has_workspace_source(self) -> bool:
        return any(self._workspace_scope(scope) for scope in self._get_scopes())

    @staticmethod
    def _workspace_scope(scope: LocalFSScope) -> bool:
        return scope.source_id.startswith('local-workspace:')

    def execute_workspace_call(self, method: str, arguments: Dict[str, Any], *,
                               call_id: str, tool_name: str, identity: Dict[str, Any],
                               cancel_check: Any, versions: Dict[str, str]) -> Any:
        # Registry instances are shared. Keep this invocation's identity out of them.
        invocation = copy.copy(self)
        invocation._workspace_call = (call_id, tool_name, dict(identity), cancel_check)
        invocation._observed_versions = versions
        invocation._operation_sequence = 0
        invocation._execution_started = False
        try:
            return getattr(invocation, method)(**arguments)
        except Exception as error:
            error.workspace_execution_started = invocation._execution_started
            raise

    def _core_path(self, filepath: str, scope: LocalFSScope) -> str:
        target, selected = self._resolve_with_scope(filepath)
        if selected != scope or not self._workspace_scope(scope):
            raise ToolExecutionError('path is not within the selected workspace source')
        for root in scope.roots:
            relative = os.path.relpath(target, os.path.abspath(root))
            if relative != os.pardir and not relative.startswith(os.pardir + os.sep):
                return relative.replace(os.sep, '/')
        raise ToolExecutionError('path is not within the selected workspace source')

    @staticmethod
    def _core_result(response: Dict[str, Any]) -> Dict[str, Any]:
        envelope = response.get('response') if isinstance(response, dict) else None
        body = envelope if isinstance(envelope, dict) else response
        if not isinstance(body, dict):
            return {}
        data = body if 'operation_id' in body else body.get('data', body)
        return data if isinstance(data, dict) else {}

    def _core_operation(self, scope: LocalFSScope, operation: str, path: str, **parameters: Any) -> Dict[str, Any]:
        context = self._workspace_context()
        invocation = getattr(self, '_workspace_call', None)
        if context is None or invocation is None:
            raise ToolExecutionError('workspace operation requires the prepared execution context')
        call_id, tool_name, identity, cancel_check = invocation
        if not ((identity.get('history_id') and identity.get('run_id'))
                or (identity.get('task_id') and identity.get('generation'))):
            raise ToolExecutionError('workspace run identity is unavailable')
        relative = self._core_path(path, scope)
        version_key = scope.source_id + ':' + relative
        if operation in {'replace', 'overwrite', 'append', 'delete'} and not parameters.get('expected_version'):
            parameters['expected_version'] = self._observed_versions.get(version_key, '')
            if not parameters['expected_version']:
                raise ToolExecutionError('read the file before editing it, or supply its observed expected_version')
        self._operation_sequence += 1
        payload = {
            **context, **identity, **parameters,
            'call_id': f'{int(time.time() * 1000)}/{call_id}:{self._operation_sequence}',
            'tool_name': tool_name, 'operation': operation, 'path': relative,
        }
        base = f"internal/conversations/{context['conversation_id']}/workspace-operations"
        if cancel_check is not None:
            cancel_check(None)
        prepared = self._core_result(post_core_api(base + ':prepare', payload))
        operation_id = str(prepared.get('operation_id') or '').strip()
        if not operation_id:
            raise ToolExecutionError('Core did not return an operation id')
        deadline = float(prepared.get('expires_at') or 0) / 1000
        while prepared.get('status') == 'preparing' or (prepared.get('decision') == 'pending' and prepared.get('status') == 'pending'):
            if cancel_check is not None:
                cancel_check(None)
            if not deadline or time.time() >= deadline:
                raise ToolExecutionError('workspace approval expired')
            time.sleep(min(1.0, max(0.0, deadline - time.time())))
            if cancel_check is not None:
                cancel_check(None)
            prepared = self._core_result(get_core_api(base + '/' + operation_id, {key: value for key, value in identity.items() if key != 'lease_token'}))
        if prepared.get('decision') != 'allowed' or prepared.get('status') not in {'allowed', 'completed'}:
            raise ToolExecutionError(f'workspace operation {prepared.get("status") or "denied"}')
        if cancel_check is not None:
            cancel_check(None)
        self._execution_started = True
        executed = self._core_result(post_core_api(base + '/' + operation_id + ':execute', payload))
        if executed.get('status') != 'completed':
            raise ToolExecutionError(f'workspace execution {executed.get("status") or "unavailable"}')
        receipt = executed.get('receipt') is True
        if receipt and operation in {'read', 'ls', 'glob', 'grep', 'info'}:
            raise ToolExecutionError('workspace observation already completed; its content is not replayed')
        if operation == 'delete':
            self._observed_versions.pop(version_key, None)
        elif operation in {'read', 'create', 'replace', 'overwrite', 'append'} and executed.get('version'):
            self._observed_versions[version_key] = str(executed['version'])
        return executed

    def _bound_scope_for_file(self, filepath: str) -> Optional[LocalFSScope]:
        if not self._has_workspace_source():
            return None
        _, scope = self._resolve_with_scope(filepath)
        return scope if self._workspace_scope(scope) else None

    def _resolve_with_scope(self, target: str) -> tuple[str, LocalFSScope]:
        scopes = self._get_scopes()
        if not scopes:
            raise ToolExecutionError('No local filesystem paths are configured')
        workspace_scopes = [scope for scope in scopes if self._workspace_scope(scope)]
        excluded_workspace = getattr(self, '_excluded_workspace', ())
        if workspace_scopes and not os.path.isabs(target) and os.pardir in target.replace('\\', '/').split('/'):
            raise ToolExecutionError('workspace paths cannot contain parent traversal')
        # Workspace paths are lexical: Core alone resolves links and touches the disk.
        for scope in workspace_scopes:
            for root in scope.roots:
                base = os.path.abspath(root)
                candidate = os.path.abspath(target if os.path.isabs(target) else os.path.join(base, target))
                try:
                    if os.path.commonpath([base, candidate]) == base:
                        return candidate, scope
                except ValueError:
                    continue
        if all(self._workspace_scope(scope) for scope in scopes):
            raise ToolExecutionError('path is outside the selected workspace')
        target = os.path.realpath(target)
        for scope in scopes:
            if self._workspace_scope(scope):
                continue
            for root in scope.roots:
                base = os.path.realpath(root)
                try:
                    if os.path.commonpath([base, target]) == base:
                        # An ordinary-source alias must not become a workspace bypass.
                        if any(os.path.commonpath([os.path.abspath(r), target]) == os.path.abspath(r)
                               for ws in [*workspace_scopes, *excluded_workspace] for r in ws.roots):
                            raise ToolExecutionError('workspace aliases require a workspace path')
                        return target, scope
                except ValueError:
                    continue
        raise ToolExecutionError(f'Path {target} is not within the configured sources')

    def _resolve_dir(self, path: str) -> tuple[str, LocalFSScope]:
        resolved, scope = self._resolve_with_scope(path)
        if self._workspace_scope(scope):
            raise ToolExecutionError('workspace directories must be queried through Core')
        if not os.path.isdir(resolved):
            raise ToolExecutionError(f'Path is not a directory: {path}')
        return resolved, scope

    def _iter_roots(self, path: Optional[str]) -> list[tuple[str, LocalFSScope]]:
        if path is not None and str(path).strip() not in ('', '.'):
            return [self._resolve_dir(str(path))]
        return [(os.path.realpath(root), scope) for scope in self._get_scopes()
                if not self._workspace_scope(scope) for root in scope.roots if os.path.isdir(root)]

    def _workspace_discovery(self, method: str, path: Optional[str], **arguments: Any) -> Optional[Dict[str, Any]]:
        scopes = self._get_scopes()
        workspace = [scope for scope in scopes if self._workspace_scope(scope)]
        if not workspace:
            return None
        all_roots = path is None or str(path).strip() in ('', '.')
        if not all_roots:
            resolved, scope = self._resolve_with_scope(str(path))
            if not self._workspace_scope(scope):
                return None
            targets = [(resolved, scope)]
        else:
            targets = [(root, scope) for scope in workspace for root in scope.roots]
        field = 'entries' if method in {'ls', 'info'} else 'matches'
        combined: Dict[str, Any] = {'path': path, field: []}
        for target, scope in targets:
            operation = 'info' if all_roots and method in {'ls', 'info'} else method
            result = self._core_operation(scope, operation, target,
                                          pattern=arguments.get('pattern', ''), glob=arguments.get('glob', '*'),
                                          limit=arguments.get('max_entries', arguments.get('max_results', 200)))
            data = result.get('data') if isinstance(result.get('data'), dict) else result
            if method == 'info' and not all_roots:
                return {**data, 'source_id': scope.source_id}
            combined.setdefault('skipped', []).extend(data.get('skipped', []))
            combined['truncated'] = combined.get('truncated', False) or bool(data.get('truncated'))
            values = [data] if operation == 'info' else data.get(field, [])
            for value in values:
                if isinstance(value, dict):
                    value = {**value, 'source_id': scope.source_id}
                combined[field].append(value)
        ordinary = [scope for scope in scopes if not self._workspace_scope(scope)]
        if all_roots and ordinary:
            local = copy.copy(self)
            local._scope_override = ordinary
            local._excluded_workspace = workspace
            combined[field].extend(getattr(local, method)(path=path, **arguments).get(field, []))
        if field == 'matches':
            limit = max(1, arguments.get('max_results', 200))
            combined.update(pattern=arguments.get('pattern', ''), match_count=min(len(combined[field]), limit),
                            truncated=combined.get('truncated', False) or len(combined[field]) > limit)
            combined[field] = combined[field][:limit]
        else:
            limit = max(1, arguments.get('max_entries', 200))
            combined.update(entry_count=min(len(combined[field]), limit), max_entries=limit,
                            truncated=combined.get('truncated', False) or len(combined[field]) > limit)
            combined[field] = combined[field][:limit]
        return combined

    @staticmethod
    def _file_extension(path: str) -> str:
        return os.path.splitext(path)[1].lower().lstrip('.')

    def _is_visible_file(self, scope: LocalFSScope, path: str) -> bool:
        return self._file_extension(path) in scope.file_extensions

    def _ensure_visible_file(self, scope: LocalFSScope, path: str) -> None:
        if not self._is_visible_file(scope, path):
            raise ToolExecutionError(f'File extension is not allowed: {path}')

    def _resolve_visible_file(self, path: str) -> Optional[tuple[str, LocalFSScope]]:
        try:
            resolved, scope = self._resolve_with_scope(path)
            if self._workspace_scope(scope):
                return None
            if os.path.isfile(resolved) and self._is_visible_file(scope, resolved):
                return resolved, scope
        except (OSError, ToolExecutionError):
            return None
        return None

    def _resolve_visible_file_for_scope(self, path: str, scope: LocalFSScope) -> Optional[str]:
        visible = self._resolve_visible_file(path)
        if not visible:
            return None
        resolved, resolved_scope = visible
        if resolved_scope != scope or self._workspace_scope(resolved_scope):
            return None
        return resolved

    def _entry(self, path: str, scope: LocalFSScope) -> Dict[str, Any]:
        if self._workspace_scope(scope):
            raise ToolExecutionError('workspace metadata must be queried through Core')
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
        remote = self._workspace_discovery('ls', path, max_entries=max_entries)
        if remote is not None:
            return remote
        entries: List[Dict[str, Any]] = []
        limit = max(1, max_entries)

        if path is None or str(path).strip() in ('', '.'):
            for root, scope in self._iter_roots(None):
                entries.append(self._entry(root, scope))
                if len(entries) >= limit:
                    break
            return {
                'path': None,
                'entry_count': len(entries),
                'truncated': len(entries) >= limit,
                'max_entries': limit,
                'entries': entries,
            }

        safe_dir, scope = self._resolve_dir(str(path))
        with os.scandir(safe_dir) as iterator:
            for entry in sorted(iterator, key=lambda item: item.name):
                try:
                    entry_path, entry_scope = self._resolve_with_scope(entry.path)
                    if entry.is_dir(follow_symlinks=True):
                        entries.append(self._entry(entry_path, entry_scope))
                    elif entry.is_file(follow_symlinks=True) and self._is_visible_file(entry_scope, entry_path):
                        entries.append(self._entry(entry_path, entry_scope))
                except (OSError, ToolExecutionError):
                    continue
                if len(entries) >= limit:
                    break

        return {
            'path': safe_dir,
            'source_id': scope.source_id,
            'entry_count': len(entries),
            'truncated': len(entries) >= limit,
            'max_entries': limit,
            'entries': entries,
        }

    def glob(self, pattern: str, path: Optional[str] = None) -> Dict[str, Any]:
        """Find local files whose names match a glob pattern.

        Args:
            pattern: Glob pattern, e.g. ``**/*.pdf`` or ``*.csv``.
            path: Optional directory returned by ls. Omit path to search all
                available local directories; do not pass a shared parent directory.

        Returns:
            A list of matching local file paths.
        """
        remote = self._workspace_discovery('glob', path, pattern=pattern)
        if remote is not None:
            return remote
        matches: List[str] = []
        for safe_dir, scope in self._iter_roots(path):
            if self._has_rg():
                proc = self._run_rg(['--files', '--no-ignore', '--hidden', '--glob', pattern], cwd=safe_dir)
                if proc.returncode > 1:
                    raise ToolExecutionError(
                        f'ripgrep glob failed: {proc.stderr.strip() or "unknown error"}'
                    )
                raw = [os.path.join(safe_dir, p) for p in proc.stdout.splitlines() if p.strip()]
            else:
                py_pattern = pattern if '**' in pattern else f'**/{pattern}'
                raw = [os.path.join(safe_dir, p) for p in _glob.glob(py_pattern, root_dir=safe_dir, recursive=True)]
            for fpath in raw:
                resolved = self._resolve_visible_file_for_scope(fpath, scope)
                if resolved:
                    matches.append(resolved)
        matches.sort()
        return {
            'pattern': pattern,
            'path': path,
            'match_count': len(matches),
            'matches': matches[:200],
        }

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
        remote = self._workspace_discovery('grep', path, pattern=pattern, glob=glob, max_results=max_results)
        if remote is not None:
            return remote
        matches: List[Dict[str, Any]] = []
        for safe_dir, scope in self._iter_roots(path):
            if self._has_rg() and not (self._has_workspace_source() or getattr(self, '_excluded_workspace', ())):
                result = self._grep_rg(pattern, safe_dir, scope, glob, max_results - len(matches))
            else:
                result = self._grep_py(pattern, safe_dir, scope, glob, max_results - len(matches))
            matches.extend(result.get('matches', []))
            if len(matches) >= max_results:
                break
        return {
            'pattern': pattern,
            'path': path,
            'match_count': len(matches),
            'matches': matches,
        }

    def _grep_rg(
        self, pattern: str, safe_dir: str, scope: LocalFSScope, glob_filter: str, max_results: int,
    ) -> Dict[str, Any]:
        if max_results <= 0:
            return {'matches': []}
        args = ['--json', '--no-heading', '--no-ignore', '--hidden', '-g', glob_filter, '--', pattern]
        try:
            proc = self._run_rg(args, cwd=safe_dir)
        except subprocess.TimeoutExpired:
            raise ToolExecutionError(
                f'Search timed out after {_RG_TIMEOUT} seconds.'
            )

        if proc.returncode > 1:
            raise ToolExecutionError(
                f'ripgrep search failed: {proc.stderr.strip() or "unknown error"}'
            )

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

        return {
            'pattern': pattern,
            'path': safe_dir,
            'match_count': len(matches),
            'matches': matches,
        }

    def _grep_py(
        self, pattern: str, safe_dir: str, scope: LocalFSScope, glob_filter: str, max_results: int,
    ) -> Dict[str, Any]:
        if max_results <= 0:
            return {'matches': []}
        try:
            regex = re.compile(pattern)
        except re.error as exc:
            raise ToolExecutionError(f'Invalid regex: {exc}') from exc

        matches: List[Dict[str, Any]] = []
        excluded = [*getattr(self, '_excluded_workspace', ()),
                    *(scope for scope in self._get_scopes() if self._workspace_scope(scope))]
        for root, dirs, files in os.walk(safe_dir):
            dirs[:] = [name for name in dirs if not any(
                os.path.commonpath([os.path.abspath(base), os.path.realpath(os.path.join(root, name))]) == os.path.abspath(base)
                for workspace in excluded for base in workspace.roots)]
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

        return {
            'pattern': pattern,
            'path': safe_dir,
            'match_count': len(matches),
            'matches': matches,
        }

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
        bound_scope = self._bound_scope_for_file(filepath)
        if bound_scope is not None:
            result = self._core_operation(bound_scope, 'read', filepath)
            content = str(result.get('content') or '')
            lines = content.splitlines(keepends=True)
            selected = lines[start_line:start_line + max_lines]
            return {
                'filepath': result.get('path') or self._core_path(filepath, bound_scope),
                'source_id': bound_scope.source_id,
                'total_lines': len(lines),
                'start_line': start_line,
                'end_line': start_line + len(selected),
                'content': ''.join(selected),
                'version': result.get('version') or '',
            }

        safe_path, scope = self._resolve_with_scope(filepath)
        if not os.path.isfile(safe_path):
            raise ToolExecutionError(f'File not found: {filepath}')
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
            raise ToolExecutionError(f'Cannot read file: {exc}') from exc

        return {
            'filepath': safe_path,
            'source_id': scope.source_id,
            'total_lines': total,
            'start_line': start_line,
            'end_line': start_line + len(chunk),
            'content': ''.join(chunk),
        }

    def string_replace(
        self,
        filepath: str,
        old_string: str,
        new_string: str,
        expected_replacements: int = 1,
        encoding: str = 'utf-8',
        expected_version: str = '',
    ) -> Dict[str, Any]:
        """Replace an exact string in an available local text file.

        The file is changed only when the number of exact matches equals
        ``expected_replacements``. Use a multiline old_string with enough
        surrounding context to make a local edit unambiguous.

        Args:
            filepath: Local text file path to edit.
            old_string: Exact literal text to replace; must not be empty.
            new_string: Replacement text, which may be empty.
            expected_replacements: Required number of exact matches, default 1.
            expected_version: Previously observed version; defaults to this executor's last observation.
            encoding: Text encoding used to decode and encode the file, default utf-8.

        Returns:
            Replacement count and updated file metadata. On mismatch or any
            error, the original file remains unchanged.
        """
        bound_scope = self._bound_scope_for_file(filepath)
        if bound_scope is not None:
            if type(expected_replacements) is not int or not 1 <= expected_replacements <= 100 or not old_string:
                raise ToolExecutionError('old_string must be non-empty and expected_replacements must be between 1 and 100')
            if encoding.lower().replace('_', '-') != 'utf-8':
                raise ToolExecutionError('workspace files require UTF-8 encoding')
            result = self._core_operation(
                bound_scope, 'replace', filepath, content=new_string, old_content=old_string,
                expected_version=expected_version, expected_replacements=expected_replacements,
            )
            return {
                'filepath': result.get('path') or self._core_path(filepath, bound_scope),
                'source_id': bound_scope.source_id,
                'replacements': expected_replacements,
                'encoding': encoding,
                'bytes': len(str(result.get('content') or '').encode(encoding)),
                'version': result.get('version') or '',
            }

        safe_path, scope = self._resolve_with_scope(filepath)
        if not os.path.isfile(safe_path):
            raise ToolExecutionError(f'File not found: {filepath}')
        self._ensure_visible_file(scope, safe_path)

        try:
            replacement = replace_exact_text_file(
                safe_path,
                old_string,
                new_string,
                expected_replacements=expected_replacements,
                encoding=encoding,
            )
        except ValueError as exc:
            raise ToolExecutionError(str(exc)) from exc

        return {
            'filepath': safe_path,
            'source_id': scope.source_id,
            'replacements': replacement.replacements,
            'encoding': replacement.encoding,
            'bytes': len(replacement.content),
        }

    def create(self, filepath: str, content: str = '', encoding: str = 'utf-8') -> Dict[str, Any]:
        """Create one new text file in the bound workspace."""
        bound_scope = self._bound_scope_for_file(filepath)
        if bound_scope is None:
            raise ToolExecutionError('file creation requires a bound workspace')
        if encoding.lower().replace('_', '-') != 'utf-8':
            raise ToolExecutionError('workspace files require UTF-8 encoding')
        result = self._core_operation(bound_scope, 'create', filepath, content=content)
        return {
            'filepath': result.get('path') or self._core_path(filepath, bound_scope),
            'source_id': bound_scope.source_id,
            'bytes': len(content.encode(encoding)),
            'version': result.get('version') or '',
        }

    def append(self, filepath: str, content: str, expected_version: str = '', encoding: str = 'utf-8') -> Dict[str, Any]:
        """Append text to one file in the bound workspace using a version fence."""
        bound_scope = self._bound_scope_for_file(filepath)
        if bound_scope is None:
            raise ToolExecutionError('file append requires a bound workspace')
        if encoding.lower().replace('_', '-') != 'utf-8':
            raise ToolExecutionError('workspace files require UTF-8 encoding')
        result = self._core_operation(bound_scope, 'append', filepath, content=content, expected_version=expected_version)
        return {
            'filepath': result.get('path') or self._core_path(filepath, bound_scope),
            'source_id': bound_scope.source_id,
            'bytes': len(str(result.get('content') or '').encode(encoding)),
            'version': result.get('version') or '',
        }

    def delete(self, filepath: str, expected_version: str = '') -> Dict[str, Any]:
        """Delete one regular file in the bound workspace using a version fence."""
        bound_scope = self._bound_scope_for_file(filepath)
        if bound_scope is None:
            raise ToolExecutionError('file deletion requires a bound workspace')
        result = self._core_operation(bound_scope, 'delete', filepath, expected_version=expected_version)
        return {
            'filepath': result.get('path') or self._core_path(filepath, bound_scope),
            'source_id': bound_scope.source_id,
            'version': result.get('version') or expected_version,
        }

    def overwrite(self, filepath: str, content: str, expected_version: str = '', encoding: str = 'utf-8') -> Dict[str, Any]:
        """Replace a workspace file with UTF-8 text using a previously observed version."""
        scope = self._bound_scope_for_file(filepath)
        if scope is None:
            raise ToolExecutionError('file overwrite requires a bound workspace')
        if encoding.lower().replace('_', '-') != 'utf-8':
            raise ToolExecutionError('workspace files require UTF-8 encoding')
        result = self._core_operation(scope, 'overwrite', filepath, content=content, expected_version=expected_version)
        return {'filepath': result.get('path'), 'source_id': scope.source_id,
                'bytes': len(content.encode('utf-8')), 'version': result.get('version', '')}

    def mkdir(self, path: str) -> Dict[str, Any]:
        """Create one directory in the selected workspace; its parent must already exist."""
        scope = self._bound_scope_for_file(path)
        if scope is None:
            raise ToolExecutionError('directory creation requires a bound workspace')
        result = self._core_operation(scope, 'mkdir', path)
        return {'path': result.get('path'), 'source_id': scope.source_id, 'type': 'directory'}

    def info(self, path: Optional[str] = None) -> Dict[str, Any]:
        """Get metadata for an available local file or directory.

        Args:
            path: Local file or directory path. When omitted, returns metadata for
                available local root directories.

        Returns:
            File or directory metadata such as path, type, size, and update time.
        """
        remote = self._workspace_discovery('info', path)
        if remote is not None:
            return remote
        if path is None or str(path).strip() in ('', '.'):
            entries = [self._entry(root, scope) for root, scope in self._iter_roots(None)]
            return {'path': None, 'entries': entries}
        else:
            safe_path, scope = self._resolve_with_scope(str(path))
            if os.path.isfile(safe_path):
                self._ensure_visible_file(scope, safe_path)

        try:
            st = os.stat(safe_path)
        except OSError as exc:
            raise ToolExecutionError(f'Cannot get file info: {exc}') from exc

        return {
            'path': safe_path,
            'type': 'directory' if os.path.isdir(safe_path) else 'file',
            'source_id': scope.source_id,
            'size': st.st_size,
            'mtime': datetime.datetime.fromtimestamp(st.st_mtime).isoformat(),
        }
