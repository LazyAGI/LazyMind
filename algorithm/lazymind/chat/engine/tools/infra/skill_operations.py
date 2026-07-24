from __future__ import annotations

import re
from typing import Callable, Optional

from .skill_identity import skill_identity_from_content
from .skill_paths import normalize_skill_package_path
from .skill_validation import validate_skill_content


_UNICODE_MAP = {
    '\u201c': '"',
    '\u201d': '"',
    '\u2018': "'",
    '\u2019': "'",
    '\u2014': '--',
    '\u2013': '-',
    '\u2026': '...',
    '\u00a0': ' ',
}


def edit_skill_file(
    current_files: dict[str, str],
    category: str,
    name: str,
    path: str,
    content: str,
) -> dict:
    normalized_path = normalize_skill_package_path(path)
    _validate_loaded_skill_package(current_files)
    if normalized_path not in current_files:
        raise ValueError(f'edit_file target does not exist: {normalized_path}')
    if not isinstance(content, str):
        raise ValueError("edit_file requires a string field 'content'.")
    edited_files = dict(current_files)
    edited_files[normalized_path] = content
    return _build_skill_file_change(
        category,
        name,
        normalized_path,
        current_files,
        edited_files,
        result={
            'status': 'edited',
            'message': 'Skill package file change was written.',
        },
    )


def patch_skill_file(
    current_files: dict[str, str],
    category: str,
    name: str,
    path: str,
    old_text: str,
    new_text: str,
    *,
    replace_all: bool = False,
) -> dict:
    normalized_path = normalize_skill_package_path(path)
    _validate_loaded_skill_package(current_files)
    if normalized_path not in current_files:
        raise ValueError(f'patch_file target does not exist: {normalized_path}')
    if not isinstance(old_text, str):
        raise ValueError("patch_file requires a string field 'old_text'.")
    if not isinstance(new_text, str):
        raise ValueError("patch_file requires a string field 'new_text'.")
    new_content, match_count, strategy, error = fuzzy_find_and_replace(
        current_files[normalized_path],
        old_text,
        new_text,
        replace_all,
    )
    if error:
        raise ValueError(error)
    edited_files = dict(current_files)
    edited_files[normalized_path] = new_content
    return _build_skill_file_change(
        category,
        name,
        normalized_path,
        current_files,
        edited_files,
        result={
            'status': 'patched',
            'message': 'Skill package file change was written.',
            'match_count': match_count,
            'strategy': strategy,
        },
    )


def create_skill_file(
    current_files: dict[str, str],
    category: str,
    name: str,
    path: str,
    content: str,
) -> dict:
    normalized_path = normalize_skill_package_path(path)
    _validate_loaded_skill_package(current_files)
    if normalized_path == 'SKILL.md':
        raise ValueError('create_file cannot create or overwrite SKILL.md; use edit_file or patch_file instead.')
    if normalized_path in current_files:
        raise ValueError('File already exists; use edit_file or patch_file to modify it.')
    if not isinstance(content, str):
        raise ValueError("create_file requires a string field 'content'.")
    edited_files = dict(current_files)
    edited_files[normalized_path] = content
    return _build_skill_file_change(
        category,
        name,
        normalized_path,
        current_files,
        edited_files,
        result={
            'status': 'created',
            'message': 'Skill package file change was written.',
        },
    )


def delete_skill_file(
    current_files: dict[str, str],
    category: str,
    name: str,
    path: str,
) -> dict:
    normalized_path = normalize_skill_package_path(path)
    _validate_loaded_skill_package(current_files)
    if normalized_path == 'SKILL.md':
        raise ValueError(
            'SKILL.md cannot be deleted with delete_file; use remove_skill to remove the whole skill package.'
        )
    if normalized_path not in current_files:
        raise ValueError(f'delete_file target does not exist: {normalized_path}')
    edited_files = dict(current_files)
    del edited_files[normalized_path]
    return _build_skill_file_change(
        category,
        name,
        normalized_path,
        current_files,
        edited_files,
        result={
            'status': 'deleted',
            'message': 'Skill package file change was written.',
        },
    )


