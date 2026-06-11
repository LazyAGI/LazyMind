from __future__ import annotations

import json
import logging
import os
from typing import Any, Dict, Optional

import lazyllm
from fastapi import APIRouter
from fastapi.responses import StreamingResponse
from pydantic import BaseModel

logger = logging.getLogger(__name__)

router = APIRouter()

try:
    from lazymind.chat.plugins.loader import plugin_loader
    from lazymind.chat.plugins.step_agent import create_step_agent, call_summary_func
    from lazymind.chat.plugins.driver_agent import evaluate_step
    from lazymind.chat.plugins.validator import validate_all
    from lazymind.chat.plugins.config import (
        load_step_artifacts,
        load_step_checkpoint,
        load_previous_step_summary,
        load_plugin_info,
        load_attempt_count,
        PLUGIN_WORKSPACE_BASE,
    )
    _PLUGIN_ENABLED = True
except Exception as exc:
    logger.error('Plugin system failed to initialize: %s', exc)
    _PLUGIN_ENABLED = False


# ---------------------------------------------------------------------------
# Request models — Go passes only control identifiers.
# Python resolves all business data (plugin_id, step_id, artifacts, etc.)
# autonomously from the DB using plugin_session_id as the sole lookup key.
# ---------------------------------------------------------------------------

class PluginStepRequest(BaseModel):
    plugin_session_id: str
    step_exec_id: str
    user_input: str = ''
    llm_config: Optional[Dict[str, Any]] = None


class PluginDriverRequest(BaseModel):
    plugin_session_id: str
    step_result: str = ''


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _resolve_step_workspace(plugin_session_id: str, step_exec_id: str) -> str:
    """Compute the workspace path using the same convention as Go."""
    base = os.environ.get('PLUGIN_WORKSPACE_BASE', PLUGIN_WORKSPACE_BASE)
    return os.path.join(base, plugin_session_id, step_exec_id)


# ---------------------------------------------------------------------------
# Endpoints
# ---------------------------------------------------------------------------

@router.post('/api/plugin/step', summary='Execute a plugin step via StepAgent (SSE)')
async def run_plugin_step(request: PluginStepRequest):
    if not _PLUGIN_ENABLED:
        async def err():
            yield f'data: {json.dumps({"type": "step_error", "error": "plugin system disabled"})}\n\n'
        return StreamingResponse(err(), media_type='text/event-stream')

    # Resolve plugin_id and step_id from the session record.
    info = load_plugin_info(request.plugin_session_id)
    plugin_id = info['plugin_id']
    step_id = info['step_id']

    if not plugin_id or not step_id:
        err_msg = f'Cannot resolve plugin_id/step_id for session {request.plugin_session_id!r}'

        async def missing_err():
            yield f'data: {json.dumps({"type": "step_error", "error": err_msg})}\n\n'
        return StreamingResponse(missing_err(), media_type='text/event-stream')

    step_config = plugin_loader.get_step_config(plugin_id, step_id)
    plugin_tools = plugin_loader.get_plugin_tools(plugin_id)

    # Build default tools from the canonical list; never rely on a cross-request global.
    try:
        from lazymind.chat.service.component import DEFAULT_TOOLS, filter_tools
        default_tools = [cfg.instance for cfg in filter_tools(DEFAULT_TOOLS, [])]
    except Exception:
        default_tools = []

    # Use step_exec_id as the lazyllm session ID — unique per execution,
    # never collides with the parent chat session_id.
    step_sid = f'step-{request.step_exec_id}'
    lazyllm.globals._init_sid(sid=step_sid)
    lazyllm.locals._init_sid(sid=step_sid)

    from lazymind.model_config import inject_model_config as _inject_mc
    _inject_mc(request.llm_config)

    # Derive workspace path using the same convention as Go.
    step_workspace = _resolve_step_workspace(request.plugin_session_id, request.step_exec_id)

    # Query all business data from DB.
    artifacts = load_step_artifacts(request.plugin_session_id)
    checkpoint = load_step_checkpoint(request.plugin_session_id, step_id)
    previous_summary = load_previous_step_summary(request.plugin_session_id, step_id)

    agentic_config = {
        'plugin_id': plugin_id,
        'plugin_session_id': request.plugin_session_id,
        'step_exec_id': request.step_exec_id,
        'step_workspace': step_workspace,
        'step_checkpoint': checkpoint,
    }
    lazyllm.globals['agentic_config'] = agentic_config
    lazyllm.globals['plugin_event_queue'] = []

    try:
        from lazyllm import AutoModel
        llm = AutoModel(model='llm')
    except Exception as llm_init_err:
        _err_msg = str(llm_init_err)

        async def llm_err():
            yield f'data: {json.dumps({"type": "step_error", "error": f"LLM init failed: {_err_msg}"})}\n\n'
        return StreamingResponse(llm_err(), media_type='text/event-stream')

    agent = create_step_agent(
        step_config=step_config,
        artifacts=artifacts,
        checkpoint=checkpoint,
        default_tools=default_tools + plugin_tools,
        llm=llm,
        step_exec_id=request.step_exec_id,
        previous_summary=previous_summary,
    )

    async def event_stream():
        try:
            import lazyllm.module.stream_helper as _sh
            helper = _sh.StreamCallHelper(agent, init_sid=False)
            async for _chunk in helper.astream(request.user_input or ''):
                queued = list(lazyllm.globals.get('plugin_event_queue', []))
                if queued:
                    lazyllm.globals['plugin_event_queue'] = []
                    for ev in queued:
                        yield f'data: {json.dumps(ev, ensure_ascii=False)}\n\n'

            final_queued = list(lazyllm.globals.get('plugin_event_queue', []))
            lazyllm.globals['plugin_event_queue'] = []
            for ev in final_queued:
                yield f'data: {json.dumps(ev, ensure_ascii=False)}\n\n'

            # Run summary_func after all LLM output, merging in-step artifact events.
            fresh_artifacts = dict(artifacts)
            for ev in final_queued:
                if isinstance(ev, dict) and ev.get('type') == 'artifact':
                    fresh_artifacts[ev['artifact_id']] = ev['value']
            summary = call_summary_func(step_config, fresh_artifacts)
            if summary:
                ev = {'type': 'artifact', 'artifact_id': 'step_summary', 'value': summary}
                yield f'data: {json.dumps(ev, ensure_ascii=False)}\n\n'

            result_text = helper.result if hasattr(helper, 'result') else ''
            ev = {
                'type': 'step_complete',
                'step_exec_id': request.step_exec_id,
                'result_summary': str(result_text)[:300],
            }
            yield f'data: {json.dumps(ev, ensure_ascii=False)}\n\n'
        except Exception as exc:
            logger.exception('StepAgent execution failed for session %s step %s',
                             request.plugin_session_id, step_id)
            yield f'data: {json.dumps({"type": "step_error", "error": str(exc)})}\n\n'

    return StreamingResponse(event_stream(), media_type='text/event-stream')


