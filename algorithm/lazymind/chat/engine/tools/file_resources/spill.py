"""Safe, execution-scoped readers for ``workspace://`` spill resources.

The model-facing URI is deliberately not a host filesystem path.  This module
keeps URI parsing, execution-workspace resolution, identity checks, streaming
hashing, and bounded pagination in one place so Chat, SubAgent, and Workflow
readers share the same contract.
"""
from __future__ import annotations

import codecs
import hashlib
import os
import re
from dataclasses import dataclass
from urllib.parse import urlsplit

from lazyllm.tools.agent import HostFileIntent, ToolExecutionError

from ..host_access_guard import HostAccessGuard, get_host_access_guard
from ..host_file_resolution import open_input_file
from .text_window import (
    RESULT_BYTE_BUDGET,
    clamp_limit,
    clamp_offset,
    split_logical_lines,
    utf8_size,
)


RESOURCE_NOT_FOUND = 'RESOURCE_NOT_FOUND'
RESOURCE_VERSION_CHANGED = 'RESOURCE_VERSION_CHANGED'
RESOURCE_PERMISSION_DENIED = 'RESOURCE_PERMISSION_DENIED'
RESOURCE_AUTH_UNAVAILABLE = 'RESOURCE_AUTH_UNAVAILABLE'

_SPILL_PREFIX = 'workspace://'
_MAX_URI_SEGMENTS = 64
_MAX_SEGMENT_LENGTH = 255
_MAX_RESULTS = 200


class ResourceReadError(Exception):
    """Stable, model-safe failure for a spill read operation."""

    def __init__(self, code: str, message: str):
        super().__init__(code, message)
        self.code = code
        self.message = message

    def __str__(self) -> str:
        return f'{self.code}: {self.message}'


@dataclass(frozen=True)
class SpillLocator:
    uri: str
    workspace: str
    path: str
    display_name: str


def _error(code: str, message: str) -> ResourceReadError:
    return ResourceReadError(code, message)


def canonical_hash(value: str | None) -> str:
    """Normalize accepted hash spellings to the returned ``sha256:`` form."""
    raw = str(value or '').strip().lower()
    if raw.startswith('sha256:'):
        raw = raw[7:]
    if not re.fullmatch(r'[0-9a-f]{64}', raw):
        raise _error(RESOURCE_VERSION_CHANGED, 'resource hash is invalid')
    return f'sha256:{raw}'


def parse_spill_uri(uri: str) -> tuple[str, ...]:
    """Parse a strict ``workspace://tool_spills/<relative-path>`` locator."""
    raw = str(uri or '').strip()
    if not raw.startswith(_SPILL_PREFIX) or '\\' in raw or '%' in raw:
        raise _error(RESOURCE_PERMISSION_DENIED, 'invalid workspace resource URI')
    try:
        parsed = urlsplit(raw)
    except ValueError:
        raise _error(RESOURCE_PERMISSION_DENIED, 'invalid workspace resource URI') from None
    if (
        parsed.scheme != 'workspace'
        or parsed.netloc != 'tool_spills'
        or parsed.query
        or parsed.fragment
        or parsed.username is not None
        or parsed.password is not None
        or not parsed.path.startswith('/')
    ):
        raise _error(RESOURCE_PERMISSION_DENIED, 'invalid workspace resource URI')
    segments = tuple(parsed.path[1:].split('/'))
    if (
        not segments
        or len(segments) > _MAX_URI_SEGMENTS
        or any(
            not part
            or part in {'.', '..'}
            or len(part) > _MAX_SEGMENT_LENGTH
            or '\x00' in part
            or ':' in part
            for part in segments
        )
    ):
        raise _error(RESOURCE_PERMISSION_DENIED, 'invalid workspace resource URI')
    return segments


