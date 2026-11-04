"""Small, durable F4 diagnostics, independent of successful artifact publication."""
from contextlib import contextmanager
from contextvars import ContextVar
from datetime import datetime, timezone
from functools import wraps
import hashlib
import json
import logging
from pathlib import Path
import re
import threading
import time
import uuid

LOG = logging.getLogger(__name__)
_current = ContextVar('paper_f4_diagnostics', default=None)
_parent = ContextVar('paper_f4_trace_parent', default=None)
MAX_ARTIFACT_BYTES = 2 * 1024 * 1024
MAX_TOTAL_BYTES = 8 * 1024 * 1024
MAX_EVENTS = 2048
MILESTONES = {'input_validation': 10, 'plan_validation': 30, 'result_contract_validation': 55,
              'output_validation': 90, 'output_publication': 100}
LABELS = {
    'input_binding': '读取输入', 'input_validation': '校验输入', 'environment_validation': '检查执行环境',
    'plan_generation': '生成简短分析提纲', 'figure_planning': '根据分析结果安排图表',
    'plan_validation': '校验分析计划', 'redraw_intent': '确认是否仅重画图表',
    'analysis_code_generation': '生成分析代码', 'analysis_code_validation': '校验分析代码',
    'analysis_execution': '执行分析代码', 'result_contract_validation': '检查执行结果结构',
    'interpretation': '生成结论与图下分析',
    'figure_code_generation': '生成绘图代码', 'figure_code_validation': '校验绘图代码',
    'figure_execution': '执行绘图代码', 'figure_validation': '检查图表文件',
    'output_validation': '校验最终结果', 'output_publication': '保存产物', 'model_request': '等待模型响应',
    'model_transport_attempt': '等待模型通信',
}
PRIVATE_FIELDS = {'reasoning', 'reasoning_content', 'thinking', 'chain_of_thought',
                  'api_key', 'authorization', 'password', 'access_token', 'secret'}


def timestamp():
    return datetime.now(timezone.utc).isoformat(timespec='milliseconds')


def redact(text):
    text = re.sub(r'\b(?:user_|sk-)[A-Za-z0-9_-]{20,}', '<redacted>', text)
    text = re.sub(r'(?i)\bBearer\s+[^\s,;\"\x27}]+', 'Bearer <redacted>', text)
    text = re.sub(r'(?i)((?:api[_-]?key|authorization|password|secret|token)[\s\"\x27:=]+)[^\s,;}]+',
                  r'\1<redacted>', text)
    return re.sub(r'(?is)<think(?:ing)?>.*?(?:</think(?:ing)?>|$)', '<thinking omitted>', text)


def clean(value):
    if isinstance(value, dict):
        return {key: clean(item) for key, item in value.items() if str(key).lower() not in PRIVATE_FIELDS}
    if isinstance(value, (list, tuple)):
        return [clean(item) for item in value]
    return redact(value) if isinstance(value, str) else value


def exception_chain(exc):
    seen = set()
    while exc is not None and id(exc) not in seen and len(seen) < 8:
        seen.add(id(exc))
        yield exc
        exc = exc.__cause__ or exc.__context__


def safe_error(exc):
    if isinstance(exc, json.JSONDecodeError):
        return dict(type=type(exc).__name__, message=exc.msg, line=exc.lineno, column=exc.colno, position=exc.pos)
    # Validation messages can embed complete input_value reprs; keep the error, not those inputs.
    message = redact(re.sub(r'input_value=.*?(?=\n|$)', 'input_value=<omitted>', str(exc)))
    result = dict(type=type(exc).__name__, message=message[:1200])
    for key in ('code', 'field'):
        value = getattr(exc, key, None)
        if isinstance(value, str):
            result[key] = value[:160]
    causes = list(exception_chain(exc))[1:]
    if causes:
        result['causes'] = [dict(type=type(item).__name__, message=redact(str(item))[:512]) for item in causes]
    terminal = getattr(exc, 'terminal', None)
    if terminal is not None:
        result['model_call'] = {
            key: getattr(terminal, key) for key in
            ('model_call_id', 'attempt_count', 'kind', 'duration_ms', 'has_semantic_output')
            if isinstance(getattr(terminal, key, None), (str, int, bool))
        }
        failure = getattr(terminal, 'failure', None)
        if failure is not None:
            info = {}
            for key in ('origin', 'code', 'diagnostic_id', 'response_started', 'provider_http_status',
                        'provider_error_code', 'provider_error_type'):
                value = getattr(failure, key, None)
                value = getattr(value, 'value', value)
                if value is None or isinstance(value, (str, int, bool)):
                    info[key] = redact(value)[:160] if isinstance(value, str) else value
            result['model_call']['failure'] = info
    return result


