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

    ab_router = get_ab_router()
    algorithm_id = await ab_router.select_algorithm(caller_algo_id)

    registry = get_global_registry()
    proxy = get_stream_proxy()

    # Try up to 2 candidate instances; on a connect error evict the bad entry and retry.
    tried: set[str] = set()
    for _attempt in range(2):
        instance = registry.get_healthy_instance(algorithm_id, exclude=tried)
        if instance is None and algorithm_id != 'default':
            instance = registry.get_healthy_instance('default', exclude=tried)
        if instance is None:
            raise HTTPException(
                status_code=503,
                detail=f'No healthy instance available for algorithm "{algorithm_id}"',
            )
        try:
            return await proxy.forward(
                request,
                instance.url,
                algorithm_id=algorithm_id,
                instance_host=instance.host,
            )
        except HTTPException as exc:
            if exc.status_code != 503:
                raise
            # Instance unreachable — evict from in-memory cache and try next.
            logger.warning(
                'Instance %s unreachable, evicting and retrying (attempt %d)',
                instance.url, _attempt + 1,
            )
            registry.evict_instance(instance.instance_id)
            tried.add(instance.instance_id)

    raise HTTPException(status_code=503, detail='All upstream instances unreachable')


def _get_default_instance():
    """Return the default algorithm instance, raising 503 if none available."""
    registry = get_global_registry()
    instance = registry.get_healthy_instance('default')
    if instance is None:
        raise HTTPException(status_code=503, detail='No healthy algorithm instance available')
    return instance


@router.post('/api/plugin/step', summary='Proxy: plugin step execution (router mode)')
async def proxy_plugin_step(request: Request):
    instance = _get_default_instance()
    proxy = get_stream_proxy()
    return await proxy.forward(request, instance.url, instance_host=instance.host)


@router.post('/api/plugin/driver', summary='Proxy: plugin driver judgment (router mode)')
async def proxy_plugin_driver(request: Request):
    instance = _get_default_instance()
    proxy = get_stream_proxy()
    return await proxy.forward(request, instance.url, instance_host=instance.host)
