from __future__ import annotations

import json
import time
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import LOG, AutoModel

from lazymind.model_config import inject_model_config
from lazymind.chat.engine.agent_core import build_react_agent, drive_agent
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS, build_agent_tools

from .context import SubAgentContext, set_context
from .db import SubAgentDB
from . import tools as subagent_tools


def _resolve_runtime_tools(explicit: Optional[List[str]]) -> List[Any]:
    """Build the runtime tool list for a SubAgent.

    If explicit tool names are provided, use only those (looked up from DEFAULT_TOOLS).
    Otherwise fall back to all DEFAULT_TOOLS, giving the SubAgent the same tool set
    as ChatAgent minus the subagent-management tools.
    """
    if explicit:
        name_set = {str(n).strip() for n in explicit if str(n).strip()}
        configs = [cfg for cfg in DEFAULT_TOOLS if cfg.name in name_set]
    else:
        configs = list(DEFAULT_TOOLS)
    return build_agent_tools(configs)


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
        'You are NOT allowed to spawn or create sub-agents or delegate tasks to other agents. '
        'Only use the tools explicitly listed in your tool set.',
        '',
        f'Objective: {ctx.objective}',
    ]
    if ctx.params:
        lines.append(f'Parameters: {json.dumps(ctx.params, ensure_ascii=False)}')
    if ctx.input_artifact_keys:
        lines.append(f'Input artifact keys you may read: {", ".join(ctx.input_artifact_keys)}')
    lines.append(
        'You MUST call save_artifact for EACH of the following keys before you finish — '
        'do NOT skip this step even if you have already written the results in plain text: '
        + ', '.join(ctx.output_artifact_keys)
    )
    lines.append(
        'IMPORTANT: Writing results in your reply text does NOT count as saving an artifact. '
        'You must explicitly call save_artifact(key=..., value=...) for every required key. '
        'The task is considered INCOMPLETE and will be marked as FAILED if any required artifact '
        'key is missing. Do not write a final summary until all save_artifact calls are done.'
    )
    lines.append(
        'After all required artifacts are saved, write a final summary that contains the '
        'actual results and key findings — not only a reference to the artifacts. '
        'For example, if you searched for information, include the information itself. '
        'The summary must be self-contained and directly usable by the caller without '
        'opening any artifact.'
    )
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
    agent_type: Optional[str] = None,
    tools: Optional[List[str]] = None,
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
        runtime_tools = _resolve_runtime_tools(tools)
        agent = build_react_agent(
            llm=llm,
            tools=_build_subagent_tools(runtime_tools),
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
        # Accumulate streaming text/think chunks; flush to DB when a tool step follows or at end.
        _pending_text: str = ''
        _pending_think: str = ''

        async for kind, payload in drive_agent(agent, _objective_prompt(ctx), history=resume_history):
            if kind == 'event':
                item = payload
                tag = item.get('tag')
                # Persist tool steps for resume / breakpoint recovery.
                if tag in ('tool_calls', 'tool_results'):
                    # Flush accumulated text/think as a single step before tool call.
                    if _pending_think:
                        ctx.db.append_step(task_id, step_seq, 'think', {'content': _pending_think})
                        step_seq += 1
                        _pending_think = ''
                    if _pending_text:
                        ctx.db.append_step(task_id, step_seq, 'text', {'content': _pending_text})
                        step_seq += 1
                        _pending_text = ''
                    _persist_step(ctx, step_seq, item)
                    step_seq += 1
                    # Forward tool steps as SSE events so the frontend can render them.
                    if tag == 'tool_calls':
                        calls = [
                            {
                                'id': tc.get('id', ''),
                                'name': tc.get('name') or (tc.get('function') or {}).get('name', ''),
                                'args': tc.get('args') or (tc.get('function') or {}).get('arguments', {}),
                            }
                            for tc in (item.get('tool_calls') or [])
                            if isinstance(tc, dict)
                        ]
                        if calls:
                            yield _sse({'type': 'tool_calls', 'task_id': task_id, 'tool_calls': calls})
                    elif tag == 'tool_results':
                        results = [
                            {
                                'id': tr.get('id', ''),
                                'name': tr.get('name', ''),
                                'result': str(tr.get('result', tr.get('content', '')))[:2000],
                            }
                            for tr in (item.get('tool_results') or [])
                            if isinstance(tr, dict)
                        ]
                        if results:
                            yield _sse({'type': 'tool_results', 'task_id': task_id, 'tool_results': results})
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
                    if ev_type == 'think':
                        _pending_think += frame.get('think') or ''
                    else:
                        _pending_text += frame.get('text') or ''
            else:  # 'final' -- drive_agent propagates future exceptions before yielding this.
                final_result = payload
                # Flush any remaining accumulated text/think as the final step.
                if _pending_think:
                    ctx.db.append_step(task_id, step_seq, 'think', {'content': _pending_think})
                    step_seq += 1
                    _pending_think = ''
                if _pending_text:
                    ctx.db.append_step(task_id, step_seq, 'text', {'content': _pending_text})
                    step_seq += 1
                    _pending_text = ''

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
            steps = db.load_steps(task_id)
            is_ok, eval_summary = _evaluate_completion(
                llm=llm,
                objective=ctx.objective,
                steps=steps,
                saved_keys=list(saved),
                missing_keys=missing,
                force_result=final_result,
                ctx=ctx,
            )
            cost = round(time.time() - start_time, 3)
            if is_ok:
                yield _sse({'type': 'done', 'task_id': task_id, 'status': 'succeeded',
                            'summary': eval_summary, 'cost': cost})
            else:
                yield _sse({'type': 'error', 'task_id': task_id, 'status': 'failed',
                            'summary': eval_summary,
                            'message': f'缺少 artifact: {", ".join(missing)}。{eval_summary}'})
            yield 'data: [DONE]\n\n'
            return

        summary = _result_summary(final_result, output_keys)
        cost = round(time.time() - start_time, 3)
        yield _sse({'type': 'done', 'task_id': task_id, 'status': 'succeeded',
                    'summary': summary, 'cost': cost})
        yield 'data: [DONE]\n\n'
    except Exception as exc:  # noqa: BLE001
        LOG.exception('[SubAgent] run failed')
        exc_summary = str(exc)
        if db is not None:
            try:
                steps = db.load_steps(task_id)
                trace = _steps_to_trace(steps)
                exc_summary = f'异常：{exc}\n执行路径：\n{trace}'
            except Exception:
                pass
        yield _sse({'type': 'error', 'task_id': task_id, 'status': 'failed',
                    'summary': exc_summary, 'message': exc_summary})
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
        return result.strip()
    if output_keys:
        return f'已完成，产出：{", ".join(output_keys)}'
    return '已完成'


def _steps_to_trace(steps: List[Dict[str, Any]]) -> str:
    """Convert persisted steps into a compact execution trace string for LLM review."""
    lines: List[str] = []
    for s in steps:
        role = s.get('role', '')
        content = s.get('content') or {}
        if role == 'assistant':
            calls = content.get('tool_calls') or []
            names = ', '.join(tc.get('name', '?') for tc in calls) if calls else '（无工具调用）'
            lines.append(f'[assistant] called: {names}')
        elif role == 'tool':
            results = content.get('tool_results') or []
            for r in results:
                name = r.get('name', '?')
                res = str(r.get('result', ''))[:300]
                lines.append(f'[tool:{name}] {res}')
    return '\n'.join(lines) if lines else '（无步骤记录）'


def _evaluate_completion(
    llm: Any,
    objective: str,
    steps: List[Dict[str, Any]],
    saved_keys: List[str],
    missing_keys: List[str],
    force_result: Any,
    ctx: Optional[Any] = None,
) -> tuple:
    """Ask the LLM to judge whether the SubAgent substantively completed the objective.

    Returns (is_succeeded: bool, summary: str).
    The summary must contain actual findings/results, not references to artifacts.

    If the LLM judges YES and ctx is provided, the final output is auto-saved as a
    text artifact for each missing key so the task is not penalised for a missing
    save_artifact call when the content is clearly present in the final output.
    """
    trace = _steps_to_trace(steps)
    force_text = str(force_result or '').strip()
    saved_str = ', '.join(saved_keys) if saved_keys else '（无）'
    missing_str = ', '.join(missing_keys) if missing_keys else '（无）'

    prompt_lines = [
        'You are reviewing the execution of an autonomous SubAgent that stopped without '
        'calling save_artifact for all required output keys.',
        '',
        f'Original objective: {objective}',
        f'Required artifact keys: {missing_str or saved_str}',
        f'Actually saved artifact keys: {saved_str}',
        f'Missing artifact keys: {missing_str}',
        '',
        'Execution trace (tool calls and results):',
        trace,
    ]
    if force_text:
        prompt_lines += ['', f'Agent final output: {force_text[:2000]}']
    prompt_lines += [
        '',
        'Evaluation rules:',
        '- Answer YES if the agent gathered and delivered the information needed to satisfy '
        'the objective, even if it forgot to call save_artifact. The final output text counts '
        'as evidence of completion.',
        '- Answer NO only if the agent clearly failed to obtain the required information '
        '(e.g. all tool calls errored out, or the output is empty / irrelevant).',
        '',
        'Based on the above, answer TWO things:',
        '1. Did the SubAgent substantively achieve the objective? Reply YES or NO on the first line.',
        '2. Write a self-contained summary of what was actually accomplished (include key findings, '
        'data, or results inline — not references to artifacts). '
        'If nothing useful was accomplished, briefly explain what went wrong.',
    ]
    eval_prompt = '\n'.join(prompt_lines)

    try:
        summarize_llm = llm.share(stream=False)
        resp = summarize_llm(eval_prompt)
        text = resp if isinstance(resp, str) else (
            resp.get('content', '') if isinstance(resp, dict) else ''
        )
        text = (text or '').strip()
        first_line = text.split('\n')[0].strip().upper()
        is_succeeded = first_line.startswith('YES')
        rest = text[len(text.split('\n')[0]):].strip() if '\n' in text else text
        summary = rest if rest else text

        # Auto-save final output as text artifacts for each missing key when the
        # LLM judges the task as succeeded. This recovers from models that forget
        # to call save_artifact but include the results in their final reply.
        if is_succeeded and ctx is not None and force_text and missing_keys:
            content = summary if summary else force_text
            for key in missing_keys:
                try:
                    seq = ctx.next_artifact_seq(key)
                    ctx.record_local_artifact(key, 'text', {'text': content}, seq)
                    ctx.db.save_artifact(ctx.task_id, key, 'text', {'text': content}, seq)
                    ctx.emit({'type': 'artifact', 'artifact_key': key,
                              'content_type': 'text', 'seq': seq, 'value': {'text': content}})
                    LOG.info(f'[SubAgent] auto-saved missing artifact key={key!r} for task={ctx.task_id}')
                except Exception as save_err:
                    LOG.warning(f'[SubAgent] auto-save artifact key={key!r} failed: {save_err}')

        return is_succeeded, summary
    except Exception as e:
        LOG.warning(f'[SubAgent] _evaluate_completion LLM call failed: {e}')
        return False, f'执行中断，已完成步骤数：{len(steps)}，缺少产出：{missing_str}'


def _rebuild_history_from_steps(db: SubAgentDB, task_id: str) -> List[Dict[str, Any]]:
    """Rebuild LLM chat history from persisted steps for resume.

    Validates tool_call_id pairing: every assistant tool_call must have a matching tool result.
    A tool step whose result has no preceding assistant tool_call id (orphan) is discarded, and
    replay stops at the last complete assistant boundary.

    Also validates that every tool_call's function.arguments is valid JSON.  If any arguments
    field is malformed (e.g. persisted from a truncated stream), the offending assistant message
    and everything after it are dropped so the model never receives corrupt history.
    """
    steps = db.load_steps(task_id)
    history: List[Dict[str, Any]] = []
    pending_ids: set = set()
    for step in steps:
        role = step.get('role')
        content = step.get('content') or {}
        if role == 'assistant':
            tool_calls = content.get('tool_calls') or []
            # Validate function.arguments JSON before appending.
            for tc in tool_calls:
                args = (tc.get('function') or {}).get('arguments') or tc.get('args')
                if args and isinstance(args, str):
                    try:
                        json.loads(args)
                    except (ValueError, TypeError):
                        # Corrupt arguments: stop replay at the last clean boundary.
                        LOG.warning(
                            f'[SubAgent] resume: dropping corrupt tool_call '
                            f'(task={task_id}, name={(tc.get("function") or {}).get("name")})'
                        )
                        return history
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
