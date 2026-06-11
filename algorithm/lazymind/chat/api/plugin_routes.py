from __future__ import annotations

import json
import logging
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
    _PLUGIN_ENABLED = True
except Exception as exc:
    logger.error('Plugin system failed to initialize: %s', exc)
    _PLUGIN_ENABLED = False


class PluginStepRequest(BaseModel):
    plugin_id: str
    step_id: str
    step_exec_id: str
    plugin_session_id: str = ''
    step_workspace: str = ''
    user_input: str = ''
    artifacts: Optional[Dict[str, Any]] = None
    checkpoint: Optional[Dict[str, Any]] = None


class PluginDriverRequest(BaseModel):
    plugin_id: str
    step_id: str
    step_result: str = ''
    artifacts: Optional[Dict[str, Any]] = None
    attempt: int = 1


@router.post('/api/plugin/step', summary='Execute a plugin step via StepAgent (SSE)')
async def run_plugin_step(request: PluginStepRequest):
    if not _PLUGIN_ENABLED:
        async def err():
            yield f'data: {json.dumps({"type": "step_error", "error": "plugin system disabled"})}\n\n'
        return StreamingResponse(err(), media_type='text/event-stream')

    step_config = plugin_loader.get_step_config(request.plugin_id, request.step_id)
    plugin_tools = plugin_loader.get_plugin_tools(request.plugin_id)

    # Build default tools directly from the canonical list; never rely on a cross-request global.
    try:
        from lazymind.chat.service.component import DEFAULT_TOOLS, filter_tools
        default_tools = [cfg.instance for cfg in filter_tools(DEFAULT_TOOLS, [])]
    except Exception:
        default_tools = []

    lazyllm.globals['agentic_config'] = {
        'plugin_id': request.plugin_id,
        'plugin_session_id': request.plugin_session_id,
        'step_exec_id': request.step_exec_id,
        'step_workspace': request.step_workspace,
        'step_checkpoint': request.checkpoint or {},
    }
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
        artifacts=request.artifacts or {},
        checkpoint=request.checkpoint or {},
        default_tools=default_tools + plugin_tools,
        llm=llm,
        step_exec_id=request.step_exec_id,
    )

    async def event_stream():
        try:
            import lazyllm.module.stream_helper as _sh
            helper = _sh.StreamCallHelper(agent, init_sid=False)
            async for _chunk in helper.astream(request.user_input or ''):
                # Flush any events emitted by tools during this step (e.g. checkpoint, artifact).
                queued = list(lazyllm.globals.get('plugin_event_queue', []))
                if queued:
                    lazyllm.globals['plugin_event_queue'] = []
                    for ev in queued:
                        yield f'data: {json.dumps(ev, ensure_ascii=False)}\n\n'

            # Final flush + step_complete.
            final_queued = list(lazyllm.globals.get('plugin_event_queue', []))
            lazyllm.globals['plugin_event_queue'] = []
            for ev in final_queued:
                yield f'data: {json.dumps(ev, ensure_ascii=False)}\n\n'

            # If the step has a summary_func, call it now with the latest artifacts
            # and emit the result as a synthetic artifact event. This runs after all
            # LLM output is done, so it is guaranteed to be the final step_summary value.
            fresh_artifacts = dict(request.artifacts or {})
            # Merge any artifacts emitted during this step execution.
            for ev in final_queued:
                if isinstance(ev, dict) and ev.get('type') == 'artifact':
                    fresh_artifacts[ev['artifact_id']] = ev['value']
            summary = call_summary_func(step_config, fresh_artifacts)
            if summary:
                summary_ev = {
                    'type': 'artifact',
                    'artifact_id': 'step_summary',
                    'value': summary,
                }
                yield f'data: {json.dumps(summary_ev, ensure_ascii=False)}\n\n'

            result_text = helper.result if hasattr(helper, 'result') else ''
            step_complete = {
                'type': 'step_complete',
                'step_exec_id': request.step_exec_id,
                'result_summary': str(result_text)[:300],
            }
            yield f'data: {json.dumps(step_complete, ensure_ascii=False)}\n\n'
        except Exception as exc:
            logger.exception('StepAgent execution failed for step %s', request.step_id)
            yield f'data: {json.dumps({"type": "step_error", "error": str(exc)})}\n\n'

    return StreamingResponse(event_stream(), media_type='text/event-stream')


@router.post('/api/plugin/driver', summary='Evaluate a completed step with DriverAgent')
async def run_plugin_driver(request: PluginDriverRequest):
    if not _PLUGIN_ENABLED:
        return {'judgment': 'Step completed. Proceed.'}

    try:
        from lazyllm import AutoModel
        llm = AutoModel(model='llm')
    except Exception:
        llm = None

    judgment = evaluate_step(
        plugin_id=request.plugin_id,
        step_id=request.step_id,
        step_result=request.step_result,
        artifacts=request.artifacts or {},
        attempt=request.attempt,
        llm=llm,
    )
    return {'judgment': judgment}


@router.post('/api/plugin/validate/{plugin_id}', summary='Validate a plugin configuration')
async def validate_plugin(plugin_id: str):
    if not _PLUGIN_ENABLED:
        return {'is_valid': False, 'errors': ['plugin system disabled'], 'warnings': [], 'infos': []}

    from lazymind.chat.plugins.config import PLUGIN_DIR
    import os
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