def fuzzy_find_and_replace(
    content: str,
    old_text: str,
    new_text: str,
    replace_all: bool = False,
) -> tuple[str, int, Optional[str], Optional[str]]:
    if not old_text:
        return content, 0, None, 'old_text cannot be empty.'
    if old_text == new_text:
        return content, 0, None, 'old_text and new_text are identical.'

    strategies: list[tuple[str, Callable[[str, str], list[tuple[int, int]]]]] = [
        ('exact', _strategy_exact),
        ('line_trimmed', _strategy_line_trimmed),
        ('indentation_flexible', _strategy_indentation_flexible),
        ('unicode_normalized', _strategy_unicode_normalized),
    ]

    for strategy_name, strategy in strategies:
        matches = strategy(content, old_text)
        if not matches:
            continue
        if len(matches) > 1 and not replace_all:
            return (
                content,
                0,
                None,
                f'Found {len(matches)} matches for old_text. '
                'Provide more context to make it unique, or use replace_all=True.',
            )
        effective_new = _maybe_unescape_new_text(new_text, content, matches)
        return (
            _apply_replacements(
                content,
                matches,
                effective_new,
                old_text=old_text if strategy_name != 'exact' else None,
            ),
            len(matches),
            strategy_name,
            None,
        )

    return content, 0, None, 'Could not find a match for old_text in the file.'


def _validate_loaded_skill_package(files: dict[str, str]) -> None:
    if 'SKILL.md' not in files:
        raise ValueError('Skill package must contain SKILL.md.')


def _build_skill_file_change(
    category: str,
    name: str,
    normalized_path: str,
    current_files: dict[str, str],
    edited_files: dict[str, str],
    *,
    result: dict,
) -> dict:
    if edited_files == current_files:
        raise ValueError('Edited skill package is unchanged from current package.')
    if edited_files.get('SKILL.md') != current_files.get('SKILL.md'):
        _validate_skill_identity_unchanged(category, name, edited_files.get('SKILL.md') or '')

    result = dict(result)
    result['files'] = edited_files
    result['touched_files'] = [normalized_path]
    return result


def _validate_skill_identity_unchanged(category: str, name: str, content: str) -> None:
    content_error = validate_skill_content(content)
    if content_error:
        raise ValueError(content_error)
    edited_category, edited_name = skill_identity_from_content(content)
    if edited_category != category or edited_name != name:
        raise ValueError('SKILL.md frontmatter name/category cannot be changed; use rename_skill.')


def _unicode_normalize(text: str) -> str:
    for char, replacement in _UNICODE_MAP.items():
        text = text.replace(char, replacement)
    return text


def _strategy_exact(content: str, pattern: str) -> list[tuple[int, int]]:
    matches: list[tuple[int, int]] = []
    start = 0
    while True:
        pos = content.find(pattern, start)
        if pos == -1:
            break
        matches.append((pos, pos + len(pattern)))
        start = pos + 1
    return matches


def _strategy_line_trimmed(content: str, pattern: str) -> list[tuple[int, int]]:
    content_lines = content.split('\n')
    normalized_lines = [line.strip() for line in content_lines]
    pattern_normalized = '\n'.join(line.strip() for line in pattern.split('\n'))
    return _find_line_block_matches(content_lines, normalized_lines, pattern_normalized, len(content))


def _strategy_indentation_flexible(content: str, pattern: str) -> list[tuple[int, int]]:
    content_lines = content.split('\n')
    normalized_lines = [line.lstrip() for line in content_lines]
    pattern_normalized = '\n'.join(line.lstrip() for line in pattern.split('\n'))
    return _find_line_block_matches(content_lines, normalized_lines, pattern_normalized, len(content))


def _strategy_unicode_normalized(content: str, pattern: str) -> list[tuple[int, int]]:
    normalized_content = _unicode_normalize(content)
    normalized_pattern = _unicode_normalize(pattern)
    if normalized_content == content and normalized_pattern == pattern:
        return []

    normalized_matches = _strategy_exact(normalized_content, normalized_pattern)
    if not normalized_matches:
        normalized_matches = _strategy_line_trimmed(normalized_content, normalized_pattern)
    if not normalized_matches:
        return []

    return _map_normalized_positions(content, normalized_matches)


