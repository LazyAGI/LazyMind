from __future__ import annotations

import os
from functools import wraps
from typing import Any, Dict, Optional
from lazyllm.tools import fc_register
from lazyllm.tools.agent import ToolExecutionError
from .resolver import resolve_text_target
from .text_window import RESULT_BYTE_BUDGET, grep_lines, load_text_lines, read_lines_window, utf8_size


def build_resource_read_tools() -> list:
    """Bind file readers to attachments and this conversation's file resources."""
    @wraps(read_file_resource)
    def scoped_read_file(*args, **kwargs):
        return _read_file(*args, **kwargs, resources_only=True)

    @wraps(search_file_resource)
    def scoped_grep(*args, **kwargs):
        return _grep(*args, **kwargs, resources_only=True)

    return [scoped_grep, scoped_read_file]


def _resolve_text_target_for_tool(
    target: str,
    *,
    allow_directory: bool = False,
    turn: Optional[int] = None,
    resources_only: bool = False,
):
    try:
        return resolve_text_target(
            target,
            allow_directory=allow_directory,
            turn=turn,
            resources_only=resources_only,
        )
    except (ValueError, FileNotFoundError) as exc:
        raise ToolExecutionError(str(exc)) from exc


@fc_register(host_file='NONE', exclusive=True)
def read_file_resource(
    target: str,
    offset: int = 1,
    limit: int = 2000,
    turn: Optional[int] = None,
) -> Dict[str, Any]:
    """Read text from a PDF resource, attachment, or chat-workspace file.

    Large files are always windowed by a UTF-8 byte budget. The footer is the
    only EOF signal: continue with next_offset when present; stop at End of file.
    After search_file_resource, pass offset near the hit line to inspect surrounding context.

    Args:
        target: A file resource id, unique attachment name, or workspace path.
        offset: 1-based first line (default 1).
        limit: Maximum lines to return (default 2000, max 4000).
        turn: Optional 1-based conversation turn used to disambiguate attachments.
    """
    return _read_file(target, offset, limit, turn)


def _read_file(
    target: str,
    offset: int = 1,
    limit: int = 2000,
    turn: Optional[int] = None,
    *,
    resources_only: bool = False,
) -> Dict[str, Any]:
    resolved = _resolve_text_target_for_tool(target, turn=turn, resources_only=resources_only)
    payload = read_lines_window(
        load_text_lines(resolved.path),
        offset=offset,
        limit=limit,
    )
    payload.update({
        'target': target,
        'display_name': resolved.display_name,
        'kind': resolved.kind,
    })
    if resolved.file_id:
        payload['file_id'] = resolved.file_id
    return payload


@fc_register(host_file='NONE', exclusive=True)
def search_file_resource(
    target: str,
    pattern: str,
    max_results: int = 50,
    turn: Optional[int] = None,
) -> Dict[str, Any]:
    """Search a PDF resource, attachment, or chat-workspace path.

    After a hit, call read_file_resource with offset near that line for surrounding
    context. Do not treat search_file_resource snippets as the full file.

    Args:
        target: A file resource id, unique attachment name, or workspace path.
        pattern: Literal substring or regular expression.
        max_results: Maximum matches (default 50).
        turn: Optional 1-based conversation turn used to disambiguate attachments.
    """
    return _grep(target, pattern, max_results, turn)


def _grep(
    target: str,
    pattern: str,
    max_results: int = 50,
    turn: Optional[int] = None,
    *,
    resources_only: bool = False,
) -> Dict[str, Any]:
    resolved = _resolve_text_target_for_tool(
        target,
        allow_directory=True,
        turn=turn,
        resources_only=resources_only,
    )
    files: list[str] = []
    if os.path.isfile(resolved.path):
        files = [resolved.path]
    elif os.path.isdir(resolved.path):
        for root, _dirs, names in os.walk(resolved.path):
            for name in names:
                files.append(os.path.join(root, name))
                if len(files) >= 200:
                    break
            if len(files) >= 200:
                break
    else:
        raise ToolExecutionError('target must be an existing text file or workspace directory')
    matches: list = []
    remaining = max(1, min(int(max_results or 50), 200))
    truncated = False
    total = 0
    result_bytes = 0
    for file_path in files:
        if str(file_path).lower().endswith('.pdf'):
            continue
        try:
            text_lines = load_text_lines(file_path)
        except (OSError, ValueError):
            continue
        found = grep_lines(text_lines, pattern, max_results=remaining)
        total += int(found.get('total') or 0)
        rel = os.path.relpath(file_path, resolved.workspace).replace('\\', '/')
        for item in found.get('matches') or []:
            match_target = target if len(files) == 1 else rel
            added_bytes = utf8_size(str(item.get('text') or '')) + utf8_size(match_target) + 32
            if matches and result_bytes + added_bytes > RESULT_BYTE_BUDGET:
                truncated = True
                remaining = 0
                break
            matches.append({'target': match_target, **item})
            result_bytes += added_bytes
            remaining -= 1
            if remaining <= 0:
                truncated = True
                break
        if remaining <= 0:
            break
        if found.get('truncated'):
            truncated = True
    return {
        'pattern': pattern,
        'target': target,
        'display_name': resolved.display_name,
        'kind': resolved.kind,
        'total': total,
        'truncated': truncated or total > len(matches),
        'matches': matches,
        'footer': (
            'No matches.'
            if total == 0
            else f'Showing {len(matches)} of {total} matching lines.'
        ),
        'hint': (
            'After a hit, call read_file_resource(target, offset=max(1, line-20), limit=80) '
            'for surrounding context. Read footers decide EOF, not document headings.'
        ),
    }
