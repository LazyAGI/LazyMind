from __future__ import annotations

import re
from pathlib import Path
from typing import Any, Dict, List, Optional

# Compaction-stable fields on every windowed read: path, offset, end_line, total_lines, eof.
# A later context compressor can drop earlier windows and keep these locators.

_DEFAULT_LIMIT = 2000
_MAX_LIMIT = 4000
_MAX_GREP_RESULTS = 50
_MAX_SOURCE_BYTES = 20 * 1024 * 1024
RESULT_BYTE_BUDGET = 10 * 1024
_MAX_LOGICAL_LINE_CHARS = 4000
_MAX_LOGICAL_LINE_BYTES = 8 * 1024


def utf8_size(text: str) -> int:
    return len(str(text or '').encode('utf-8', errors='replace'))


def _take_utf8_prefix(text: str, max_bytes: int) -> tuple[str, str]:
    if utf8_size(text) <= max_bytes:
        return text, ''
    size = 0
    for index, char in enumerate(text):
        char_size = utf8_size(char)
        if size + char_size > max_bytes:
            return text[:index], text[index:]
        size += char_size
    return text, ''


def clamp_limit(limit: Optional[int]) -> int:
    value = _DEFAULT_LIMIT if limit is None else int(limit)
    return max(1, min(value, _MAX_LIMIT))


def clamp_offset(offset: Optional[int]) -> int:
    value = 1 if offset is None else int(offset)
    return max(1, value)


def split_logical_lines(
    text: str,
    *,
    max_line_chars: int = _MAX_LOGICAL_LINE_CHARS,
    max_line_bytes: int = _MAX_LOGICAL_LINE_BYTES,
) -> List[str]:
    """Split physical lines deterministically so offset continuation never skips text."""
    max_line_chars = max(1, int(max_line_chars))
    max_line_bytes = max(1, int(max_line_bytes))
    physical_lines = str(text or '').splitlines()
    lines: List[str] = []
    for physical in physical_lines:
        if not physical:
            lines.append('')
            continue
        remaining = physical
        while remaining:
            candidate = remaining[:max_line_chars]
            chunk, overflow = _take_utf8_prefix(candidate, max_line_bytes)
            if not chunk:
                chunk, overflow = candidate[:1], candidate[1:]
            lines.append(chunk)
            remaining = overflow + remaining[len(candidate):]
    return lines


def load_text_lines(
    path: str,
    *,
    encoding: str = 'utf-8',
    errors: str = 'replace',
    max_bytes: int = _MAX_SOURCE_BYTES,
) -> List[str]:
    source = Path(path)
    size = source.stat().st_size
    if size > max_bytes:
        raise ValueError(
            f'file exceeds the {max_bytes // (1024 * 1024)} MiB text-read limit'
        )
    return split_logical_lines(source.read_text(encoding=encoding, errors=errors))


def read_lines_window(
    lines: List[str],
    *,
    offset: int = 1,
    limit: int = _DEFAULT_LIMIT,
) -> Dict[str, Any]:
    """Return a numbered slice. EOF is determined by line count, not document semantics."""
    offset = clamp_offset(offset)
    limit = clamp_limit(limit)
    total = len(lines)
    if total == 0:
        footer = 'End of file.'
        return {
            'text': footer,
            'offset': offset,
            'end_line': 0,
            'limit': limit,
            'total_lines': 0,
            'eof': True,
            'footer': footer,
        }
    start = min(offset, total + 1)
    requested_end = min(start + limit - 1, total) if start <= total else total
    end = 0
    body_lines: List[str] = []
    result_bytes = 0
    if start <= total:
        for index in range(start, requested_end + 1):
            rendered = f'{index}: {lines[index - 1]}'
            added = utf8_size(rendered) + (1 if body_lines else 0)
            if body_lines and result_bytes + added > RESULT_BYTE_BUDGET:
                break
            body_lines.append(rendered)
            result_bytes += added
            end = index
    eof = start > total or end >= total
    if eof:
        footer = 'End of file.'
    else:
        footer = (
            f'Showing lines {start}-{end} of {total}.\n'
            f'Use offset={end + 1} to continue.'
        )
    text = '\n'.join(body_lines + ['', footer]) if body_lines else footer
    return {
        'text': text,
        'offset': start if start <= total else offset,
        'end_line': end if start <= total else 0,
        'limit': limit,
        'total_lines': total,
        'eof': eof,
        'footer': footer,
        'next_offset': None if eof else end + 1,
    }


def grep_lines(
    lines: List[str],
    pattern: str,
    *,
    max_results: int = _MAX_GREP_RESULTS,
) -> Dict[str, Any]:
    query = str(pattern or '').strip()
    if not query:
        raise ValueError('pattern is required')
    max_results = max(1, min(int(max_results or _MAX_GREP_RESULTS), 200))
    try:
        compiled = re.compile(query, re.IGNORECASE)

        def is_hit(text: str) -> bool:
            return compiled.search(text) is not None
    except re.error:
        needle = query.lower()

        def is_hit(text: str) -> bool:
            return needle in text.lower()

    matches: List[Dict[str, Any]] = []
    total_matches = 0
    result_bytes = 0
    for index, raw in enumerate(lines, start=1):
        line = raw
        if not is_hit(line):
            continue
        total_matches += 1
        snippet = line.strip()
        if len(snippet) > 240:
            snippet = snippet[:237] + '...'
        added = utf8_size(snippet) + 32
        if len(matches) < max_results and (
            not matches or result_bytes + added <= RESULT_BYTE_BUDGET
        ):
            matches.append({'line': index, 'text': snippet})
            result_bytes += added
    truncated = total_matches > len(matches)
    hint = (
        'After a hit, call read_file with offset near that line '
        '(for example offset=max(1, line-20)) to inspect surrounding context. '
        'The read footer is the only signal for whether the file has ended.'
    )
    footer = (
        'No matches.'
        if total_matches == 0
        else f'Showing {len(matches)} of {total_matches} matching lines.'
    )
    return {
        'pattern': query,
        'total': total_matches,
        'truncated': truncated,
        'matches': matches,
        'footer': footer,
        'hint': hint if matches else (
            'No matches. Search with terms that appear in the file, then read around hit lines. '
            + hint
        ),
    }
