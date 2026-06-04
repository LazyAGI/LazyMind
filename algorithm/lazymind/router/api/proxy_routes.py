from __future__ import annotations

import json
import logging
from typing import Optional

from fastapi import APIRouter, HTTPException, Request

from lazymind.router.core.ab_router import get_ab_router
from lazymind.router.core.registry import get_global_registry
from lazymind.router.core.stream_proxy import get_stream_proxy

logger = logging.getLogger(__name__)

router = APIRouter()


async def _parse_session_and_algo(request: Request) -> tuple[str, Optional[str]]:
    """Extract session_id and optional algorithm_id from the JSON body without consuming it."""
    try:
        body_bytes = await request.body()
        data = json.loads(body_bytes) if body_bytes else {}
    except Exception:
        data = {}
    session_id = data.get('session_id', 'default_session') or 'default_session'
    algo_id = data.get('algorithm_id') or None
    return session_id, algo_id


@router.post('/api/chat/stream', summary='Proxy: streaming chat (router mode)')
async def proxy_chat_stream(request: Request):
    session_id, caller_algo_id = await _parse_session_and_algo(request)

    ab_router = get_ab_router()
    algorithm_id = await ab_router.select_algorithm(session_id, caller_algo_id)

    registry = get_global_registry()
    instance = registry.get_healthy_instance(algorithm_id)

    if instance is None:
        # Try fallback to 'default'
        if algorithm_id != 'default':
            instance = registry.get_healthy_instance('default')
        if instance is None:
            raise HTTPException(
                status_code=503,
                detail=f'No healthy instance available for algorithm "{algorithm_id}"',
            )

    proxy = get_stream_proxy()
    return await proxy.forward(
        request,
        instance.url,
        algorithm_id=algorithm_id,
        instance_host=instance.host,
    )


@router.get('/api/chat/tools', summary='Proxy: list available tools (router mode)')
async def proxy_chat_tools(request: Request):
    registry = get_global_registry()
    # Try each known algorithm until one responds
    for algo_id in registry.get_all_algorithms():
        instance = registry.get_healthy_instance(algo_id)
        if instance:
            proxy = get_stream_proxy()
            return await proxy.forward(request, instance.url, algorithm_id=algo_id)

    # Fallback to 'default' if no algorithm known yet
    instance = registry.get_healthy_instance('default')
    if instance is None:
        raise HTTPException(status_code=503, detail='No healthy instance available')
    proxy = get_stream_proxy()
    return await proxy.forward(request, instance.url, algorithm_id='default')
