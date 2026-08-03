import logging
import uuid
from contextlib import asynccontextmanager
from typing import Annotated, Callable, Literal

from fastapi import Depends, FastAPI, Header, Query, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse, Response
from pydantic import BaseModel, Field

from channel_gateway.bootstrap import GatewayComponents, build_components
from channel_gateway.common.application.providers import (
    AccountApplicationService,
    ConnectionApplicationService,
)
from channel_gateway.common.errors import GatewayError


logging.basicConfig(level=logging.INFO, format='%(asctime)s %(levelname)s %(name)s %(message)s')
logging.getLogger('httpx').setLevel(logging.WARNING)
_logger = logging.getLogger(__name__)


class ConnectionSessionCreate(BaseModel):
    provider: str = Field(min_length=1, max_length=32)


class ConnectionChallengeSubmit(BaseModel):
    type: str = Field(default='numeric_code', max_length=32)
    value: str = Field(min_length=1, max_length=12)


class QRCodeView(BaseModel):
    payload: str
    version: int
    expires_at: str


class ChallengeView(BaseModel):
    type: str
    prompt: str
    input_mode: str


class AccountView(BaseModel):
    id: str
    provider: str
    label: str
    status: Literal['provisioning', 'connected', 'disconnected']
    runtime_status: Literal[
        'stopped',
        'starting',
        'running',
        'degraded',
        'failed',
    ]
    connected_at: str | None
    last_poll_at: str | None
    last_message_at: str | None
    last_error: str | None
    updated_at: str


class SessionErrorView(BaseModel):
    code: str
    message: str
    retryable: bool


class ConnectionSessionView(BaseModel):
    id: str
    provider: str
    mode: Literal['qr_code']
    status: Literal[
        'preparing',
        'waiting_scan',
        'scanned',
        'verification_required',
        'confirming',
        'connected',
        'expired',
        'canceled',
        'failed',
    ]
    revision: int
    message: str
    qr: QRCodeView | None
    challenge: ChallengeView | None
    poll_after_ms: int
    allowed_actions: list[
        Literal['cancel', 'submit_challenge', 'refresh']
    ]
    account: AccountView | None
    error: SessionErrorView | None


class AccountListView(BaseModel):
    items: list[AccountView]


def permission_required(*permissions: str):
    """Static marker consumed by backend/scripts/extract_api_permissions.py."""

    def decorator(function: Callable):
        function.__required_permissions__ = set(permissions)
        return function

    return decorator


@asynccontextmanager
async def lifespan(application: FastAPI):
    components = build_components()
    try:
        components.start()
        application.state.components = components
        _logger.info('channel_gateway_started')
        yield
    finally:
        components.stop()
        _logger.info('channel_gateway_stopped')


app = FastAPI(
    title='LazyMind Channel Gateway',
    description='Unified external chat channel gateway.',
    version='0.1.0',
    docs_url='/api/channel-gateway/v1/docs',
    redoc_url=None,
    openapi_url='/api/channel-gateway/v1/openapi.json',
    lifespan=lifespan,
)


def current_owner(request: Request) -> str:
    value = (request.headers.get('X-User-Id') or '').strip()
    if not value:
        raise GatewayError(401, 'UNAUTHORIZED', '请先登录')
    return value


def components(request: Request) -> GatewayComponents:
    return request.app.state.components


def connection_service(request: Request) -> ConnectionApplicationService:
    return components(request).connections


def account_service(request: Request) -> AccountApplicationService:
    return components(request).accounts


@app.middleware('http')
async def security_headers(request: Request, call_next):
    response = await call_next(request)
    if request.url.path.startswith('/api/channel-gateway/'):
        response.headers['Cache-Control'] = 'no-store'
        response.headers['Pragma'] = 'no-cache'
    return response


