from contextlib import contextmanager
from urllib.parse import urlsplit

import requests
from lazyllm.tools.agent import ToolExecutionError

from lazymind.common.integrations.remote_fs import RemoteFS
from .text_window import RESULT_BYTE_BUDGET, grep_lines, read_lines_window, split_logical_lines, utf8_size


def remote_skill_uri(target: str):
    raw = str(target or '').strip()
    if not raw.lower().startswith('remote:'):
        return None
    try:
        parsed = urlsplit(raw)
    except ValueError as exc:
        raise ToolExecutionError('invalid_skill_uri: malformed remote URI') from exc
    parts = parsed.path.strip('/').split('/')
    if (parsed.scheme != 'remote' or parsed.netloc != 'skills' or parsed.query or parsed.fragment
            or not raw.startswith('remote://') or '\\' in raw or '%' in raw
            or any(part in ('', '.', '..') for part in parts)):
        raise ToolExecutionError('invalid_skill_uri: expected remote://skills/<skill-path>')
    return 'remote://skills/' + '/'.join(parts)


@contextmanager
def remote_errors():
    try:
        yield
    except ToolExecutionError:
        raise
    except (RuntimeError, requests.RequestException, OSError) as exc:
        cause = exc
        status = None
        while cause is not None:
            response = getattr(cause, 'response', None)
            if response is not None:
                status = response.status_code
                break
            cause = cause.__cause__
        code = ('remote_resource_not_found' if status == 404 or isinstance(exc, FileNotFoundError)
                else 'remote_resource_access_denied' if status in (401, 403) or isinstance(exc, PermissionError)
                else 'skill_remote_mount_unavailable')
        raise ToolExecutionError(code) from exc


class _RemoteFileTooLarge(ToolExecutionError):
    pass


def _lines(fs, uri):
    data = fs.read_limited(uri, 20 * 1024 * 1024)
    if len(data) > 20 * 1024 * 1024:
        raise _RemoteFileTooLarge('remote_resource_too_large: text-read limit is 20 MiB')
    return split_logical_lines(data.decode('utf-8', errors='replace'))


def read_remote(uri, offset, limit):
    with remote_errors():
        payload = read_lines_window(_lines(RemoteFS(), uri), offset=offset, limit=limit)
    return {**payload, 'target': uri, 'display_name': uri.rsplit('/', 1)[-1], 'kind': 'skill_reference'}


def _entries(fs, uri, recursive, max_depth):
    pending = [(uri, 0)]
    seen = {uri}
    entries = []
    depth_truncated = False
    while pending:
        current, depth = pending.pop(0)
        for item in fs.ls(current, detail=True):
            child = remote_skill_uri(item['name'])
            if not child or not child.startswith(current + '/'):
                raise ToolExecutionError('invalid_skill_uri: remote listing escaped requested directory')
            if child in seen:
                continue
            if len(entries) >= 200:
                return entries, True
            seen.add(child)
            entries.append((child, item.get('type') in ('directory', 'dir')))
            if recursive and entries[-1][1]:
                if depth < max_depth:
                    pending.append((child, depth + 1))
                else:
                    depth_truncated = True
    return entries, depth_truncated


def list_remote(uri, recursive, max_depth):
    with remote_errors():
        entries, truncated = _entries(RemoteFS(), uri, recursive, max(0, min(int(max_depth), 20)))
    return {'status': 'ok', 'path': uri,
            'entries': sorted(path[len(uri) + 1:] for path, _ in entries), 'truncated': truncated}


def grep_remote(uri, pattern, max_results):
    matches = []
    skipped_files = []
    cap = max(1, min(int(max_results or 50), 200))
    total = size = 0
    with remote_errors():
        fs = RemoteFS()
        directory = fs.info(uri).get('type') in ('directory', 'dir')
        entries, listing_truncated = _entries(fs, uri, True, 20) if directory else ([(uri, False)], False)
        truncated = False
        for path, is_directory in entries:
            if is_directory or path.lower().endswith('.pdf'):
                continue
            try:
                lines = _lines(fs, path)
            except _RemoteFileTooLarge:
                if not directory:
                    raise
                skipped_files.append({'target': path, 'reason': 'remote_resource_too_large'})
                continue
            found = grep_lines(lines, pattern, max_results=cap)
            total += found['total']
            for item in found['matches']:
                added = utf8_size(item['text']) + utf8_size(path) + 32
                if len(matches) >= cap or (matches and size + added > RESULT_BYTE_BUDGET):
                    truncated = True
                    break
                matches.append({'target': path, **item})
                size += added
            if truncated or len(matches) >= cap:
                truncated = True
                break
    truncated = truncated or listing_truncated or bool(skipped_files) or total > len(matches)
    return {'target': uri, 'kind': 'skill_reference', 'pattern': pattern, 'matches': matches,
            'total': total, 'truncated': truncated, 'skipped_files': skipped_files,
            'footer': 'Search truncated.' if truncated else f'Showing {len(matches)} matching lines.',
            'hint': 'Use read_file_resource(target, offset=hit line) to read the matching reference.'}
