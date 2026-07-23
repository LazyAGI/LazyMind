import asyncio
import logging
import os
from contextlib import suppress

from services.cloud_oauth_service import cloud_oauth_service


logger = logging.getLogger('auth-service.cloud-oauth-health')

_task: asyncio.Task | None = None


def _env_bool(name: str, default: bool) -> bool:
    raw = (os.getenv(name) or '').strip().lower()
    if not raw:
        return default
    return raw in {'1', 'true', 'yes', 'on'}


def _env_int(name: str, default: int, minimum: int) -> int:
    raw = (os.getenv(name) or '').strip()
    if not raw:
        return default
    try:
        value = int(raw)
    except ValueError:
        return default
    return max(minimum, value)


def is_enabled() -> bool:
    return _env_bool('LAZYMIND_CLOUD_AUTH_HEALTH_CHECK_ENABLED', True)


def interval_seconds() -> int:
    return _env_int('LAZYMIND_CLOUD_AUTH_HEALTH_CHECK_INTERVAL_SECONDS', 1200, 60)


def batch_size() -> int:
    return _env_int('LAZYMIND_CLOUD_AUTH_HEALTH_CHECK_BATCH_SIZE', 100, 1)


def retry_interval_seconds() -> int:
    return _env_int('LAZYMIND_CLOUD_AUTH_HEALTH_RETRY_INTERVAL_SECONDS', 60, 10)


def retry_max_interval_seconds() -> int:
    return _env_int(
        'LAZYMIND_CLOUD_AUTH_HEALTH_RETRY_MAX_INTERVAL_SECONDS',
        300,
        retry_interval_seconds(),
    )


async def _run_loop() -> None:
    retry_delay = retry_interval_seconds()
    while True:
        delay = interval_seconds()
        try:
            result = await asyncio.to_thread(
                cloud_oauth_service.run_health_check_once,
                provider='feishu',
                batch_size=batch_size(),
            )
            logger.info('cloud auth health check completed: %s', result)
            if result.get('retryable_errors'):
                delay = retry_delay
                retry_delay = min(retry_delay * 2, retry_max_interval_seconds())
            else:
                retry_delay = retry_interval_seconds()
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            logger.exception('cloud auth health check failed: %s', exc)
        await asyncio.sleep(delay)


def start() -> None:
    global _task
    if not is_enabled() or _task is not None:
        return
    try:
        loop = asyncio.get_running_loop()
    except RuntimeError:
        return
    _task = loop.create_task(_run_loop())


async def stop() -> None:
    global _task
    if _task is None:
        return
    _task.cancel()
    with suppress(asyncio.CancelledError):
        await _task
    _task = None
