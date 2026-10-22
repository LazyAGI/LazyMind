from __future__ import annotations

import json
import copy
import re
import time
from typing import Any, Optional

from lazymind.config import config as _cfg
from lazymind.chat.runtime_events import RunAccumulator, RunOutcome
from lazymind.chat.service.run_metrics import RunMetricsTracker
from lazymind.chat.service.utils import (
    build_stream_citation_scanner,
    materialize_source_views,
    register_existing_sources,
    reset_citation_state,
    rewrite_markdown_image_urls,
    rewrite_citations,
)
from lazymind.chat.service.utils.citations import added_citation_markers
from lazymind.chat.service.component.chat_exports import ChatExportStream
from lazymind.chat.service.component.tool_rendering import (
    _preview_language,
    _tool_call_frame_text,
    _tool_result_frame_text,
)

_STREAM_CHUNK_SIZE = 24
_CAPABILITY_DEPENDENCY_MARKER = 'MEDIA_CAPABILITY_DEPENDENCY_MISSING'


def _capability_dependency_from_value(value: Any, depth: int = 0) -> Optional[dict[str, Any]]:
    if depth > 6 or value is None:
        return None
    if isinstance(value, dict):
        if value.get('status') == 'blocked' and isinstance(value.get('missing'), list):
            return dict(value)
        for nested in value.values():
            dependency = _capability_dependency_from_value(nested, depth + 1)
            if dependency is not None:
                return dependency
        return None
    if isinstance(value, (list, tuple)):
        for nested in value:
            dependency = _capability_dependency_from_value(nested, depth + 1)
            if dependency is not None:
                return dependency
        return None
    if not isinstance(value, str):
        return None
    marker_index = value.find(_CAPABILITY_DEPENDENCY_MARKER)
    if marker_index < 0:
        return None
    payload_text = value[marker_index + len(_CAPABILITY_DEPENDENCY_MARKER):].lstrip()
    try:
        payload, _ = json.JSONDecoder().raw_decode(payload_text)
    except (TypeError, ValueError):
        return None
    if not isinstance(payload, dict):
        return None
    if payload.get('status') != 'blocked' or not isinstance(payload.get('missing'), list):
        return None
    return payload


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
    citation_plugin: Any = None,
):
    collected = citation_plugin.collect() if citation_plugin is not None else None
    for field, seg in scanned_segments:
        if not seg:
            continue
        if field == 'think':
            yield False, _stream_frame(think=seg)
            continue
        text = rewrite_markdown_image_urls(seg, config=citation_state)
        yield True, _stream_frame(
            text=text,
            sources=collected if collected and '#source-' in text else None,
        )


