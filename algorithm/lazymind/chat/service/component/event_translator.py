from __future__ import annotations

import re
from typing import Any, Optional

from lazymind.config import config as _cfg
from lazymind.chat.service.utils import (
    build_stream_citation_scanner,
    reset_citation_state,
    rewrite_markdown_image_urls,
    rewrite_citations,
)
from lazymind.chat.service.component.tool_rendering import (
    _preview_language,
    _tool_call_frame_text,
    _tool_result_frame_text,
)

_STREAM_CHUNK_SIZE = 24


def _stream_frame(
    *,
    think: Optional[str] = None,
    text: Optional[str] = None,
    sources: Optional[list[dict[str, Any]]] = None,
    extra: Optional[dict[str, Any]] = None,
) -> dict[str, Any]:
    frame = {
        'think': think,
        'text': text,
        'sources': sources or [],
    }
    if extra:
        frame.update(extra)
    return frame


def _iter_text_chunks(text: str, chunk_size: int = _STREAM_CHUNK_SIZE):
    if not text:
        return
    if '![' in text:
        yield text
        return
    chunk_size = max(1, int(chunk_size or _STREAM_CHUNK_SIZE))
    for start in range(0, len(text), chunk_size):
        yield text[start:start + chunk_size]


def _iter_scanned_text_frames(
    scanned_segments: Any,
    citation_state: dict[str, Any],
):
    for field, seg in scanned_segments:
        if not seg:
            continue
        if field == 'think':
            yield False, _stream_frame(think=seg)
            continue
        yield True, _stream_frame(
            text=rewrite_markdown_image_urls(seg, config=citation_state),
        )


class AgentEventFrameTranslator:
    def __init__(self, *, query: str) -> None:
        self.query = query
        self.citation_state: dict[str, Any] = {}
        reset_citation_state(self.citation_state)
        self.language = _preview_language(query)
        self._pending_previews: dict[str, str] = {}
        self.streamed_text = False
        self.tool_call_turns = 0
        self.text_scanner, self.citation_plugin = build_stream_citation_scanner(self.citation_state)

    def feed(self, event: Any) -> list[dict[str, Any]]:
        frames: list[dict[str, Any]] = []
        event_type = str(event.get('tag', '') or '')
        if event_type == 'task_created':
            task_created = {k: v for k, v in event.items() if k != 'tag'}
            frames.append(_stream_frame(extra={'task_created': task_created}))
            return frames
        if event_type == 'ask_pending':
            ask_data = {k: v for k, v in event.items() if k != 'tag'}
            frames.append(_stream_frame(extra={'ask_pending': ask_data}))
            return frames
        if event_type == 'intent_updated':
            payload = {k: v for k, v in event.items() if k != 'tag'}
            frames.append(_stream_frame(extra={'intent_updated': payload}))
            return frames
        if event_type == 'heartbeat':
            frames.append(_stream_frame(extra={'heartbeat': True}))
            return frames

        if event_type == 'think':
            delta = str(event.get('delta', '') or '')
            if delta:
                frames.append(_stream_frame(think=delta))
            return frames

        if event_type == 'text':
            delta = str(event.get('delta', '') or '')
            if not delta:
                return frames
            for has_text, frame in _iter_scanned_text_frames(
                self.text_scanner.feed(delta), self.citation_state,
            ):
                self.streamed_text = self.streamed_text or has_text
                frames.append(frame)
            return frames

        if event_type == 'tool_calls':
            tool_calls = [tc for tc in (event.get('tool_calls', []) or []) if isinstance(tc, dict)]
            if tool_calls:
                self.tool_call_turns += 1
                parts: list[str] = []
                for tc in tool_calls:
                    text, pv = _tool_call_frame_text(tc, self.language)
                    parts.append(text)
                    if pv:
                        self._pending_previews[str(tc.get('id', ''))] = pv
                frames.append(_stream_frame(text=''.join(parts)))
            return frames

        if event_type == 'tool_results':
            tool_results = [tr for tr in (event.get('tool_results', []) or []) if isinstance(tr, dict)]
            if tool_results:
                parts = [
                    _tool_result_frame_text(
                        tr,
                        self.language,
                        self._pending_previews.pop(str(tr.get('id', '')), ''),
                    )
                    for tr in tool_results
                ]
                frames.append(_stream_frame(text=''.join(parts)))

        if event_type == 'subagent_think':
            think = str(event.get('think') or '')
            if think:
                frames.append(_stream_frame(think=think))

        return frames

    def flush(self) -> list[dict[str, Any]]:
        frames: list[dict[str, Any]] = []
        for has_text, frame in _iter_scanned_text_frames(
            self.text_scanner.flush(), self.citation_state,
        ):
            self.streamed_text = self.streamed_text or has_text
            frames.append(frame)
        return frames

    def _collect_sources(self) -> Any:
        return self.citation_plugin.collect()

    def finish(self, final_result: Any) -> list[dict[str, Any]]:
        frames = self.flush()
        output = _format_final_result(final_result, self.citation_state)
        chunk_size = int(_cfg['agentic_stream_chunk_size'] or _STREAM_CHUNK_SIZE)

        if not self.streamed_text:
            think = str(output.get('think') or '')
            if think:
                for chunk in _iter_text_chunks(think, chunk_size):
                    frames.append(_stream_frame(think=chunk))

            final_text = rewrite_markdown_image_urls(
                str(output.get('text') or ''),
                config=self.citation_state,
            )
            for chunk in _iter_text_chunks(final_text, chunk_size):
                frames.append(_stream_frame(text=chunk))

        sources = output.get('sources') or self._collect_sources()
        if sources:
            frames.append(_stream_frame(text='', sources=sources))

        return frames


_THINK_BLOCK_PATTERN = re.compile(r'<think>(.*?)</think>', re.DOTALL)


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
