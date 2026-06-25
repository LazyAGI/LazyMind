import json
import logging
import time
from pathlib import Path

import traceback
import yaml
from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.responses import Response
from starlette.exceptions import HTTPException as StarletteHTTPException

from api.auth import router as auth_router
from api.authorization import router as authorization_router
from api.cloud_oauth import router as cloud_oauth_router
from api.group import router as group_router
from api.role import router as role_router
from api.user import router as user_router
from core.errors import AppException, error_payload_from_exception
from core.state_errors import StateBackendAuthenticationError, StateBackendError
from services import cloud_oauth_health


# Ensure logs are visible (uvicorn log_config may set default level to WARNING and disable existing loggers)
logging.basicConfig(level=logging.INFO, format='%(message)s', force=True)

_API_PREFIX = '/api/authservice'
_OPENAPI_JSON_PATH = f'{_API_PREFIX}/openapi.json'
_SWAGGER_JSON_PATH = f'{_API_PREFIX}/swagger.json'
_OPENAPI_YAML_PATH = f'{_API_PREFIX}/openapi.yaml'
_DOCS_PATH = f'{_API_PREFIX}/docs'

app = FastAPI(
    title='Auth Service',
    description=(
        'LazyMind authentication and authorization service '
        '(login, registration, token, user/role/group management)'
    ),
    version='1.0.0',
    docs_url=_DOCS_PATH,
    redoc_url=None,
    oauth2_redirect_url=None,
    openapi_url=_OPENAPI_JSON_PATH,
)

_SWAGGER_PATHS = {_OPENAPI_JSON_PATH, _SWAGGER_JSON_PATH, _OPENAPI_YAML_PATH, _DOCS_PATH}

_logger = logging.getLogger('uvicorn.error')
_logger.setLevel(logging.INFO)
if not _logger.handlers:
    _logger.addHandler(logging.StreamHandler())
_logger.propagate = True


@app.middleware('http')
async def _log_request(request: Request, call_next):
    """Log one request entry plus access-log for each request"""
    if request.url.path in _SWAGGER_PATHS:
        return await call_next(request)
    client_ip = None
    if request.client:
        client_ip = request.client.host
    _logger.info('Request started: %s %s from %s', request.method, request.url.path, client_ip)
    print(f'Request started: {request.method} {request.url.path} from {client_ip}', flush=True)
    start = time.time()
    try:
        response = await call_next(request)
    except AppException:
        # Business exceptions are formatted by exception_handler and should not all be 500
        raise
    except StateBackendError:
        # Handled by state backend handler for clearer error output
        raise
    except StarletteHTTPException:
        raise
    except Exception as e:
        cost_ms = int((time.time() - start) * 1000)
        _logger.exception(
            'unhandled_exception method=%s path=%s cost_ms=%d',
            request.method,
            request.url.path,
            cost_ms,
            extra={'method': request.method, 'path': request.url.path, 'cost_ms': cost_ms},
        )
        # Force traceback output to stdout to avoid invisibility caused by uvicorn/logger config
        print(traceback.format_exc(), flush=True)
        _logger.error(
            'unhandled_exception_detail type=%s module=%s message=%s',
            type(e).__name__,
            type(e).__module__,
            str(e),
        )
        print(
            f'unhandled_exception method={request.method} path={request.url.path} cost_ms={cost_ms} '
            f'type={type(e).__name__} module={type(e).__module__} message={e}',
            flush=True,
        )
        return JSONResponse(status_code=500, content={'code': 500, 'message': 'Internal Server Error', 'ex_mesage': ''})
    cost_ms = int((time.time() - start) * 1000)
    if response.status_code >= 500:
        _logger.error(
            'access-log method=%s path=%s status=%s cost_ms=%d',
            request.method,
            request.url.path,
            response.status_code,
            cost_ms,
            extra={
                'method': request.method,
                'path': request.url.path,
                'status': response.status_code,
                'cost_ms': cost_ms,
            },
        )
        print(
            f'access-log method={request.method} path={request.url.path}'
            f' status={response.status_code} cost_ms={cost_ms}',
            flush=True,
        )
    else:
        _logger.info(
            'access-log method=%s path=%s status=%s cost_ms=%d',
            request.method,
            request.url.path,
            response.status_code,
            cost_ms,
            extra={
                'method': request.method,
                'path': request.url.path,
                'status': response.status_code,
                'cost_ms': cost_ms,
            },
        )
        print(
            f'access-log method={request.method} path={request.url.path}'
            f' status={response.status_code} cost_ms={cost_ms}',
            flush=True,
        )
    return response


def _copy_headers(headers) -> dict[str, str]:
    d = dict(headers)
    d.pop('content-length', None)
    return d


