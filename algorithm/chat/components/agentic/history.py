from __future__ import annotations

import json
import re
from collections import OrderedDict
from html import escape
from typing import Any, Optional

from chat.utils.markdown_images import rewrite_markdown_image_urls
from chat.utils.stream_scanner import (
    BasePlugin,
    IncrementalScanner,
    MarkdownImageHoldPlugin,
)

from chat.components.agentic.tool_stream import (
    _TOOL_CALL_TAG,
    _TOOL_PREVIEW_TAG,
    _TOOL_RESULT_PREVIEW_TAG,
    _TOOL_RESULT_TAG,
)

_CITATION_REFS_KEY = '_citation_sources'
_CITATION_KEY_MAP_KEY = '_citation_key_map'
_CITATION_NEXT_KEY = '_citation_next_index'
_CITATION_DOC_KEY_MAP_KEY = '_citation_doc_key_map'
_CITATION_NEXT_DOC_KEY = '_citation_next_doc_index'
_CITATION_DOC_CHUNK_NEXT_KEY = '_citation_next_chunk_index_map'
_CITATION_INDEX_PATTERN = r'\d+\.\d+'
_CITATION_PATTERN = re.compile(r'\[\[(' + _CITATION_INDEX_PATTERN + r')\]\]')
_SOURCE_LINK_PATTERN = re.compile(r'\[(\d+)\]\(#source-(' + _CITATION_INDEX_PATTERN + r')(?:\s+"[^"]*")?\)')
_SOURCE_REF_PATTERN = re.compile(r'\[\[(' + _CITATION_INDEX_PATTERN + r')\]\]')
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


def _history_citation_key(item: dict[str, Any]) -> Optional[str]:
    uid = item.get('uid') or item.get('segement_id')
    if uid:
        return f'uid:{uid}'
    docid = item.get('docid') or item.get('document_id')
    group = item.get('group') or item.get('group_name')
    number = item.get('number') or item.get('segment_number')
    if docid and group and number is not None:
        return f'node:{docid}:{group}:{number}'
    text = item.get('text') or item.get('content')
    if docid and text:
        return f'text:{docid}:{str(text)[:80]}'
    return None


def _history_document_citation_key(item: dict[str, Any]) -> Optional[str]:
    metadata = item.get('metadata') if isinstance(item.get('metadata'), dict) else {}
    global_md = item.get('global_metadata') if isinstance(item.get('global_metadata'), dict) else {}
    docid = item.get('docid') or item.get('document_id') or global_md.get('docid')
    if not docid:
        return None
    dataset_id = item.get('kb_id') or item.get('dataset_id') or global_md.get('kb_id') or metadata.get('kb_id') or ''
    return f'doc:{dataset_id}:{docid}'


def _split_citation_index(index: Any) -> tuple[int | None, int | None]:
    if isinstance(index, str) and '.' in index:
        document_index, chunk_index = index.split('.', 1)
        if document_index.isdigit() and chunk_index.isdigit():
            return int(document_index), int(chunk_index)
    return None, None


def _history_source_node_from_item(index: str, item: dict[str, Any]) -> dict[str, Any]:
    metadata = item.get('metadata') if isinstance(item.get('metadata'), dict) else {}
    global_md = item.get('global_metadata') if isinstance(item.get('global_metadata'), dict) else {}
    content = item.get('text') if item.get('text') is not None else item.get('content', '')
    document_index, chunk_index = _split_citation_index(index)
    return {
        'file_id': '',
        'file_name': (
            item.get('file_name')
            or global_md.get('file_name')
            or metadata.get('file_name')
            or metadata.get('source')
            or 'title_example'
        ),
        'document_id': item.get('docid') or item.get('document_id') or global_md.get('docid', ''),
        'segement_id': item.get('uid') or item.get('segement_id') or '',
        'dataset_id': item.get('kb_id') or item.get('dataset_id') or global_md.get('kb_id', ''),
        'index': index,
        'display_index': item.get('display_index') or document_index,
        'document_index': item.get('document_index') or document_index,
        'chunk_index': item.get('chunk_index') if item.get('chunk_index') is not None else chunk_index,
        'content': content or '',
        'group_name': item.get('group') or item.get('group_name') or '',
        'segment_number': (
            metadata.get('store_num')
            or metadata.get('lazyllm_store_num')
            or item.get('number')
            or item.get('segment_number')
            or -1
        ),
        'page': metadata.get('page', -1),
        'bbox': metadata.get('bbox', []),
    }