def response_info(raw):
    """Describe malformed responses without storing prompts or reasoning text."""
    if isinstance(raw, dict):
        return dict(response_type='object', chars=len(json.dumps(raw, ensure_ascii=False, default=str)))
    text = str(raw or '')
    stripped = text.strip()
    kind = ('empty' if not stripped else 'json_fence' if stripped.startswith('```json') else
            'code_fence' if stripped.startswith('```') else 'json_object' if stripped.startswith('{') else
            'json_array' if stripped.startswith('[') else 'tagged_text' if stripped.startswith('<') else 'plain_text')
    result = dict(response_type=kind, chars=len(text), sha256=hashlib.sha256(text.encode()).hexdigest(),
                  contains_thinking_tag=bool(re.search(r'(?i)<think(?:ing)?>', text)))
    if re.search(r'(?i)"(?:reasoning|reasoning_content|thinking|chain_of_thought)"\s*:', text):
        result['preview_omitted'] = 'reasoning_fields'
    else:
        preview = redact(text).strip()
        result['preview'] = preview if len(preview) <= 512 else preview[:256] + '\n...\n' + preview[-256:]
        result['preview_truncated'] = len(preview) > 512
    return result


class Recorder:
    def __init__(self, workspace, task_id='', emit=None):
        self.started = time.monotonic()
        self.lock = threading.RLock()
        self.emit = emit
        self.sequence = 0
        self.total_bytes = 0
        self.warned = False
        self.clock = {}
        self.progress = 5
        from .live import PublicProgress
        self.public = PublicProgress(self)
        self.root = Path(workspace) / 'f4-trace' / uuid.uuid4().hex
        self.root.mkdir(parents=True, mode=0o700)
        self.summary = dict(schema='paper-f4-trace.v1', trace_id=self.root.name, task_id=task_id,
                            status='running', started_at=timestamp(), stages=[], artifacts=[])
        self._guard(lambda: self._write_json(self.root.parent / 'latest.json', {
            'trace_id': self.root.name, 'summary': self.root.name + '/summary.json',
        }))
        self.record('run_started')

    @staticmethod
    def _write_json(path, value):
        temporary = path.with_suffix('.tmp')
        temporary.write_text(json.dumps(value, ensure_ascii=False, allow_nan=False), encoding='utf-8')
        temporary.replace(path)

    def _guard(self, operation):
        try:
            return operation()
        except (OSError, TypeError, ValueError) as exc:
            self.summary['storage_error'] = type(exc).__name__
            if not self.warned:
                LOG.warning('F4 trace storage failed: %s', type(exc).__name__)
                self.warned = True

    def record(self, event, **details):
        with self.lock:
            self.sequence += 1
            self.summary.update(updated_at=timestamp(), elapsed_ms=round((time.monotonic() - self.started) * 1000))
            if self.sequence <= MAX_EVENTS:
                value = dict(seq=self.sequence, time=self.summary['updated_at'], event=event,
                             parent_id=_parent.get(), **details)

                def append():
                    with (self.root / 'events.jsonl').open('a', encoding='utf-8') as stream:
                        stream.write(json.dumps(value, ensure_ascii=False, allow_nan=False) + '\n')
                self._guard(append)
            else:
                self.summary['omitted_events'] = self.sequence - MAX_EVENTS
            self._guard(lambda: self._write_json(self.root / 'summary.json', self.summary))

    def begin(self, name, details):
        with self.lock:
            identifier = uuid.uuid4().hex
            item = dict(id=identifier, stage=name, parent_id=_parent.get(), status='running',
                        started_at=timestamp(), **details)
            self.summary['stages'].append(item)
            self.clock[identifier] = time.monotonic()
            self.record('stage_started', stage_id=identifier, stage=name, **details)
            parent = next((s for s in self.summary['stages'] if s['id'] == item['parent_id']), {})
            label = LABELS.get(name, name)
            if name == 'model_transport_attempt' and parent.get('stage') == 'model_request':
                parent = next((s for s in self.summary['stages'] if s['id'] == parent['parent_id']), parent)
            if name in ('model_request', 'model_transport_attempt') and parent:
                label = LABELS.get(parent['stage'], parent['stage']) + ' · 等待模型响应'
            attempt = details.get('attempt', parent.get('attempt'))
            figure = details.get('figure_id', parent.get('figure_id'))
            suffix = (f' · 第 {attempt} 次' if attempt else '') + (f' · {figure}' if figure else '')
            if self.emit:
                try:
                    from ..runtime_events import current_events
                    from .live import PUBLIC_STAGES
                    events = current_events()
                    # Keep F4's detailed snapshot and the shared execution log in sync.
                    if name in PUBLIC_STAGES and item['parent_id'] is None:
                        if events is not None:
                            events.begin_stage(identifier, label + suffix, min(95, self.progress))
                        else:
                            self.emit({'type': 'progress', 'progress': min(95, self.progress),
                                       'current_phase': f'F4 · {label}{suffix}'})
                except Exception:
                    LOG.warning('F4 trace progress notification failed')
            self.public.begin(item, LABELS.get(name, name))
            return item

    def end(self, item, exc=None):
        with self.lock:
            item.update(status='failed' if exc else 'passed', ended_at=timestamp(),
                        duration_ms=round((time.monotonic() - self.clock.pop(item['id'])) * 1000))
            if exc:
                item['error'] = safe_error(exc)
            else:
                self.progress = max(self.progress, MILESTONES.get(item['stage'], self.progress))
            self.record('stage_finished', stage_id=item['id'], stage=item['stage'], status=item['status'],
                        duration_ms=item['duration_ms'], **({'error': item['error']} if exc else {}))
            self.public.end(item, exc)
            from ..runtime_events import current_events
            events = current_events()
            if events is not None:
                events.end_stage(item['id'], exc)

    def save(self, name, value):
        with self.lock:
            if not re.fullmatch(r'[A-Za-z0-9_.-]+', name):
                raise ValueError('Invalid F4 trace artifact name')
            value = clean(value)
            raw = (value if isinstance(value, str) else json.dumps(value, ensure_ascii=False, allow_nan=False)).encode()
            info = dict(path=name, bytes=len(raw), sha256=hashlib.sha256(raw).hexdigest())
            if len(raw) > MAX_ARTIFACT_BYTES or self.total_bytes + len(raw) > MAX_TOTAL_BYTES:
                info['omitted'] = 'size_limit'
            else:
                written = self._guard(lambda: (self.root / name).write_bytes(raw))
                if written is None:
                    info['omitted'] = 'storage_error'
                else:
                    self.total_bytes += len(raw)
            self.summary['artifacts'] = [a for a in self.summary['artifacts'] if a['path'] != name] + [info]
            self.record('artifact_saved', **info)
            self.public.note(name, value)

    def finish(self, exc=None):
        with self.lock:
            self.summary.update(status='failed' if exc else 'completed', ended_at=timestamp())
            if exc:
                self.summary['error'] = safe_error(exc)
            self.record('run_finished', status=self.summary['status'])
            self.public.flush()
            LOG.info('F4 trace %s: task=%s status=%s path=%s',
                     'incomplete' if self.summary.get('storage_error') else 'saved',
                     self.summary['task_id'], self.summary['status'], self.root)