class AgentEventFrameTranslator:
    def __init__(
        self,
        *,
        query: str,
        run_id: str = '',
        clock=None,
        started_at: Optional[float] = None,
        enable_exports: bool = False,
    ) -> None:
        self.export_stream = ChatExportStream() if enable_exports else None
        self.query = query
        self.run = RunAccumulator(run_id=run_id or 'unbound-run')
        self.citation_state: dict[str, Any] = {}
        reset_citation_state(self.citation_state)
        self.language = _preview_language(query)
        self._pending_previews: dict[str, str] = {}
        self._mail_drafts: dict[str, dict[str, Any]] = {}
        self._ordinary_ask: dict[str, Any] | None = None
        self.streamed_text = False
        self.ask_pending_emitted = False
        self.capability_dependency_emitted = False
        self.tool_call_turns = 0
        self.metrics = RunMetricsTracker(clock or time.monotonic, started_at=started_at)
        self.model_events: list[dict[str, Any]] = []
        self.last_metrics: Optional[dict[str, Any]] = None
        self.text_scanner, self.citation_plugin = build_stream_citation_scanner(self.citation_state)
        self._model_output_index = 0
        self._model_output_checkpoint = None

    def _snapshot_model_output(self):
        mapper = self.citation_plugin.display_mapper
        return {
            'scanner_state': self.text_scanner.state,
            'scanner_buf': self.text_scanner.buf,
            'code_state': self.text_scanner.code_state.copy(),
            'collected': copy.deepcopy(self.citation_plugin._collected),
            'streamed_indices': list(self.citation_plugin._streamed_indices),
            'display_map': dict(mapper._doc_display_map),
            'next_display_index': mapper._next_display_index,
            'cited_indices': list(self.citation_state.get('_cited_source_indices') or []),
            'export': {name: copy.deepcopy(getattr(self.export_stream, name)) for name in
                       ('raw', 'pending', 'emitted', 'offset', 'fence', 'depth', 'candidate', 'blocks')}
            if self.export_stream is not None else None,
            'streamed_text': self.streamed_text,
            'semantic_output': self.run.semantic_output,
        }

    def _restore_model_output(self):
        checkpoint = self._model_output_checkpoint
        if checkpoint is None:
            return
        self.text_scanner.state = checkpoint['scanner_state']
        self.text_scanner.buf = checkpoint['scanner_buf']
        self.text_scanner.code_state = checkpoint['code_state'].copy()
        self.citation_plugin._collected = copy.deepcopy(checkpoint['collected'])
        self.citation_plugin._streamed_indices = list(checkpoint['streamed_indices'])
        mapper = self.citation_plugin.display_mapper
        mapper._doc_display_map = dict(checkpoint['display_map'])
        mapper._next_display_index = checkpoint['next_display_index']
        # The shared registry contains real tool results. Restore only the
        # model's citation projection, keeping the original config dictionary.
        self.citation_state['_cited_source_indices'] = list(checkpoint['cited_indices'])
        if checkpoint['export'] is not None:
            for key, value in checkpoint['export'].items():
                setattr(self.export_stream, key, copy.deepcopy(value))
        self.streamed_text = checkpoint['streamed_text']
        self.run.semantic_output = checkpoint['semantic_output']
        self._model_output_checkpoint = None

    def _export_frame(self, frame):
        if self.export_stream is not None and frame.get('text'):
            frame['text'] = self.export_stream.feed(frame['text'])
        return frame

    def feed(self, event: Any) -> list[dict[str, Any]]:
        output = event.get('model_output')
        if output is None:
            return self._feed(event)
        if not isinstance(output, dict):
            raise ValueError('model_output must be an object')
        if output.get('run_id') not in (None, '', self.run.run_id):
            return []
        index, phase = output.get('call_index'), output.get('phase')
        if type(index) is not int or index < 1 or phase not in (None, 'begin', 'commit', 'rollback'):
            raise ValueError('invalid model output boundary')
        metadata = {'run_id': self.run.run_id, 'call_index': index}
        if phase is not None:
            metadata['phase'] = phase
        if phase is None and event.get('tag') == 'runtime_event' and (
            event.get('runtime_event') or {}
        ).get('type') in {'model_call_started', 'model_retry_scheduled', 'model_call_finished'}:
            semantic_output = self.run.semantic_output
            last_model_terminal = self.run.last_model_terminal
            frames = self._feed(event)
            if index != self._model_output_index or self._model_output_checkpoint is None:
                self.run.semantic_output = semantic_output
                self.run.last_model_terminal = last_model_terminal
            return frames
        if phase == 'begin':
            if index <= self._model_output_index:
                return []
            if self._model_output_checkpoint is not None or index != self._model_output_index + 1:
                raise ValueError('model output began without closing the previous call')
            self._model_output_index = index
            self._model_output_checkpoint = self._snapshot_model_output()
            return [_stream_frame(extra={'model_output': metadata})]
        if index != self._model_output_index or self._model_output_checkpoint is None:
            return []
        if phase == 'rollback':
            self._restore_model_output()
            return [_stream_frame(extra={'model_output': metadata})]
        if phase == 'commit':
            if event.get('tag') == 'runtime_event':
                if (event.get('runtime_event') or {}).get('type') != 'model_switched':
                    raise ValueError('only model_switched can accompany output commit')
                frames = self._feed(event)
            elif event.get('tag') == 'model_output':
                frames = [_stream_frame()]
            else:
                raise ValueError('model output commit must not contain semantic data')
            self._model_output_checkpoint = None
        else:
            if event.get('tag') not in {'text', 'think'}:
                raise ValueError('model output identity is only valid on model content')
            frames = self._feed(event)
        for frame in frames:
            frame['model_output'] = dict(metadata)
        return frames

    def _feed(self, event: Any) -> list[dict[str, Any]]:
        frames: list[dict[str, Any]] = []
        event_type = str(event.get('tag', '') or '')
        if event_type == 'model_fallback_failed':
            code = event.get('fallback_code')
            if code not in {'model_fallback_exhausted', 'model_context_not_fit', 'runtime_failure'}:
                raise ValueError('invalid model fallback terminal code')
            self.run.fallback_code = code
            return frames
        if event_type == 'runtime_event':
            value = event.get('runtime_event')
            if not isinstance(value, dict):
                raise ValueError('runtime_event payload must be an object')
            value = dict(value)
            value['run_id'] = self.run.run_id
            if value.get('schema_version') != 1:
                raise ValueError('unsupported runtime_event schema_version')
            if value.get('type') not in {
                'model_call_started', 'model_retry_scheduled', 'model_call_finished', 'model_switched',
            }:
                raise ValueError('unexpected upstream runtime_event type')
            if not isinstance(value.get('data'), dict):
                raise ValueError('runtime_event data must be an object')
            semantic_output = self.run.semantic_output
            self.run.observe_model_event(value)
            if self._model_output_index and self._model_output_checkpoint is None:
                # A late SDK terminal is still an observed call, but cannot
                # resurrect semantic output after the Host closed its output.
                self.run.semantic_output = semantic_output
            self.model_events.append(value)
            if value.get('type') == 'model_call_started':
                self.metrics.on_model_call_started()
                # This event only establishes the local timing boundary; the
                # browser has no rendering or recovery behavior for it.
                return frames
            elif value.get('type') == 'model_call_finished':
                self.metrics.on_model_call_finished(duration_ms=value['data'].get('duration_ms'))
            client_event = value
            if value.get('type') == 'model_call_finished':
                client_data = dict(value['data'])
                client_data.pop('usage', None)
                client_event = {**value, 'data': client_data}
            frames.append(_stream_frame(extra={'runtime_event': client_event}))
            return frames
        if event_type == 'task_created':
            task_created = {k: v for k, v in event.items() if k != 'tag'}
            frames.append(_stream_frame(extra={'task_created': task_created}))
            return frames
        if event_type == 'artifact_created':
            artifact = {k: v for k, v in event.items() if k != 'tag'}
            frames.append(_stream_frame(extra={'artifact_created': artifact}))
            return frames
        if event_type == 'ask_pending':
            ask_data = {k: v for k, v in event.items() if k != 'tag'}
            mail_draft = ask_data.get('mail_draft')
            extra_drafts = ask_data.get('mail_drafts')
            has_mail_draft = (
                isinstance(mail_draft, dict)
                or (isinstance(extra_drafts, list) and any(isinstance(item, dict) for item in extra_drafts))
            )
            if self.capability_dependency_emitted and not has_mail_draft:
                return frames
            if not has_mail_draft or ask_data.get('mail_draft_only') is False:
                self._ordinary_ask = {
                    key: value for key, value in ask_data.items()
                    if key not in {'mail_draft', 'mail_drafts', 'mail_draft_only', 'mail_mailbox_choice'}
                }
            if isinstance(extra_drafts, list):
                for item in extra_drafts:
                    if isinstance(item, dict) and item.get('draft_id'):
                        self._mail_drafts[str(item['draft_id'])] = item
            if isinstance(mail_draft, dict) and mail_draft.get('draft_id'):
                self._mail_drafts[str(mail_draft['draft_id'])] = mail_draft
            if self._ordinary_ask is not None:
                ask_data = dict(self._ordinary_ask)
            drafts = list(self._mail_drafts.values())
            ask_data['mail_drafts'] = drafts
            ask_data['mail_draft_only'] = self._ordinary_ask is None
            if drafts:
                ask_data['mail_draft'] = mail_draft if isinstance(mail_draft, dict) else drafts[-1]
            awaiting_user = self._ordinary_ask is not None or any(
                item.get('requires_confirmation', str(item.get('status') or '') not in {
                    'sent', 'sending',
                })
                for item in self._mail_drafts.values()
            )
            self.ask_pending_emitted = bool(awaiting_user)
            self.run.ask_pending = bool(awaiting_user)
            frames.append(_stream_frame(extra={'ask_pending': ask_data}))
            return frames
        if event_type == 'tool_configuration':
            frames.append(_stream_frame(extra={'tool_configuration': event.get('action', {})}))
            return frames
        if event_type == 'tool_limit_pending':
            payload = {k: v for k, v in event.items() if k != 'tag'}
            frames.append(_stream_frame(extra={'tool_limit_pending': payload}))
            return frames
        if event_type == 'intent_updated':
            payload = {k: v for k, v in event.items() if k != 'tag'}
            frames.append(_stream_frame(extra={'intent_updated': payload}))
            return frames
        if event_type == 'workflow_preflight_updated':
            payload = {k: v for k, v in event.items() if k != 'tag'}
            frames.append(_stream_frame(extra={'workflow_preflight_updated': payload}))
            return frames
        if event_type == 'model_context_updated':
            payload = {k: v for k, v in event.items() if k != 'tag'}
            frames.append(_stream_frame(extra={'model_context_updated': payload}))
            return frames
        if event_type == 'heartbeat':
            frames.append(_stream_frame(extra={'heartbeat': True}))
            return frames

        if event_type == 'think':
            delta = str(event.get('delta', '') or '')
            if delta:
                self.run.semantic_output = True
                self.metrics.mark_output()
                frames.append(_stream_frame(think=delta))
            return frames

        if event_type == 'text':
            delta = str(event.get('delta', '') or '')
            if not delta:
                return frames
            self.run.semantic_output = True
            self.metrics.mark_output()
            for has_text, frame in _iter_scanned_text_frames(
                self.text_scanner.feed(delta),
                self.citation_state,
                self.citation_plugin,
            ):
                self.streamed_text = self.streamed_text or has_text
                frames.append(self._export_frame(frame))
            return frames

        if event_type in ('tool_calls', 'tool_results') and self.export_stream is not None:
            # Tool previews move preceding commentary into the thinking panel.
            self.export_stream = ChatExportStream()

        if event_type == 'tool_calls':
            tool_calls = [tc for tc in (event.get('tool_calls', []) or []) if isinstance(tc, dict)]
            if tool_calls:
                self.run.semantic_output = True
                self.tool_call_turns += 1
                self.metrics.on_tool_calls(len(tool_calls))
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
                self.run.semantic_output = True
                duration_ms = event.get('duration_ms')
                try:
                    measured = float(duration_ms) if duration_ms is not None else None
                except (TypeError, ValueError):
                    measured = None
                self.metrics.on_tool_results(duration_ms=measured)
                parts = [
                    _tool_result_frame_text(
                        tr,
                        self.language,
                        self._pending_previews.pop(str(tr.get('id', '')), ''),
                    )
                    for tr in tool_results
                ]
                dependency = _capability_dependency_from_value(tool_results)
                if dependency is not None:
                    self.capability_dependency_emitted = True
                frames.append(_stream_frame(
                    text=''.join(parts),
                    extra=(
                        {'capability_dependency': dependency}
                        if dependency is not None else None
                    ),
                ))

        if event_type == 'subagent_think':
            think = str(event.get('think') or '')
            if think:
                self.run.semantic_output = True
                frames.append(_stream_frame(think=think))

        return frames

    def finish_run(
        self,
        *,
        outcome: RunOutcome,
        usage: Optional[dict[str, Any]] = None,
        usage_map: Optional[dict[str, Any]] = None,
        module_id: Optional[str] = None,
        llm_config: Optional[dict[str, Any]] = None,
        turn_seq: Optional[int] = None,
        max_input_tokens: Optional[int] = None,
        prefer_usage_snapshot: bool = False,
    ) -> dict[str, Any]:
        output = None
        if self._model_output_checkpoint is not None:
            output = {'run_id': self.run.run_id, 'call_index': self._model_output_index, 'phase': 'rollback'}
            self._restore_model_output()
            if outcome == RunOutcome.SUCCEEDED:
                outcome = RunOutcome.FAILED
                self.run.fallback_code = 'runtime_failure'
        metrics = self.metrics.snapshot(
            usage=usage,
            usage_map=usage_map,
            model_events=self.model_events,
            module_id=module_id,
            llm_config=llm_config,
            turn_seq=turn_seq,
            max_input_tokens=max_input_tokens,
            prefer_usage_snapshot=prefer_usage_snapshot,
        )
        self.last_metrics = metrics
        client_metrics = {
            key: value for key, value in metrics.items()
            if key != 'provider_usages'
        }
        frame = _stream_frame(extra={
            # Performance data is an observation side-channel. Keep it out of
            # run_terminal so chat-history persistence does not become an
            # observability store.
            'runtime_event': self.run.finish(outcome=outcome),
            # Provider-specific usage frames stay in the local full
            # observation; the browser only needs the normalized summary.
            'performance_metrics': client_metrics,
        })
        if output is not None:
            frame['model_output'] = output
        elif self.export_stream is not None and (
            ':::export' in ''.join(self.export_stream.raw)
            or len(self.export_stream.pending) > self.export_stream.emitted
        ):
            frame['export_snapshot'] = self.export_stream.finish()
        return frame

    def flush(self) -> list[dict[str, Any]]:
        frames: list[dict[str, Any]] = []
        for has_text, frame in _iter_scanned_text_frames(
            self.text_scanner.flush(),
            self.citation_state,
            self.citation_plugin,
        ):
            self.streamed_text = self.streamed_text or has_text
            frames.append(self._export_frame(frame))
        return frames

    def _collect_sources(self) -> Any:
        return self.citation_plugin.collect()

    def finish(self, final_result: Any) -> list[dict[str, Any]]:
        frames = self.flush()
        # ask_user is a stop tool. Its return value is an internal execution
        # receipt, while the preceding ask_pending event is the user-facing
        # response. Never stream that receipt as ordinary assistant text.
        if self.ask_pending_emitted or self.capability_dependency_emitted:
            return frames
        output = _format_final_result(
            final_result,
            self.citation_state,
            display_mapper=self.citation_plugin.display_mapper,
            streamed_citation_indices=self.citation_plugin.streamed_indices,
        )
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
                frames.append(self._export_frame(_stream_frame(text=chunk)))
        else:
            suffix = str(output.get('citation_suffix') or '')
            if suffix:
                for chunk in _iter_text_chunks(suffix, chunk_size):
                    frames.append(self._export_frame(_stream_frame(text=chunk)))

        sources = materialize_source_views(
            self.citation_state,
            [
                *(output.get('source_views') or []),
                *(self._collect_sources() or []),
            ],
        )
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