@app.middleware('http')
async def _standardize_json_response(request: Request, call_next):
    response = await call_next(request)
    if request.url.path in _SWAGGER_PATHS:
        return response

    content_type = (response.headers.get('content-type') or '').lower()
    if 'application/json' not in content_type:
        return response

    body = b''
    async for chunk in response.body_iterator:
        body += chunk

    try:
        payload = json.loads(body.decode('utf-8')) if body else None
    except Exception:
        return Response(
            content=body,
            status_code=response.status_code,
            headers=_copy_headers(response.headers),
            media_type='application/json',
        )

    if isinstance(payload, dict) and (
        ('swagger' in payload or 'openapi' in payload)
        and 'info' in payload
        and 'paths' in payload
    ):
        return Response(
            content=body,
            status_code=response.status_code,
            headers=_copy_headers(response.headers),
            media_type='application/json',
        )

    if 200 <= response.status_code < 300:
        wrapped = {'code': response.status_code, 'message': 'success', 'data': payload}
        return JSONResponse(content=wrapped, status_code=response.status_code, headers=_copy_headers(response.headers))

    if isinstance(payload, dict) and 'code' in payload and 'message' in payload:
        payload.setdefault('ex_mesage', '')
        return JSONResponse(content=payload, status_code=response.status_code, headers=_copy_headers(response.headers))

    msg = None
    if isinstance(payload, dict):
        msg = payload.get('message')
    if not isinstance(msg, str) or not msg:
        msg = 'An error occurred'
    wrapped = {'code': response.status_code, 'message': msg, 'ex_mesage': ''}
    return JSONResponse(content=wrapped, status_code=response.status_code, headers=_copy_headers(response.headers))


@app.exception_handler(AppException)
def _handle_app_exception(_, exc: AppException):
    return JSONResponse(status_code=exc.http_code, content=error_payload_from_exception(exc))


@app.exception_handler(StateBackendError)
def _handle_state_backend_error(_, exc: StateBackendError):
    from core.errors import ErrorCodes, raise_error
    # Full stack trace must be printed here; otherwise logs may only show
    # "State backend is unavailable" and hide the root cause.
    tb = ''.join(traceback.format_exception(type(exc), exc, exc.__traceback__))
    _logger.error('state_backend_error type=%s message=%s\n%s', type(exc).__name__, str(exc), tb)
    print(f'state_backend_error type={type(exc).__name__} message={exc}\n{tb}', flush=True)
    try:
        if isinstance(exc, StateBackendAuthenticationError):
            raise_error(ErrorCodes.STATE_BACKEND_AUTH_FAILED)
        raise_error(ErrorCodes.STATE_BACKEND_UNAVAILABLE)
    except AppException as e:
        return JSONResponse(status_code=e.http_code, content=error_payload_from_exception(e))


@app.exception_handler(StarletteHTTPException)
def _handle_http_exception(_, exc: StarletteHTTPException):
    message = exc.detail if isinstance(exc.detail, str) else 'HTTP error'
    return JSONResponse(
        status_code=exc.status_code,
        content={'code': exc.status_code, 'message': message, 'ex_mesage': ''},
    )


@app.exception_handler(RequestValidationError)
def _handle_validation_error(_, exc: RequestValidationError):
    return JSONResponse(
        status_code=400,
        content={
            'code': 400,
            'message': 'Invalid request parameters',
            'ex_mesage': json.dumps(exc.errors(), ensure_ascii=False),
        },
    )


@app.on_event('startup')
async def _start_cloud_oauth_health_check():
    cloud_oauth_health.start()


@app.on_event('shutdown')
async def _stop_cloud_oauth_health_check():
    await cloud_oauth_health.stop()


def _export_openapi_artifacts() -> None:
    schema = app.openapi()
    current_dir = Path(__file__).resolve().parent
    repo_root = current_dir.parent.parent
    outputs = {
        current_dir / 'openapi.json': 'json',
        repo_root / 'api' / 'backend' / 'auth-service' / 'swagger.json': 'json',
        repo_root / 'api' / 'backend' / 'auth-service' / 'openapi.yml': 'yaml',
        Path('/openapi-export/auth-service/swagger.json'): 'json',
        Path('/openapi-export/auth-service/openapi.yml'): 'yaml',
    }
    json_body = json.dumps(schema, ensure_ascii=False, indent=2) + '\n'
    yaml_body = yaml.dump(schema, allow_unicode=True, sort_keys=False)
    for output, kind in outputs.items():
        try:
            output.parent.mkdir(parents=True, exist_ok=True)
            if kind == 'json':
                output.write_text(json_body, encoding='utf-8')
            else:
                output.write_text(yaml_body, encoding='utf-8')
        except OSError:
            continue


@app.get(_SWAGGER_JSON_PATH, include_in_schema=False)
def swagger_json():
    return JSONResponse(content=app.openapi())


@app.get(_OPENAPI_YAML_PATH, include_in_schema=False)
def openapi_yaml():
    """openapi.yaml document"""
    schema = app.openapi()
    body = yaml.dump(schema, allow_unicode=True, sort_keys=False)
    return Response(content=body, media_type='application/x-yaml')


app.include_router(auth_router, prefix=_API_PREFIX)
app.include_router(authorization_router, prefix=_API_PREFIX)
app.include_router(cloud_oauth_router, prefix=_API_PREFIX)
app.include_router(user_router, prefix=_API_PREFIX)
app.include_router(role_router, prefix=_API_PREFIX)
app.include_router(group_router, prefix=_API_PREFIX)

_export_openapi_artifacts()