def _history_citation_index(item: dict[str, Any]) -> Optional[str]:
    raw_index = item.get('citation_index') or item.get('index')
    if isinstance(raw_index, str) and re.fullmatch(_CITATION_INDEX_PATTERN, raw_index):
        return raw_index
    ref = item.get('ref')
    if isinstance(ref, str):
        match = _SOURCE_REF_PATTERN.fullmatch(ref.strip())
        if match:
            return match.group(1)
    return None


def _restore_history_index_state(index: str, item: dict[str, Any], config: dict[str, Any]) -> None:
    document_index, chunk_index = _split_citation_index(index)
    if document_index is None or chunk_index is None:
        return
    doc_key = _history_document_citation_key(item)
    if not doc_key:
        return
    doc_key_map = config.setdefault(_CITATION_DOC_KEY_MAP_KEY, {})
    doc_chunk_next_map = config.setdefault(_CITATION_DOC_CHUNK_NEXT_KEY, {})
    doc_key_map[doc_key] = document_index
    next_doc_index = int(config.get(_CITATION_NEXT_DOC_KEY) or 1)
    if document_index >= next_doc_index:
        config[_CITATION_NEXT_DOC_KEY] = document_index + 1
    next_chunk_index = int(doc_chunk_next_map.get(doc_key) or 1)
    if chunk_index >= next_chunk_index:
        doc_chunk_next_map[doc_key] = chunk_index + 1


def _restore_history_citation_item(item: dict[str, Any], config: dict[str, Any]) -> None:
    index = _history_citation_index(item)
    if index is None:
        return
    text = item.get('text') if item.get('text') is not None else item.get('content')
    if not text:
        return

    refs = config.setdefault(_CITATION_REFS_KEY, {})
    key_map = config.setdefault(_CITATION_KEY_MAP_KEY, {})
    _restore_history_index_state(index, item, config)

    source = _history_source_node_from_item(index, item)
    existing = refs.get(index) or refs.get(str(index))
    if not isinstance(existing, dict) or (not existing.get('content') and source.get('content')):
        refs[index] = source

    key = _history_citation_key(item)
    if key:
        key_map[key] = index


def _restore_history_citations(result: Any, config: Optional[dict[str, Any]]) -> None:
    if config is None:
        return
    if isinstance(result, dict):
        _restore_history_citation_item(result, config)
        for value in result.values():
            _restore_history_citations(value, config)
        return
    if isinstance(result, list):
        for item in result:
            _restore_history_citation_item(item, config)


def _restore_source_links_to_refs(text: str) -> str:
    return _SOURCE_LINK_PATTERN.sub(lambda match: f'[[{match.group(2)}]]', text or '')


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
                        _restore_history_citations(seg['result'], config)
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


def _reset_citation_state(config: dict) -> None:
    config[_CITATION_REFS_KEY] = {}
    config[_CITATION_KEY_MAP_KEY] = {}
    config[_CITATION_NEXT_KEY] = 1
    config[_CITATION_DOC_KEY_MAP_KEY] = {}
    config[_CITATION_NEXT_DOC_KEY] = 1
    config[_CITATION_DOC_CHUNK_NEXT_KEY] = {}
    config['_image_url_registry'] = {}


