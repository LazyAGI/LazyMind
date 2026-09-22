from __future__ import annotations

import asyncio

import httpx

from lazymind.router.core.registry import get_global_registry


async def clear_worker_session_env(conversation_ids: list[str]) -> list[str]:
    workers = await get_global_registry().list_active_instances()
    urls = sorted({worker.url for worker in workers})
    async with httpx.AsyncClient(timeout=3.0, trust_env=False) as client:
        async def clear(url: str) -> list[str]:
            response = await client.post(
                f'{url}/api/chat/session-env:clear',
                json={'conversation_ids': conversation_ids},
            )
            response.raise_for_status()
            body = response.json()
            if body.get('ok') is not True:
                raise RuntimeError('worker did not confirm session env cleanup')
            return body.get('cleared', [])

        results = await asyncio.gather(*(clear(url) for url in urls), return_exceptions=True)
    if any(isinstance(result, BaseException) for result in results):
        raise RuntimeError('session env cleanup failed on one or more workers')
    return sorted({name for result in results for name in result})
