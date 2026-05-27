from __future__ import annotations
import json
import re
import uuid
from dataclasses import dataclass, field
from typing import Any, Callable, Iterator
from evo.orchestrator import capabilities as caps
from evo.runtime.config import EVO_TARGET_CHAT_URL
from evo.service.core import schemas
from evo.service.core.intent_store import Intent, IntentPreview, PlanResult

FLOWS = ('dataset_gen', 'eval', 'run', 'apply', 'abtest')
FLOW_BY_STEP = {'1': 'dataset_gen', '2': 'eval', '3': 'run', '4': 'apply', '5': 'abtest'}
RESUMABLE_STATUSES = {'paused', 'failed_transient'}
SCHEMAS = {
    'run.start': schemas.RunCreate, 'apply.start': schemas.ApplyCreate,
    'dataset_gen.start': schemas.DatasetGenCreate, 'eval.run': schemas.EvalCreate,
    'eval.fetch': schemas.EvalCreate, 'abtest.create': schemas.AbtestCreate,
    'checkpoint.continue': schemas.CheckpointContinue, 'checkpoint.rewind': schemas.CheckpointRewind,
    'checkpoint.answer': schemas.CheckpointAnswer, 'checkpoint.cancel': schemas.CheckpointCancel,
}


@dataclass
class PlanContext:
    thread_id: str
    recent_history: list[tuple[str, str]] = field(default_factory=list)
    thread_state_summary: str = ''
    capabilities_with_safety: list[dict] = field(default_factory=list)
    thread_state: dict[str, Any] = field(default_factory=dict)


@dataclass
class Draft:
    reply: str
    ops: list[dict[str, Any]]
    source: str
    prompt: str = ''
    raw: Any = None


class State:
    def __init__(self, ctx: PlanContext) -> None:
        self.ctx = ctx
        self.data = ctx.thread_state or {}
        self.inputs = self.data.get('inputs') or {}
        self.latest = self.data.get('latest_tasks') or {}
        self.active = self.data.get('active_tasks') or []
        self.artifacts = self.data.get('artifacts') or {}
        self.checkpoint = self.data.get('pending_checkpoint') or {}

    def latest_id(self, flow: str) -> str | None:
        return (self.latest.get(flow) or {}).get('id')

    def latest_payload(self, flow: str) -> dict:
        return dict((self.latest.get(flow) or {}).get('payload') or {})

    def artifact(self, key: str) -> str | None:
        vals = self.artifacts.get(key) or []
        return str(vals[-1]) if vals else None

    def success(self, flow: str) -> bool:
        return (self.latest.get(flow) or {}).get('status') in {'succeeded', 'accepted'}

    def active_flow(self, flow: str | None) -> bool:
        return any(t.get('status') == 'running' and (not flow or t.get('flow') == flow) for t in self.active)


