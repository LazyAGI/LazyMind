"""Project F3 model deltas onto existing task logs and attempt-scoped artifact previews."""
import json
import time
from contextlib import contextmanager
from threading import Event, Thread

from ..runtime_events import PaperRuntimeEvents, StructuredPreview, current_events
from .schema import ExperimentDraft, Preview

GENERATION_HEARTBEAT_SECONDS = 15


@contextmanager
def generation_progress(on_event):
    """Keep model generation observable even when the provider sends no usable preview."""
    done = Event()
    started = time.monotonic()

    def waiting():
        while not done.wait(GENERATION_HEARTBEAT_SECONDS):
            on_event({'tag': 'heartbeat', 'elapsed_seconds': int(time.monotonic() - started)})

    worker = Thread(target=waiting, daemon=True) if on_event else None
    if worker:
        worker.start()
    try:
        yield
    finally:
        done.set()
        if worker:
            worker.join()


class ExperimentStream:
    def __init__(self, emit, topic, slots):
        self.emit = emit
        self.topic = topic
        self.slots = set(slots)
        self.events = current_events() or PaperRuntimeEvents(emit, 'experiment_document')
        self.streams = self.events._previews
        self.contents = self.events._contents
        self._structured = StructuredPreview(self._project, events=self.events)
        self.think = ''
        self.last_parse = 0.0
        self.last_think = time.monotonic()
        self.last_phase = ''
        self.progress = 0
        self.phase('正在设计实验并生成方案', 5)

    def phase(self, text, progress):
        if progress >= self.progress and text != self.last_phase:
            self.progress = progress
            self.last_phase = text
            self.events.progress(progress, text)

    def __call__(self, event):
        tag, delta = event.get('tag'), event.get('delta')
        if tag == 'heartbeat':
            self.emit({'type': 'progress', 'progress': self.progress,
                       'current_phase': f'{self.last_phase}（已等待 {event["elapsed_seconds"]} 秒，尚未执行代码）'})
        elif tag == 'phase':
            self.flush_think()
            self.phase(event['phase'], 85)
        elif tag == 'think' and isinstance(delta, str):
            self.think += delta
            if len(self.think) >= 512 or time.monotonic() - self.last_think >= 0.3:
                self.flush_think()
        elif tag == 'text' and isinstance(delta, str):
            self.flush_think()
            self._structured.last_parse = self.last_parse
            self._structured(event)
            self.last_parse = self._structured.last_parse

    def flush_think(self):
        if self.think:
            self.emit({'type': 'think', 'think': self.think})
            self.think = ''
            self.last_think = time.monotonic()

    def publish(self, slot, text):
        if slot not in self.slots or not text or self.contents.get(slot) == text:
            return
        self.events.preview(text, slot)

    def preview(self):
        self._structured.flush()

    def _project(self, candidate):
        # Model fields are projected to the same preview filenames as host packaging.
        code, config, dependencies = (candidate.get(k) for k in ('code', 'config', 'dependencies'))
        candidate = ExperimentDraft.discard_extra_model_fields(candidate)
        plan, explanation = candidate.get('plan'), candidate.get('explanation')
        if isinstance(plan, str) and plan:
            text = f'# 实验研究 · {self.topic}\n\n> 生成中的预览，尚未完成校验；未运行实验。\n\n## 实验方案\n{plan}'
            if isinstance(explanation, str) and explanation:
                text += '\n\n## 代码与运行说明\n' + explanation
                self.phase('正在生成实验代码与运行说明', 30)
            self.publish('experiment_document', text)
        files = candidate.get('code_files')
        if isinstance(code, str):
            files = [{'path': 'experiment.py', 'content': code}]
            if isinstance(config, dict):
                files.append({'path': 'config.json', 'content': json.dumps(config, ensure_ascii=False, indent=2)})
            if isinstance(dependencies, list) and all(isinstance(v, str) for v in dependencies):
                files.append({'path': 'requirements.txt', 'content': '\n'.join(dependencies)})
        if isinstance(files, list):
            languages = {'experiment.py': 'python', 'config.json': 'json',
                         'requirements.txt': 'text', 'README.md': 'markdown'}
            parts = []
            for item in files:
                if isinstance(item, dict) and item.get('path') in languages and isinstance(item.get('content'), str):
                    name = item['path']
                    parts.append(f'## {name}\n```{languages[name]}\n' + item['content'])
            if parts:
                self.phase('正在生成实验代码包', 50)
                self.publish('experiment_code', '# 实验代码（预览，未执行）\n\n' + '\n```\n\n'.join(parts))
        if isinstance(candidate.get('results'), dict):
            try:
                table = Preview.model_validate(candidate['results']).table().model_dump()
            except ValueError:
                return
            self.phase('正在整理模拟结果与来源说明', 75)
            self.publish('experiment_results', '# 模拟结果（预览，未执行）\n\n```json\n'
                         + json.dumps(table, ensure_ascii=False, indent=2) + '\n```\n')

    def validated(self, output):
        self.flush_think()
        self.events.completed_preview(output['experiment']['report'], 'experiment_document')
        table = output['experiment']['results']
        if table is not None:
            from .handoff import source_notice
            preview = {**table, 'rows': table['rows'][:20]}
            self.publish('experiment_results', '# 实验结果\n\n' + source_notice(output['result_meta']) + '\n\n```json\n'
                         + json.dumps(preview, ensure_ascii=False, indent=2) + '\n```\n'
                         + f'共 {len(table["rows"])} 行，以上展示前 20 行；完整数据见结果文件。\n')
        self.phase('校验通过，正在保存实验方案、代码包和结果', 95)

    def close(self, error=None):
        self.flush_think()
        for stream in self.streams.values():
            if error is not None:
                stream.abort('本次生成未完成校验或保存，预览已作废：' + str(error)[:300])
            else:
                stream.end()
