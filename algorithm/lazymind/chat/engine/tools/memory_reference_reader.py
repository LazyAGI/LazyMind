from __future__ import annotations

from typing import Any, Dict, List, Union

from lazymind.chat.engine.tools.infra import (
    MemoryRemoteStore,
    tool_error,
    tool_success,
)
from lazymind.review.memory_review.errors import MemoryNotFoundError, MemoryStoreError
from lazymind.review.memory_review.paths import split_reference_ref

# Per-reference read limits: truncate when either bound is reached first.
MAX_REFERENCE_READ_LINES = 200
MAX_REFERENCE_READ_CHARS = 8000
MAX_REFERENCE_READ_COUNT = 10

_REFERENCE_TRUNCATION_WARNING = (
    '\n\n<!-- WARNING: This reference content was truncated because it exceeded '
    'the limits. Use a narrower #anchor, read fewer refs in one call, '
    'or ask the user if the omitted detail matters. -->\n'
)


def truncate_reference_content(
    content: str,
    *,
    max_lines: int = MAX_REFERENCE_READ_LINES,
    max_chars: int = MAX_REFERENCE_READ_CHARS,
) -> tuple[str, bool]:
    """Truncate reference text when line count or character count exceeds limits."""
    if max_lines < 1:
        raise ValueError('max_lines must be >= 1')
    if max_chars < 1:
        raise ValueError('max_chars must be >= 1')

    text = content if isinstance(content, str) else ''
    if not text:
        return '', False

    lines = text.splitlines(keepends=True)
    if not lines:
        lines = [text]

    parts: list[str] = []
    char_count = 0
    truncated = False

    for line_idx, line in enumerate(lines):
        if line_idx >= max_lines:
            truncated = True
            break
        if char_count + len(line) <= max_chars:
            parts.append(line)
            char_count += len(line)
            continue
        remaining = max_chars - char_count
        if remaining > 0:
            parts.append(line[:remaining])
        truncated = True
        break

    result = ''.join(parts)
    if not truncated and result != text:
        truncated = True
    return result, truncated


def _normalize_refs(refs: Union[str, List[str]]) -> list[str]:
    if isinstance(refs, str):
        raw_items = [refs]
    elif isinstance(refs, list):
        raw_items = refs
    else:
        return []

    normalized: list[str] = []
    seen: set[str] = set()
    for item in raw_items:
        ref = str(item or '').strip()
        if not ref or ref in seen:
            continue
        seen.add(ref)
        normalized.append(ref)
    return normalized


def _read_single_reference(store: MemoryRemoteStore, raw_ref: str) -> dict[str, Any]:
    path, anchor = split_reference_ref(raw_ref)
    content = store.store.read_reference(raw_ref)
    truncated_content, truncated = truncate_reference_content(content)
    if truncated:
        truncated_content = f'{truncated_content.rstrip()}{_REFERENCE_TRUNCATION_WARNING}'
    return {
        'ref': raw_ref,
        'path': path,
        'anchor': anchor or None,
        'content': truncated_content,
        'content_length': len(truncated_content),
        'original_content_length': len(content),
        'truncated': truncated,
    }


def read_memory_reference(refs: Union[str, List[str]]) -> Dict[str, Any]:
    """Read detailed user-preference reference files on demand.

    The User Preference Index injected in the system prompt lists short summaries
    with optional ``ref`` pointers to files under ``memory/users/references/``.
    Call this tool only when the current task matches one or more listed
    preferences AND the injected summaries are not sufficient. Pass the exact
    ``ref`` values from those index entries (for example
    ``references/response.md`` or ``references/response.md#tone``). You may pass
    one ref or several related refs in the same call when the task genuinely
    needs multiple preference details. Do not invent paths or load unrelated
    references.

    Each reference is truncated independently when it exceeds the read limits
    (line count or character count, whichever is hit first). Truncated entries
    include a warning marker at the end of ``content``.

    Soul, profile, and the preference index itself are already injected at chat
    start; do not use this tool to re-read them.

    Args:
        refs: One preference-index ref or a list of refs to read in order.

    Returns:
        A unified tool payload with ``items`` (one entry per ref), plus
        ``ref_count`` and ``truncated_count``.
    """
    normalized_refs = _normalize_refs(refs)
    if not normalized_refs:
        return tool_error('read_memory_reference', 'refs is required.')

    if len(normalized_refs) > MAX_REFERENCE_READ_COUNT:
        return tool_error(
            'read_memory_reference',
            f'At most {MAX_REFERENCE_READ_COUNT} refs may be read per call; '
            f'got {len(normalized_refs)}.',
        )

    store = MemoryRemoteStore()
    items: list[dict[str, Any]] = []
    for raw_ref in normalized_refs:
        try:
            split_reference_ref(raw_ref)
        except ValueError as exc:
            return tool_error(
                'read_memory_reference',
                f'Invalid ref {raw_ref!r}: {exc}',
            )
        try:
            items.append(_read_single_reference(store, raw_ref))
        except MemoryNotFoundError:
            return tool_error(
                'read_memory_reference',
                f'Reference not found for ref={raw_ref!r}.',
            )
        except MemoryStoreError as exc:
            return tool_error(
                'read_memory_reference',
                f'Failed to read {raw_ref!r}: {exc}',
            )
        except Exception as exc:
            return tool_error(
                'read_memory_reference',
                f'Failed to read {raw_ref!r}: {exc}',
            )

    truncated_count = sum(1 for item in items if item.get('truncated'))
    return tool_success('read_memory_reference', {
        'items': items,
        'ref_count': len(items),
        'truncated_count': truncated_count,
    })