def resolve_spill_locator(uri: str, workspace: str) -> SpillLocator:
    """Resolve a spill URI below one trusted execution workspace."""
    raw_workspace = str(workspace or '').strip()
    if not raw_workspace:
        raise _error(RESOURCE_AUTH_UNAVAILABLE, 'spill workspace is unavailable')
    root = os.path.realpath(raw_workspace)
    if not os.path.isabs(root):
        raise _error(RESOURCE_AUTH_UNAVAILABLE, 'spill workspace is unavailable')
    segments = ('tool_spills', *parse_spill_uri(uri))
    spill_root = os.path.realpath(os.path.join(root, 'tool_spills'))
    raw_candidate = os.path.abspath(os.path.join(root, *segments))
    if os.path.realpath(raw_candidate) != raw_candidate:
        raise _error(RESOURCE_PERMISSION_DENIED, 'resource path must not use a symbolic link')
    candidate = os.path.realpath(raw_candidate)
    try:
        inside = os.path.commonpath((spill_root, candidate)) == spill_root
    except ValueError:
        inside = False
    if not inside:
        raise _error(RESOURCE_PERMISSION_DENIED, 'resource is outside the active spill directory')
    try:
        is_file = os.path.isfile(candidate)
    except OSError as exc:
        if isinstance(exc, PermissionError):
            raise _error(RESOURCE_PERMISSION_DENIED, 'resource access was denied') from None
        is_file = False
    if not is_file:
        raise _error(RESOURCE_NOT_FOUND, 'resource was not found')
    return SpillLocator(
        uri=str(uri),
        workspace=root,
        path=candidate,
        display_name=os.path.basename(candidate),
    )


def _logical_chunks(physical: str):
    # ``splitlines()`` returns no item for an empty string, while an empty
    # physical line in a file represents one logical line.
    chunks = split_logical_lines(physical if physical else '\n')
    return chunks or ['']


def _iter_lines(stream):
    """Yield decoded logical lines and return the hash of the raw bytes."""
    decoder = codecs.getincrementaldecoder('utf-8')('replace')
    digest = hashlib.sha256()
    buffer = ''
    while True:
        chunk = stream.read(1024 * 1024)
        if not chunk:
            break
        digest.update(chunk)
        buffer += decoder.decode(chunk, final=False)
        while True:
            indexes = [index for index in (buffer.find('\n'), buffer.find('\r')) if index >= 0]
            if not indexes:
                chunks = split_logical_lines(buffer)
                if len(chunks) > 1:
                    for line in chunks[:-1]:
                        yield line
                    buffer = chunks[-1]
                break
            index = min(indexes)
            terminator = buffer[index]
            if terminator == '\r' and index + 1 == len(buffer):
                # Wait for the next chunk to distinguish CR from CRLF.
                break
            end = index + 1
            if terminator == '\r' and end < len(buffer) and buffer[end] == '\n':
                end += 1
            physical, buffer = buffer[:index], buffer[end:]
            for line in _logical_chunks(physical):
                yield line
    buffer += decoder.decode(b'', final=True)
    if buffer:
        for line in _logical_chunks(buffer):
            yield line
    return digest.hexdigest()


def _guarded_stream(locator: SpillLocator):
    """Open the locator through an identity-checked, no-follow read handle."""
    active = get_host_access_guard()
    try:
        if active is not None:
            active.validate()
        guard = HostAccessGuard((HostFileIntent(locator.path, 'read'),))
        guard.validate()
        stream = open_input_file(locator.path)
    except ResourceReadError:
        raise
    except ToolExecutionError:
        raise _error(RESOURCE_PERMISSION_DENIED, 'resource identity could not be verified') from None
    except FileNotFoundError:
        raise _error(RESOURCE_NOT_FOUND, 'resource was not found') from None
    except PermissionError:
        raise _error(RESOURCE_PERMISSION_DENIED, 'resource access was denied') from None
    except OSError:
        raise _error(RESOURCE_PERMISSION_DENIED, 'resource identity could not be verified') from None
    return guard, stream


def _finish_scan(locator: SpillLocator, guard: HostAccessGuard, digest: str, expected_hash: str | None,
                 before_stat: os.stat_result) -> str:
    try:
        guard.validate()
        after_stat = os.stat(locator.path, follow_symlinks=False)
    except FileNotFoundError:
        raise _error(RESOURCE_VERSION_CHANGED, 'resource disappeared during the read') from None
    except ToolExecutionError:
        raise _error(RESOURCE_PERMISSION_DENIED, 'resource identity could not be verified') from None
    except OSError:
        raise _error(RESOURCE_PERMISSION_DENIED, 'resource identity could not be verified') from None
    if (before_stat.st_size, before_stat.st_mtime_ns) != (after_stat.st_size, after_stat.st_mtime_ns):
        raise _error(RESOURCE_VERSION_CHANGED, 'resource changed during the read')
    actual = f'sha256:{digest}'
    if expected_hash is not None and canonical_hash(expected_hash) != actual:
        raise _error(RESOURCE_VERSION_CHANGED, 'resource version changed; restart from the first page')
    return actual


