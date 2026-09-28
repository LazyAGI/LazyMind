from __future__ import annotations

import asyncio

import httpx

from lazymind.config import config
from lazymind.router.core.registry import get_global_registry

_INPUT_TIMEOUT = 8.0  # Leave room for the response within Core's ten-second deadline.
_PROBE_CONCURRENCY = 8


async def submit_worker_session_env(
    ask_id: str, conversation_id: str, value: str, *, cancel: bool = False,
) -> str | None:
    async with asyncio.timeout(_INPUT_TIMEOUT):
        workers = await get_global_registry().list_active_instances()
        headers = {'X-LazyMind-Internal-Token': str(config['core_internal_token'] or '')}
        async with httpx.AsyncClient(timeout=3.0, trust_env=False, headers=headers) as client:
            limit = asyncio.Semaphore(_PROBE_CONCURRENCY)

            async def probe_owner(url: str) -> str | None:
                async with limit:
                    try:
                        probe = await client.get(f'{url}/api/chat/session-env:pending', params={
                            'ask_id': ask_id, 'conversation_id': conversation_id,
                        })
                        probe.raise_for_status()
                        body = probe.json()
                        return url if isinstance(body, dict) and body.get('ok') is True else None
                    except (httpx.HTTPError, ValueError):
                        return None

            # Probe only metadata concurrently; cancel discovery before writing to one owner.
            probes = [asyncio.create_task(probe_owner(url)) for url in sorted({worker.url for worker in workers})]
            owner = None
            try:
                for probe in asyncio.as_completed(probes):
                    owner = await probe
                    if owner is not None:
                        break
            finally:
                for probe in probes:
                    probe.cancel()
                await asyncio.gather(*probes, return_exceptions=True)
            if owner is None:
                return None
            response = await client.post(f'{owner}/api/chat/session-env:input', json={
                'ask_id': ask_id, 'conversation_id': conversation_id, 'cancel': cancel,
                **({'value': value} if not cancel else {}),
            })
            response.raise_for_status()
            result = response.json()
            status = result.get('status')
            return status if result.get('ok') is True and status in {'configured', 'canceled'} else None


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
