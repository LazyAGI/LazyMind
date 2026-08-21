from __future__ import annotations

import ast
import hashlib
import json
import os
import re
import tempfile
from typing import Any, Callable, Optional

from lazymind.config import config

from .context_estimator import estimate_tokens

_ERROR_LINE = re.compile(
    r'(error|exception|traceback|failed|fatal|panic|assert)',
    re.IGNORECASE,
)
_EXIT_CODE = re.compile(r'(?:exit(?:\s*code)?|return(?:ed)?\s*code|status)\s*[:=]?\s*(-?\d+)', re.I)
_URL = re.compile(r'https?://[^\s\'"<>]+', re.IGNORECASE)


def _as_text(content: Any) -> str:
    if content is None:
        return ''
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for part in content:
            if isinstance(part, dict):
                parts.append(str(part.get('text') or part.get('content') or part))
            else:
                parts.append(str(part))
        return '\n'.join(parts)
    if isinstance(content, dict):
        try:
            return json.dumps(content, ensure_ascii=False, indent=2, default=str)
        except (TypeError, ValueError):
            return str(content)
    return str(content)


def _try_parse_mapping(text: str) -> Any:
    stripped = text.strip()
    if not stripped or stripped[0] not in '{[':
        return None
    try:
        return json.loads(stripped)
    except (TypeError, ValueError):
        try:
            return ast.literal_eval(stripped)
        except (SyntaxError, ValueError):
            return None


def _result_payload(parsed: Any) -> dict[str, Any]:
    current = parsed
    if isinstance(current, dict) and current.get('ok') is True and 'value' in current:
        current = current.get('value')
    if not isinstance(current, dict):
        return {}
    result = current.get('result')
    return result if isinstance(result, dict) else current


def _file_result_details(tool_name: str, content: Any) -> dict[str, Any]:
    if _classify(tool_name) != 'file':
        return {}
    parsed = _try_parse_mapping(_as_text(content))
    payload = _result_payload(parsed)
    if not payload:
        return {}
    locator = (
        payload.get('target')
        or payload.get('file_id')
        or payload.get('filepath')
        or payload.get('path')
        or payload.get('file')
        or payload.get('filename')
    )
    if not locator:
        return {}
    body = ''
    for key in ('text', 'content', 'body'):
        if isinstance(payload.get(key), str):
            body = payload[key]
            break
    return {
        'locator': str(locator),
        'offset': payload.get('offset', payload.get('start_line')),
        'end_line': payload.get('end_line'),
        'next_offset': payload.get('next_offset'),
        'total_lines': payload.get('total_lines'),
        'eof': payload.get('eof'),
        'body': body,
    }


def _head_tail(text: str, head: int = 600, tail: int = 400) -> str:
    if len(text) <= head + tail + 80:
        return text
    omitted = len(text) - head - tail
    return f'{text[:head]}\n...[{omitted} chars omitted]...\n{text[-tail:]}'


def _pick_error_lines(text: str, limit: int = 8) -> list[str]:
    lines = []
    for line in text.splitlines():
        if _ERROR_LINE.search(line):
            lines.append(line.strip())
            if len(lines) >= limit:
                break
    return lines


def _classify(tool_name: str) -> str:
    name = str(tool_name or '').strip().lower()
    if not name:
        return 'generic'
    if any(token in name for token in (
        'run_script', 'shell', 'terminal', 'bash', 'execute_command', 'cmd',
    )):
        return 'shell'
    if any(token in name for token in (
        'url_fetch', 'web_search', 'kb_search', 'kb_tmp_search', 'kb_keyword',
        'academic_search', 'search_provider', 'search_in_files', 'grep', 'glob',
    )) or name.endswith('_search') or 'search' in name:
        return 'search'
    if any(token in name for token in (
        'read_file', 'read_user_attachment', 'feishuwikifs_read', 'cat_file',
    )) or name.endswith('_read') or name.endswith('.read') or name == 'read' \
            or name.endswith('_read_file') or name.endswith('_read_with_references'):
        return 'file'
    if 'read' in name and any(token in name for token in ('file', 'fs', 'local', 'attachment')):
        return 'file'
    return 'generic'


