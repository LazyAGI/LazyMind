from __future__ import annotations

import json
import time
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import LOG, AutoModel

from lazymind.config import config as _cfg
from lazymind.model_config import inject_model_config
from lazymind.chat.engine.agent_core import build_react_agent, drive_agent
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

from .context import SubAgentContext, set_context
from .db import SubAgentDB
from . import tools as subagent_tools


def _build_subagent_tools(extra_tools: Optional[List[Any]]) -> List[Any]:
    base = [
        subagent_tools.save_artifact,
        subagent_tools.get_artifact,
        subagent_tools.list_artifacts,
    ]
    if extra_tools:
        base.extend(extra_tools)
    return base


def _objective_prompt(ctx: SubAgentContext) -> str:
    lines = [
        'You are an autonomous SubAgent. Complete the objective below using the available tools.',
        '',
        f'Objective: {ctx.objective}',
    ]
    if ctx.params:
        lines.append(f'Parameters: {json.dumps(ctx.params, ensure_ascii=False)}')
    if ctx.input_artifact_keys:
        lines.append(f'Input artifact keys you may read: {", ".join(ctx.input_artifact_keys)}')
    lines.append(
        'You MUST produce the following output artifacts via save_artifact before finishing: '
        + ', '.join(ctx.output_artifact_keys)
    )
    lines.append('When all required artifacts are saved, output a short final summary.')
    return '\n'.join(lines)


def _persist_step(ctx: SubAgentContext, seq: int, event: Dict[str, Any]) -> None:
    tag = event.get('tag')
    if tag == 'tool_calls':
        tool_calls = []
        for tc in event.get('tool_calls', []) or []:
            if not isinstance(tc, dict):
                continue
            tool_calls.append({
                'id': tc.get('id', ''),
                'name': tc.get('name') or (tc.get('function') or {}).get('name', ''),
                'args': tc.get('args') or (tc.get('function') or {}).get('arguments', {}),
            })
        ctx.db.append_step(ctx.task_id, seq, 'assistant', {'text': '', 'tool_calls': tool_calls})
    elif tag == 'tool_results':
        results = []
        for tr in event.get('tool_results', []) or []:
            if not isinstance(tr, dict):
                continue
            results.append({
                'tool_call_id': tr.get('id', ''),
                'name': tr.get('name', ''),
                'result': tr.get('result', tr.get('content', '')),
            })
        ctx.db.append_step(ctx.task_id, seq, 'tool', {'tool_results': results})