def _stat_for_read(path: str) -> os.stat_result:
    try:
        return os.stat(path, follow_symlinks=False)
    except FileNotFoundError:
        raise _error(RESOURCE_NOT_FOUND, 'resource was not found') from None
    except PermissionError:
        raise _error(RESOURCE_PERMISSION_DENIED, 'resource access was denied') from None
    except OSError:
        raise _error(RESOURCE_PERMISSION_DENIED, 'resource identity could not be verified') from None


def read_spill_window(uri: str, workspace: str, *, offset: int = 1, limit: int = 2000,
                      expected_hash: str | None = None) -> dict:
    """Read one bounded line window while scanning and hashing the full spill."""
    locator = resolve_spill_locator(uri, workspace)
    start = clamp_offset(offset)
    window_limit = clamp_limit(limit)
    body: list[str] = []
    result_bytes = 0
    end = 0
    total = 0
    stopped = False
    before_stat = _stat_for_read(locator.path)
    guard, stream = _guarded_stream(locator)
    try:
        iterator = _iter_lines(stream)
        while True:
            try:
                line = next(iterator)
            except StopIteration as done:
                digest = str(done.value or hashlib.sha256(b'').hexdigest())
                break
            total += 1
            if stopped or total < start or total >= start + window_limit:
                continue
            rendered = f'{total}: {line}'
            added = utf8_size(rendered) + (1 if body else 0)
            if body and result_bytes + added > RESULT_BYTE_BUDGET:
                stopped = True
                continue
            body.append(rendered)
            result_bytes += added
            end = total
    finally:
        stream.close()
    content_hash = _finish_scan(locator, guard, digest, expected_hash, before_stat)
    eof = start > total or end >= total
    footer = 'End of file.' if eof else (
        f'Showing lines {start}-{end} of {total}.\nUse offset={end + 1} to continue.'
    )
    text = '\n'.join(body + ['', footer]) if body else footer
    return {
        'text': text,
        'target': uri,
        'display_name': locator.display_name,
        'kind': 'workspace',
        'offset': start if start <= total else start,
        'end_line': end if start <= total else 0,
        'limit': window_limit,
        'total_lines': total,
        'eof': eof,
        'footer': footer,
        'next_offset': None if eof else end + 1,
        'content_hash': content_hash,
    }


def grep_spill_resource(uri: str, workspace: str, pattern: str, *, max_results: int = 50) -> dict:
    """Search a spill without bypassing its guard or reading limit."""
    query = str(pattern or '').strip()
    if not query:
        raise _error(RESOURCE_PERMISSION_DENIED, 'pattern is required')
    cap = max(1, min(int(max_results or 50), _MAX_RESULTS))
    try:
        compiled = re.compile(query, re.IGNORECASE)

        def matches(value: str) -> bool:
            return compiled.search(value) is not None
    except re.error:
        needle = query.lower()

        def matches(value: str) -> bool:
            return needle in value.lower()

    locator = resolve_spill_locator(uri, workspace)
    before_stat = _stat_for_read(locator.path)
    guard, stream = _guarded_stream(locator)
    found: list[dict] = []
    total = 0
    result_bytes = 0
    line_no = 0
    try:
        iterator = _iter_lines(stream)
        while True:
            try:
                line = next(iterator)
            except StopIteration as done:
                digest = str(done.value or hashlib.sha256(b'').hexdigest())
                break
            line_no += 1
            if not matches(line):
                continue
            total += 1
            snippet = line.strip()
            if len(snippet) > 240:
                snippet = snippet[:237] + '...'
            added = utf8_size(snippet) + 32
            if len(found) >= cap or (found and result_bytes + added > RESULT_BYTE_BUDGET):
                continue
            found.append({'line': line_no, 'text': snippet})
            result_bytes += added
    finally:
        stream.close()
    content_hash = _finish_scan(locator, guard, digest, None, before_stat)
    return {
        'pattern': query,
        'target': uri,
        'display_name': locator.display_name,
        'kind': 'workspace',
        'total': total,
        'truncated': total > len(found),
        'matches': found,
        'content_hash': content_hash,
        'footer': 'No matches.' if total == 0 else f'Showing {len(found)} of {total} matching lines.',
        'hint': 'After a hit, call read_resource(uri, offset=hit line) for surrounding context.',
    }