@app.exception_handler(GatewayError)
def handle_gateway_error(request: Request, exc: GatewayError):
    request_id = request.headers.get('X-Request-Id') or f'req_{uuid.uuid4().hex}'
    return JSONResponse(
        status_code=exc.http_status,
        content={
            'error': {
                'code': exc.code,
                'message': exc.message,
                'retryable': exc.retryable,
                'request_id': request_id,
            }
        },
        headers={'Cache-Control': 'no-store'},
    )


@app.exception_handler(RequestValidationError)
def handle_request_validation_error(request: Request, exc: RequestValidationError):
    request_id = request.headers.get('X-Request-Id') or f'req_{uuid.uuid4().hex}'
    _logger.info('request_validation_failed path=%s errors=%s', request.url.path, len(exc.errors()))
    return JSONResponse(
        status_code=422,
        content={
            'error': {
                'code': 'INVALID_REQUEST',
                'message': '请求参数不正确',
                'retryable': False,
                'request_id': request_id,
            }
        },
        headers={'Cache-Control': 'no-store'},
    )


@app.get('/healthz')
def healthz():
    return {'status': 'ok'}


@app.get('/readyz')
def readyz(request: Request):
    components(request).store.ping()
    return {'status': 'ready'}


@app.get(
    '/api/channel-gateway/v1/channel-accounts',
    response_model=AccountListView,
)
@permission_required('qa.read')
def list_channel_accounts(
    provider: Annotated[str, Query(min_length=1, max_length=32)],
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[AccountApplicationService, Depends(account_service)],
):
    return gateway.list_accounts(owner_user_id, provider)


@app.delete(
    '/api/channel-gateway/v1/channel-accounts/{account_id}',
    status_code=204,
)
@permission_required('qa.write')
def disconnect_channel_account(
    account_id: str,
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[AccountApplicationService, Depends(account_service)],
):
    gateway.disconnect_account(owner_user_id, account_id)
    return Response(status_code=204)


@app.post(
    '/api/channel-gateway/v1/connection-sessions',
    response_model=ConnectionSessionView,
    status_code=201,
)
@permission_required('qa.write')
def create_connection_session(
    payload: ConnectionSessionCreate,
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[
        ConnectionApplicationService,
        Depends(connection_service),
    ],
    idempotency_key: Annotated[str | None, Header(alias='Idempotency-Key')] = None,
):
    return gateway.create_session(
        owner_user_id=owner_user_id,
        provider=payload.provider,
        idempotency_key=idempotency_key,
    )


@app.get(
    '/api/channel-gateway/v1/connection-sessions/{session_id}',
    response_model=ConnectionSessionView,
)
@permission_required('qa.read')
def get_connection_session(
    session_id: str,
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[
        ConnectionApplicationService,
        Depends(connection_service),
    ],
):
    return gateway.get_session(owner_user_id, session_id)


@app.post(
    '/api/channel-gateway/v1/connection-sessions/{session_id}:submit-challenge',
    response_model=ConnectionSessionView,
)
@permission_required('qa.write')
def submit_connection_challenge(
    session_id: str,
    payload: ConnectionChallengeSubmit,
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[
        ConnectionApplicationService,
        Depends(connection_service),
    ],
):
    return gateway.submit_challenge(
        owner_user_id=owner_user_id,
        session_id=session_id,
        challenge_type=payload.type,
        value=payload.value,
    )


@app.post(
    '/api/channel-gateway/v1/connection-sessions/{session_id}:refresh',
    response_model=ConnectionSessionView,
)
@permission_required('qa.write')
def refresh_connection_session(
    session_id: str,
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[
        ConnectionApplicationService,
        Depends(connection_service),
    ],
):
    return gateway.refresh_session(owner_user_id, session_id)


@app.delete(
    '/api/channel-gateway/v1/connection-sessions/{session_id}',
    status_code=204,
)
@permission_required('qa.write')
def cancel_connection_session(
    session_id: str,
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[
        ConnectionApplicationService,
        Depends(connection_service),
    ],
):
    gateway.cancel_session(owner_user_id, session_id)
    return Response(status_code=204)
