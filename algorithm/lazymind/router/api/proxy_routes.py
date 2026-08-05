from __future__ import annotations

import json
import logging
from typing import Optional

import httpx
from fastapi import APIRouter, Header, HTTPException, Request, Response

from lazymind.chat.api.knowledge_search_routes import (
    INTERNAL_TOKEN_HEADER,
    expected_internal_token,
    require_internal_token,
)
from lazymind.router.core.ab_router import get_ab_router
from lazymind.router.core.registry import get_global_registry
from lazymind.router.core.stream_proxy import get_stream_proxy

logger = logging.getLogger(__name__)

router = APIRouter()


async def _parse_algo_id(request: Request) -> Optional[str]:
    """Extract optional algorithm_id from the JSON body without consuming it."""
    try:
        body_bytes = await request.body()
        data = json.loads(body_bytes) if body_bytes else {}
    except Exception:
        data = {}
    return data.get('algorithm_id') or None


@router.post('/api/chat/stream', summary='Proxy: streaming chat (router mode)')
async def proxy_chat_stream(request: Request):
    caller_algo_id = await _parse_algo_id(request)
    return await _select_and_forward(request, caller_algo_id)


@router.post('/api/chat/tools', summary='Proxy: list chat tools (router mode)')
async def proxy_chat_tools(request: Request):
    caller_algo_id = await _parse_algo_id(request)
    return await _select_and_forward(request, caller_algo_id)


@router.post('/api/chat/sensitive-check', summary='Proxy: sensitive-word check (router mode)')
async def proxy_sensitive_check(request: Request):
    caller_algo_id = await _parse_algo_id(request)
    return await _select_and_forward(request, caller_algo_id)


@router.post('/api/chat/context-usage', summary='Proxy: estimate chat context usage (router mode)')
async def proxy_chat_context_usage(request: Request):
    caller_algo_id = await _parse_algo_id(request)
    return await _select_and_forward(request, caller_algo_id)


@router.post('/api/chat/context-prompt', summary='Proxy: export chat context (router mode)')
async def proxy_chat_context_prompt(request: Request):
    caller_algo_id = await _parse_algo_id(request)
    return await _select_and_forward(request, caller_algo_id)


@router.post('/internal/knowledge:search', summary='Proxy: pure knowledge search (router mode)')
async def proxy_knowledge_search(
    request: Request,
    x_lazymind_internal_token: Optional[str] = Header(default=None, alias=INTERNAL_TOKEN_HEADER),
):
    require_internal_token(x_lazymind_internal_token)
    caller_algo_id = await _parse_algo_id(request)
    return await _select_and_forward_internal_json(request, caller_algo_id)


@router.post('/api/subagent/run', summary='Proxy: SubAgent execution (router mode)')
async def proxy_subagent_run(request: Request):
    # SubAgent requests carry no algorithm_id; let the AB router resolve the default.
    caller_algo_id = await _parse_algo_id(request)
    return await _select_and_forward(request, caller_algo_id)


async def _select_and_forward(request: Request, caller_algo_id: Optional[str]):
    algorithm_id, instance = await _select_instance(caller_algo_id)

    proxy = get_stream_proxy()
    return await proxy.forward(
        request,
        instance.url,
        algorithm_id=algorithm_id,
        instance_host=instance.host,
    )


async def _select_and_forward_internal_json(request: Request, caller_algo_id: Optional[str]):
    algorithm_id, instance = await _select_instance(caller_algo_id)
    target_url = instance.url.rstrip('/') + request.url.path
    if request.url.query:
        target_url += '?' + request.url.query

    headers = {
        k: v
        for k, v in request.headers.items()
        if k.lower() not in (
            'host',
            'content-length',
            'transfer-encoding',
            'connection',
            INTERNAL_TOKEN_HEADER.lower(),
        )
    }
    headers[INTERNAL_TOKEN_HEADER] = expected_internal_token()
    body = await request.body()
    async with httpx.AsyncClient(timeout=30.0, trust_env=False) as client:
        upstream = await client.request(request.method, target_url, headers=headers, content=body)

    response_headers = {
        k: v
        for k, v in upstream.headers.items()
        if k.lower() not in ('content-length', 'transfer-encoding', 'connection')
    }
    if algorithm_id:
        response_headers['X-Algorithm-Id'] = algorithm_id
    if instance.host:
        response_headers['X-Instance-Host'] = instance.host
    return Response(
        content=upstream.content,
        status_code=upstream.status_code,
        headers=response_headers,
        media_type=upstream.headers.get('content-type', 'application/json'),
    )


async def _select_instance(caller_algo_id: Optional[str]):
    ab_router = get_ab_router()
    algorithm_id = await ab_router.select_algorithm(caller_algo_id)

    registry = get_global_registry()
    instance = registry.get_healthy_instance(algorithm_id)

    if instance is None:
        if caller_algo_id:
            raise HTTPException(
                status_code=503,
                detail=f'No healthy instance available for algorithm "{algorithm_id}"',
            )
        fallback_id = 'default'
        if algorithm_id != fallback_id:
            instance = registry.get_healthy_instance(fallback_id)
            if instance is not None:
                algorithm_id = fallback_id
        if instance is None:
            raise HTTPException(
                status_code=503,
                detail=f'No healthy instance available for algorithm "{algorithm_id}"',
            )
    return algorithm_id, instance