def _citation_source(config: dict, index: str) -> Optional[dict[str, Any]]:
    refs = config.get(_CITATION_REFS_KEY)
    if not isinstance(refs, dict):
        return None
    source = refs.get(index) or refs.get(str(index))
    return source if isinstance(source, dict) else None


def _citation_link(index: str, source: dict[str, Any]) -> str:
    document_index, _ = _split_citation_index(index)
    display_index = source.get('display_index') or source.get('document_index') or document_index
    title = escape(str(source.get('file_name') or 'title'), quote=True)
    return f'[{display_index}](#source-{index} "{title}")'


def _rewrite_citations(text: str, config: dict) -> tuple[str, list[dict[str, Any]]]:
    collected: OrderedDict[str, dict[str, Any]] = OrderedDict()

    def _replace(match: re.Match) -> str:
        index = match.group(1)
        source = _citation_source(config, index)
        if not source:
            return ''
        collected.setdefault(index, source)
        return _citation_link(index, source)

    rewritten = _CITATION_PATTERN.sub(_replace, text)

    for match in _SOURCE_LINK_PATTERN.finditer(rewritten):
        index = match.group(2)
        source = _citation_source(config, index)
        if source:
            collected.setdefault(index, source)

    return rewritten, list(collected.values())


def _registered_citation_sources(config: dict) -> list[dict[str, Any]]:
    refs = config.get(_CITATION_REFS_KEY)
    if not isinstance(refs, dict):
        return []
    return [source for source in refs.values() if isinstance(source, dict)]


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


def _format_non_stream_result(result: Any, config: dict) -> dict[str, Any]:
    if isinstance(result, dict):
        raw_text = str(result.get('text') or result.get('message') or '')
        existing_think = result.get('think') or result.get('reasoning_content') or ''
        output = dict(result)
    else:
        raw_text = '' if result is None else str(result)
        existing_think = ''
        output = {}

    think, body = _split_think_and_body(raw_text, existing_think)
    body = rewrite_markdown_image_urls(body, config=config)
    text, sources = _rewrite_citations(body, config)
    output.update({
        'think': think,
        'text': text.strip(),
        'sources': sources or _registered_citation_sources(config),
    })
    return output


class _ConfigCitationPlugin(BasePlugin):
    prefix_set = {'['}
    _pat = _CITATION_PATTERN
    _link_pat = _SOURCE_LINK_PATTERN

    def __init__(self, config: dict[str, Any]):
        self._config = config
        self._collected: OrderedDict[str, dict[str, Any]] = OrderedDict()

    def match(self, src: str, pos: int):
        link_match = self._link_pat.match(src, pos)
        if link_match:
            index = link_match.group(2)
            source = _citation_source(self._config, index)
            if source:
                self._collected.setdefault(index, source)
            return (link_match.end(), link_match.group(0))

        match = self._pat.match(src, pos)
        if not match:
            return None
        index = match.group(1)
        source = _citation_source(self._config, index)
        if not source:
            return (match.end(), '')
        self._collected.setdefault(index, source)
        return (match.end(), _citation_link(index, source))

    def collect(self) -> list[dict[str, Any]]:
        return list(self._collected.values())

    def last_incomplete_pos(self, buf: str) -> int | None:
        last_double = buf.rfind('[[')
        if last_double != -1 and ']]' not in buf[last_double + 2:]:
            return last_double
        source_link_start = buf.rfind('](#source')
        if source_link_start != -1 and ')' not in buf[source_link_start:]:
            open_bracket = buf.rfind('[', 0, source_link_start)
            if open_bracket != -1:
                return open_bracket
        if buf.endswith('['):
            return len(buf) - 1
        return None


def _build_stream_citation_scanner(
    config: dict[str, Any],
) -> tuple[IncrementalScanner, _ConfigCitationPlugin]:
    plugin = _ConfigCitationPlugin(config)
    return IncrementalScanner(
        [plugin, MarkdownImageHoldPlugin()],
        initial_state='BODY',
    ), plugin


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
