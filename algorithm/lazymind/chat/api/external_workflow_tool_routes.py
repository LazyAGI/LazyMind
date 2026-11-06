"""Internal Core-to-Chat gateway for explicitly audited Workflow tools."""
from fastapi import APIRouter, Depends, HTTPException

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
        raise HTTPException(status_code=status, detail={'code': exc.code, 'message': str(exc)}) from exc


@router.post('/api/workflow/external-tools/list')
def list_tools(request: ExternalToolRequest):
    return _invoke(list_external_tools, request)


@router.post('/api/workflow/external-tools/call')
def call_tool(request: ExternalToolCallRequest):
    return _invoke(call_external_tool, request)
