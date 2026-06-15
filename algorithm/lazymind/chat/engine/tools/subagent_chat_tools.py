from __future__ import annotations

import time
import uuid
from typing import Any, Dict, List, Optional

import lazyllm

from lazymind.chat.engine.subagent.db import TaskQueryDB
from lazymind.chat.engine.tools.infra import handle_tool_errors, tool_success
from lazyllm.tools.agent.base import _write_agent_data

# How often to emit a heartbeat while polling in auto mode (seconds).
_HEARTBEAT_INTERVAL = 15
# Poll interval for auto-mode status checks (seconds).
_POLL_INTERVAL = 2
_TERMINAL = {'succeeded', 'failed', 'interrupted', 'canceled'}


def _agentic_config() -> Dict[str, Any]:
    try:
        return lazyllm.globals['agentic_config'] or {}
    except Exception:
        return {}


def _mode() -> str:
    mode = str(_agentic_config().get('mode') or 'auto')
    return mode if mode in ('auto', 'manual') else 'auto'


@handle_tool_errors
def create_subagent(
    agent_type: str,
    title: str,
    objective: str,
    params: Optional[Dict[str, Any]] = None,
    input_artifact_keys: Optional[List[str]] = None,
    output_artifact_keys: Optional[List[str]] = None,
    tools: Optional[List[str]] = None,
    resume: bool = False,
) -> Dict[str, Any]:
    """Spawn an autonomous SubAgent to handle a complex, long-running, or tool-heavy subtask.

    Use this when a step is complex enough to warrant its own tool-calling chain, takes a long
    time, or streams outputs incrementally (e.g. generating multiple images). For simple steps,
    just use ordinary tools or reason directly instead. To resume an interrupted task, set
    resume=True and pass the interrupted task's title so it continues from its last step.

    Args:
        agent_type (str): The kind of SubAgent, e.g. 'image_generation', 'research'.
        title (str): A short human-readable task title, e.g. 'generate image'.
        objective (str): A clear description of what the SubAgent must accomplish.
        params (dict): Optional parameters for the task, e.g. {"count": 4}.
        input_artifact_keys (list): Artifact keys this SubAgent may read from prior tasks.
        output_artifact_keys (list): Artifact keys this SubAgent must produce (fixed declaration).
        tools (list): Optional explicit tool names; defaults to the agent_type tool set.
        resume (bool): Set to True when the user explicitly asks to continue or retry a
            FAILED or interrupted task. Pass the failed task's title so the agent can locate
            and resume it from its last saved step. Do NOT create a new task if the user says
            "continue" or "retry" — always pass resume=True with the original title instead.

    Returns:
        In auto mode, a summary after the SubAgent finishes. In manual mode, an immediate
        acknowledgement that the task is running in the background.
    """
    mode = _mode()
    params = params or {}
    input_artifact_keys = input_artifact_keys or []
    output_artifact_keys = output_artifact_keys or []

    task_id = str(uuid.uuid4())
    if resume:
        existing = _resolve_task(title, _list_conversation_tasks())
        if existing and existing.get('task_id'):
            task_id = str(existing['task_id'])

    _write_agent_data(
        'task_created',
        task_id=task_id,
        title=title,
        agent_type=agent_type,
        mode=mode,
        objective=objective,
        params=params,
        input_artifact_keys=input_artifact_keys,
        output_artifact_keys=output_artifact_keys,
        tools=tools or [],
        resume=bool(resume),
    )

    if mode == 'auto':
        last_heartbeat = time.time()
        status_row: Dict[str, Any] = {}
        db = TaskQueryDB()
        while True:
            try:
                status_row = db.get_task_status(task_id) or {}
            except Exception:
                status_row = {}
            status = str(status_row.get('status') or '')
            if status in _TERMINAL:
                break
            now = time.time()
            if now - last_heartbeat >= _HEARTBEAT_INTERVAL:
                _write_agent_data('heartbeat')
                last_heartbeat = now
            time.sleep(_POLL_INTERVAL)

        if status_row.get('status') == 'succeeded':
            summary = str(status_row.get('summary') or '').strip()
            artifacts = _fetch_task_artifacts(task_id)
            result: Dict[str, Any] = {'status': 'ok', 'artifacts': artifacts}
            if summary:
                result['summary'] = summary
            if artifacts:
                arts_lines = [_describe_artifact(a) for a in artifacts]
                arts_text = '\n'.join(arts_lines)
                msg = (
                    f"Task '{title}' completed."
                    + (f' Summary: {summary}\n' if summary else '')
                    + f'Artifacts:\n{arts_text}'
                )
            else:
                msg = (
                    f"Task '{title}' completed."
                    + (f' Summary: {summary}' if summary
                       else f" Output keys: {', '.join(output_artifact_keys) or '(none)'}.")
                )
            result['message'] = msg
        else:
            phase = status_row.get('current_phase') or status_row.get('status')
            summary = str(status_row.get('summary') or '').strip()
            resume_hint = (
                f"To resume, call create_subagent(title='{title}', resume=True, ...) to continue from the last step."
            )
            if summary:
                msg = f"Task '{title}' did not fully succeed:\n{summary}\n{resume_hint}"
            else:
                msg = f"Task '{title}' failed: {phase or status_row.get('status')}. {resume_hint}"
            result = {'status': 'failed', 'message': msg, 'summary': summary}
        return tool_success('create_subagent', result)

    # manual: return immediately; Go runs the SubAgent in the background.
    msg = f"Task '{title}' started in the background. Use get_subagent_status('{title}') to check progress."
    return tool_success('create_subagent', {'status': 'ok', 'message': msg})


