from core.deps import require_internal_service_token
from core.errors import ErrorCodes
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
            'authorization': ErrorCodes.MCP_OAUTH_AUTHORIZATION_REQUIRED,
            'invalid': ErrorCodes.MCP_OAUTH_INVALID_REQUEST,
            'busy': ErrorCodes.MCP_OAUTH_BUSY,
            'configuration': ErrorCodes.MCP_OAUTH_CONFIGURATION_INVALID,
        }
        status, code, message = errors.get(exc.kind, ErrorCodes.MCP_OAUTH_PROVIDER_FAILED)
        result = {'code': code, 'message': message, 'ex_mesage': ''}
        if exc.kind == 'authorization':
            result['status'] = 'needs_authorization'
        return JSONResponse(status_code=status, content=result)
    except Exception:  # noqa: BLE001 - redact all credential-bearing failure details
        # OAuth provider responses/codes/credentials must never reach generic exception logs.
        status, code, message = ErrorCodes.MCP_OAUTH_STORAGE_UNAVAILABLE
        return JSONResponse(status_code=status, content={'code': code, 'message': message, 'ex_mesage': ''})


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