async def run_subagent_stream(
    task_id: str,
    db_dsn: str,
    resume: bool = False,
    model_config: Optional[Dict[str, Any]] = None,
):
    """Async generator yielding Task SSE lines.

    Events: task_start / progress / text / think / artifact / done / error.
    text and think frames come from AgentEventFrameTranslator (same as ChatAgent),
    giving a unified LLM output representation across both agent types.
    """
    start_time = time.time()
    db: Optional[SubAgentDB] = None
    emitted: List[Dict[str, Any]] = []

    def _emit(ev: Dict[str, Any]) -> None:
        emitted.append(ev)

    def _sse(ev: Dict[str, Any]) -> str:
        return 'data: ' + json.dumps(ev, ensure_ascii=False, default=str) + '\n\n'

    try:
        db = SubAgentDB(db_dsn)
        task = db.load_task(task_id)
        if not task:
            yield _sse({'type': 'error', 'status': 'failed', 'message': f'task {task_id} not found'})
            yield 'data: [DONE]\n\n'
            return

        output_keys = _coerce_str_list(task.get('output_artifact_keys'))
        input_keys = _coerce_str_list(task.get('input_artifact_keys'))
        params = _coerce_dict(task.get('params'))

        ctx = SubAgentContext(
            task_id=task_id,
            conversation_id=str(task.get('conversation_id') or ''),
            agent_type=str(task.get('agent_type') or ''),
            objective=str(task.get('objective') or ''),
            params=params,
            workspace_path=str(task.get('workspace_path') or ''),
            input_artifact_keys=input_keys,
            output_artifact_keys=output_keys,
            db=db,
            emit=_emit,
        )
        ctx.ensure_workspace()

        sid = task_id
        lazyllm.globals._init_sid(sid=sid)
        lazyllm.locals._init_sid(sid=sid)
        inject_model_config(model_config)
        set_context(ctx)

        yield _sse({'type': 'task_start', 'task_id': task_id})

        llm = AutoModel(model='llm')
        agent = build_react_agent(
            llm=llm,
            tools=_build_subagent_tools(None),
            force_summarize_context=ctx.objective,
        )

        step_seq = db.max_step_seq(task_id) + 1 if resume else 0
        resume_history = _rebuild_history_from_steps(db, task_id) if resume else None
        progress = 5
        yield _sse({'type': 'progress', 'task_id': task_id, 'progress': progress,
                    'current_phase': '恢复执行...' if resume else '开始执行...'})

        # translator unifies text/think output with ChatAgent frame semantics.
        translator = AgentEventFrameTranslator(query=ctx.objective)
        final_result: Any = None

        async for kind, payload in drive_agent(agent, _objective_prompt(ctx), history=resume_history):
            if kind == 'event':
                item = payload
                tag = item.get('tag')
                # Persist tool steps for resume / breakpoint recovery.
                if tag in ('tool_calls', 'tool_results'):
                    _persist_step(ctx, step_seq, item)
                    step_seq += 1
                    # Drain artifact events emitted synchronously by tools.
                    while emitted:
                        ev = emitted.pop(0)
                        ev['task_id'] = task_id
                        yield _sse(ev)
                    if tag == 'tool_results' and progress < 90:
                        progress = min(90, progress + 15)
                        yield _sse({'type': 'progress', 'task_id': task_id, 'progress': progress,
                                    'current_phase': '执行中...'})
                # Translate all events (text/think/tool_calls/tool_results) via shared translator.
                for frame in translator.feed(item):
                    ev_type = 'think' if frame.get('think') else 'text'
                    yield _sse({'type': ev_type, 'task_id': task_id,
                                'think': frame.get('think'), 'text': frame.get('text')})
            else:  # 'final' -- drive_agent propagates future exceptions before yielding this.
                final_result = payload

        # Drain remaining artifact events.
        while emitted:
            ev = emitted.pop(0)
            ev['task_id'] = task_id
            yield _sse(ev)

        # Flush any buffered text/think from translator (e.g. citation scanning remainder).
        for frame in translator.finish(final_result):
            ev_type = 'think' if frame.get('think') else 'text'
            yield _sse({'type': ev_type, 'task_id': task_id,
                        'think': frame.get('think'), 'text': frame.get('text')})

        # Completeness check: every declared output key must have at least one artifact.
        saved = set(ctx.saved_keys())
        missing = [k for k in output_keys if k not in saved]
        if missing:
            yield _sse({'type': 'error', 'task_id': task_id, 'status': 'failed',
                        'message': f'缺少 artifact: {", ".join(missing)}'})
            yield 'data: [DONE]\n\n'
            return

        summary = _result_summary(final_result, output_keys)
        cost = round(time.time() - start_time, 3)
        yield _sse({'type': 'done', 'task_id': task_id, 'status': 'succeeded',
                    'summary': summary, 'cost': cost})
        yield 'data: [DONE]\n\n'
    except Exception as exc:  # noqa: BLE001
        LOG.exception('[SubAgent] run failed')
        yield _sse({'type': 'error', 'task_id': task_id, 'status': 'failed', 'message': str(exc)})
        yield 'data: [DONE]\n\n'
    finally:
        if db is not None:
            db.dispose()


def _coerce_str_list(value: Any) -> List[str]:
    if isinstance(value, list):
        return [str(v) for v in value if str(v).strip()]
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except ValueError:
            return []
        if isinstance(parsed, list):
            return [str(v) for v in parsed if str(v).strip()]
    return []


def _coerce_dict(value: Any) -> Dict[str, Any]:
    if isinstance(value, dict):
        return value
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except ValueError:
            return {}
        if isinstance(parsed, dict):
            return parsed
    return {}


def _result_summary(result: Any, output_keys: List[str]) -> str:
    if isinstance(result, str) and result.strip():
        return result.strip()[:500]
    if output_keys:
        return f'已完成，产出：{", ".join(output_keys)}'
    return '已完成'


def _rebuild_history_from_steps(db: SubAgentDB, task_id: str) -> List[Dict[str, Any]]:
    """Rebuild LLM chat history from persisted steps for resume.

    Validates tool_call_id pairing: every assistant tool_call must have a matching tool result.
    A tool step whose result has no preceding assistant tool_call id (orphan) is discarded, and
    replay stops at the last complete assistant boundary.
    """
    steps = db.load_steps(task_id)
    history: List[Dict[str, Any]] = []
    pending_ids: set = set()
    for step in steps:
        role = step.get('role')
        content = step.get('content') or {}
        if role == 'assistant':
            tool_calls = content.get('tool_calls') or []
            pending_ids = {tc.get('id') for tc in tool_calls if tc.get('id')}
            history.append({
                'role': 'assistant',
                'content': content.get('text', ''),
                'tool_calls': tool_calls,
            })
        elif role == 'tool':
            results = content.get('tool_results') or []
            valid = [r for r in results if r.get('tool_call_id') in pending_ids]
            if not valid:
                # Orphan tool results: drop and stop replay at the last complete boundary.
                if history and history[-1].get('role') == 'assistant':
                    history.pop()
                break
            for r in valid:
                history.append({
                    'role': 'tool',
                    'tool_call_id': r.get('tool_call_id'),
                    'name': r.get('name', ''),
                    'content': str(r.get('result', '')),
                })
            pending_ids = set()
    return history
