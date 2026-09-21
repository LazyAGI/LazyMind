import logging
import json
import uuid
from contextlib import asynccontextmanager
from typing import Annotated, Callable, Literal

from fastapi import Depends, FastAPI, Header, Path, Query, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse, Response
from pydantic import BaseModel, ConfigDict, Field, SecretStr, model_validator

from channel_gateway.bootstrap import GatewayComponents, build_components
from channel_gateway.common.application.providers import (
    AccountApplicationService,
    ConnectionApplicationService,
)
from channel_gateway.common.errors import GatewayError


logging.basicConfig(level=logging.INFO, format='%(asctime)s %(levelname)s %(name)s %(message)s')
logging.getLogger('httpx').setLevel(logging.WARNING)
_logger = logging.getLogger(__name__)


class WeComCredentials(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    bot_id: str = Field(min_length=1, max_length=256, pattern=r'^[^\s\x00-\x1f]+$')
    secret: SecretStr = Field(min_length=1, max_length=4096)


class ConnectionSessionCreate(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    provider: str = Field(min_length=1, max_length=32)
    create_new: bool = False
    reauthorize: bool = False
    credentials: WeComCredentials | None = None
    account_id: str | None = Field(default=None, min_length=1, max_length=256)

    @model_validator(mode='after')
    def validate_mode(self):
        if self.credentials is not None and self.provider.strip().lower() != 'wecom':
            raise ValueError('Invalid connection mode')
        if self.create_new and (self.provider.strip().lower() != 'feishu' or self.account_id is not None):
            raise ValueError('Invalid connection intent')
        if self.reauthorize and (self.provider.strip().lower() != 'feishu' or not self.account_id or self.create_new):
            raise ValueError('Invalid reauthorization intent')
        return self


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


class AccountRename(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    label: str = Field(min_length=1, max_length=80, pattern=r'^[^\x00-\x1f\x7f]+$')

    @model_validator(mode='after')
    def trim_label(self):
        self.label = self.label.strip()
        if not self.label:
            raise ValueError('Empty account label')
        return self


class DefaultRecipientUpdate(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    recipient_id: str = Field(max_length=256, pattern=r'^[^\x00-\x1f]*$')


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
    avatar_url: str | None = None
    capabilities: dict = Field(default_factory=dict)
    identity: dict[str, str] = Field(default_factory=dict)
    binding_status: Literal['connected', 'paused', 'unbound']
    default_recipient_id: str = ''


class SessionErrorView(BaseModel):
    code: str
    message: str
    retryable: bool


class ConnectionSessionView(BaseModel):
    id: str
    provider: str
    mode: Literal['qr_code', 'credentials']
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


Identifier = Annotated[str, Field(min_length=1, max_length=256, pattern=r'^[^\x00-\x1f]+$')]
OptionalIdentifier = Annotated[str, Field(max_length=256, pattern=r'^[^\x00-\x1f]*$')]


class TaskNotificationCreate(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    event_id: Identifier
    task_id: Identifier
    schedule_id: Identifier
    event: Literal['succeeded', 'failed', 'waiting']
    config_revision: int = Field(ge=1)
    title: str = Field(min_length=1, max_length=200)
    body: str = Field(min_length=1, max_length=262144)
    content: Literal['summary', 'full']
    channel: Literal['wechat', 'feishu', 'wecom']
    account_id: Identifier
    recipient_id: OptionalIdentifier = ''


class NotificationRetry(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    idempotency_key: str = Field(min_length=1, max_length=128)
    confirm_duplicate_risk: bool = False


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
    path = request.url.path
    if request.method in ('POST', 'PUT', 'PATCH') and path.startswith('/api/channel-gateway/'):
        limit = 2 * 1024 * 1024 if path.endswith('/task-notifications') else 16 * 1024
        raw = bytearray()
        async for chunk in request.stream():
            raw.extend(chunk)
            if len(raw) > limit:
                return handle_gateway_error(request, GatewayError(413, 'INVALID_REQUEST', '请求内容超过限制'))
        request._body = bytes(raw)

        def unique_fields(pairs):
            result = {}
            for key, value in pairs:
                if key in result:
                    raise ValueError('Duplicate field')
                result[key] = value
            return result

        try:
            if raw:
                json.loads(raw, object_pairs_hook=unique_fields)
        except (ValueError, RecursionError):
            return handle_gateway_error(request, GatewayError(422, 'INVALID_REQUEST', '请求参数不正确'))
    response = await call_next(request)
    if request.url.path.startswith('/api/channel-gateway/'):
        response.headers['Cache-Control'] = 'no-store'
        response.headers['Pragma'] = 'no-cache'
    return response


@app.exception_handler(GatewayError)
def handle_gateway_error(request: Request, exc: GatewayError):
    request_id = request.headers.get('X-Request-Id') or f'req_{uuid.uuid4().hex}'
    if len(request_id) > 128:
        request_id = f'req_{uuid.uuid4().hex}'
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
    if len(request_id) > 128:
        request_id = f'req_{uuid.uuid4().hex}'
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


@app.get('/api/channel-gateway/v1/channel-accounts/{account_id}/notification-targets')
@permission_required('qa.read')
def notification_targets(
    request: Request, account_id: Identifier, owner: Annotated[str, Depends(current_owner)],
    cursor: Annotated[str, Query(max_length=256)] = '',
    recipient_id: Annotated[str, Query(max_length=256)] = '',
    limit: Annotated[int, Query(ge=1, le=100)] = 20,
):
    return components(request).notifications.notification_targets(
        owner, account_id, cursor=cursor, recipient_id=recipient_id, limit=limit)


@app.get('/api/channel-gateway/v1/channel-accounts/{account_id}/notification-groups')
@permission_required('qa.read')
def notification_groups(request: Request, account_id: Identifier, owner: Annotated[str, Depends(current_owner)],
                        cursor: Annotated[str, Query(max_length=2048)] = '',
                        limit: Annotated[int, Query(ge=1, le=100)] = 100):
    return components(request).notifications.notification_groups(owner, account_id, cursor, limit)


@app.put('/api/channel-gateway/v1/channel-accounts/{account_id}/default-recipient', response_model=AccountView)
@permission_required('qa.write')
def set_default_recipient(request: Request, account_id: Identifier, payload: DefaultRecipientUpdate,
                          owner: Annotated[str, Depends(current_owner)]):
    return components(request).notifications.set_default_recipient(owner, account_id, payload.recipient_id)


@app.get('/api/channel-gateway/v1/channel-accounts/{account_id}')
@permission_required('qa.read')
def channel_account_detail(request: Request, account_id: Identifier, owner: Annotated[str, Depends(current_owner)]):
    return components(request).notifications.account_detail(
        owner, account_id, include_references=request.query_params.get('include_references') != 'false')


@app.post('/api/channel-gateway/v1/task-notifications', status_code=201)
@permission_required('qa.write')
def enqueue_notification(request: Request, payload: TaskNotificationCreate,
                         owner: Annotated[str, Depends(current_owner)]):
    return components(request).notifications.enqueue(owner, payload.model_dump())


@app.get('/api/channel-gateway/v1/channel-accounts/{account_id}/notification-references')
@permission_required('qa.read')
def notification_references(
    request: Request, account_id: Identifier, owner: Annotated[str, Depends(current_owner)],
    cursor: Annotated[str, Query(max_length=512)] = '', limit: Annotated[int, Query(ge=1, le=100)] = 20,
):
    return components(request).notifications.references(owner, account_id, cursor, limit)


@app.get('/api/channel-gateway/v1/task-notifications')
@permission_required('qa.read')
def notification_history(
    request: Request, task_id: Identifier, owner: Annotated[str, Depends(current_owner)],
    cursor: Annotated[int, Query(ge=0, le=9223372036854775807)] = 0,
    limit: Annotated[int, Query(ge=1, le=100)] = 20,
):
    return components(request).store.notification_history(owner, task_id, cursor, limit)


@app.get('/api/channel-gateway/v1/task-notifications/{notification_id}')
@permission_required('qa.read')
def get_notification(request: Request, notification_id: Identifier, owner: Annotated[str, Depends(current_owner)]):
    return components(request).store.get_notification(owner, notification_id)


@app.post('/api/channel-gateway/v1/task-notifications/{notification_id}:retry', status_code=201)
@permission_required('qa.write')
def retry_notification(request: Request, notification_id: Identifier, payload: NotificationRetry,
                       owner: Annotated[str, Depends(current_owner)]):
    return components(request).notifications.retry(owner, notification_id, payload.idempotency_key,
                                                   payload.confirm_duplicate_risk)


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


@app.patch('/api/channel-gateway/v1/channel-accounts/{account_id}', response_model=AccountView)
@permission_required('qa.write')
def rename_channel_account(
    account_id: Identifier, payload: AccountRename,
    owner: Annotated[str, Depends(current_owner)],
    gateway: Annotated[AccountApplicationService, Depends(account_service)],
):
    return gateway.rename_account(owner, account_id, payload.label)


@app.post('/api/channel-gateway/v1/channel-accounts/{account_id}:archive', status_code=204)
@permission_required('qa.write')
def archive_channel_account(
    account_id: Identifier, owner: Annotated[str, Depends(current_owner)],
    gateway: Annotated[AccountApplicationService, Depends(account_service)],
):
    gateway.archive_account(owner, account_id)
    return Response(status_code=204)


@app.post('/api/channel-gateway/v1/channel-accounts/{account_id}:pause', status_code=204)
@permission_required('qa.write')
def pause_channel_account(
    account_id: Annotated[str, Path(min_length=1, max_length=256)],
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[AccountApplicationService, Depends(account_service)],
):
    gateway.pause_account(owner_user_id, account_id)
    return Response(status_code=204)


@app.post('/api/channel-gateway/v1/channel-accounts/{account_id}:resume', response_model=AccountView)
@permission_required('qa.write')
def resume_channel_account(
    account_id: Annotated[str, Path(min_length=1, max_length=256)],
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[AccountApplicationService, Depends(account_service)],
):
    return gateway.resume_account(owner_user_id, account_id)


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
        credentials=({'bot_id': payload.credentials.bot_id,
                      'secret': payload.credentials.secret.get_secret_value()} if payload.credentials else None),
        account_id=payload.account_id,
        create_new=payload.create_new,
        reauthorize=payload.reauthorize,
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
