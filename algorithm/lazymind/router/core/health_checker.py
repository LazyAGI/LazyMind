from __future__ import annotations

import asyncio
import logging
from datetime import datetime, timedelta, timezone
from typing import TYPE_CHECKING

import httpx
from sqlalchemy import delete, select

from lazymind.config import config
import lazymind.router.config  # noqa: F401 — registers router config keys
from lazymind.router.config import resolve_host
from lazymind.router.db.client import AsyncSessionLocal
from lazymind.router.db.models import RouterChildProcess, RouterInstance

if TYPE_CHECKING:
    from lazymind.router.core.process_manager import ProcessManager
    from lazymind.router.core.registry import GlobalRegistry

logger = logging.getLogger(__name__)

# Backoff schedule in seconds for restarting a failed child process
_BACKOFF_SCHEDULE = [1, 2, 4, 8, 16, 32, 60]


class HealthChecker:
    """Manages periodic health probing, heartbeats, and global registry refresh.

    Responsibilities:
    1. Probe child processes owned by this instance every `router_health_interval` seconds.
       On N consecutive failures: mark unhealthy → trigger restart with backoff.
    2. Update this instance's heartbeat in `router_instances` every `router_heartbeat_interval` s.
    3. Trigger GlobalRegistry.refresh() every `router_registry_refresh_interval` s.
    4. Clean up dead instance records (heartbeat timeout > `router_instance_timeout`) every cycle.
    """

    def __init__(self, process_manager: ProcessManager, registry: GlobalRegistry) -> None:
        self._pm = process_manager
        self._registry = registry
        # port -> consecutive failure count
        self._failure_counts: dict[int, int] = {}
        # port -> asyncio.Task for pending restart
        self._restart_tasks: dict[int, asyncio.Task] = {}

    # ------------------------------------------------------------------
    # Main loop
    # ------------------------------------------------------------------

    async def run_forever(self) -> None:
        tasks = [
            asyncio.create_task(self._health_loop(), name='health-probe'),
            asyncio.create_task(self._heartbeat_loop(), name='heartbeat'),
            asyncio.create_task(self._registry_refresh_loop(), name='registry-refresh'),
            asyncio.create_task(self._cleanup_dead_instances_loop(), name='cleanup-dead'),
        ]
        try:
            await asyncio.gather(*tasks)
        except asyncio.CancelledError:
            for t in tasks:
                t.cancel()
            raise

    # ------------------------------------------------------------------
    # Health probing
    # ------------------------------------------------------------------

    async def _health_loop(self) -> None:
        while True:
            await self._probe_all()
            await asyncio.sleep(config['router_health_interval'])

    async def _probe_all(self) -> None:
        async with AsyncSessionLocal() as session:
            rows = await session.execute(
                select(RouterChildProcess).where(
                    RouterChildProcess.instance_id == self._pm.instance_id,
                    RouterChildProcess.status.in_(['starting', 'healthy', 'unhealthy']),
                )
            )
            children = rows.scalars().all()

        probe_tasks = [self._probe_child(child.port) for child in children]
        if probe_tasks:
            await asyncio.gather(*probe_tasks, return_exceptions=True)

    async def _probe_child(self, port: int) -> None:
        url = f'http://127.0.0.1:{port}/health'
        healthy = False
        try:
            async with httpx.AsyncClient(timeout=3.0) as client:
                resp = await client.get(url)
            healthy = resp.status_code < 500
        except Exception:
            healthy = False

        if healthy:
            self._failure_counts[port] = 0
            await self._update_child_status(port, 'healthy', failures=0)
        else:
            count = self._failure_counts.get(port, 0) + 1
            self._failure_counts[port] = count
            logger.warning('Child on port %d failed health check (%d/%d)', port,
                           count, config['router_health_max_failures'])

            if count >= config['router_health_max_failures']:
                await self._update_child_status(port, 'unhealthy', failures=count)
                # Trigger restart if not already pending
                if port not in self._restart_tasks or self._restart_tasks[port].done():
                    backoff_idx = min(count - config['router_health_max_failures'], len(_BACKOFF_SCHEDULE) - 1)
                    delay = _BACKOFF_SCHEDULE[backoff_idx]
                    self._restart_tasks[port] = asyncio.create_task(
                        self._deferred_restart(port, delay)
                    )
            else:
                await self._update_child_status(port, 'unhealthy', failures=count)

    async def _deferred_restart(self, port: int, delay: float) -> None:
        logger.info('Scheduling restart for port %d in %.0fs', port, delay)
        await asyncio.sleep(delay)
        try:
            await self._pm.restart_instance(resolve_host(), port)
            self._failure_counts[port] = 0
            logger.info('Restarted child process on port %d', port)
        except Exception as exc:
            logger.error('Failed to restart child process on port %d: %s', port, exc)

    async def _update_child_status(
        self, port: int, status: str, failures: int
    ) -> None:
        now = datetime.now(timezone.utc)
        async with AsyncSessionLocal() as session:
            await session.execute(
                RouterChildProcess.__table__.update()
                .where(
                    RouterChildProcess.host == resolve_host(),
                    RouterChildProcess.port == port,
                    RouterChildProcess.instance_id == self._pm.instance_id,
                )
                .values(
                    status=status,
                    failures=failures,
                    last_health_at=now,
                    updated_at=now,
                )
            )
            await session.commit()

    # ------------------------------------------------------------------
    # Heartbeat
    # ------------------------------------------------------------------

    async def _heartbeat_loop(self) -> None:
        while True:
            await asyncio.sleep(config['router_heartbeat_interval'])
            try:
                await self._update_heartbeat()
            except Exception as exc:
                logger.warning('Heartbeat update failed: %s', exc)

    async def _update_heartbeat(self) -> None:
        now = datetime.now(timezone.utc)
        async with AsyncSessionLocal() as session:
            await session.execute(
                RouterInstance.__table__.update()
                .where(RouterInstance.instance_id == self._pm.instance_id)
                .values(last_heartbeat=now)
            )
            await session.commit()

    # ------------------------------------------------------------------
    # Global registry refresh
    # ------------------------------------------------------------------

    async def _registry_refresh_loop(self) -> None:
        while True:
            await asyncio.sleep(config['router_registry_refresh_interval'])
            try:
                await self._registry.refresh()
            except Exception as exc:
                logger.warning('Registry refresh failed: %s', exc)

    # ------------------------------------------------------------------
    # Dead instance cleanup
    # ------------------------------------------------------------------

    async def _cleanup_dead_instances_loop(self) -> None:
        while True:
            await asyncio.sleep(config['router_heartbeat_interval'] * 2)
            try:
                await self._cleanup_dead_instances()
            except Exception as exc:
                logger.warning('Dead instance cleanup failed: %s', exc)

    async def _cleanup_dead_instances(self) -> None:
        """Delete child_process records and instance records for stale instances."""
        timeout_secs = config['router_instance_timeout']
        cutoff = datetime.now(timezone.utc) - timedelta(seconds=timeout_secs)
        async with AsyncSessionLocal() as session:
            dead = await session.execute(
                select(RouterInstance.instance_id).where(
                    RouterInstance.last_heartbeat < cutoff
                )
            )
            dead_ids = [r.instance_id for r in dead]

        if not dead_ids:
            return

        logger.info('Cleaning up %d dead router instance(s): %s', len(dead_ids), dead_ids)
        async with AsyncSessionLocal() as session:
            await session.execute(
                delete(RouterChildProcess).where(
                    RouterChildProcess.instance_id.in_(dead_ids)
                )
            )
            await session.execute(
                delete(RouterInstance).where(
                    RouterInstance.instance_id.in_(dead_ids)
                )
            )
            await session.commit()
