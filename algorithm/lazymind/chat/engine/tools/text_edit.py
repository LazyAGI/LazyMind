from __future__ import annotations

from dataclasses import dataclass
import difflib
import os
import stat
import tempfile
from typing import Any

import regex as re


@dataclass(frozen=True)
class ExactReplacement:
    content: bytes
    replacements: int
    encoding: str
    matches: tuple[dict[str, Any], ...] = ()
    diff: str = ''


_REGEX_FLAGS = {
    'IGNORECASE': re.IGNORECASE,
    'MULTILINE': re.MULTILINE,
    'DOTALL': re.DOTALL,
}
_MAX_PREVIEW_MATCHES = 100
_MAX_MATCH_EXCERPT_CHARS = 800
_MAX_DIFF_LINES = 160
_MAX_DIFF_CHARS = 20_000
_REGEX_TIMEOUT_SECONDS = 2.0


def _line_number(text: str, offset: int) -> int:
    return text.count('\n', 0, offset) + 1


def _dominant_newline(text: str) -> str:
    crlf = text.count('\r\n')
    bare_lf = text.count('\n') - crlf
    bare_cr = text.count('\r') - crlf
    if crlf >= bare_lf and crlf >= bare_cr and crlf:
        return '\r\n'
    if bare_cr > bare_lf:
        return '\r'
    return '\n'


def _normalize_replacement_newlines(value: str, newline: str) -> str:
    return value.replace('\r\n', '\n').replace('\r', '\n').replace('\n', newline)


def _literal_pattern(value: str) -> Any:
    normalized = value.replace('\r\n', '\n').replace('\r', '\n')
    parts = normalized.split('\n')
    return re.compile(r'(?:\r\n|\n|\r)'.join(re.escape(part) for part in parts))


def _parse_regex_flags(value: str) -> int:
    flags = 0
    names = [item.strip().upper() for item in str(value or '').split(',') if item.strip()]
    for name in names:
        if name not in _REGEX_FLAGS:
            raise ValueError(
                f'Unsupported regex flag {name!r}; use IGNORECASE, MULTILINE, and/or DOTALL'
            )
        flags |= _REGEX_FLAGS[name]
    return flags


def build_text_diff(before: str, after: str) -> str:
    lines = list(difflib.unified_diff(
        before.splitlines(keepends=True),
        after.splitlines(keepends=True),
        fromfile='before',
        tofile='after',
        n=3,
    ))
    rendered = ''.join(lines)
    if len(lines) > _MAX_DIFF_LINES or len(rendered) > _MAX_DIFF_CHARS:
        raise ValueError(
            'Preview diff is too large to validate safely; narrow the match pattern and try again'
        )
    return rendered


def build_text_replacement(
    original: bytes,
    pattern: str,
    replacement: str,
    expected_replacements: int = 1,
    encoding: str = 'utf-8',
    mode: str = 'literal',
    regex_flags: str = 'MULTILINE',
) -> ExactReplacement:
    """Build a literal or regex replacement and a bounded validation preview."""
    if not isinstance(pattern, str) or not pattern:
        raise ValueError('old_string must be a non-empty string')
    if not isinstance(replacement, str):
        raise ValueError('new_string must be a string')
    if (not isinstance(expected_replacements, int)
            or isinstance(expected_replacements, bool)
            or not 1 <= expected_replacements <= _MAX_PREVIEW_MATCHES):
        raise ValueError(
            f'expected_replacements must be an integer between 1 and {_MAX_PREVIEW_MATCHES}'
        )

    text = original.decode(encoding, errors='strict')
    if '\x00' in text:
        raise ValueError('File contains NUL bytes and is not a supported text file')
    normalized_mode = str(mode or 'literal').strip().lower()
    if normalized_mode == 'literal':
        compiled = _literal_pattern(pattern)
        replacement_text = _normalize_replacement_newlines(replacement, _dominant_newline(text))
        try:
            matches = list(compiled.finditer(text, timeout=_REGEX_TIMEOUT_SECONDS))
            updated = compiled.sub(
                lambda _: replacement_text, text, timeout=_REGEX_TIMEOUT_SECONDS,
            )
        except TimeoutError as exc:
            raise ValueError('Text matching timed out; narrow the literal match') from exc
    elif normalized_mode == 'regex':
        compiled = re.compile(pattern, _parse_regex_flags(regex_flags))
        try:
            matches = list(compiled.finditer(text, timeout=_REGEX_TIMEOUT_SECONDS))
            updated = compiled.sub(replacement, text, timeout=_REGEX_TIMEOUT_SECONDS)
        except TimeoutError as exc:
            raise ValueError('Regex execution timed out; simplify or narrow the pattern') from exc
    else:
        raise ValueError("mode must be 'literal' or 'regex'")

    match_count = len(matches)
    if match_count != expected_replacements:
        raise ValueError(
            f'Expected {expected_replacements} match(es), found {match_count}; file was not changed'
        )
    if updated == text:
        raise ValueError('Replacement would not change the file')
    summaries = []
    for index, match in enumerate(matches, start=1):
        matched_text = match.group(0)
        excerpt = matched_text[:_MAX_MATCH_EXCERPT_CHARS]
        summaries.append({
            'index': index,
            'start_line': _line_number(text, match.start()),
            'end_line': _line_number(text, match.end()),
            'matched_text': excerpt,
            'matched_text_truncated': len(matched_text) > len(excerpt),
        })
    return ExactReplacement(
        content=updated.encode(encoding, errors='strict'),
        replacements=match_count,
        encoding=encoding,
        matches=tuple(summaries),
        diff=build_text_diff(text, updated),
    )


def build_exact_replacement(
    original: bytes,
    old_string: str,
    new_string: str,
    expected_replacements: int = 1,
    encoding: str = 'utf-8',
) -> ExactReplacement:
    """Build an exact text replacement without changing the source file."""
    return build_text_replacement(
        original,
        old_string,
        new_string,
        expected_replacements=expected_replacements,
        encoding=encoding,
        mode='literal',
    )


def replace_exact_text_file(
    filepath: str,
    old_string: str,
    new_string: str,
    expected_replacements: int = 1,
    encoding: str = 'utf-8',
) -> ExactReplacement:
    """Atomically replace exact text in a file while preserving mode and ownership."""
    with open(filepath, 'rb') as source:
        replacement = build_exact_replacement(
            source.read(), old_string, new_string, expected_replacements, encoding,
        )

    stat_result = os.stat(filepath)
    parent = os.path.dirname(filepath)
    temp_path = ''
    try:
        with tempfile.NamedTemporaryFile(
            dir=parent,
            prefix=f'.{os.path.basename(filepath)}.',
            suffix='.tmp',
            delete=False,
        ) as temp_file:
            temp_path = temp_file.name
            temp_file.write(replacement.content)
            temp_file.flush()
            os.fsync(temp_file.fileno())
        os.chmod(temp_path, stat.S_IMODE(stat_result.st_mode))
        if hasattr(os, 'chown'):
            try:
                os.chown(temp_path, stat_result.st_uid, stat_result.st_gid)
            except PermissionError:
                temp_stat = os.stat(temp_path)
                if (temp_stat.st_uid, temp_stat.st_gid) != (stat_result.st_uid, stat_result.st_gid):
                    raise
        os.replace(temp_path, filepath)
        temp_path = ''
    finally:
        if temp_path:
            try:
                os.unlink(temp_path)
            except FileNotFoundError:
                pass
    return replacement