def _format_final_result(
    result: Any,
    config: dict,
    display_mapper: Any = None,
    streamed_citation_indices: tuple[str, ...] = (),
) -> dict[str, Any]:
    if isinstance(result, dict):
        raw_text = str(result.get('text') or result.get('message') or '')
        existing_think = result.get('think') or result.get('reasoning_content') or ''
        existing_sources = result.get('sources')
    else:
        raw_text = '' if result is None else str(result)
        existing_think = ''
        existing_sources = None

    register_existing_sources(config, existing_sources)
    think, body = _split_think_and_body(raw_text, existing_think)
    body = rewrite_markdown_image_urls(body, config=config)
    text, cited_sources = rewrite_citations(body, config, display_mapper=display_mapper)
    suffix_markers = added_citation_markers(streamed_citation_indices, body)
    citation_suffix = ''
    extra_cited: list[dict[str, Any]] = []
    if suffix_markers:
        citation_suffix, extra_cited = rewrite_citations(
            suffix_markers, config, display_mapper=display_mapper,
        )
    return {
        'think': think,
        'text': text.strip(),
        'citation_suffix': citation_suffix,
        'source_views': [
            *(existing_sources if isinstance(existing_sources, list) else []),
            *cited_sources,
            *extra_cited,
        ],
    }