class Planner:
    def __init__(self, *, llm: Callable[[str], Any],
                 stream_llm: Callable[[str, Callable[[], bool]], Iterator[str]] | None = None) -> None:
        self.llm = llm
        self.stream_llm = stream_llm

    def draft(self, message: str, ctx: PlanContext) -> Intent:
        return self._intent(message, ctx, self._draft(message, ctx))

    def draft_stream(
        self, message: str, ctx: PlanContext, cancel_requested: Callable[[], bool]
    ) -> Iterator[dict[str, Any]]:
        if self.stream_llm is None:
            yield {'type': 'final', 'intent': self.draft(message, ctx)}
            return
        state = State(ctx)
        prompt = _prompt(message, ctx, checkpoint=bool(state.checkpoint))
        parsed, raw, emitted = yield from _stream_plan(self.stream_llm, prompt, cancel_requested)
        intent = self._intent(message, ctx, _draft_from_parsed(parsed, _source(state), prompt, raw))
        suffix = intent.reply[len(emitted):] if emitted and intent.reply.startswith(emitted) else intent.reply
        if suffix:
            yield {'type': 'reply_delta', 'delta': suffix}
        yield {'type': 'final', 'intent': intent}

    def materialize(self, intent: Intent, ctx: PlanContext, user_edit: dict | None = None) -> PlanResult:
        raw_ops = user_edit.get('ops', []) if user_edit else [
            {'op': p.op, 'args': p.params_summary} for p in intent.suggested_ops_preview]
        ops, validation, warnings = [], [], []
        for item in raw_ops:
            op, args = (item or {}).get('op'), dict((item or {}).get('args') or {})
            try:
                caps.validate(op, args)
                _validate(op, args, ctx)
                ops.append({'op': op, 'args': args})
                validation.append({'op': op, 'args': args, 'status': 'accepted'})
            except Exception as exc:
                warnings.append(f'validation failed: {exc}')
                validation.append({'op': op, 'args': args, 'status': 'rejected', 'error': str(exc)})
        return PlanResult(intent_id=intent.intent_id, ops=ops, warnings=warnings,
                          trace={'raw_ops': raw_ops, 'validation': validation})

    def _draft(self, message: str, ctx: PlanContext) -> Draft:
        state = State(ctx)
        if shortcut := _checkpoint_shortcut(message, state):
            return shortcut
        if shortcut := _stage_shortcut(message, ctx, state):
            return shortcut
        prompt = _prompt(message, ctx, checkpoint=bool(state.checkpoint))
        try:
            return _draft_from_parsed(_parse_json(self.llm(prompt)), _source(state), prompt, None)
        except Exception as exc:
            msg = f'我还在等待当前断点确认，但无法安全执行这条消息：{exc}' if state.checkpoint else f'收到：{message}。暂无可自动执行的操作。'
            op = {'op': 'checkpoint.answer', 'reason': '回答断点问题', 'args': {'message': msg}} if state.checkpoint else None
            return Draft(msg, [op] if op else [], 'fallback', prompt, {'error': str(exc)})

    def _intent(self, message: str, ctx: PlanContext, draft: Draft) -> Intent:
        state = State(ctx)
        previews, warnings = [], []
        for item in _normalize(draft.ops, ctx, state, message):
            op, args = item.get('op', ''), item.get('args') or {}
            if op not in caps.REGISTRY:
                warnings.append(f'unknown op: {op}')
                continue
            cap = caps.get(op)
            previews.append(IntentPreview(op, item.get('reason') or f'{cap.flow}: {cap.description}', cap.safety, args))
        reply = draft.reply or f'收到：{message}。'
        if (
            previews
            and previews[0].op not in {'checkpoint.answer', 'checkpoint.cancel'}
            and not state.checkpoint.get('terminal')
        ):
            reply += ' 我会在后台执行，并把过程写入事件流。'
        return Intent(intent_id=f'intent_{ctx.thread_id}_{uuid.uuid4().hex[:8]}', thread_id=ctx.thread_id,
                      user_message=message, reply=reply, suggested_ops_preview=previews, requires_confirm=False,
                      trace={'source': draft.source, 'prompt': draft.prompt, 'raw_answer': draft.raw,
                             'parsed': {'reply': draft.reply, 'ops': draft.ops}, 'warnings': warnings})


def _source(state: State) -> str:
    return 'checkpoint_llm' if state.checkpoint else 'llm'


def _extra_instructions(flow: str | None, message: str) -> dict[str, str]:
    text = message.strip()
    return {'extra_instructions': text} if text and flow in {'run', 'apply'} else {}


def _latest_eval_id(state: State) -> str | None:
    return state.latest_payload('eval').get('eval_id') or state.artifact('eval_ids') or (
        state.latest_id('eval') if state.success('eval') else None)