def _describe_artifact(a: Dict[str, Any]) -> str:
    """Return a one-line human-readable description of an artifact for the ChatAgent."""
    key = a.get('artifact_key') or '?'
    ct = a.get('content_type') or 'text'
    value = a.get('value') or {}
    if isinstance(value, str):
        try:
            import json as _json
            value = _json.loads(value)
        except Exception:
            value = {'text': value}

    source = value.get('_source_tool') or ''
    source_prefix = f'via {source} ' if source else ''

    if ct == 'text':
        text = str(value.get('text') or '')
        length = len(text)
        return f'  {source_prefix}text ({length} chars) stored at key={key}'
    if ct == 'json':
        data = value.get('data')
        if isinstance(data, dict):
            detail = f'JSON object, top-level keys: {", ".join(list(data.keys())[:8])}'
        elif isinstance(data, list):
            detail = f'JSON array, {len(data)} items'
        else:
            detail = 'JSON data'
        return f'  {source_prefix}{detail} stored at key={key}'
    if ct == 'image':
        path = str(value.get('path') or '')
        name = path.split('/')[-1] if path else 'unknown'
        return f'  {source_prefix}image ({name}) stored at key={key}'
    if ct == 'file':
        filename = value.get('filename') or ''
        size = value.get('size') or 0
        size_str = f'{size // 1024} KB' if size >= 1024 else f'{size} B'
        return f'  {source_prefix}file {filename} ({size_str}) stored at key={key}'
    if ct == 'file_list':
        paths = value.get('paths') or []
        return f'  {source_prefix}{len(paths)} files stored at key={key}'
    return f'  [{key}] ({ct}) stored at key={key}'


def _fetch_task_artifacts(task_id: str) -> List[Dict[str, Any]]:
    """Fetch artifacts for a task by scanning the conversation task list."""
    tasks = _list_conversation_tasks()
    for t in tasks:
        if str(t.get('task_id') or t.get('id') or '') == task_id:
            arts = t.get('artifacts') or []
            return list(arts) if isinstance(arts, list) else []
    return []


def _list_conversation_tasks() -> List[Dict[str, Any]]:
    cfg = _agentic_config()
    conv_id = str(cfg.get('conversation_id') or cfg.get('session_id') or '').strip()
    if not conv_id:
        return []
    try:
        return TaskQueryDB().list_tasks_by_conversation(conv_id)
    except Exception:
        return []


def _resolve_task(task_ref: str, tasks: List[Dict[str, Any]]) -> Optional[Dict[str, Any]]:
    ref = str(task_ref or '').strip()
    if not ref:
        return None
    # Chinese ordinal reference, e.g. "第N个" / "第N步" (task index by position)
    import re
    m = re.search(r'第\s*(\d+)\s*[个步]', ref)
    if m:
        idx = int(m.group(1))
        for t in tasks:
            if t.get('seq_in_conversation') == idx:
                return t
    # exact title
    for t in tasks:
        if str(t.get('title') or '') == ref:
            return t
    # agent_type
    for t in tasks:
        if str(t.get('agent_type') or '') == ref:
            return t
    # substring title match
    for t in tasks:
        if ref in str(t.get('title') or ''):
            return t
    return None


