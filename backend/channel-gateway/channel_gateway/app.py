import logging
import uuid
from contextlib import asynccontextmanager
from typing import Annotated

from fastapi import Depends, FastAPI, Header, Query, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse, Response

from channel_gateway.common.channel_actions import ChannelActionExecutor
from channel_gateway.common.channel_message import ChannelMessageService
from channel_gateway.common.database import GatewayStore
from channel_gateway.common.inbound import InboundMessageProcessor
from channel_gateway.common.intent_router import (
    ChannelIntentClassifier,
    ExactShortcutParser,
)
from channel_gateway.common.lazymind import LazyMindClient
from channel_gateway.common.models import (
    AccountListView,
    ConnectionChallengeSubmit,
    ConnectionSessionCreate,
    ConnectionSessionView,
)
from channel_gateway.common.rbac import permission_required
from channel_gateway.common.security import JsonCipher
from channel_gateway.settings import Settings
from channel_gateway.wechat.runtime import WeChatRuntime
from channel_gateway.wechat.service import GatewayError, WeChatConnectionService


logging.basicConfig(level=logging.INFO, format='%(asctime)s %(levelname)s %(name)s %(message)s')
logging.getLogger('httpx').setLevel(logging.WARNING)
_logger = logging.getLogger(__name__)

settings = Settings()
store = GatewayStore(settings.database_dsn)
cipher = JsonCipher(settings.credential_key_path)
lazymind = LazyMindClient(
    settings.core_base_url,
    settings.core_chat_timeout_seconds,
)
messages = ChannelMessageService(
    store=store,
    shortcuts=ExactShortcutParser(store),
    classifier=ChannelIntentClassifier(lazymind),
    executor=ChannelActionExecutor(store=store, client=lazymind),
)
inbound = InboundMessageProcessor(store=store, messages=messages)
runtime = WeChatRuntime(
    settings=settings,
    store=store,
    cipher=cipher,
    inbound=inbound,
    lazymind=lazymind,
)
service = WeChatConnectionService(
    settings=settings,
    store=store,
    cipher=cipher,
    on_account_connected=runtime.start_account,
)


@asynccontextmanager
async def lifespan(application: FastAPI):
    try:
        service.start()
        runtime.start()
        application.state.connection_service = service
        _logger.info('channel_gateway_started')
        yield
    finally:
        runtime.stop()
        service.stop()
        _logger.info('channel_gateway_stopped')


app = FastAPI(
    title='LazyMind Channel Gateway',
    description='Unified channel connection and authorization gateway.',
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


def connection_service(request: Request) -> WeChatConnectionService:
    return request.app.state.connection_service


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
def readyz():
    store.ping()
    return {'status': 'ready'}


@app.get(
    '/api/channel-gateway/v1/channel-accounts',
    response_model=AccountListView,
)
@permission_required('qa.read')
def list_channel_accounts(
    provider: Annotated[str, Query(min_length=1, max_length=32)],
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[WeChatConnectionService, Depends(connection_service)],
):
    return gateway.list_accounts(owner_user_id, provider)


@app.post(
    '/api/channel-gateway/v1/connection-sessions',
    response_model=ConnectionSessionView,
    status_code=201,
)
@permission_required('qa.write')
def create_connection_session(
    payload: ConnectionSessionCreate,
    owner_user_id: Annotated[str, Depends(current_owner)],
    gateway: Annotated[WeChatConnectionService, Depends(connection_service)],
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
    gateway: Annotated[WeChatConnectionService, Depends(connection_service)],
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
    gateway: Annotated[WeChatConnectionService, Depends(connection_service)],
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
    gateway: Annotated[WeChatConnectionService, Depends(connection_service)],
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
    gateway: Annotated[WeChatConnectionService, Depends(connection_service)],
):
    gateway.cancel_session(owner_user_id, session_id)
    return Response(status_code=204)