def compact_shell_result(tool_name: str, content: Any) -> tuple[str, str]:
    text = _as_text(content)
    parsed = _try_parse_mapping(text)
    command = ''
    exit_code = ''
    body = text
    if isinstance(parsed, dict):
        command = str(
            parsed.get('command')
            or parsed.get('cmd')
            or parsed.get('script')
            or ((parsed.get('result') or {}) if isinstance(parsed.get('result'), dict) else {}).get('command')
            or ''
        )
        for key in ('exit_code', 'returncode', 'status', 'code'):
            if key in parsed and parsed[key] is not None:
                exit_code = str(parsed[key])
                break
        for key in ('stdout', 'stderr', 'output', 'result', 'msg'):
            value = parsed.get(key)
            if isinstance(value, str) and value.strip():
                body = value
                break
            if isinstance(value, dict):
                nested = value.get('stdout') or value.get('output') or value.get('content')
                if isinstance(nested, str) and nested.strip():
                    body = nested
                    break
    if not exit_code:
        match = _EXIT_CODE.search(text)
        if match:
            exit_code = match.group(1)
    errors = _pick_error_lines(body)
    lines = [
        '[Earlier tool result compacted]',
        f'Tool: {tool_name or "shell"}',
    ]
    if command:
        lines.append(f'Command: {command}')
    if exit_code != '':
        lines.append(f'Result: exit code {exit_code}')
    if errors:
        lines.append('Key errors:')
        lines.extend(f'- {item}' for item in errors[:5])
    lines.append('Output excerpt:')
    lines.append(_head_tail(body, head=500, tail=400))
    return '\n'.join(lines), 'shell'


def compact_file_result(tool_name: str, content: Any) -> tuple[str, str]:
    text = _as_text(content)
    details = _file_result_details(tool_name, content)
    locator = details.get('locator', '')
    offset = details.get('offset')
    end_line = details.get('end_line')
    next_offset = details.get('next_offset')
    total_lines = details.get('total_lines')
    eof = details.get('eof')
    body = details.get('body') or text
    lines = [
        '[Earlier tool result compacted]',
        f'Tool: {tool_name or "file"}',
    ]
    if locator:
        lines.append(f'Target: {locator}')
    if offset is not None or end_line is not None or total_lines is not None:
        lines.append(
            f'Read range: offset={offset if offset is not None else "?"} '
            f'end={end_line if end_line is not None else "?"} '
            f'total_lines={total_lines if total_lines is not None else "?"}'
        )
    if eof is not None:
        lines.append(f'EOF: {bool(eof)}')
    if next_offset is not None:
        lines.append(f'Continue with read_file(target={locator!r}, offset={next_offset}).')
    lines.append('Content excerpt:')
    lines.append(_head_tail(body, head=500, tail=300))
    return '\n'.join(lines), 'file_locator' if locator else 'file'


def compact_search_result(tool_name: str, content: Any) -> tuple[str, str]:
    text = _as_text(content)
    parsed = _try_parse_mapping(text)
    query = ''
    urls: list[str] = []
    snippets: list[str] = []
    if isinstance(parsed, dict):
        query = str(
            parsed.get('query')
            or parsed.get('keyword')
            or parsed.get('q')
            or ((parsed.get('result') or {}) if isinstance(parsed.get('result'), dict) else {}).get('query')
            or ''
        )
        candidates = []
        for key in ('results', 'items', 'documents', 'matches', 'hits'):
            value = parsed.get(key)
            if isinstance(value, list):
                candidates = value
                break
        result = parsed.get('result')
        if not candidates and isinstance(result, dict):
            for key in ('results', 'items', 'documents', 'matches', 'hits'):
                value = result.get(key)
                if isinstance(value, list):
                    candidates = value
                    break
            for key in ('text', 'content', 'final_url'):
                if isinstance(result.get(key), str) and result[key].strip():
                    snippets.append(result[key].strip()[:400])
        for item in candidates[:5]:
            if isinstance(item, dict):
                title = str(item.get('title') or item.get('name') or item.get('path') or '')
                url = str(item.get('url') or item.get('link') or item.get('href') or '')
                snippet = str(
                    item.get('snippet')
                    or item.get('content')
                    or item.get('text')
                    or item.get('summary')
                    or ''
                )
                if url:
                    urls.append(url)
                if title or snippet:
                    snippets.append(f'{title}: {snippet}'.strip(': ').strip()[:400])
            elif isinstance(item, str):
                snippets.append(item[:400])
    if not urls:
        urls = _URL.findall(text)[:5]
    if not snippets:
        snippets = [_head_tail(text, head=500, tail=200)]
    lines = [
        '[Earlier tool result compacted]',
        f'Tool: {tool_name or "search"}',
    ]
    if query:
        lines.append(f'Query: {query}')
    if urls:
        lines.append('Sources:')
        lines.extend(f'- {url}' for url in urls[:5])
    lines.append('Top snippets:')
    lines.extend(f'- {snippet}' for snippet in snippets[:5])
    return '\n'.join(lines), 'search'


