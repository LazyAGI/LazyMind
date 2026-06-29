"""Plugin API routes.

Routes:
    POST /api/plugin/driver              DriverAgent evaluation endpoint (called by Go EventLoop).
    POST /api/plugin/step-cancel         Enqueue cancel signal into the step_done FileSystemQueue (called by Go :stop).
    GET  /api/plugin/slot-binding        Slot binding lookup (called by Go OnArtifactEvent).
    GET  /api/plugins                    List all loaded plugins.
    GET  /api/plugins/{plugin_id}        Get plugin spec (supports Accept-Language for i18n labels).
"""
from __future__ import annotations

from typing import Any, Dict, List, Optional

from fastapi import APIRouter, Header, HTTPException, Query
from pydantic import BaseModel

from lazymind.chat.plugin import plugin_loader
from lazymind.chat.plugin.driver_agent import DriverEvaluationError, evaluate_step

router = APIRouter()


class DriverRequest(BaseModel):
    plugin_id: str
    step_id: str
    step_result: str
    session_id: Optional[str] = None
    history_files_per_turn: Optional[Dict[str, List[str]]] = None
    llm_config: Optional[Dict[str, Any]] = None
    plugin_artifacts_summary: Optional[str] = None


class DriverResponse(BaseModel):
    message: str  # Natural-language assessment passed verbatim to ChatAgent as user input


class StepCancelRequest(BaseModel):
    session_id: str
    step_id: str


class StepCancelResponse(BaseModel):
    ok: bool


class TaskCancelRequest(BaseModel):
    task_id: Optional[str] = None
    conversation_id: Optional[str] = None


class TaskCancelResponse(BaseModel):
    ok: bool


@router.post('/api/plugin/driver', response_model=DriverResponse, summary='Evaluate plugin step result')
async def plugin_driver(req: DriverRequest) -> DriverResponse:
    """DriverAgent evaluation endpoint.

    Called by the Go EventLoop after a plugin_step SubAgent reaches terminal status.
    Returns a natural-language assessment that the Go EventLoop forwards verbatim to
    the ChatAgent as a synthetic user turn.  The ChatAgent then decides autonomously
    whether to advance, retry, rewind, or complete the plugin.
    """
    try:
        result = evaluate_step(
            plugin_id=req.plugin_id,
            step_id=req.step_id,
            step_result=req.step_result,
            session_id=req.session_id,
            user_files=[p for paths in (req.history_files_per_turn or {}).values() for p in paths] or None,
            llm_config=req.llm_config,
            plugin_artifacts_summary=req.plugin_artifacts_summary,
        )
    except DriverEvaluationError as exc:
        raise HTTPException(status_code=503, detail=str(exc)) from exc
    return DriverResponse(message=result.get('message', ''))


@router.post('/api/plugin/step-cancel', response_model=StepCancelResponse, summary='Cancel a running plugin step')
async def step_cancel(req: StepCancelRequest) -> StepCancelResponse:
    """Enqueue a cancel signal for a running plugin step.

    Called by the Go EventLoop when the user stops chat generation.
    The signal is written into the FileSystemQueue that _wait_for_step_done polls,
    causing the dynamic-mode advance_step tool to unblock and return immediately.
    """
    import json
    try:
        from lazyllm.common.queue import FileSystemQueue
        queue_key = f'step_done_{req.session_id}_{req.step_id}'
        fsq = FileSystemQueue(klass=queue_key)
        fsq.enqueue(json.dumps({'tag': 'cancel'}))
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc))
    return StepCancelResponse(ok=True)


@router.post('/api/plugin/task-cancel', response_model=TaskCancelResponse, summary='Cancel a running SubAgent task')
async def task_cancel(req: TaskCancelRequest) -> TaskCancelResponse:
    """Enqueue a cancel signal for a running SubAgent ReAct loop.

    Called by the Go EventLoop when the user stops chat generation.
    The signal is written into the FileSystemQueue(klass='cancel') scoped
    to the task's sid, causing the ReAct stop_condition to raise CancelledError.

    Supports two identification modes:
    - task_id: direct task/session ID (original SubAgent path)
    - conversation_id: looks up the active chat session from _active_sessions
    """
    import json as _json
    from lazymind.chat.service.chat_service import _active_sessions
    try:
        import lazyllm
        from lazyllm.common.queue import FileSystemQueue

        sid: Optional[str] = None
        if req.conversation_id:
            sid = _active_sessions.get(req.conversation_id)
        elif req.task_id:
            sid = req.task_id

        if not sid:
            return TaskCancelResponse(ok=False)

        lazyllm.globals._init_sid(sid=sid)
        FileSystemQueue(klass='cancel').enqueue(_json.dumps({'tag': 'cancel'}))
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc))
    return TaskCancelResponse(ok=True)


