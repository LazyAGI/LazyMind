"""Read-only F4 progress. Core verifies the caller's task ownership before any file access."""
import asyncio
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import time
from uuid import UUID

import httpx
from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import StreamingResponse

from lazymind.common.token_estimation import estimate_tokens

from .live import MAX_REASONING, public_error

router = APIRouter()
WORKSPACES = Path(os.environ.get('LAZYMIND_SUBAGENT_WORKSPACE_ROOT', '/data/subagent'))
GATEWAY = os.environ.get('LAZYMIND_F4_GATEWAY_URL', 'http://kong:8000').rstrip('/')


async def authorized_task(task_id, authorization):
    if not authorization.lower().startswith('bearer '):
        raise HTTPException(401, 'Authentication required')
    try:
        async with httpx.AsyncClient(timeout=10, trust_env=False, follow_redirects=False) as client:
            # The gateway validates the bearer and injects the identity used by Core.
            # Never forward a browser-supplied X-User-Id or substitute service credentials.
            response = await client.get(GATEWAY + '/api/core/tasks/' + task_id,
                                        headers={'Authorization': authorization})
        if response.status_code == 401:
            raise HTTPException(401, 'Authentication required')
        if response.status_code in (403, 404):
            raise HTTPException(404, 'Task not found')
        if response.status_code != 200:
            raise HTTPException(503, 'Progress is temporarily unavailable')
        body = response.json()
        value = body.get('data', body)
        task = value.get('task', {})
        if (task.get('task_id') != task_id or task.get('agent_type') != 'workflow_step'
                or task.get('title') != 'paper-writing:data_analysis'):
            raise HTTPException(404, 'Task not found')
        return task
    except (httpx.HTTPError, ValueError, TypeError, AttributeError):
        raise HTTPException(503, 'Progress is temporarily unavailable') from None


def read_snapshot(task_id, task):
    measured = datetime.now(timezone.utc).isoformat()
    value = dict(schema_version=1, task_id=task_id, execution_id='pending', revision=0, process_steps=[],
                 timing=dict(started_at=task.get('created_at'), finished_at=None, measured_at=measured))
    pointers = list(WORKSPACES.glob(f'*/{task_id}/f4-trace/latest.json'))
    if len(pointers) == 1:
        try:
            pointer = json.loads(pointers[0].read_text(encoding='utf-8'))
            trace_id = pointer.get('trace_id', '')
            if not re.fullmatch(r'[0-9a-f]{32}', trace_id):
                raise ValueError('Invalid trace')
            source = pointers[0].parent / trace_id / 'public.json'
            if not source.resolve().is_relative_to(WORKSPACES.resolve()):
                raise ValueError('Invalid progress path')
            if source.stat().st_size > 1024 * 1024:
                raise ValueError('Progress exceeds its display limit')
            public = json.loads(source.read_text(encoding='utf-8'))
            if public.get('task_id') != task_id or public.get('execution_id') != trace_id:
                raise ValueError('Progress belongs to another execution')
            steps = []
            for item in public['process_steps'][:1000]:
                if not isinstance(item, dict) or not str(item.get('step_id', '')).startswith('f4-'):
                    continue
                step = {key: item[key] for key in ('step_id', 'revision', 'order', 'title', 'status', 'summary',
                                                   'started_at', 'finished_at', 'elapsed_ms') if key in item}
                for channel in ('output', 'reasoning'):
                    output = item.get(channel)
                    if (isinstance(output, dict) and isinstance(output.get('text'), str)
                            and output.get('format') in ('json', 'python', 'text')
                            and output.get('mode') in ('streaming', 'buffered', 'complete', 'interrupted')):
                        if channel == 'reasoning' and (output['format'] != 'text'
                                                       or len(output['text'].encode('utf-8')) > MAX_REASONING):
                            continue
                        step[channel] = {key: output[key]
                                         for key in ('text', 'format', 'mode', 'truncated') if key in output}
                        step[channel]['estimated_tokens'] = estimate_tokens(output['text'])
                steps.append(step)
            value.update(execution_id=trace_id, revision=public['revision'], process_steps=steps,
                         timing={**public['timing'], 'measured_at': measured},
                         error=public_error(public.get('error')))
            if not value.get('error') and task.get('status') == 'failed':
                summary_path = source.with_name('summary.json')
                if (summary_path.resolve().is_relative_to(WORKSPACES.resolve())
                        and summary_path.stat().st_size <= 2 * 1024 * 1024):
                    saved = json.loads(summary_path.read_text(encoding='utf-8'))
                    if saved.get('task_id') == task_id and saved.get('trace_id') == trace_id:
                        value['error'] = public_error(saved.get('error'))
        except (OSError, ValueError, KeyError, TypeError):
            pass  # Startup and atomic replacement may briefly have no readable public snapshot.
    value['status'] = task['status']
    if value['status'] in ('succeeded', 'failed', 'canceled', 'interrupted') and not value['timing']['finished_at']:
        value['timing']['finished_at'] = task.get('updated_at')
    return value


@router.get('/api/chat/paper-f4/tasks/{task_id}/progress')
async def paper_progress(task_id: UUID, request: Request):
    identifier = str(task_id)
    authorization = request.headers.get('authorization', '')
    task = await authorized_task(identifier, authorization)
    if request.query_params.get('stream') != '1':
        return read_snapshot(identifier, task)

    async def events():
        nonlocal task
        last = None
        checked = time.monotonic()
        heartbeat = checked
        while not await request.is_disconnected():
            if time.monotonic() - checked >= 5:
                try:
                    task = await authorized_task(identifier, authorization)
                except HTTPException:
                    yield 'data: {"type":"resync_required"}\n\n'
                    return
                checked = time.monotonic()
            value = read_snapshot(identifier, task)
            identity = (value['execution_id'], value['revision'], value['status'])
            if identity != last:
                yield 'data: ' + json.dumps({'type': 'task_snapshot', 'data': value}, ensure_ascii=False) + '\n\n'
                last = identity
            if value['status'] in ('succeeded', 'failed', 'canceled', 'interrupted'):
                yield 'data: [DONE]\n\n'
                return
            if time.monotonic() - heartbeat >= 15:
                yield ': heartbeat\n\n'
                heartbeat = time.monotonic()
            await asyncio.sleep(0.5)

    return StreamingResponse(events(), media_type='text/event-stream',
                             headers={'Cache-Control': 'no-store', 'X-Accel-Buffering': 'no'})
