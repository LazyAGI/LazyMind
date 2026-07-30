"""Plugin API routes.

Routes:
    POST /api/writer/documents:sync      Persist a user-edited WriterDocument.
    POST /api/plugin/driver              DriverAgent evaluation endpoint (called by Go EventLoop).
    GET  /api/plugin/slot-binding        Slot binding lookup (called by Go OnArtifactEvent).
    GET  /api/plugins                    List all loaded plugins.
    GET  /api/plugins/{plugin_id}        Get plugin spec (supports Accept-Language for i18n labels).
"""
from __future__ import annotations

import tempfile
from typing import Any, Dict, List, Optional

from fastapi import APIRouter, Header, HTTPException, Query
from pydantic import BaseModel, Field

from lazyllm.tools.tool_config_inject import inject_tool_config
from lazyllm.tools.writer.data_models import PatchResult, PatchSet, WriterDocument
from lazyllm.tools.writer.tools import WriterResourceTools, WriterRevisionTools
from lazyllm.tools.writer.tools.revision_tools import apply_patch_to_ir
from lazyllm.tools.writer.utils import load_artifact_json

from lazymind.chat.plugin import plugin_loader

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


class TaskCancelRequest(BaseModel):
    task_id: Optional[str] = None
    conversation_id: Optional[str] = None


class TaskCancelResponse(BaseModel):
    ok: bool


class WriterDocumentSyncRequest(BaseModel):
    source_document: WriterDocument
    revised_document: WriterDocument
    tool_config: Dict[str, Any] = Field(default_factory=dict)


def _writer_artifact(result: dict, key: Optional[str] = None) -> str:
    path = result.get('artifact_path') if key is None else (
        (result.get('metadata') or {}).get('artifact_paths') or {}
    ).get(key)
    if not path:
        raise ValueError(f'Writer tool did not return artifact {key or "primary"!r}.')
    return path


@router.post('/api/writer/documents:sync', summary='Persist an edited WriterDocument to its provider')
def sync_writer_document(request: WriterDocumentSyncRequest) -> dict:
    source, revised = request.source_document, request.revised_document
    if source.document_id != revised.document_id:
        raise HTTPException(status_code=400, detail='WriterDocument document_id values must match.')
    if not request.tool_config.get('feishu'):
        raise HTTPException(status_code=400, detail='tool_config.feishu is required.')

    try:
        inject_tool_config(request.tool_config)
        with tempfile.TemporaryDirectory(prefix='writer-sync-') as root:
            revision = WriterRevisionTools(llm=None, artifact_store=root)
            patch_output = revision.build_patch_set_from_documents(source, revised)
            patch = load_artifact_json(_writer_artifact(patch_output), PatchSet)
            candidate, local_result = apply_patch_to_ir(source, patch)
            if not patch.hunks and patch.new_title is None:
                candidate.ui_editable = True
                local_result.message = 'No document changes.'
                return _writer_sync_response(False, patch, local_result, candidate)

            write_output = WriterResourceTools(
                llm=None, artifact_store=root,
            ).apply_patch_to_document(patch, source)
            persisted = load_artifact_json(
                _writer_artifact(write_output, 'persisted_document'), WriterDocument,
            )
            result = load_artifact_json(
                _writer_artifact(write_output, 'patch_result'), PatchResult,
            )
            persisted.ui_editable = True
            return _writer_sync_response(True, patch, result, persisted)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except Exception as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc


def _writer_sync_response(
    changed: bool,
    patch: PatchSet,
    result: PatchResult,
    document: WriterDocument,
) -> dict:
    return {
        'success': result.success,
        'changed': changed,
        'feishu_synced': result.success,
        'patch_set': patch.model_dump(),
        'patch_result': result.model_dump(),
        'persisted_document': document.model_dump(),
    }


@router.post('/api/plugin/driver', response_model=DriverResponse, summary='Evaluate plugin step result')
async def plugin_driver(req: DriverRequest) -> DriverResponse:
    """DriverAgent evaluation endpoint.

    Called by the Go EventLoop after a plugin_step SubAgent reaches terminal status.
    Returns a natural-language assessment that the Go EventLoop forwards verbatim to
    the ChatAgent as a synthetic user turn.  The ChatAgent then decides autonomously
    whether to advance, retry, rewind, or complete the plugin.
    """
    from lazymind.chat.plugin.driver_agent import DriverEvaluationError, evaluate_step

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


@router.get('/api/plugin/slot-binding', summary='Lookup slot binding for slot')
async def slot_binding(
    plugin_id: str = Query(..., description='Plugin identifier'),  # noqa: B008
    slot: str = Query(..., description='Slot id to look up'),  # noqa: B008
) -> Dict[str, Any]:
    """Return the slot_id and cardinality bound to a slot, if any.

    Direct lookup via plugin.yaml ui.tabs[].slots[].id.
    """
    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        raise HTTPException(status_code=404, detail=f'Plugin {plugin_id!r} not found')

    slot_def = spec.get_slot(slot)
    if slot_def:
        return {
            'slot_id': slot_def.get('id', ''),
            'cardinality': slot_def.get('cardinality', 'single'),
        }

    return {
        'slot_id': '',
        'cardinality': 'single',
    }


@router.get('/api/plugins', summary='List all loaded plugins')
async def list_plugins(
    accept_language: Optional[str] = Header(None, alias='Accept-Language'),  # noqa: B008
) -> Dict[str, Any]:
    """Return summary information for all loaded plugins with i18n labels if Accept-Language is set."""
    lang = _parse_best_lang(accept_language)
    plugin_loader.ensure_loaded()
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
        # Raw YAML texts for frontend read-only editor display.
        'plugin_yaml_raw': spec.plugin_yaml_raw,
        'state_yaml_raw': spec.state_yaml_raw,
        'scenario_raw': spec.scenario_md,
    }


@router.post('/api/plugins/{plugin_id}', include_in_schema=False)
@router.put('/api/plugins/{plugin_id}', include_in_schema=False)
@router.patch('/api/plugins/{plugin_id}', include_in_schema=False)
@router.delete('/api/plugins/{plugin_id}', include_in_schema=False)
async def builtin_plugin_write_forbidden(plugin_id: str) -> None:  # noqa: ARG001
    """Explicitly reject all write operations on built-in plugins."""
    raise HTTPException(status_code=403, detail='built-in plugins are read-only')


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