def compact_generic_result(tool_name: str, content: Any) -> tuple[str, str]:
    text = _as_text(content)
    if len(text) <= 1200:
        return text, 'generic'
    lines = [
        '[Earlier tool result compacted]',
        f'Tool: {tool_name or "tool"}',
        f'Original length: {len(text)} chars',
        'Excerpt:',
        _head_tail(text, head=500, tail=400),
    ]
    return '\n'.join(lines), 'generic'


_COMPACTORS: dict[str, Callable[[str, Any], tuple[str, str]]] = {
    'shell': compact_shell_result,
    'file': compact_file_result,
    'search': compact_search_result,
    'generic': compact_generic_result,
}


def compact_tool_result(tool_name: str, content: Any) -> tuple[str, str, int, int]:
    """Return compacted content, compactor id, before tokens, after tokens."""
    before = estimate_tokens(_as_text(content))
    kind = _classify(tool_name)
    compacted, compactor = _COMPACTORS[kind](tool_name, content)
    after = estimate_tokens(compacted)
    if after >= before:
        # Keep original when compaction does not help.
        return _as_text(content), 'noop', before, before
    return compacted, compactor, before, after


def tool_result_utf8_size(content: Any) -> int:
    return len(_as_text(content).encode('utf-8', errors='replace'))


def spill_threshold_bytes() -> int:
    return max(1, int(config['context_compression_spill_bytes']))


def is_oversized_tool_result(content: Any, threshold: Optional[int] = None) -> bool:
    limit = spill_threshold_bytes() if threshold is None else max(1, int(threshold))
    return tool_result_utf8_size(content) > limit


def _spill_filename(tool_name: str, content: str) -> str:
    safe = re.sub(r'[^A-Za-z0-9_.-]+', '_', tool_name or 'tool').strip('._')[:80]
    digest = hashlib.sha256(content.encode('utf-8', errors='replace')).hexdigest()[:16]
    return f'{safe or "tool"}_{digest}.txt'


def spill_tool_result_to_workspace(
    workspace: str,
    tool_name: str,
    content: Any,
) -> Optional[str]:
    root = os.path.realpath(str(workspace or ''))
    if not root:
        return None
    text = _as_text(content)
    spill_dir = os.path.join(root, 'tool_spills')
    os.makedirs(spill_dir, exist_ok=True)
    path = os.path.join(spill_dir, _spill_filename(tool_name, text))
    temporary = ''
    try:
        with tempfile.NamedTemporaryFile(
            mode='w',
            encoding='utf-8',
            dir=spill_dir,
            prefix='.spill-',
            suffix='.tmp',
            delete=False,
        ) as handle:
            temporary = handle.name
            handle.write(text)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        if temporary and os.path.exists(temporary):
            os.unlink(temporary)
    return os.path.relpath(path, root)


def format_spilled_tool_notice(
    tool_name: str,
    content: Any,
    rel_path: str,
    size_bytes: int,
) -> str:
    size_kb = size_bytes / 1024
    return '\n'.join([
        '[Large tool result offloaded to workspace]',
        f'Tool: {tool_name or "tool"}',
        f'File path (relative to workspace): {rel_path}',
        f'Size: {size_kb:.1f} KB',
        'Use read_file on this path if you need more than the excerpt below.',
        'Excerpt:',
        _head_tail(_as_text(content), head=500, tail=300),
    ])


def compact_or_spill_tool_result(
    tool_name: str,
    content: Any,
    *,
    workspace: Optional[str] = None,
    threshold: Optional[int] = None,
) -> tuple[str, str, int, int, str, int]:
    text = _as_text(content)
    before = estimate_tokens(text)
    size_bytes = tool_result_utf8_size(content)
    limit = spill_threshold_bytes() if threshold is None else max(1, int(threshold))
    if _file_result_details(tool_name, content):
        compacted, compactor, before_tokens, after_tokens = compact_tool_result(tool_name, content)
        return compacted, compactor, before_tokens, after_tokens, '', 0
    if workspace and size_bytes > limit:
        try:
            rel_path = spill_tool_result_to_workspace(workspace, tool_name, content)
        except Exception:
            rel_path = None
        if rel_path:
            notice = format_spilled_tool_notice(tool_name, content, rel_path, size_bytes)
            return notice, 'spill', before, estimate_tokens(notice), rel_path, size_bytes
    compacted, compactor, before_tokens, after_tokens = compact_tool_result(tool_name, content)
    return compacted, compactor, before_tokens, after_tokens, '', 0
