from __future__ import annotations

import asyncio
from typing import Annotated, Optional

from fastapi import APIRouter, Header, HTTPException, Request

from lazymind.chat.api.knowledge_search_routes import INTERNAL_TOKEN_HEADER, require_internal_token
from lazymind.document_tools import reading
from lazymind.document_tools import discovery
from lazymind.document_tools.read_execution import run_document_operation

router = APIRouter()
READ_WAIT_SECONDS = 25.0
_ERROR_STATUS = {
    'INVALID_ARGUMENT': 400, 'AUTH_REQUIRED': 401, 'ACCESS_DENIED': 403, 'NOT_FOUND': 404,
    'VERSION_CHANGED': 409, 'CONTENT_TOO_LARGE': 413, 'UNSUPPORTED': 422,
    'RATE_LIMITED': 429, 'TIMEOUT': 504,
    'RESOURCE_LIMIT_EXCEEDED': 413, 'BUSY': 503,
}


@router.post('/internal/documents:read', response_model=reading.DocumentReadResponse)
async def read_document(
    request: Request,
    x_lazymind_internal_token: Annotated[Optional[str], Header(alias=INTERNAL_TOKEN_HEADER)] = None,
):
    return await _invoke(request, x_lazymind_internal_token, 'read', reading.DocumentReadRequest)


@router.post('/internal/documents:browse', response_model=discovery.DocumentDiscoveryResponse)
async def browse_documents(
    request: Request,
    x_lazymind_internal_token: Annotated[Optional[str], Header(alias=INTERNAL_TOKEN_HEADER)] = None,
):
    return await _invoke(request, x_lazymind_internal_token, 'browse', discovery.DocumentDiscoveryRequest)


@router.post('/internal/documents:search', response_model=discovery.DocumentDiscoveryResponse)
async def search_documents(
    request: Request,
    x_lazymind_internal_token: Annotated[Optional[str], Header(alias=INTERNAL_TOKEN_HEADER)] = None,
):
    return await _invoke(request, x_lazymind_internal_token, 'search', discovery.DocumentDiscoveryRequest)


async def _invoke(request, provided_token, operation, model):
    require_internal_token(provided_token)
    try:
        # Avoid FastAPI's default validation response echoing credential input.
        body = bytearray()
        async for chunk in request.stream():
            body.extend(chunk)
            if len(body) > 65536:
                raise ValueError('request too large')
        payload = model.model_validate_json(bytes(body))
    except ValueError:
        raise HTTPException(
            422, detail={'code': 'INVALID_ARGUMENT', 'message': 'invalid document read request'}) from None
    try:
        return await asyncio.wait_for(
            _run_connected(request, operation, payload), timeout=READ_WAIT_SECONDS,
        )
    except TimeoutError:
        raise HTTPException(504, detail={'code': 'TIMEOUT', 'message': 'document read timed out'}) from None
    except reading.DocumentReadError as exc:
        raise HTTPException(_ERROR_STATUS.get(exc.code, 503), detail={'code': exc.code, 'message': str(exc)}) from None


async def _run_connected(request, operation, payload):
    async def disconnected():
        while True:
            if (await request.receive())['type'] == 'http.disconnect':
                return

    work = asyncio.create_task(run_document_operation(operation, payload))
    disconnect = asyncio.create_task(disconnected())
    try:
        done, _ = await asyncio.wait({work, disconnect}, return_when=asyncio.FIRST_COMPLETED)
        if work in done:
            return await work
        raise HTTPException(499, detail={'code': 'CANCELLED', 'message': 'document request disconnected'})
    finally:
        work.cancel()
        disconnect.cancel()
        await asyncio.gather(work, disconnect, return_exceptions=True)
