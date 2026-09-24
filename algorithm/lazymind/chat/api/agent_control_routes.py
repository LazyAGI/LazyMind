from __future__ import annotations

import secrets

from fastapi import APIRouter, Depends, HTTPException, Request
from pydantic import BaseModel, SecretStr

from lazymind.config import config

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


class SessionEnvInputRequest(BaseModel):
    conversation_id: str
    ask_id: str
    value: SecretStr | None = None
    cancel: bool = False


class SessionEnvInputResponse(BaseModel):
    ok: bool
    status: str | None = None


def require_env_input_service(request: Request) -> None:
    token = str(config['core_internal_token'] or '').strip()
    supplied = request.headers.get('X-LazyMind-Internal-Token', '')
    if not token or not secrets.compare_digest(token.encode(), supplied.encode()):
        raise HTTPException(status_code=401, detail='Internal service authentication required')


@router.get('/api/chat/session-env:pending', response_model=AgentControlResponse,
            dependencies=[Depends(require_env_input_service)])
async def session_env_pending(conversation_id: str, ask_id: str) -> AgentControlResponse:
    from lazymind.chat.engine.agent_runtime.env_input import session_env_inputs
    return AgentControlResponse(ok=session_env_inputs.owns(ask_id, conversation_id))


@router.post('/api/chat/session-env:input', response_model=SessionEnvInputResponse,
             dependencies=[Depends(require_env_input_service)])
async def submit_session_env(req: SessionEnvInputRequest) -> SessionEnvInputResponse:
    from lazymind.chat.engine.agent_runtime.env_input import session_env_inputs
    value = req.value.get_secret_value() if req.value is not None else ''
    if not req.cancel and (not value.strip() or '\0' in value or value.strip() == '<redacted>'):
        raise HTTPException(status_code=400, detail='Invalid environment variable value')
    try:
        if req.cancel:
            status = session_env_inputs.cancel(req.ask_id, req.conversation_id)
        else:
            status = 'configured' if session_env_inputs.submit(req.ask_id, req.conversation_id, value) else None
        if status is None and config['enable_router']:
            from lazymind.router.core.session_env import submit_worker_session_env
            status = await submit_worker_session_env(req.ask_id, req.conversation_id, value, cancel=req.cancel)
    except Exception:
        raise HTTPException(status_code=409, detail='Environment input expired; request a new card') from None
    return SessionEnvInputResponse(ok=status is not None, status=status)


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
    if config['enable_router']:
        from lazymind.router.core.session_env import clear_worker_session_env
        try:
            cleared.extend(await clear_worker_session_env(req.conversation_ids))
        except Exception:
            raise HTTPException(status_code=503, detail='Session environment cleanup is incomplete') from None
    return SessionEnvClearResponse(ok=True, cleared=sorted(set(cleared)))
