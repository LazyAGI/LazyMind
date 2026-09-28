"""Bounded discovery through the macOS system index; no document reads."""
from __future__ import annotations

import os
from pathlib import Path
from datetime import datetime, timezone
import selectors
import subprocess
import sys
import time


def _index_available(scope):
    volume = str(scope) if scope else '/'
    while not os.path.ismount(volume):
        volume = os.path.dirname(volume)
    try:
        result = subprocess.run(['/usr/bin/mdutil', '-s', volume], capture_output=True,
                                timeout=1, env={**os.environ, 'LC_ALL': 'C'})
    except (OSError, subprocess.TimeoutExpired):
        return False
    return result.returncode == 0 and b'Indexing enabled' in result.stdout


def search_spotlight(query: str, match: str = 'content', path: str = '',
                     kind: str = 'any', limit: int = 30) -> dict:
    """Query the current macOS user's Spotlight index with bounded output."""
    if sys.platform != 'darwin':
        return {'status': 'unavailable', 'reason': 'Spotlight requires a macOS host'}
    if not isinstance(query, str) or not query.strip() or len(query) > 256:
        raise ValueError('query must contain 1–256 characters')
    if any(ord(c) < 32 or c in '*?"\\' for c in query):
        raise ValueError('use literal keywords without wildcards, quotes or control characters')
    if match not in {'name', 'content', 'either'} or kind not in {'any', 'file', 'directory'}:
        raise ValueError('invalid match or kind')
    if isinstance(limit, bool) or not isinstance(limit, int) or not 1 <= limit <= 100:
        raise ValueError('limit must be between 1 and 100')
    scope = Path(path).expanduser().resolve() if path else None
    if scope is not None and not scope.is_dir():
        raise ValueError('path must be an existing directory')
    value = f'"*{query.strip()}*"cd'
    expressions = {'name': f'kMDItemFSName == {value}',
                   'content': f'kMDItemTextContent == {value}'}
    expression = expressions.get(match) or f'({expressions["name"]} || {expressions["content"]})'
    if kind != 'any':
        operator = '==' if kind == 'directory' else '!='
        expression += f' && kMDItemContentType {operator} "public.folder"'
    started = time.monotonic()
    if not _index_available(scope):
        return {'status': 'unavailable', 'backend': 'spotlight', 'query': query,
                'scope': str(scope) if scope else None, 'results': [],
                'stop_reason': 'index_unavailable',
                'elapsed_ms': round((time.monotonic() - started) * 1000),
                'coverage': 'Spotlight index status could not be confirmed; no search was performed.'}
    results, seen = [], set()
    stopped, pending, received = 'finished', b'', 0
    command = ['/usr/bin/mdfind', '-0']
    if scope is not None:
        command += ['-onlyin', str(scope)]
    command.append(expression)
    with subprocess.Popen(command,
                          stdout=subprocess.PIPE, stderr=subprocess.DEVNULL) as process:
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                while True:
                    remaining = 8 - (time.monotonic() - started)
                    if remaining <= 0:
                        stopped = 'timeout'
                        break
                    if not selector.select(remaining):
                        stopped = 'timeout'
                        break
                    chunk = os.read(process.stdout.fileno(), 65536)
                    if not chunk:
                        break
                    received += len(chunk)
                    if received > 1024 * 1024:
                        stopped = 'output_limit'
                        break
                    parts = (pending + chunk).split(b'\0')
                    pending = parts.pop()
                    for raw in parts:
                        if not raw:
                            continue
                        try:
                            candidate = Path(os.fsdecode(raw)).resolve()
                            if (scope is not None and not candidate.is_relative_to(scope)) or not candidate.exists():
                                continue
                        except (OSError, ValueError, RuntimeError):
                            continue
                        if candidate in seen:
                            continue
                        seen.add(candidate)
                        if len(results) >= limit:
                            stopped = 'result_limit'
                            break
                        results.append({'path': str(candidate), 'title': candidate.name,
                                        'kind': 'directory' if candidate.is_dir() else 'file',
                                        'source': 'local', 'backend': 'spotlight', 'match': match})
                        try:
                            stat = candidate.stat()
                            results[-1].update(size=stat.st_size, modified_at=datetime.fromtimestamp(
                                stat.st_mtime, timezone.utc).isoformat())
                        except OSError:
                            pass
                    if stopped != 'finished':
                        break
        finally:
            if process.poll() is None:
                if stopped != 'finished':
                    process.terminate()
                try:
                    process.wait(timeout=1)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                    stopped = 'timeout'
        if stopped == 'finished' and process.returncode:
            stopped = 'search_error'
    status = 'ok' if stopped == 'finished' else ('partial' if results else 'error')
    return {'status': status, 'query': query,
            'scope': str(scope) if scope is not None else None, 'backend': 'spotlight',
            'results': results, 'stop_reason': stopped,
            'elapsed_ms': round((time.monotonic() - started) * 1000),
            'coverage': 'Spotlight index only; empty results do not prove absence. '
                        'Discovery does not grant permission to read a file.'}