@router.get('/api/plugin/slot-binding', summary='Lookup slot binding for artifact key')
async def slot_binding(
    plugin_id: str = Query(..., description='Plugin identifier'),  # noqa: B008
    artifact_key: str = Query(..., description='Artifact key to look up'),  # noqa: B008
) -> Dict[str, Any]:
    """Return the slot_id and cardinality bound to an artifact key, if any.

    Priority:
    1. Direct lookup via plugin.yaml ui.tabs[].slots[].artifact_key (new in Phase 3).
    2. Legacy: search step outputs in state.yml.
    """
    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        raise HTTPException(status_code=404, detail=f'Plugin {plugin_id!r} not found')

    # Fast path: direct artifact_key → slot lookup via plugin.yaml ui.tabs.
    slot_def = spec.get_slot_for_artifact_key(artifact_key)
    if slot_def:
        return {
            'slot_id': slot_def.get('id', ''),
            'cardinality': slot_def.get('cardinality', 'single'),
        }

    # Fallback: legacy state.yml output mapping.
    slot_id: Optional[str] = None
    cardinality = 'single'
    for step_cfg in spec._steps.values():
        for out in step_cfg.get('outputs', []):
            if out.get('artifact_id') == artifact_key:
                slot_id = out.get('slot_id')
                if slot_id:
                    slot_def2 = spec.get_slot_def(slot_id)
                    if slot_def2:
                        cardinality = slot_def2.get('cardinality', 'single')
                break
        if slot_id:
            break

    return {
        'slot_id': slot_id or '',
        'cardinality': cardinality,
    }


@router.get('/api/plugins', summary='List all loaded plugins')
async def list_plugins(
    accept_language: Optional[str] = Header(None, alias='Accept-Language'),  # noqa: B008
) -> Dict[str, Any]:
    """Return summary information for all loaded plugins with i18n labels if Accept-Language is set."""
    lang = _parse_best_lang(accept_language)
    if lang:
        return {'plugins': [plugin_loader.get_plugin_with_i18n(pid, lang) for pid in plugin_loader._registry]}
    return {'plugins': plugin_loader.list_plugins()}


@router.get('/api/plugins/{plugin_id}', summary='Get plugin spec')
async def get_plugin(
    plugin_id: str,
    accept_language: Optional[str] = Header(None, alias='Accept-Language'),  # noqa: B008
) -> Dict[str, Any]:
    """Return the full plugin specification with optional i18n label resolution.

    Pass Accept-Language header (e.g. 'zh-CN') to receive translated tab/slot/step labels.
    """
    lang = _parse_best_lang(accept_language)
    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        raise HTTPException(status_code=404, detail=f'Plugin {plugin_id!r} not found')

    # Apply i18n if requested.
    resolved = plugin_loader.get_plugin_with_i18n(plugin_id, lang) or {}

    steps_detail = []
    for step in resolved.get('steps', []):
        sid = step.get('id', '')
        steps_detail.append({
            'id': sid,
            'label': step.get('label', ''),
            'config': spec.get_step_config(sid),
        })
    return {
        'id': spec.plugin_id,
        'name': resolved.get('name', spec.yaml.get('name', spec.plugin_id)),
        'description': resolved.get('description', spec.yaml.get('description', '')),
        'steps': steps_detail,
        'ui': resolved.get('ui', spec.yaml.get('ui', {})),
        'state': spec.state,
        'i18n': spec.yaml.get('i18n', {}),
    }


def _parse_best_lang(accept_language: Optional[str]) -> str:
    """Parse the Accept-Language header and return the highest-priority language tag.

    Returns an empty string if header is absent or cannot be parsed.
    """
    if not accept_language:
        return ''
    # Format: 'zh-CN,zh;q=0.9,en;q=0.8'
    parts = [p.strip() for p in accept_language.split(',')]
    if not parts:
        return ''
    first = parts[0].split(';')[0].strip()
    return first