def _normalize(ops: list[dict[str, Any]], ctx: PlanContext, state: State, message: str = '') -> list[dict[str, Any]]:
    out = []
    for item in ops or []:
        op, args = item.get('op'), dict(item.get('args') or {})
        op, args = _normalize_control_op(op, args, state)
        if op == 'checkpoint.rewind' and (extra := _extra_instructions(_flow_arg(args), message)):
            args.setdefault('input_patch', {}).setdefault('extra_instructions', extra['extra_instructions'])
        if state.checkpoint and (op in {'task.continue_latest', 'thread.retry'} or op == _checkpoint_next_op(state)):
            op, args = 'checkpoint.continue', {}
        elif op in {'task.continue_latest', 'thread.retry'}:
            args = _retry_args(args, state)
            if restart := _restart_failed_op(args.get('flow'), state):
                op, args = restart
        if op == 'eval.run':
            if not args.get('eval_id'):
                args.setdefault('dataset_id', state.artifact('dataset_ids'))
            if args.get('eval_id') and not args.get('dataset_id'):
                op = 'eval.fetch'
            if args.get('dataset_id'):
                args.pop('eval_id', None)
                args['resume'] = not _reset_request(message, 'eval')
            _fill_eval(args, state.inputs)
        elif op == 'run.start':
            args.setdefault('eval_id', _latest_eval_id(state))
            if _reset_request(message, 'run'):
                args.update(_extra_instructions('run', message))
        elif op == 'apply.start':
            report_id = state.latest_payload('run').get('report_id') or (state.latest.get('run') or {}).get(
                'report_id'
            )
            args.setdefault('report_id', report_id)
            if _reset_request(message, 'apply'):
                args.update(_extra_instructions('apply', message))
        elif op == 'abtest.create':
            _fill_abtest(args, state)
        elif op == 'dataset_gen.start':
            args.setdefault('kb_id', state.inputs.get('kb_id'))
            args.setdefault('algo_id', state.inputs.get('algo_id') or 'general_algo')
            args.setdefault('eval_name', state.inputs.get('eval_name') or f'{ctx.thread_id}_eval')
            args.setdefault('num_cases', state.inputs.get('num_cases'))
            if _reset_request(message, 'dataset_gen') and 'resume' not in args:
                args['resume'] = False
        if op == 'eval.run' and _reset_request(message, 'eval') and 'resume' not in args:
            args['resume'] = False
        clean_args = {k: v for k, v in args.items() if v is not None}
        out.append({'op': op, 'reason': item.get('reason', ''), 'args': clean_args})
    return out


def _checkpoint_shortcut(message: str, state: State) -> Draft | None:
    if not state.checkpoint:
        return None
    text = message.strip().lower()
    action, flow = _checkpoint_action(text, state)
    if action == 'rewind' and flow:
        patch: dict[str, Any] = {'resume': False, **_extra_instructions(flow, message)}
        return Draft(
            f'重新执行{_FLOW_LABELS[flow]}。',
            [{'op': 'checkpoint.rewind', 'args': {'to_stage': flow, 'input_patch': patch}}],
            'checkpoint_rule',
        )
    if action == 'continue':
        return Draft('继续执行当前断点的下一步。', [{'op': 'checkpoint.continue', 'args': {}}], 'checkpoint_rule')
    return None


def _checkpoint_next_op(state: State) -> str | None:
    next_op = state.checkpoint.get('next_op')
    return next_op.get('op') if isinstance(next_op, dict) else None


_FLOW_META = {
    'dataset_gen': ('评测集生成', ('评测集', '数据集', 'dataset', 'dataset_gen', '第1步', '第一步', 'step1', 'step 1')),
    'eval': ('评测', ('评测', 'eval', '第2步', '第二步', 'step2', 'step 2')),
    'run': ('分析', ('分析', 'run', '第3步', '第三步', 'step3', 'step 3')),
    'apply': ('代码修改', ('代码', '修改', '优化', 'apply', '第4步', '第四步', 'step4', 'step 4')),
    'abtest': ('ABTest', ('abtest', 'ab test', '第5步', '第五步', 'step5', 'step 5')),
}
_FLOW_LABELS = {flow: meta[0] for flow, meta in _FLOW_META.items()}
_RERUN_WORDS = ('重新', '重跑', '再跑', '从头', '重置', 'rerun', 'reset')
_RETRY_WORDS = ('重试', 'retry')
_STOP_WORDS = ('停止', '暂停', 'stop', 'pause')
_CONTINUE_WORDS = ('继续', '续跑', '恢复', '运行', '下一步', '确认', '执行', '开始', 'continue', 'resume', 'next')


