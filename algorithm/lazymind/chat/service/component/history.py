from __future__ import annotations

import json
import re
from typing import Any

from lazymind.chat.service.utils.citations import (
    SOURCE_LINK_PATTERN,
    SOURCE_REF_PATTERN,
)

from lazymind.chat.service.component.tool_rendering import (
    _TOOL_CALL_TAG,
    _TOOL_PREVIEW_TAG,
    _TOOL_RESULT_PREVIEW_TAG,
    _TOOL_RESULT_TAG,
)

_HISTORY_TAG_PATTERN = re.compile(
    r'<(?P<tag>tp|trp|tool_call|tool_result)(?P<attrs>[^>]*)>(?P<body>.*?)</(?P=tag)>',
    re.DOTALL,
)
_WHITESPACE_BEFORE_PUNCT_PATTERN = re.compile(r'\s+([。！？，、.!?,;:])')
_MULTI_SPACE_PATTERN = re.compile(r'[ \t]{2,}')


def _history_message_content(message: dict[str, Any]) -> str:
    content = message.get('content')
    return content if isinstance(content, str) else ''


def _strip_history_citations(text: str) -> str:
    if not text:
        return ''
    text = SOURCE_LINK_PATTERN.sub('', text)
    text = SOURCE_REF_PATTERN.sub('', text)
    text = _WHITESPACE_BEFORE_PUNCT_PATTERN.sub(r'\1', text)
    return _MULTI_SPACE_PATTERN.sub(' ', text)


def _sanitize_history_tool_result(result: Any) -> Any:
    if isinstance(result, str):
        return _strip_history_citations(result)
    if isinstance(result, list):
        return [_sanitize_history_tool_result(item) for item in result]
    if isinstance(result, dict):
        sanitized = {}
        for key, value in result.items():
            if key in ('citation_index', 'ref'):
                continue
            sanitized[key] = _sanitize_history_tool_result(value)
        return sanitized
    return result


def _parse_history_assistant_content(
    content: str,
) -> list[dict[str, Any]]:
    segments: list[dict[str, Any]] = []
    cursor = 0
    content = content or ''

    while cursor < len(content):
        think_start = content.find('<think>', cursor)
        tag_match = _HISTORY_TAG_PATTERN.search(content, cursor)
        tag_start = tag_match.start() if tag_match else -1

        next_start = len(content)
        next_kind = ''
        if think_start >= 0 and think_start < next_start:
            next_start = think_start
            next_kind = 'think'
        if tag_start >= 0 and tag_start < next_start:
            next_start = tag_start
            next_kind = 'tag'

        if not next_kind:
            remaining = content[cursor:]
            if remaining:
                segments.append({'type': 'text', 'content': remaining})
            break

        if next_start > cursor:
            segments.append({'type': 'text', 'content': content[cursor:next_start]})

        if next_kind == 'think':
            think_body_start = next_start + len('<think>')
            think_end = content.find('</think>', think_body_start)
            if think_end >= 0:
                think_content = content[think_body_start:think_end]
                cursor = think_end + len('</think>')
            else:
                think_content = content[think_body_start:]
                cursor = len(content)
            segments.append({'type': 'reasoning', 'content': think_content})
            continue

        assert tag_match is not None
        cursor = tag_match.end()
        tag = tag_match.group('tag')
        body = tag_match.group('body') or ''
        if tag in (_TOOL_PREVIEW_TAG, _TOOL_RESULT_PREVIEW_TAG):
            continue
        try:
            payload = json.loads(body)
        except json.JSONDecodeError:
            continue
        if not isinstance(payload, dict):
            continue
        if tag == _TOOL_CALL_TAG:
            tool_call_id = str(payload.get('id') or '')
            tool_name = str(payload.get('name') or '')
            if not tool_call_id or not tool_name:
                continue
            arguments = payload.get('arguments', {})
            if not isinstance(arguments, dict):
                arguments = {}
            segments.append({
                'type': 'tool_call',
                'id': tool_call_id,
                'name': tool_name,
                'arguments': arguments,
            })
        elif tag == _TOOL_RESULT_TAG:
            segments.append({
                'type': 'tool_result',
                'id': str(payload.get('id') or ''),
                'name': str(payload.get('name') or ''),
                'result': payload.get('result'),
            })
    return segments


def _append_pending_assistant(
    normalized: list[dict[str, Any]],
    pending_reasoning_parts: list[str],
    pending_text_parts: list[str],
    pending_tool_calls: list[dict[str, Any]],
    saw_structured_segments: bool,
) -> None:
    reasoning = '\n'.join(
        part.strip() for part in pending_reasoning_parts if str(part).strip()
    ).strip()
    text = ''.join(pending_text_parts).strip()
    if not reasoning and not text and not pending_tool_calls:
        return
    msg: dict[str, Any] = {'role': 'assistant', 'content': text}
    if saw_structured_segments:
        msg['reasoning_content'] = reasoning
    if pending_tool_calls:
        msg['tool_calls'] = list(pending_tool_calls)
    normalized.append(msg)
    pending_reasoning_parts.clear()
    pending_text_parts.clear()
    pending_tool_calls.clear()


def normalize_history_for_agent(
    history: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    normalized: list[dict[str, Any]] = []
    for message in history or []:
        if not isinstance(message, dict):
            continue
        role = str(message.get('role') or '').strip()
        if role == 'assistant':
            content = _history_message_content(message)
            segments = _parse_history_assistant_content(content)

            pending_reasoning_parts: list[str] = []
            pending_text_parts: list[str] = []
            pending_tool_calls: list[dict[str, Any]] = []
            saw_structured_segments = False

            for seg in segments:
                seg_type = seg['type']
                if seg_type == 'reasoning':
                    saw_structured_segments = True
                    pending_reasoning_parts.append(seg['content'])
                elif seg_type == 'text':
                    pending_text_parts.append(_strip_history_citations(seg['content']))
                elif seg_type == 'tool_call':
                    saw_structured_segments = True
                    pending_tool_calls.append({
                        'id': seg['id'],
                        'type': 'function',
                        'function': {
                            'name': seg['name'],
                            'arguments': json.dumps(seg['arguments'], ensure_ascii=False),
                        },
                    })
                elif seg_type == 'tool_result':
                    saw_structured_segments = True
                    _append_pending_assistant(
                        normalized,
                        pending_reasoning_parts,
                        pending_text_parts,
                        pending_tool_calls,
                        saw_structured_segments,
                    )
                    normalized.append({
                        'role': 'tool',
                        'tool_call_id': seg['id'],
                        'name': seg['name'],
                        'content': (
                            _sanitize_history_tool_result(seg['result'])
                            if isinstance(seg['result'], str)
                            else json.dumps(
                                _sanitize_history_tool_result(seg['result']),
                                ensure_ascii=False,
                                separators=(',', ':'),
                            )
                        ),
                    })

            _append_pending_assistant(
                normalized,
                pending_reasoning_parts,
                pending_text_parts,
                pending_tool_calls,
                saw_structured_segments,
            )
            continue

        if role == 'user':
            content = _history_message_content(message)
            if content:
                normalized.append({'role': 'user', 'content': content})
            continue

        content = _history_message_content(message)
        if content:
            normalized.append({'role': role or 'assistant', 'content': content})
    return normalized
