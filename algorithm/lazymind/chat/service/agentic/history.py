from __future__ import annotations

import json
import re
from typing import Any, Optional

from lazymind.chat.service.utils.citations import (
    CITATION_INDEX_PATTERN,
    SOURCE_LINK_PATTERN,
    build_stream_citation_scanner,
    reset_citation_state,
    restore_history_citations,
    restore_history_source_links,
    rewrite_citations,
)
from lazymind.chat.service.utils.markdown_images import rewrite_markdown_image_urls

from lazymind.chat.service.agentic.tool_stream import (
    _TOOL_CALL_TAG,
    _TOOL_PREVIEW_TAG,
    _TOOL_RESULT_PREVIEW_TAG,
    _TOOL_RESULT_TAG,
)

_THINK_BLOCK_PATTERN = re.compile(r'<think>(.*?)</think>', re.DOTALL)
_HISTORY_TAG_PATTERN = re.compile(
    r'<(?P<tag>tp|trp|tool_call|tool_result)(?P<attrs>[^>]*)>(?P<body>.*?)</(?P=tag)>',
    re.DOTALL,
)
_KB_TOOL_PREFIX = 'kb_'


def _history_message_content(message: dict[str, Any]) -> str:
    content = message.get('content')
    return content if isinstance(content, str) else ''


def _tool_result_message_content(result: Any) -> str:
    if isinstance(result, str):
        return result
    return json.dumps(result, ensure_ascii=False, separators=(',', ':'))


def _is_kb_tool_name(name: Any) -> bool:
    return isinstance(name, str) and name.startswith(_KB_TOOL_PREFIX)


def _reset_citation_state(config: dict) -> None:
    reset_citation_state(config)


def _build_stream_citation_scanner(config: dict[str, Any]):
    return build_stream_citation_scanner(config)


def _restore_source_links_to_refs(text: str) -> str:
    return SOURCE_LINK_PATTERN.sub(lambda match: f'[[{match.group(2)}]]', text or '')


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


def _normalize_history_for_agent(
    history: list[dict[str, Any]],
    config: Optional[dict[str, Any]] = None,
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
                    restore_history_source_links(seg['content'], config)
                    pending_text_parts.append(_restore_source_links_to_refs(seg['content']))
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
                    if _is_kb_tool_name(seg['name']):
                        restore_history_citations(seg['result'], config)
                    normalized.append({
                        'role': 'tool',
                        'tool_call_id': seg['id'],
                        'name': seg['name'],
                        'content': _tool_result_message_content(seg['result']),
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

        if role == 'tool':
            content = _history_message_content(message)
            normalized.append({
                'role': 'tool',
                'tool_call_id': str(message.get('tool_call_id') or ''),
                'name': str(message.get('name') or ''),
                'content': content,
            })
            continue

        content = _history_message_content(message)
        if content:
            normalized.append({'role': role or 'assistant', 'content': content})
    return normalized


def _split_think_and_body(raw_text: str, existing_think: Any = '') -> tuple[str, str]:
    think_parts: list[str] = []
    if existing_think:
        think_parts.append(str(existing_think))

    def _collect_think(match: re.Match) -> str:
        think_parts.append(match.group(1))
        return ''

    body = _THINK_BLOCK_PATTERN.sub(_collect_think, raw_text or '')
    if '<think>' in body:
        before, after = body.split('<think>', 1)
        if '</think>' in after:
            think, rest = after.split('</think>', 1)
            think_parts.append(think)
            body = before + rest
        else:
            think_parts.append(after)
            body = before
    body = body.replace('</think>', '')
    think = '\n'.join(part.strip() for part in think_parts if str(part).strip())
    return think.strip(), body


def _merge_sources(
    cited_sources: list[dict[str, Any]],
    existing_sources: Any,
) -> list[dict[str, Any]]:
    merged: list[dict[str, Any]] = []
    seen: set[str] = set()

    def _push(source: Any) -> None:
        if not isinstance(source, dict):
            return
        key = str(
            source.get('index')
            or source.get('segement_id')
            or source.get('document_id')
            or id(source)
        )
        if key in seen:
            return
        seen.add(key)
        merged.append(source)

    for source in cited_sources or []:
        _push(source)
    if isinstance(existing_sources, list):
        for source in existing_sources:
            _push(source)
    return merged


def _format_final_result(result: Any, config: dict) -> dict[str, Any]:
    if isinstance(result, dict):
        raw_text = str(result.get('text') or result.get('message') or '')
        existing_think = result.get('think') or result.get('reasoning_content') or ''
        existing_sources = result.get('sources')
    else:
        raw_text = '' if result is None else str(result)
        existing_think = ''
        existing_sources = None

    think, body = _split_think_and_body(raw_text, existing_think)
    body = rewrite_markdown_image_urls(body, config=config)
    text, cited_sources = rewrite_citations(body, config)
    return {
        'think': think,
        'text': text.strip(),
        'sources': _merge_sources(cited_sources, existing_sources),
    }


def _count_user_turns(history: list[dict[str, Any]], current_query: str | None) -> int:
    count = 0
    for msg in history or []:
        if isinstance(msg, dict) and msg.get('role') == 'user':
            content = msg.get('content')
            if isinstance(content, str) and content.strip():
                count += 1
    if current_query and current_query.strip():
        count += 1
    return count


def _count_tool_turns(history: list[dict[str, Any]]) -> int:
    count = 0
    for msg in history or []:
        if (
            isinstance(msg, dict)
            and msg.get('role') == 'assistant'
            and isinstance(msg.get('tool_calls'), list)
            and msg.get('tool_calls')
        ):
            count += 1
    return count