def _find_line_block_matches(
    content_lines: list[str],
    normalized_lines: list[str],
    normalized_pattern: str,
    content_length: int,
) -> list[tuple[int, int]]:
    pattern_line_count = len(normalized_pattern.split('\n'))
    matches: list[tuple[int, int]] = []
    for idx in range(len(normalized_lines) - pattern_line_count + 1):
        block = '\n'.join(normalized_lines[idx:idx + pattern_line_count])
        if block == normalized_pattern:
            matches.append(
                _calculate_line_positions(content_lines, idx, idx + pattern_line_count, content_length)
            )
    return matches


def _calculate_line_positions(
    content_lines: list[str],
    start_line: int,
    end_line: int,
    content_length: int,
) -> tuple[int, int]:
    start_pos = sum(len(line) + 1 for line in content_lines[:start_line])
    end_pos = sum(len(line) + 1 for line in content_lines[:end_line]) - 1
    return start_pos, min(content_length, end_pos)


def _map_normalized_positions(
    original: str,
    normalized_matches: list[tuple[int, int]],
) -> list[tuple[int, int]]:
    orig_to_norm: list[int] = []
    norm_pos = 0
    for char in original:
        orig_to_norm.append(norm_pos)
        replacement = _UNICODE_MAP.get(char)
        norm_pos += len(replacement) if replacement is not None else 1
    orig_to_norm.append(norm_pos)

    norm_to_orig_start: dict[int, int] = {}
    for orig_pos, mapped_norm_pos in enumerate(orig_to_norm[:-1]):
        norm_to_orig_start.setdefault(mapped_norm_pos, orig_pos)

    matches: list[tuple[int, int]] = []
    original_length = len(original)
    for norm_start, norm_end in normalized_matches:
        if norm_start not in norm_to_orig_start:
            continue
        orig_start = norm_to_orig_start[norm_start]
        orig_end = orig_start
        while orig_end < original_length and orig_to_norm[orig_end] < norm_end:
            orig_end += 1
        matches.append((orig_start, orig_end))
    return matches


def _maybe_unescape_new_text(
    new_text: str,
    content: str,
    matches: list[tuple[int, int]],
) -> str:
    if '\\t' not in new_text and '\\r' not in new_text:
        return new_text
    matched_regions = ''.join(content[start:end] for start, end in matches)
    result = new_text
    if '\\t' in result and '\t' in matched_regions:
        result = result.replace('\\t', '\t')
    if '\\r' in result and '\r' in matched_regions:
        result = result.replace('\\r', '\r')
    return result


def _apply_replacements(
    content: str,
    matches: list[tuple[int, int]],
    new_text: str,
    old_text: Optional[str] = None,
) -> str:
    result = content
    for start, end in sorted(matches, key=lambda match: match[0], reverse=True):
        replacement = (
            _reindent_replacement(content[start:end], old_text, new_text)
            if old_text is not None
            else new_text
        )
        result = result[:start] + replacement + result[end:]
    return result


def _reindent_replacement(file_region: str, old_text: Optional[str], new_text: str) -> str:
    if old_text is None or not new_text:
        return new_text
    old_first = _first_meaningful_line(old_text)
    file_first = _first_meaningful_line(file_region)
    if old_first is None or file_first is None:
        return new_text

    old_indent = _leading_whitespace(old_first)
    file_indent = _leading_whitespace(file_first)
    if old_indent == file_indent:
        return new_text

    out_lines: list[str] = []
    for line in new_text.split('\n'):
        if not line.strip():
            out_lines.append(line)
            continue
        line_indent = _leading_whitespace(line)
        if line_indent.startswith(old_indent):
            out_lines.append(file_indent + line[len(old_indent):])
        else:
            out_lines.append(file_indent + line.lstrip(' \t'))
    return '\n'.join(out_lines)


def _first_meaningful_line(text: str) -> Optional[str]:
    for line in text.split('\n'):
        if line.strip():
            return line
    return None


def _leading_whitespace(line: str) -> str:
    match = re.match(r'[ \t]*', line)
    return match.group(0) if match else ''
