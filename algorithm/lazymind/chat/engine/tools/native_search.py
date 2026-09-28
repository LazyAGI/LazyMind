"""Bounded local OS-index discovery. No document reads or source authorization."""
from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import threading
import time

from lazyllm.tools import fc_register
from lazyllm.tools.agent import HostFileIntent, HostFileResolution, resolve_host_path

from .spotlight import search_spotlight

_TIMEOUT = 8
_MAX_OUTPUT = 1024 * 1024


def native_search_available() -> bool:
    if sys.platform == 'darwin':
        return os.access('/usr/bin/mdfind', os.X_OK)
    return sys.platform == 'win32' and bool(shutil.which('powershell.exe'))


def _windows_output(command: list[str], payload: bytes) -> tuple[bytes, str]:
    """Bound pipe consumption on Windows, where selectors cannot monitor pipes."""
    chunks = []
    stop = 'finished'
    with subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                          stderr=subprocess.DEVNULL, creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0)) as proc:
        def consume():
            nonlocal stop
            size = 0
            while True:
                chunk = proc.stdout.read(8192)
                if not chunk:
                    break
                size += len(chunk)
                if size > _MAX_OUTPUT:
                    stop = 'output_limit'
                    proc.kill()
                    break
                chunks.append(chunk)

        worker = threading.Thread(target=consume, daemon=True)
        worker.start()
        try:
            proc.stdin.write(payload)
            proc.stdin.close()
            proc.wait(timeout=_TIMEOUT)
        except subprocess.TimeoutExpired:
            stop = 'timeout'
            proc.kill()
            proc.wait()
        except BrokenPipeError:
            stop = 'search_error'
            proc.kill()
            proc.wait()
        finally:
            worker.join(timeout=1)
        if stop == 'finished' and proc.returncode:
            stop = 'index_unavailable' if proc.returncode == 3 else 'search_error'
    return b''.join(chunks), stop


def _search_scope(arguments):
    path = arguments.get('path', '')
    if not isinstance(path, str):
        raise ValueError('path must be a directory string')
    if path:
        path = arguments['path'] = resolve_host_path(path)
    # An empty scope queries the OS index, not the current conversation directory.
    return HostFileResolution(arguments, (HostFileIntent(path or os.path.abspath(os.sep), 'read'),))


@fc_register(host_file=_search_scope, execute_in_sandbox=False)
def native_search(query: str, match: str = 'either', path: str = '',
                  kind: str = 'any', limit: int = 30) -> dict:
    """Discover local paths in the OS index without reading document bodies.

    Args:
        query: Short literal keywords (1–256 characters), not a full question.
        match: name, content, or either (default).
        path: Optional directory scope; empty uses the system index's default scope.
        kind: any, file, or directory.
        limit: Maximum candidates, 1–100, default 30.

    Returns:
        dict: Absolute paths, metadata, status, stop_reason and index coverage.
            Read text with read; use read_file_resource for PDF or Office.
    """
    if not isinstance(query, str) or not query.strip() or len(query) > 256:
        raise ValueError('query must contain 1–256 characters')
    if any(ord(c) < 32 or c in '*?"\\' for c in query):
        raise ValueError('use literal keywords without wildcards, quotes or control characters')
    if match not in {'name', 'content', 'either'} or kind not in {'any', 'file', 'directory'}:
        raise ValueError('invalid match or kind')
    if isinstance(limit, bool) or not isinstance(limit, int) or not 1 <= limit <= 100:
        raise ValueError('limit must be between 1 and 100')
    if not isinstance(path, str):
        raise ValueError('path must be a directory string')
    scope = os.path.realpath(os.path.expanduser(path)) if path else ''
    if scope and not os.path.isdir(scope):
        raise ValueError('path must be an existing directory')
    backend = {'darwin': 'spotlight', 'win32': 'windows-search'}.get(sys.platform, 'unsupported')
    result = {'status': 'unavailable', 'backend': backend, 'query': query, 'scope': scope or None,
              'results': [], 'stop_reason': 'unavailable', 'elapsed_ms': 0,
              'coverage': 'OS index only; empty results do not prove absence. '
                          'Discovery grants no read/write permission.'}
    if not native_search_available():
        return result
    started = time.monotonic()
    try:
        if sys.platform == 'darwin':
            return search_spotlight(query, match, scope, kind, limit)
        script = Path(__file__).with_name('windows_search.ps1').read_text(encoding='utf-8')
        encoded_script = base64.b64encode(script.encode('utf-16-le')).decode('ascii')
        payload = json.dumps(dict(query=query.strip(), match=match, path=scope, kind=kind, limit=limit)).encode('utf-8')
        raw, stop = _windows_output(
            [shutil.which('powershell.exe'), '-NoProfile', '-NonInteractive', '-EncodedCommand', encoded_script],
            payload)
        # Each line is independently valid, retaining complete hits on timeout/truncation.
        results, seen = [], set()
        for line in raw.splitlines():
            try:
                hit = json.loads(line)
                candidate = os.path.realpath(hit['path'])
                if not os.path.isabs(candidate) or not os.path.exists(candidate):
                    continue
                if scope and os.path.commonpath([scope, candidate]) != scope:
                    continue
                key = os.path.normcase(candidate)
                if key in seen:
                    continue
                seen.add(key)
                if len(results) == limit:
                    if stop == 'finished':
                        stop = 'result_limit'
                    break
                hit.update(path=candidate, source='local', backend=backend, match=match)
                results.append(hit)
            except (ValueError, KeyError, TypeError, OSError):
                continue
        result.update(results=results, stop_reason=stop,
                      status=('ok' if stop == 'finished' else 'unavailable' if stop == 'index_unavailable'
                              else 'partial' if results else 'error'))
    except OSError:
        result.update(status='unavailable', stop_reason='unavailable')
    result['elapsed_ms'] = round((time.monotonic() - started) * 1000)
    return result
