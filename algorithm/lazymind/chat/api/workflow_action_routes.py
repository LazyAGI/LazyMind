"""Synchronous runtime for Workflow-owned artifact actions."""
from __future__ import annotations

import base64
import inspect
import logging
from typing import Any, Dict, Literal, Optional

import httpx
import yaml
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field

from lazyllm.tools.tool_config_inject import inject_tool_config
from lazymind.chat.engine.subagent.runner import _workflow_package_tools
from lazymind.config import config
from lazymind.model_config import inject_model_config
from lazymind.workflow_sdk import WorkflowClient

router = APIRouter()
logger = logging.getLogger(__name__)


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
    reserved = {'artifact', 'artifact_store'} & kwargs.keys()
    if reserved:
        raise HTTPException(status_code=400, detail=f'reserved arguments: {sorted(reserved)}')
    parameters = inspect.signature(tool).parameters
    if 'artifact' in parameters:
        kwargs['artifact'] = request.artifact
    if 'artifact_store' in parameters:
        kwargs['artifact_store'] = request.artifact_store
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