def _stage_shortcut(message: str, ctx: PlanContext, state: State) -> Draft | None:
    text, flow = message.strip().lower(), _mentioned_flow(message.strip().lower())
    active_flow = _running_flow(state)
    if active_flow and flow and flow != active_flow:
        return Draft(
            f'当前正在执行{_FLOW_LABELS[active_flow]}，不能操作{_FLOW_LABELS[flow]}。请先暂停或等待当前任务完成。',
            [],
            'stage_rule',
        )
    if _has(text, _STOP_WORDS):
        args = {'flow': flow} if flow else {}
        label = _FLOW_LABELS[flow] if flow else (_FLOW_LABELS.get(active_flow) or '当前任务')
        return Draft(f'已暂停{label}。', [{'op': 'task.stop_active', 'args': args}], 'stage_rule')
    if not flow:
        return None
    if not _has(text, _CONTINUE_WORDS + _RERUN_WORDS + _RETRY_WORDS):
        return None
    if active_flow:
        return Draft(f'{_FLOW_LABELS[flow]}正在执行中，请等待完成或先暂停当前任务。', [], 'stage_rule')
    retry_restart = _has(text, _RETRY_WORDS) and (
        (state.latest.get(flow) or {}).get('status') not in RESUMABLE_STATUSES | {'stopping'}
    )
    if _reset_request(message, flow) or retry_restart:
        if blocker := _stage_blocker(flow, state):
            return Draft(blocker, [], 'stage_rule')
        return Draft(
            f'重新执行{_FLOW_LABELS[flow]}。',
            [_start_op(flow, ctx, state, resume=False, message=message)],
            'stage_rule',
        )
    if (state.latest.get(flow) or {}).get('status') in RESUMABLE_STATUSES | {'stopping'}:
        op = {'op': 'task.continue_latest', 'args': {'flow': flow}}
        return Draft(f'继续执行{_FLOW_LABELS[flow]}。', [op], 'stage_rule')
    if blocker := _stage_blocker(flow, state):
        return Draft(blocker, [], 'stage_rule')
    return Draft(f'继续执行{_FLOW_LABELS[flow]}。', [_start_op(flow, ctx, state, resume=True)], 'stage_rule')


def _start_op(
    flow: str, ctx: PlanContext, state: State, *, resume: bool, message: str | None = None
) -> dict[str, Any]:
    extra = _extra_instructions(flow, message or '')
    if flow == 'eval':
        return {'op': 'eval.run', 'args': {'dataset_id': state.artifact('dataset_ids'), 'resume': resume}}
    if flow == 'dataset_gen':
        return {'op': 'dataset_gen.start', 'args': {'resume': resume}}
    if flow == 'run':
        return {'op': 'run.start', 'args': extra}
    if flow == 'apply':
        return {'op': 'apply.start', 'args': extra}
    return {'op': 'abtest.create', 'args': {}}


def _checkpoint_action(text: str, state: State) -> tuple[str | None, str | None]:
    flow = _mentioned_flow(text)
    if _has(text, _RERUN_WORDS) or (flow and state.checkpoint.get('terminal') and _has(text, _RETRY_WORDS)):
        return ('rewind', flow)
    next_flow = _op_flow(_checkpoint_next_op(state) or '')
    if _has(text, _CONTINUE_WORDS) or (next_flow and flow == next_flow):
        return ('continue', None)
    return (None, None)


def _mentioned_flow(text: str) -> str | None:
    for flow, (_, words) in _FLOW_META.items():
        if _has(text, words):
            return flow
    return None


