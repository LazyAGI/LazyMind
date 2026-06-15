from __future__ import annotations

import time
import uuid
from typing import Any, Dict, List, Optional

import lazyllm

from lazymind.chat.engine.tools.infra import get_core_api, handle_tool_errors, tool_success
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
        title (str): A short human-readable task title, e.g. '生图'.
        objective (str): A clear description of what the SubAgent must accomplish.
        params (dict): Optional parameters for the task, e.g. {"count": 4}.
        input_artifact_keys (list): Artifact keys this SubAgent may read from prior tasks.
        output_artifact_keys (list): Artifact keys this SubAgent must produce (fixed declaration).
        tools (list): Optional explicit tool names; defaults to the agent_type tool set.
        resume (bool): When True, resume the interrupted task whose title matches `title`.

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
        while True:
            try:
                status_row = get_core_api(f'/internal/subagent/tasks/{task_id}') or {}
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
            msg = (
                f"任务'{title}'已完成。产出 key：{', '.join(output_artifact_keys) or '（无）'}。"
                f"如需完整内容可调用 get_subagent_artifacts('{title}')。"
            )
        else:
            phase = status_row.get('current_phase') or status_row.get('summary') or status_row.get('status')
            msg = f"任务'{title}'执行失败：{phase}"
        return tool_success('create_subagent', {'status': 'ok', 'message': msg})

    # manual: return immediately; Go runs the SubAgent in the background.
    msg = f"任务'{title}'已开始后台执行。可通过 get_subagent_status('{title}') 查询进度。"
    return tool_success('create_subagent', {'status': 'ok', 'message': msg})


def _list_conversation_tasks() -> List[Dict[str, Any]]:
    cfg = _agentic_config()
    conv_id = str(cfg.get('conversation_id') or cfg.get('session_id') or '').strip()
    if not conv_id:
        return []
    try:
        data = get_core_api(f'/conversations/{conv_id}/tasks') or {}
    except Exception:
        return []
    tasks = data.get('tasks')
    return tasks if isinstance(tasks, list) else []


def _resolve_task(task_ref: str, tasks: List[Dict[str, Any]]) -> Optional[Dict[str, Any]]:
    ref = str(task_ref or '').strip()
    if not ref:
        return None
    # "第N个" / "第N步"
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
        line = f"{t.get('seq_in_conversation')}. {t.get('title')}（{t.get('agent_type')}, {t.get('status')}"
        if str(t.get('status')) == 'running':
            line += f", {t.get('progress_pct', 0)}%"
        line += '）'
        lines.append(line)
    msg = '\n'.join(lines) if lines else '当前对话暂无 SubAgent 任务。'
    return tool_success('list_subagents', {'status': 'ok', 'message': msg, 'tasks': tasks})


@handle_tool_errors
def get_subagent_status(task_ref: str) -> Dict[str, Any]:
    """Get the status of a SubAgent task.

    Args:
        task_ref (str): A task reference: title, "第N个", or the agent type name.

    Returns:
        A status summary including progress and current phase.
    """
    tasks = _list_conversation_tasks()
    task = _resolve_task(task_ref, tasks)
    if not task:
        return tool_success('get_subagent_status', {'status': 'empty', 'message': f'未找到任务：{task_ref}'})
    msg = (
        f"{task.get('title')}（{task.get('status')}）：已完成 {task.get('progress_pct', 0)}%"
    )
    phase = task.get('current_phase')
    if phase:
        msg += f'，{phase}'
    eta = task.get('estimated_sec')
    if eta:
        msg += f'，预计还需 {eta} 秒。'
    return tool_success('get_subagent_status', {'status': 'ok', 'message': msg, 'task': task})


@handle_tool_errors
def list_subagent_artifacts(task_ref: str) -> Dict[str, Any]:
    """List the artifact keys produced by a SubAgent task.

    Args:
        task_ref (str): A task reference: title, "第N个", or the agent type name.

    Returns:
        A summary of artifact keys and their content types.
    """
    tasks = _list_conversation_tasks()
    task = _resolve_task(task_ref, tasks)
    if not task:
        return tool_success('list_subagent_artifacts', {'status': 'empty', 'message': f'未找到任务：{task_ref}'})
    arts = task.get('artifacts') or []
    summary: Dict[str, str] = {}
    for a in arts:
        summary[a.get('artifact_key')] = a.get('content_type')
    parts = [f'{k}（{v}）' for k, v in summary.items()]
    msg = f"{task.get('title')}任务共有 {len(summary)} 个成果：" + ('、'.join(parts) if parts else '（无）')
    return tool_success('list_subagent_artifacts', {'status': 'ok', 'message': msg, 'keys': summary})


@handle_tool_errors
def get_subagent_artifacts(task_ref: str, keys: Optional[List[str]] = None) -> Dict[str, Any]:
    """Get the artifacts produced by a SubAgent task.

    Args:
        task_ref (str): A task reference: title, "第N个", or the agent type name.
        keys (list): Optional list of artifact keys to fetch; omit to return all.

    Returns:
        A structured description of each artifact (file paths or text summaries).
    """
    tasks = _list_conversation_tasks()
    task = _resolve_task(task_ref, tasks)
    if not task:
        return tool_success('get_subagent_artifacts', {'status': 'empty', 'message': f'未找到任务：{task_ref}'})
    arts = task.get('artifacts') or []
    if keys:
        keyset = set(keys)
        arts = [a for a in arts if a.get('artifact_key') in keyset]
    return tool_success('get_subagent_artifacts', {'status': 'ok', 'artifacts': arts, 'task_title': task.get('title')})
