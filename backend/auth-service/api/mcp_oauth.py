from core.deps import require_internal_service_token
from fastapi import APIRouter, Depends
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field
from services.mcp_oauth import OAuthError, mcp_oauth_service

router = APIRouter(prefix='/v1/mcp-oauth', tags=['mcp-oauth'], dependencies=[Depends(require_internal_service_token)])


class Identity(BaseModel):
    user_id: str = Field(min_length=1, max_length=64)
    server_id: str = Field(min_length=1, max_length=128)
    server_url: str = Field(min_length=1, max_length=4096)


class Callback(Identity):
    code: str = Field(min_length=1, max_length=8192)
    state: str = Field(min_length=1, max_length=256)


class Token(Identity):
    grant_id: str = Field(min_length=1, max_length=64)
    grant_version: int = Field(ge=1)
    rejected_token_version: int | None = Field(default=None, ge=1)


def _call(operation, body):
    try:
        return getattr(mcp_oauth_service, operation)(**body.model_dump())
    except OAuthError as exc:
        errors = {
            'authorization': (401, 1001101, 'MCP authorization required'),
            'invalid': (400, 1001102, 'Invalid MCP OAuth request or state'),
            'busy': (503, 1001104, 'MCP authorization is busy; retry shortly'),
            'configuration': (503, 1001105, 'MCP OAuth public callback is not configured correctly'),
        }
        status, code, message = errors.get(exc.kind, (502, 1001103, 'MCP OAuth provider request failed'))
        result = {'code': code, 'message': message, 'ex_mesage': ''}
        if exc.kind == 'authorization':
            result['status'] = 'needs_authorization'
        return JSONResponse(status_code=status, content=result)
    except Exception:  # noqa: BLE001 - redact all credential-bearing failure details
        # OAuth provider responses/codes/credentials must never reach generic exception logs.
        return JSONResponse(status_code=503, content={
            'code': 1001106, 'message': 'MCP OAuth storage or encryption is unavailable', 'ex_mesage': ''})


@router.post('/authorize')
def authorize(body: Identity):
    return _call('authorize', body)


@router.post('/callback')
def callback(body: Callback):
    return _call('callback', body)


@router.post('/status')
def status(body: Identity):
    return _call('status', body)


@router.post('/disconnect')
def disconnect(body: Identity):
    return _call('disconnect', body)


@router.post('/token')
def token(body: Token):
    return _call('token', body)