@handle_tool_errors
def list_subagents(status: Optional[str] = None) -> Dict[str, Any]:
    """List SubAgent tasks in the current conversation, optionally filtered by status.

    Args:
        status (str): Optional filter: pending / running / succeeded / failed / interrupted.

    Returns:
        A natural-language list of tasks with their status and progress.
    """
    tasks = _list_conversation_tasks()
    if status:
        tasks = [t for t in tasks if str(t.get('status') or '') == status]
    lines = []
    for t in tasks:
        line = f"{t.get('seq_in_conversation')}. {t.get('title')} ({t.get('agent_type')}, {t.get('status')}"
        if str(t.get('status')) == 'running':
            line += f", {t.get('progress_pct', 0)}%"
        line += ')'
        lines.append(line)
    msg = '\n'.join(lines) if lines else 'No SubAgent tasks in the current conversation.'
    return tool_success('list_subagents', {'status': 'ok', 'message': msg, 'tasks': tasks})


@handle_tool_errors
def get_subagent_status(task_ref: str) -> Dict[str, Any]:
    """Get the status of a SubAgent task.

    Args:
        task_ref (str): A task reference: title, "task N" (e.g. "第N个"), or the agent type name.
    """
    tasks = _list_conversation_tasks()
    task = _resolve_task(task_ref, tasks)
    if not task:
        return tool_success('get_subagent_status', {'status': 'empty', 'message': f'Task not found: {task_ref}'})
    msg = (
        f"{task.get('title')} ({task.get('status')}): {task.get('progress_pct', 0)}% complete"
    )
    phase = task.get('current_phase')
    if phase:
        msg += f', {phase}'
    eta = task.get('estimated_sec')
    if eta:
        msg += f', estimated {eta}s remaining.'
    return tool_success('get_subagent_status', {'status': 'ok', 'message': msg, 'task': task})


@handle_tool_errors
def list_subagent_artifacts(task_ref: str) -> Dict[str, Any]:
    """List the artifact keys produced by a SubAgent task.

    Args:
        task_ref (str): A task reference: title, "task N" (e.g. "第N个"), or the agent type name.

    Returns:
        A summary of artifact keys and their content types.
    """
    tasks = _list_conversation_tasks()
    task = _resolve_task(task_ref, tasks)
    if not task:
        return tool_success('list_subagent_artifacts', {'status': 'empty', 'message': f'Task not found: {task_ref}'})
    arts = task.get('artifacts') or []
    summary: Dict[str, str] = {}
    for a in arts:
        summary[a.get('artifact_key')] = a.get('content_type')
    parts = [f'{k} ({v})' for k, v in summary.items()]
    msg = f"Task '{task.get('title')}' has {len(summary)} artifact(s): " + (', '.join(parts) if parts else '(none)')
    return tool_success('list_subagent_artifacts', {'status': 'ok', 'message': msg, 'keys': summary})


@handle_tool_errors
def get_subagent_artifacts(task_ref: str, keys: Optional[List[str]] = None) -> Dict[str, Any]:
    """Get the artifacts produced by a SubAgent task.

    Args:
        task_ref (str): A task reference: title, "task N" (e.g. "第N个"), or the agent type name.
        keys (list): Optional list of artifact keys to fetch; omit to return all.

    Returns:
        A structured description of each artifact (file paths or text summaries).
    """
    tasks = _list_conversation_tasks()
    task = _resolve_task(task_ref, tasks)
    if not task:
        return tool_success('get_subagent_artifacts', {'status': 'empty', 'message': f'Task not found: {task_ref}'})
    arts = task.get('artifacts') or []
    if keys:
        keyset = set(keys)
        arts = [a for a in arts if a.get('artifact_key') in keyset]
    return tool_success('get_subagent_artifacts', {'status': 'ok', 'artifacts': arts, 'task_title': task.get('title')})
