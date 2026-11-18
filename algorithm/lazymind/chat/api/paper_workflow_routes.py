"""Internal paper candidate verification, isolated from the shared tool gateway."""
from typing import Any

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel, ConfigDict

from lazymind.chat.api.agent_control_routes import require_env_input_service

router = APIRouter(dependencies=[Depends(require_env_input_service)])


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