def _running_flow(state: State) -> str | None:
    for row in state.active:
        if row.get('status') == 'running' and row.get('flow') in FLOWS:
            return str(row.get('flow'))
    return None


def _stage_blocker(flow: str, state: State) -> str:
    if flow == 'dataset_gen':
        return ''
    if flow == 'eval':
        return '' if state.artifact('dataset_ids') or state.success('dataset_gen') else 'Step1 评测集生成尚未完成，不能执行评测。'
    if flow == 'run':
        return '' if state.success('eval') else 'Step2 评测尚未完成，不能执行分析。'
    if flow == 'apply':
        report_id = state.latest_payload('run').get('report_id') or (state.latest.get('run') or {}).get('report_id')
        return '' if state.success('run') and report_id else 'Step3 分析尚未完成，不能执行代码修改。'
    if flow == 'abtest':
        return '' if _apply_ready(state.latest.get('apply') or {}) else 'Step4 代码修改尚未成功，不能执行 ABTest。'
    return ''


def _has(text: str, words: tuple[str, ...]) -> bool:
    return any(word in text for word in words)


def _reset_request(message: str, flow: str) -> bool:
    text = message.strip().lower()
    reset_words = ('重新', '重跑', '从头', '重置', '重新生成', 'rerun', 'reset')
    resume_words = ('重试', '继续', '续跑', '恢复', 'retry', 'resume', 'continue')
    return _mentioned_flow(text) == flow and _has(text, reset_words) and not _has(text, resume_words)


def _normalize_control_op(op: str | None, args: dict, state: State) -> tuple[str | None, dict]:
    if not op:
        return op, args
    flow = _flow_arg(args) or _op_flow(op)
    action = op.split('.', 1)[1] if '.' in op else ''
    if action == 'continue' and flow in FLOWS:
        return 'task.continue_latest', {'flow': flow}
    if action == 'stop' and flow in FLOWS:
        return 'task.stop_active', {'flow': flow}
    if action == 'cancel' and flow in FLOWS:
        return 'task.cancel_active', {'flow': flow}
    if op in {'apply.accept', 'apply.reject'} and not args.get('task_id'):
        args['task_id'] = state.latest_id('apply')
    if op == 'checkpoint.rewind' and not args.get('to_stage'):
        args['to_stage'] = flow
    return op, args


def _retry_args(args: dict, state: State) -> dict:
    flow = _flow_arg(args) or _latest_retry_flow(state)
    return {'flow': flow} if flow else {}


def _flow_arg(args: dict) -> str | None:
    raw = args.get('flow') or args.get('stage') or args.get('to_stage')
    if raw in FLOWS:
        return str(raw)
    step = str(args.get('step') or args.get('stage_index') or '').strip()
    return FLOW_BY_STEP.get(step)


def _op_flow(op: str) -> str | None:
    flow = op.split('.', 1)[0]
    return flow if flow in FLOWS else None


def _latest_retry_flow(state: State) -> str | None:
    rows = [row for row in state.latest.values() if row.get('status') in RESUMABLE_STATUSES | {'failed_permanent'}]
    rows.sort(key=lambda row: float(row.get('updated_at') or 0), reverse=True)
    return rows[0].get('flow') if rows else None


def _restart_failed_op(flow: str | None, state: State) -> tuple[str, dict] | None:
    if not flow or (state.latest.get(flow) or {}).get('status') != 'failed_permanent':
        return None
    if flow == 'dataset_gen':
        args = {'kb_id': state.inputs.get('kb_id'), 'algo_id': state.inputs.get('algo_id') or 'general_algo',
                'eval_name': state.inputs.get('eval_name') or f'{state.ctx.thread_id}_eval'}
        if state.inputs.get('num_cases'):
            args['num_cases'] = state.inputs['num_cases']
        return ('dataset_gen.start', args)
    if flow == 'eval':
        args = {'dataset_id': state.artifact('dataset_ids'), 'target_chat_url': EVO_TARGET_CHAT_URL}
        _fill_eval(args, state.inputs)
        return ('eval.run', args)
    if flow == 'run':
        eval_id = _latest_eval_id(state)
        return ('run.start', {'eval_id': eval_id})
    if flow == 'apply':
        return ('apply.start', {'report_id': state.latest_payload('run').get('report_id')})
    if flow == 'abtest':
        args: dict = {}
        _fill_abtest(args, state)
        return ('abtest.create', args)
    return None


