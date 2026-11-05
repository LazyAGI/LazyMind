"""Explicit public F4 milestones and response previews in a bounded public snapshot."""
import copy
import json
import re
import time

PUBLIC_STAGES = {
    'input_binding', 'input_validation', 'environment_validation', 'redraw_intent', 'plan_generation', 'figure_planning',
    'analysis_code_generation', 'analysis_execution', 'result_contract_validation',
    'figure_code_generation', 'figure_execution', 'figure_validation',
    'interpretation', 'output_publication',
}
MAX_OUTPUT = 96 * 1024
MAX_REASONING = 32 * 1024


def public_error(error):
    """Expose bounded contract messages, never exception causes or raw trace payloads."""
    from .trace import redact
    if not isinstance(error, dict):
        return None
    code = error.get('code', '')
    if not isinstance(code, str) or not re.fullmatch(r'[A-Z][A-Z_]{0,63}', code):
        return dict(code='EXECUTION_FAILED', message='本次执行失败；诊断已保存，请检查执行记录。')
    message = str(error.get('message', '执行失败'))
    message = re.sub(r'input_value=.*?(?=\n|$)', 'input_value=<omitted>', message)
    message = re.sub(r'\s*For further information visit https?://[^\s]+', '', message)
    message = re.sub(r'\s*\[type=[^\]]+\]', '', message)
    return dict(code=code, field=str(error.get('field', ''))[:160], message=redact(message)[:1200])


class PublicProgress:
    def __init__(self, recorder):
        self.recorder = recorder
        self.steps = {}
        self.last_sent = {}
        self.figures = {}

    def send(self, identifier, force=True):
        if identifier not in self.steps:
            return
        now = time.monotonic()
        if not force and now - self.last_sent.get(identifier, 0) < 1:
            return
        step = self.steps[identifier]
        step['revision'] += 1
        self.last_sent[identifier] = now
        self.flush()

    def flush(self):
        from .trace import timestamp
        steps = copy.deepcopy(list(self.steps.values()))
        remaining = 512 * 1024
        for step in reversed(steps):
            for channel in ('output', 'reasoning'):
                if channel not in step:
                    continue
                output = step[channel]
                raw = output['text'].encode('utf-8')
                output['text'] = raw[:remaining].decode('utf-8', errors='ignore')
                output['truncated'] = output.get('truncated', False) or len(raw) > remaining
                remaining = max(0, remaining - len(raw))
        summary = self.recorder.summary
        error = public_error(summary.get('error'))
        self.recorder._guard(lambda: self.recorder._write_json(self.recorder.root / 'public.json', dict(
            schema_version=1, task_id=summary['task_id'], execution_id=summary['trace_id'],
            revision=sum(s['revision'] for s in steps), process_steps=steps, error=error,
            timing=dict(started_at=summary['started_at'], finished_at=summary.get('ended_at'),
                        measured_at=timestamp()))))

    def owner(self, item):
        while item and item['id'] not in self.steps:
            item = next((s for s in self.recorder.summary['stages'] if s['id'] == item.get('parent_id')), None)
        return item['id'] if item else None

    def begin(self, item, label):
        if item['stage'] in PUBLIC_STAGES and item.get('parent_id') is None:
            suffix = (f" · {self.figures.get(item['figure_id'], item['figure_id'])}" if item.get('figure_id') else '')
            suffix += f" · 第 {item['attempt']} 次" if item.get('attempt') else ''
            self.steps[item['id']] = dict(
                step_id='f4-' + item['id'], revision=0, order=len(self.steps),
                title=(label + suffix)[:100], status='running', summary='', started_at=item['started_at'],
                finished_at=None, elapsed_ms=None)
            self.send(item['id'])
        elif item['stage'] == 'model_transport_attempt':
            owner = self.owner(item)
            if owner:
                attempt = item.get('attempt', 1)
                self.steps[owner]['summary'] = (
                    '等待模型返回内容' if attempt == 1 else
                    f'前次请求尚未返回内容且连接已断开，正在第 {attempt} 次请求；已完成的步骤保留。')
                self.send(owner)

    def end(self, item, exc):
        identifier = item['id']
        if identifier not in self.steps:
            return
        step = self.steps[identifier]
        step.update(status='failed' if exc else 'succeeded', finished_at=item['ended_at'],
                    elapsed_ms=item['duration_ms'])
        if exc:
            from .trace import safe_error
            step['summary'] = public_error(safe_error(exc))['message']
        elif step['summary'].startswith(('等待模型', '首次请求', '前次请求', '已收到推理片段', '正在接收模型', '模型返回已结束', '模型完整结果')):
            step['summary'] = '本阶段已完成。'
        self.send(identifier)

    def note(self, name, value):
        if name in ('figure-plan.json', 'locked-plan.json'):
            self.figures = {f['id']: f.get('title', f['id']) for f in value.get('figures', [])}
        stages = ({'input_validation'} if name == 'input-summary.json' else
                  {'plan_generation'} if name == 'analysis-plan.json' else
                  {'figure_planning'} if name == 'figure-plan.json' else set())
        identifier = next((k for k in reversed(self.steps)
                           if next(s for s in self.recorder.summary['stages'] if s['id'] == k)['stage'] in stages), None)
        if not identifier:
            return
        if name == 'input-summary.json':
            tables = value.get('tables', [])
            summary = f"已读取 {sum(t['rows'] for t in tables)} 行数据、{len(tables)} 张数据表，输入校验通过。"
        elif name == 'analysis-plan.json':
            summary = f"分析提纲已确定：{len(value.get('questions', []))} 个研究问题，具体结果由分析代码生成。"
        else:
            summary = f"已根据执行结果安排 {len(value.get('figures', []))} 张图；可检查比较、来源、单位和实际样本量。"
            from .trace import redact
            raw = redact(json.dumps(value, ensure_ascii=False, indent=2)).encode('utf-8')
            self.steps[identifier]['output'] = dict(text=raw[:MAX_OUTPUT].decode('utf-8', errors='ignore'),
                                                    format='json', mode='final', truncated=len(raw) > MAX_OUTPUT)
        self.steps[identifier]['summary'] = summary
        self.send(identifier)


