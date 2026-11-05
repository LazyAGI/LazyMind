"""Execution-scoped paper previews and public stages shared by every paper algorithm."""
from contextlib import contextmanager
from contextvars import ContextVar
from datetime import datetime, timezone
import logging
import re
from threading import RLock
import time
import uuid

from ..writing import DraftMarkdownStreamEventEmitter

_current = ContextVar('paper_runtime_events', default=None)
_log = logging.getLogger(__name__)


class _PaperPreview(DraftMarkdownStreamEventEmitter):
    # Preserve provider chunks instead of manufacturing thousands of character events.
    MAX_DELTA_CHARS = 4096


class PaperRuntimeEvents:
    def __init__(self, emit, slot):
        self._emit = emit
        self._slot = slot
        self._previews = {}
        self._contents = {}
        self._progress = 0
        self._last = None
        self._closed = False
        self._lock = RLock()
        self._stage = None
        self._stage_started = None
        self._sequence = 0
        self._stages = {}

    def begin_stage(self, identifier, title, percent=None):
        with self._lock:
            if self._closed:
                return
            previous = self._finish_stage('succeeded')
            item = {'step_id': 'paper-' + identifier, 'revision': 1, 'order': self._sequence,
                    'title': title[:100], 'status': 'running',
                    'started_at': datetime.now(timezone.utc).isoformat(),
                    'finished_at': None, 'elapsed_ms': None}
            self._stages[identifier] = (item, time.monotonic())
            self._progress = max(self._progress, min(99, int(percent or 0)))
            self._send(self._progress, title, [s for s in (previous, item) if s])

    def end_stage(self, identifier, error=None):
        with self._lock:
            stored = self._stages.pop(identifier, None)
            if stored is None or self._closed:
                return
            item, started = stored
            item = {**item, 'revision': item['revision'] + 1, 'status': 'failed' if error else 'succeeded',
                    'finished_at': datetime.now(timezone.utc).isoformat(),
                    'elapsed_ms': round((time.monotonic() - started) * 1000)}
            self._send(self._progress, item['title'] + ('失败' if error else '已完成'), [item])

    def _finish_stage(self, status):
        if self._stage is None:
            return None
        stage = {**self._stage, 'revision': self._stage['revision'] + 1, 'status': status,
                 'finished_at': datetime.now(timezone.utc).isoformat(),
                 'elapsed_ms': round((time.monotonic() - self._stage_started) * 1000)}
        self._stage = None
        return stage

    def _send(self, percent, phase, stages):
        self._sequence += 1
        event = {'type': 'progress', 'progress': percent, 'current_phase': phase,
                 'public_display': {'schema_version': 1, 'event_key': f'paper-{self._sequence}',
                                    'process_steps': stages}}
        # One source supplies both the public timeline and legacy execution text.
        try:
            self._emit({'type': 'text', 'text': phase + '\n'})
            self._emit(event)
        except Exception:
            _log.warning('Paper progress delivery failed', exc_info=True)

    def progress(self, percent, phase):
        with self._lock:
            if self._closed:
                return
            # Only Core may declare success after all required artifacts are published.
            self._progress = max(self._progress, min(99, int(percent)))
            state = (self._progress, phase)
            if state == self._last:
                return
            previous = self._finish_stage('succeeded') if self._last is None or phase != self._last[1] else None
            if self._stage is None:
                self._stage = {'step_id': 'paper-' + uuid.uuid4().hex, 'revision': 1,
                               'order': self._sequence, 'title': phase[:100], 'status': 'running',
                               'started_at': datetime.now(timezone.utc).isoformat(),
                               'finished_at': None, 'elapsed_ms': None}
                self._stage_started = time.monotonic()
            else:
                self._stage['revision'] += 1
            self._last = state
            self._send(self._progress, phase, [s for s in (previous, self._stage.copy()) if s])

    def section_progress(self, event):
        total = int(event.get('section_total') or 0)
        done = int(event.get('section_completed') or 0)
        percent = 30 + int(50 * min(done, total) / total) if total else 30
        phase = f'正在生成正文，已完成 {done}/{total} 章' if total else '正在生成正文'
        self.progress(percent, phase)

    def delta(self, text, slot=None):
        with self._lock:
            if self._closed or not text:
                return
            slot = slot or self._slot
            if slot not in self._previews:
                self._previews[slot] = _PaperPreview(self._emit, slot=slot)
            self._previews[slot].feed(text)
            self._contents[slot] = self._contents.get(slot, '') + text

    def preview(self, text, slot=None):
        """Project structured output without appending stale JSON-derived content."""
        with self._lock:
            slot = slot or self._slot
            previous = self._contents.get(slot, '')
            if self._closed or not text or previous == text:
                return
            if text.startswith(previous):
                self.delta(text[len(previous):], slot)
            else:
                self.restart(text, slot)

    def restart(self, prefix, slot=None):
        with self._lock:
            if self._closed:
                return
            slot = slot or self._slot
            if slot not in self._previews:
                self.delta(prefix, slot)
            else:
                self._previews[slot].restart(prefix)
                self._contents[slot] = prefix

    def completed_preview(self, text, slot=None):
        """Replace generated text in one event, never replay a completed document."""
        with self._lock:
            if self._closed or not text:
                return
            slot = slot or self._slot
            if self._contents.get(slot) != text:
                if slot in self._previews:
                    self._previews[slot].abort('预览已由完整结果替换。')
                self._previews[slot] = _PaperPreview(self._emit, slot=slot)
                self._previews[slot].feed_complete(text)
                self._contents[slot] = text
            elif slot in self._previews:
                self._previews[slot].end()

    def finish_preview(self):
        with self._lock:
            for preview in self._previews.values():
                preview.end()

    def complete(self):
        with self._lock:
            self.finish_preview()
            self.progress(99, '产物已生成，正在确认发布')
            self._closed = True

    def wait_for_input(self, question):
        with self._lock:
            stage = self._finish_stage('succeeded')
            self._send(self._progress, '等待确认：' + question, [stage] if stage else [])
            self._closed = True

    def abort(self):
        with self._lock:
            for preview in self._previews.values():
                preview.abort('生成未完成，预览已撤销；请查看任务错误。')
            stage = self._finish_stage('failed')
            if stage:
                self._send(self._progress, '本次执行未完成', [stage])
            for identifier in list(self._stages):
                self.end_stage(identifier, error=True)
            self._closed = True