def _fill_eval(args: dict, inputs: dict) -> None:
    args['target_chat_url'] = EVO_TARGET_CHAT_URL
    options = dict(args.get('options') or args.get('eval_options') or {})
    if inputs.get('dataset_name'):
        options.setdefault('dataset_name', inputs['dataset_name'])
    if options:
        args['options'] = options
    args.pop('eval_options', None)


def _fill_abtest(args: dict, state: State) -> None:
    args.setdefault('apply_id', state.latest_id('apply'))
    args.setdefault(
        'baseline_eval_id',
        state.latest_payload('eval').get('eval_id') or state.artifact('eval_ids') or state.latest_id('eval'),
    )
    args.setdefault('dataset_id', state.artifact('dataset_ids'))
    args['target_chat_url'] = EVO_TARGET_CHAT_URL
    if state.inputs.get('dataset_name'):
        args.setdefault('eval_options', {}).setdefault('dataset_name', state.inputs['dataset_name'])


def _validate(op: str, args: dict, ctx: PlanContext) -> None:
    payload = {**args, 'thread_id': ctx.thread_id}
    if model := SCHEMAS.get(op):
        model(**payload)
    state = State(ctx)
    if state.active and op not in {'task.stop_active', 'task.cancel_active'}:
        raise ValueError(f'thread already has running task: {_active_summary(state)}')
    if state.checkpoint and not op.startswith('checkpoint.'):
        raise ValueError('pending checkpoint only accepts checkpoint.* ops')
    if op in {'task.continue_latest', 'thread.retry'} and not _has_resumable(state, args):
        raise ValueError('no paused or transient failed task to continue')
    if op.startswith('checkpoint.'):
        _validate_checkpoint(op, args, state.checkpoint)
    if (
        op == 'abtest.create'
        and (row := state.latest.get('apply') or {}).get('id') == args.get('apply_id')
        and not _apply_ready(row)
    ):
        raise ValueError('abtest.create requires a succeeded apply with final tests passed')


def _validate_checkpoint(op: str, args: dict, checkpoint: dict) -> None:
    if not checkpoint:
        raise ValueError('no pending checkpoint')
    if op == 'checkpoint.continue' and not checkpoint.get('next_op'):
        raise ValueError('checkpoint has no next_op to continue')
    if op == 'checkpoint.rewind' and args.get('to_stage') not in (checkpoint.get('allowed_stages') or FLOWS):
        raise ValueError(f"rewind stage {args.get('to_stage')!r} is not allowed")


def _prompt(message: str, ctx: PlanContext, *, checkpoint: bool) -> str:
    return (f'User message: {message}\n\nState:\n{ctx.thread_state_summary}\n\nCapabilities:\n{_caps(ctx)}\n\n'
            f'You are the Evo {"checkpoint" if checkpoint else "planner"} intent agent. Return strict JSON only: '
            '{"reply":"Chinese user-facing reply","ops":[{"op":"registered.op","reason":"short reason","args":{}}]}\n'
            'Use checkpoint.* only when pending_checkpoint exists. Prefer exact user intent; do not invent IDs. '
            'Never ask the user for task_id; use flow/stage instead. Stage map: 第1步=dataset_gen, 第2步=eval, '
            '第3步=run, 第4步=apply, 第5步=abtest. For retry/续跑/继续执行, use task.continue_latest with optional flow; '
            'restart a stage only when the user explicitly asks to rerun that named stage.')


