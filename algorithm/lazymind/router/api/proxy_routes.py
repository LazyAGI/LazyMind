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


@router.post('/api/subagent/run', summary='Proxy: SubAgent execution (router mode)')
async def proxy_subagent_run(request: Request):
    # SubAgent requests carry no algorithm_id; let the AB router resolve the default.
    caller_algo_id = await _parse_algo_id(request)
    return await _select_and_forward(request, caller_algo_id)


async def _select_and_forward(request: Request, caller_algo_id: Optional[str]):
    ab_router = get_ab_router()
    algorithm_id = await ab_router.select_algorithm(caller_algo_id)

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