class StructuredPreview:
    """Bounded partial-JSON parsing; each algorithm only supplies a public renderer."""
    def __init__(self, render, *, slot=None, events=None):
        self.events = events if events is not None else current_events()
        self.render = render
        self.slot = slot
        self.buffer = ''
        self.last_parse = 0.0

    def __call__(self, event):
        if self.events is None or event.get('tag') != 'text':
            return
        delta = event.get('delta')
        if not isinstance(delta, str):
            return
        self.buffer += delta
        if len(self.buffer) > 1_000_000:
            raise ValueError('模型预览超过 1 MB，已停止生成')
        if time.monotonic() - self.last_parse < 0.2:
            return
        self.flush()

    def flush(self):
        if self.events is None or not self.buffer:
            return
        from json_repair import loads
        self.last_parse = time.monotonic()
        try:
            value = loads(self.buffer, stream_stable=True)
        except (ValueError, TypeError, IndexError):
            return
        if isinstance(value, dict):
            text = self.render(value)
            if text:
                self.events.preview(text, self.slot)


def generate_markdown(model, prompt, *, bindings=None):
    """Shared Markdown transport, reasoning filtering and split citation recovery."""
    from lazyllm.tools.writer.tools.base import WriterToolBase
    events = current_events()
    pending = ''
    leading = True

    def restore(text):
        return bindings.restore_citations(text) if bindings is not None else text

    def on_delta(event):
        nonlocal pending, leading
        if event.get('tag') != 'text' or not isinstance(event.get('delta'), str):
            return
        pending += event['delta']
        if leading:
            pending = pending.lstrip()
            # Some providers put reasoning in text, with tags split across chunks.
            while pending.lower().startswith('<think'):
                close = re.search(r'</think>\s*', pending, re.I)
                if not close:
                    return
                pending = pending[close.end():].lstrip()
            if not pending or '<think'.startswith(pending.lower()):
                return
            leading = False
        partial = re.search(r'\[(?:M\d*)?$', pending)
        end = partial.start() if partial else len(pending)
        events.delta(restore(pending[:end]))
        pending = pending[end:]

    streaming = events is not None and callable(getattr(model, 'share', None))
    text = WriterToolBase(llm=model)._call_llm_text(
        prompt, stream_output={'_stream_sink': on_delta} if streaming else False,
    ).strip()
    if re.match(r'^<think\b', text, re.I):
        raise ValueError('模型输出存在未闭合的思考内容。')
    if events and pending and not leading:
        events.delta(restore(pending))
    if events and not streaming:
        events.completed_preview(restore(text))
    return restore(text)


def current_events():
    return _current.get()


def report_progress(percent, phase):
    events = current_events()
    if events is not None:
        events.progress(percent, phase)


@contextmanager
def bind_runtime_events(emit, slot):
    events = PaperRuntimeEvents(emit, slot)
    token = _current.set(events)
    try:
        yield events
    except BaseException:
        events.abort()
        raise
    finally:
        _current.reset(token)
