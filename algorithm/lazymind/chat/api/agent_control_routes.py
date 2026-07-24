from __future__ import annotations

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

router = APIRouter()


class ToolLimitDecisionRequest(BaseModel):
    conversation_id: str
    decision_id: str
    action: str


class AgentControlResponse(BaseModel):
    ok: bool


@router.post('/api/agent/tool-limit-decision', response_model=AgentControlResponse,
             summary='Continue or summarize a ChatAgent after its tool-round limit')
async def tool_limit_decision(req: ToolLimitDecisionRequest) -> AgentControlResponse:
    from lazymind.chat.engine.agent_runtime.tool_limit_control import tool_limit_decision_coordinator
    from lazymind.chat.service.chat_service import _active_sessions

    action = req.action.strip().lower()
    if action not in {'continue', 'summarize'}:
        raise HTTPException(status_code=400, detail='action must be continue or summarize')
    sid = _active_sessions.get(req.conversation_id.strip())
    if not sid or not tool_limit_decision_coordinator.submit(sid, req.decision_id, action):
        return AgentControlResponse(ok=False)
    return AgentControlResponse(ok=True)
