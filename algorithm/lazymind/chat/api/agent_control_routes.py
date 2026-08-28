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


class SessionEnvClearRequest(BaseModel):
    conversation_ids: list[str]


class SessionEnvClearResponse(BaseModel):
    ok: bool
    cleared: list[str] = []


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


@router.post('/api/chat/session-env:clear', response_model=SessionEnvClearResponse,
             summary='Drop conversation-scoped skill env vars after the conversation is deleted')
async def clear_session_env(req: SessionEnvClearRequest) -> SessionEnvClearResponse:
    from lazymind.chat.service.chat_service import clear_conversation_env

    cleared = []
    for conversation_id in req.conversation_ids or []:
        key = str(conversation_id or '').strip()
        if key and clear_conversation_env(key):
            cleared.append(key)
    return SessionEnvClearResponse(ok=True, cleared=cleared)