class ResponsePreview:
    """Keep provider-returned reasoning and formal text in separate bounded previews."""
    def __init__(self, recorder, parent_id, json_output, *, output_format=None):
        self.recorder = recorder
        item = next((s for s in recorder.summary['stages'] if s['id'] == parent_id), None)
        self.owner = recorder.public.owner(item)
        self.text = ''
        self.received_text = False
        self.truncated = False
        self.format = output_format or ('json' if json_output else 'python')
        self.reasoning = ''
        self.reasoning_bytes = 0
        self.reasoning_truncated = False
        self.received_reasoning = False
        from ..runtime_events import current_events
        owner = next((s for s in recorder.summary['stages'] if s['id'] == self.owner), {})
        self.document_preview = (current_events() if owner.get('stage') == 'interpretation'
                                 and self.format == 'markdown' else None)

    def _append_reasoning(self, text):
        if not text:
            return
        self.received_reasoning = True
        if self.reasoning_truncated:
            return
        raw = text.encode('utf-8')
        available = MAX_REASONING - self.reasoning_bytes
        clipped = raw[:available].decode('utf-8', errors='ignore')
        self.reasoning += clipped
        self.reasoning_bytes += len(clipped.encode('utf-8'))
        self.reasoning_truncated |= len(raw) > available
        self._publish_reasoning('streaming')

    def _publish_reasoning(self, mode, force=False):
        if not self.owner or not self.reasoning:
            return
        from .trace import redact
        with self.recorder.lock:
            step = self.recorder.public.steps[self.owner]
            first = 'reasoning' not in step
            visible = redact(self.reasoning).encode('utf-8')
            step['reasoning'] = dict(text=visible[:MAX_REASONING].decode('utf-8', errors='ignore'),
                                     format='text', mode=mode,
                                     truncated=self.reasoning_truncated or len(visible) > MAX_REASONING)
            if mode == 'streaming' and not self.text:
                step['summary'] = '已收到推理片段，等待模型返回正文。'
            self.recorder.public.send(self.owner, force or first)

    def _publish(self, mode, force=False):
        if not self.owner:
            return
        from .trace import redact
        with self.recorder.lock:
            first = 'output' not in self.recorder.public.steps[self.owner]
            visible = redact(self.text).encode('utf-8')[:MAX_OUTPUT].decode('utf-8', errors='ignore')
            self.recorder.public.steps[self.owner]['output'] = dict(
                text=visible, format='text' if self.format == 'markdown' else self.format, mode=mode,
                truncated=self.truncated or len(self.text.encode('utf-8')) > MAX_OUTPUT)
            self.recorder.public.steps[self.owner]['summary'] = (
                '正在接收模型返回的正文' if mode == 'streaming' else
                '模型完整结果已返回，可分段查看。' if mode == 'buffered' else
                '本次模型返回已中断，阶段结果尚未确认。' if mode == 'interrupted' else
                '模型返回已结束，后续将解析与校验。')
            self.recorder.public.send(self.owner, force or first)

    def sink(self, payload):
        delta = payload.get('delta')
        if not delta:
            return
        if payload.get('tag') == 'think':
            self._append_reasoning(str(delta))
            return
        if payload.get('tag') != 'text':
            return
        self.received_text = True
        text = str(delta)
        self.truncated |= len(self.text) + len(text) > MAX_OUTPUT
        self.text += text[:max(0, MAX_OUTPUT - len(self.text))]
        self._publish('streaming')
        if self.document_preview:
            self.document_preview.preview(self.text)

    def finish(self, raw, *, normalized=None):
        from ..plotting.code_response import normalize_response
        normalized = normalized or normalize_response(raw)
        streamed_reasoning = self.received_reasoning
        encoded = normalized.reasoning.encode('utf-8')
        self.reasoning = encoded[:MAX_REASONING].decode('utf-8', errors='ignore')
        self.reasoning_bytes = len(self.reasoning.encode('utf-8'))
        self.reasoning_truncated = len(encoded) > MAX_REASONING
        if self.owner and not self.reasoning:
            with self.recorder.lock:
                step = self.recorder.public.steps[self.owner]
                if 'reasoning' in step:
                    step['reasoning'] = dict(text='', format='text', mode='complete', truncated=False)
        text = (json.dumps(normalized.body, ensure_ascii=False) if isinstance(normalized.body, dict)
                else normalized.body)
        self._publish_reasoning('complete' if streamed_reasoning else 'buffered', force=True)
        self.truncated = len(text.encode('utf-8')) > MAX_OUTPUT
        self.text = text[:MAX_OUTPUT]
        self._publish('complete' if self.received_text else 'buffered', force=True)
        if self.document_preview:
            self.document_preview.preview(text)

    def abort(self):
        self._publish_reasoning('interrupted', force=True)
        self._publish('interrupted', force=True)
