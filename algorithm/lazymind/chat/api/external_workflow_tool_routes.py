"""Internal Core-to-Chat gateway for explicitly audited Workflow tools."""
from typing import Any

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel, ConfigDict

from lazymind.chat.api.agent_control_routes import require_env_input_service
from lazymind.chat.workflow.external_tools import (
    ExternalToolCallRequest, ExternalToolRequest, call_external_tool, list_external_tools,
)
from lazymind.chat.workflow.external_ppt_tools import ExternalToolError

router = APIRouter(dependencies=[Depends(require_env_input_service)])


def _invoke(function, request):
    try:
        return function(request)
    except ExternalToolError as exc:
        status = 503 if exc.code in {'DEPENDENCY_MISSING', 'TOOL_ADAPTER_UNAVAILABLE'} else 400
        detail = {'code': exc.code, 'message': str(exc)}
        if exc.public_message:
            detail['public_message'] = exc.public_message
        raise HTTPException(status_code=status, detail=detail) from exc


@router.post('/api/workflow/external-tools/list')
def list_tools(request: ExternalToolRequest):
    return _invoke(list_external_tools, request)


@router.post('/api/workflow/external-tools/call')
def call_tool(request: ExternalToolCallRequest):
    return _invoke(call_external_tool, request)


class PaperVerification(BaseModel):
    model_config = ConfigDict(extra='forbid')
    context: dict[str, Any]
    artifacts: list[dict[str, Any]]


@router.post('/api/workflow/paper/verify')
def verify_paper(request: PaperVerification):
    from lazymind.document_tools.paper.agent_tools import PaperContext, verify_publication
    try:
        return verify_publication(PaperContext.parse(request.context), request.artifacts)
    except ValueError as exc:
        raise HTTPException(status_code=422, detail={'code': 'PAPER_OUTPUT_INVALID', 'message': str(exc)}) from exc