@contextmanager
def capture(workspace, task_id='', emit=None):
    try:
        recorder = Recorder(workspace, task_id, emit)
    except OSError as exc:
        LOG.warning('F4 trace unavailable: %s', type(exc).__name__)
        yield None
        return
    token = _current.set(recorder)
    parent = _parent.set(None)
    try:
        yield recorder
    except BaseException as exc:
        recorder.finish(exc)
        raise
    else:
        recorder.finish()
    finally:
        _parent.reset(parent)
        _current.reset(token)


@contextmanager
def stage(name, **details):
    recorder = _current.get()
    if recorder is None:
        yield
        return
    item = recorder.begin(name, details)
    token = _parent.set(item['id'])
    try:
        yield
    except BaseException as exc:
        recorder.end(item, exc)
        raise
    else:
        recorder.end(item)
    finally:
        _parent.reset(token)


def save(name, value):
    recorder = _current.get()
    if recorder:
        recorder._guard(lambda: recorder.save(name, value))


def enabled():
    return _current.get() is not None


def response_preview(json_output, *, output_format=None):
    recorder = _current.get()
    if recorder is None or not recorder.emit:
        return None
    from .live import ResponsePreview
    return ResponsePreview(recorder, _parent.get(), json_output, output_format=output_format)


def response(raw):
    recorder = _current.get()
    if recorder:
        recorder.record('model_response', **response_info(raw))


def figure_name(identifier):
    return hashlib.sha256(identifier.encode()).hexdigest()[:12]


def workflow_trace(function):
    @wraps(function)
    def wrapped():
        from lazymind.chat.engine.subagent.context import require_context
        context = require_context()
        if context.params.get('step_id') != 'data_analysis':
            return function()
        with capture(context.workspace_path, context.task_id, context.emit):
            return function()
    return wrapped
