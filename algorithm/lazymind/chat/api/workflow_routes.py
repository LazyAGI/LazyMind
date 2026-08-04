"""Workflow API routes.

Routes:
    POST /api/writer/documents:sync      Persist a LazyMind WriterDocument edit.
    POST /api/subagent/tasks:cancel      LazyMind task cancellation callback.
"""
from __future__ import annotations

import tempfile
from typing import Any, Dict, Optional

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field

from lazyllm.tools.tool_config_inject import inject_tool_config
from lazyllm.tools.writer.data_models import PatchResult, PatchSet, WriterDocument
from lazyllm.tools.writer.tools import WriterResourceTools, WriterRevisionTools
from lazyllm.tools.writer.tools.revision_tools import apply_patch_to_ir
from lazyllm.tools.writer.utils import load_artifact_json

router = APIRouter()


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