def _draft_from_parsed(parsed: dict, source: str, prompt: str, raw: Any) -> Draft:
    parsed = _plan_dict(parsed)
    return Draft(
        str(parsed.get('reply') or ''),
        list(parsed.get('ops') or []),
        source,
        prompt,
        raw if raw is not None else parsed,
    )


def _stream_plan(stream_llm, prompt: str, cancel_requested) -> Iterator[tuple[dict, Any, str]]:
    parts, reply = [], _ReplyDeltaExtractor()
    for chunk in stream_llm(prompt, cancel_requested):
        if cancel_requested():
            raise RuntimeError('MESSAGE_CANCELLED')
        parts.append(str(chunk or ''))
        if delta := reply.feed(str(chunk or '')):
            yield {'type': 'reply_delta', 'delta': delta}
    raw = ''.join(parts)
    return _parse_json(raw), raw, reply.text


class _ReplyDeltaExtractor:
    def __init__(self) -> None:
        self.buf = ''
        self.text = ''
        self.on = False
        self.done = False
        self.esc = ''

    def feed(self, chunk: str) -> str:
        out = []
        for ch in chunk:
            if self.done:
                continue
            if not self.on:
                self.buf += ch
                if m := re.search(r'"reply"\s*:\s*"', self.buf):
                    self.on = True
                    out.append(self.feed(self.buf[m.end():]))
                continue
            if self.esc:
                self.esc += ch
                if self.esc.startswith('\\u') and len(self.esc) < 6:
                    continue
                val = _json_string(self.esc)
                out.append(val)
                self.text += val
                self.esc = ''
                continue
            if ch == '\\':
                self.esc = '\\'
            elif ch == '"':
                self.done = True
            else:
                out.append(ch)
                self.text += ch
        return ''.join(out)


def _apply_ready(row: dict) -> bool:
    result = (row.get('payload') or {}).get('result') or {}
    final_commit = row.get('final_commit') or result.get('final_commit')
    return row.get('status') in {'succeeded', 'accepted'} and result.get('status') == 'SUCCEEDED' and bool(final_commit)


def _has_resumable(state: State, args: dict) -> bool:
    flow, task_id = args.get('flow'), args.get('task_id')
    return any(
        row.get('status') in RESUMABLE_STATUSES | {'stopping'}
        and (not flow or row.get('flow') == flow)
        and (not task_id or row.get('id') == task_id)
        for row in state.latest.values()
    )


def _active_summary(state: State) -> str:
    return ', '.join(f"{row.get('flow')}:{row.get('id')}" for row in state.active[:3])


def _caps(ctx: PlanContext) -> str:
    return '\n'.join(f"- {c['op']} flow={c['flow']} safety={c['safety']}" for c in ctx.capabilities_with_safety)


def _parse_json(raw: Any) -> dict:
    if isinstance(raw, (dict, list)):
        return _plan_dict(raw)
    text = str(raw or '').strip()
    if m := re.search('```(?:json)?\\s*(.*?)```', text, re.S | re.I):
        text = m.group(1).strip()
    try:
        return _plan_dict(json.loads(text))
    except json.JSONDecodeError:
        s, e = text.find('{'), text.rfind('}')
        candidate = text[s:e + 1] if s >= 0 and e > s else text
        try:
            from json_repair import repair_json
            return _plan_dict(json.loads(repair_json(candidate)))
        except Exception:
            return _plan_dict(json.loads(candidate))


def _plan_dict(value: Any) -> dict:
    if isinstance(value, dict):
        return dict(value)
    if isinstance(value, list):
        if all(isinstance(item, dict) and item.get('op') for item in value):
            return {'reply': '', 'ops': value}
        for item in reversed(value):
            if isinstance(item, dict) and ('reply' in item or 'ops' in item):
                return dict(item)
    return {}


def _json_string(value: str) -> str:
    try:
        return json.loads(f'"{value}"')
    except Exception:
        return value[-1]
