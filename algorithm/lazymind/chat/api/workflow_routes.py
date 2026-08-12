"""Workflow API routes.

Routes:
    POST /api/writer/documents:sync      Persist a LazyMind WriterDocument edit.
    POST /api/subagent/tasks:cancel      LazyMind task cancellation callback.
"""
from __future__ import annotations

import base64
import inspect
import logging
import tempfile
from typing import Any, Dict, List, Literal, Optional

import httpx
import yaml
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field

from lazyllm.tools.tool_config_inject import inject_tool_config
from lazyllm.tools.writer.data_models import PatchResult, PatchSet, WriterDocument
from lazyllm.tools.writer.tools import WriterResourceTools, WriterRevisionTools
from lazyllm.tools.writer.tools.revision_tools import apply_patch_to_ir
from lazyllm.tools.writer.utils import load_artifact_json
from lazymind.chat.engine.subagent.runner import _workflow_package_tools
from lazymind.config import config
from lazymind.model_config import inject_model_config
from lazymind.workflow_sdk import WorkflowClient

router = APIRouter()
logger = logging.getLogger(__name__)


class TaskCancelRequest(BaseModel):
    task_id: Optional[str] = None
    conversation_id: Optional[str] = None


class TaskCancelResponse(BaseModel):
    ok: bool


class WorkflowDriverRequest(BaseModel):
    workflow_id: str
    step_id: str
    step_result: str
    acceptance: str = ''
    driver_prompt: str = ''
    session_id: Optional[str] = None
    history_files_per_turn: Optional[Dict[str, List[str]]] = None
    llm_config: Optional[Dict[str, Any]] = None
    workflow_artifacts_summary: Optional[str] = None


class WorkflowDriverResponse(BaseModel):
    message: str


class WriterDocumentSyncRequest(BaseModel):
    source_document: WriterDocument
    revised_document: WriterDocument
    tool_config: Dict[str, Any] = Field(default_factory=dict)


class WorkflowActionInvokeRequest(BaseModel):
    workflow_id: str
    revision_id: str
    tree_hash: str = ''
    user_id: str = ''
    action: str
    phase: Literal['preview', 'execute']
    slot: str
    artifact: Any = None
    arguments: Dict[str, Any] = Field(default_factory=dict)
    artifact_store: str = ''
    llm_config: Optional[Dict[str, Any]] = None
    tool_config: Optional[Dict[str, Any]] = None


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


def _action_definition(request: WorkflowActionInvokeRequest) -> Dict[str, Any]:
    package = WorkflowClient(
        str(config['core_api_url']).rstrip('/'), request.user_id,
        host='lazymind', transport=httpx,
    ).get_workflow(request.workflow_id, request.revision_id).result
    if str(package.get('revision_id') or '') != request.revision_id:
        raise HTTPException(status_code=409, detail='workflow revision changed')
    if request.tree_hash and str(package.get('tree_hash') or '') != request.tree_hash:
        raise HTTPException(status_code=409, detail='workflow tree hash changed')
    files = package.get('files') if isinstance(package.get('files'), dict) else {}
    encoded = files.get('workflow.yaml')
    if not encoded:
        raise HTTPException(status_code=404, detail='workflow definition not found')
    raw = base64.b64decode(encoded) if isinstance(encoded, str) else bytes(encoded)
    document = yaml.safe_load(raw.decode('utf-8')) or {}
    actions = document.get('artifact_actions') or {}
    definition = actions.get(request.action) if isinstance(actions, dict) else None
    if not isinstance(definition, dict):
        raise HTTPException(status_code=404, detail='artifact action not found')
    return definition


@router.post('/api/workflow/actions:invoke', summary='Invoke a Workflow-owned artifact action')
def invoke_workflow_action(request: WorkflowActionInvokeRequest) -> Dict[str, Any]:
    definition = _action_definition(request)
    if request.slot not in (definition.get('slots') or []):
        raise HTTPException(status_code=400, detail='action is not enabled for this slot')
    tool_name = str(definition.get(f'{request.phase}_tool') or '')
    tools = _workflow_package_tools(request.model_dump(), [tool_name]) if tool_name else {}
    tool = tools.get(tool_name)
    if tool is None:
        raise HTTPException(status_code=500, detail='artifact action tool is unavailable')

    kwargs = dict(request.arguments)
    reserved = {'artifact', 'artifact_store', 'slot'} & kwargs.keys()
    if reserved:
        raise HTTPException(status_code=400, detail=f'reserved arguments: {sorted(reserved)}')
    parameters = inspect.signature(tool).parameters
    if 'artifact' in parameters:
        kwargs['artifact'] = request.artifact
    if 'artifact_store' in parameters:
        kwargs['artifact_store'] = request.artifact_store
    if 'slot' in parameters:
        kwargs['slot'] = request.slot
    try:
        inject_model_config(request.llm_config or {})
        inject_tool_config(request.tool_config or {})
        return {'result': tool(**kwargs)}
    except ValueError as exc:
        code = str(getattr(exc, 'error_code', 'WORKFLOW_ACTION_INVALID'))
        detail: Dict[str, Any] = {'code': code, 'message': str(exc)}
        detail.update(getattr(exc, 'details', {}) or {})
        status = 409 if code in {'SELECTION_AMBIGUOUS', 'SELECTION_STALE'} else 422
        raise HTTPException(status_code=status, detail=detail) from exc
    except TypeError as exc:
        raise HTTPException(
            status_code=422,
            detail={'code': 'WORKFLOW_ACTION_INVALID', 'message': str(exc)},
        ) from exc
    except Exception as exc:
        logger.exception(
            'Workflow artifact action failed: workflow=%s action=%s phase=%s',
            request.workflow_id, request.action, request.phase,
        )
        raise HTTPException(
            status_code=502,
            detail={'code': 'WORKFLOW_ACTION_FAILED', 'message': str(exc)},
        ) from exc


@router.post('/api/workflow/driver', response_model=WorkflowDriverResponse,
             summary='Evaluate a terminal Workflow attempt')
async def workflow_driver(req: WorkflowDriverRequest) -> WorkflowDriverResponse:
    from lazymind.chat.workflow.driver_agent import DriverEvaluationError, evaluate_step

    try:
        result = evaluate_step(
            workflow_id=req.workflow_id, step_id=req.step_id, step_result=req.step_result,
            acceptance=req.acceptance, driver_prompt=req.driver_prompt,
            session_id=req.session_id,
            user_files=[p for paths in (req.history_files_per_turn or {}).values() for p in paths] or None,
            llm_config=req.llm_config,
            workflow_artifacts_summary=req.workflow_artifacts_summary,
        )
    except DriverEvaluationError as exc:
        raise HTTPException(status_code=503, detail=str(exc)) from exc
    return WorkflowDriverResponse(message=result['message'])


@router.post('/api/workflow/task-cancel', response_model=TaskCancelResponse, summary='Cancel a running SubAgent task')
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