@router.post('/api/plugin/driver', summary='Evaluate a completed step with DriverAgent')
async def run_plugin_driver(request: PluginDriverRequest):
    if not _PLUGIN_ENABLED:
        return {'judgment': 'Step completed. Proceed.'}

    info = load_plugin_info(request.plugin_session_id)
    plugin_id = info['plugin_id']
    step_id = info['step_id']
    attempt = load_attempt_count(request.plugin_session_id, step_id)

    try:
        from lazyllm import AutoModel
        llm = AutoModel(model='llm')
    except Exception:
        llm = None

    artifacts = load_step_artifacts(request.plugin_session_id)

    judgment = evaluate_step(
        plugin_id=plugin_id,
        step_id=step_id,
        step_result=request.step_result,
        artifacts=artifacts,
        attempt=attempt,
        llm=llm,
    )
    return {'judgment': judgment}


@router.post('/api/plugin/validate/{plugin_id}', summary='Validate a plugin configuration')
async def validate_plugin(plugin_id: str):
    if not _PLUGIN_ENABLED:
        return {'is_valid': False, 'errors': ['plugin system disabled'], 'warnings': [], 'infos': []}

    from lazymind.chat.plugins.config import PLUGIN_DIR
    plugin_dir = os.path.join(PLUGIN_DIR, plugin_id)
    if not os.path.isdir(plugin_dir):
        return {
            'is_valid': False,
            'errors': [f'Plugin directory not found: {plugin_dir}'],
            'warnings': [],
            'infos': [],
        }

    result = validate_all(plugin_dir)
    return {
        'is_valid': result.is_valid,
        'errors': result.errors,
        'warnings': result.warnings,
        'infos': result.infos,
    }


@router.get('/api/plugin/list', summary='List all loaded plugins')
async def list_plugins():
    if not _PLUGIN_ENABLED:
        return {'plugins': []}

    plugins = []
    for pid in plugin_loader.list_plugin_ids():
        py = plugin_loader.get_plugin_yaml(pid)
        plugins.append({
            'id': pid,
            'name': py.get('name', pid),
            'description': py.get('description', ''),
            'legacy_mode': plugin_loader.is_legacy_mode(pid),
        })
    return {'plugins': plugins}
