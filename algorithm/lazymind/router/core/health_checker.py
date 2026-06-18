from __future__ import annotations

import asyncio
import logging
from datetime import datetime, timedelta, timezone
from typing import TYPE_CHECKING, Optional

import httpx
from sqlalchemy import delete, select

from lazymind.config import config
import lazymind.router.config  # noqa: F401 — registers router config keys
from lazymind.router.config import resolve_host
from lazymind.router.db.client import AsyncSessionLocal, HeartbeatSessionLocal
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
        """Run all background loops, restarting any that crash unexpectedly."""
        loop_fns = [
            ('health-probe', self._health_loop),
            ('heartbeat', self._heartbeat_loop),
            ('registry-refresh', self._registry_refresh_loop),
            ('cleanup-dead', self._cleanup_dead_instances_loop),
        ]
        tasks: dict[str, asyncio.Task] = {
            name: asyncio.create_task(fn(), name=name)
            for name, fn in loop_fns
        }
        fn_map = dict(loop_fns)
        try:
            while True:
                done, _ = await asyncio.wait(
                    tasks.values(), return_when=asyncio.FIRST_COMPLETED
                )
                for task in done:
                    name = task.get_name()
                    exc = task.exception() if not task.cancelled() else None
                    if exc is not None:
                        logger.error(
                            'Background loop "%s" crashed: %s — restarting in 5s',
                            name, exc, exc_info=exc,
                        )
                        await asyncio.sleep(5)
                        tasks[name] = asyncio.create_task(fn_map[name](), name=name)
                    else:
                        # Normal exit or cancellation means we should stop entirely
                        raise asyncio.CancelledError
        except asyncio.CancelledError:
            for t in tasks.values():
                t.cancel()
            await asyncio.gather(*tasks.values(), return_exceptions=True)
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

        # Recover silently-dead child processes that have no DB record.
        # This can happen when _cleanup_dead_instances removes our own records
        # due to a transient heartbeat gap. We check the in-memory _procs dict
        # directly, which is authoritative for this process.
        await self._recover_missing_children()

    async def _recover_missing_children(self) -> None:
        """Re-register or respawn local child processes not tracked in DB.

        When _cleanup_dead_instances removes this instance's records (due to a
        transient heartbeat gap), _probe_all can no longer see the children via
        DB query. We cross-check against the in-memory _procs dict and either
        re-register the still-running process or spawn a replacement.
        """
        local_procs: dict[int, object] = self._pm._procs  # port -> Popen
        if not local_procs:
            return

        # Find which ports have no live DB record for this instance
        async with AsyncSessionLocal() as session:
            rows = await session.execute(
                select(RouterChildProcess.port).where(
                    RouterChildProcess.instance_id == self._pm.instance_id,
                )
            )
            tracked_ports = {r.port for r in rows}

        missing_ports = [p for p in local_procs if p not in tracked_ports]
        if not missing_ports:
            return

        for port in missing_ports:
            proc = local_procs[port]
            if proc.poll() is None:
                # Process is still alive; its DB record was swept away.
                # Re-register so normal health probing resumes.
                algo_id = self._pm._port_algo.get((self._pm.host, port), 'default')
                logger.warning(
                    'Child on port %d is alive but has no DB record; re-registering (algo=%s)',
                    port, algo_id,
                )
                try:
                    await self._pm.ensure_instance_registered()
                    from lazymind.router.core.process_manager import _upsert_child_process
                    stmt = _upsert_child_process(
                        instance_id=self._pm.instance_id,
                        algo_id=algo_id,
                        host=self._pm.host,
                        port=port,
                        pid=proc.pid,
                    )
                    async with AsyncSessionLocal() as session:
                        await session.execute(stmt)
                        await session.commit()
                except Exception as exc:
                    logger.error('Failed to re-register child on port %d: %s', port, exc)
            else:
                # Process has exited; trigger restart if not already scheduled.
                algo_id = self._pm._port_algo.get((self._pm.host, port), 'default')
                logger.warning(
                    'Child on port %d (algo=%s) has exited (rc=%s); scheduling restart',
                    port, algo_id, proc.returncode,
                )
                if port not in self._restart_tasks or self._restart_tasks[port].done():
                    self._restart_tasks[port] = asyncio.create_task(
                        self._deferred_restart(port, _BACKOFF_SCHEDULE[0])
                    )

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

            # Evict from registry immediately on first failure so no traffic is
            # sent to a potentially-dead instance while we wait for the threshold.
            self._registry.evict_instance(resolve_host(), port)

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
                await self._update_child_status(port, None, failures=count)

    async def _deferred_restart(self, port: int, delay: float) -> None:
        logger.info('Scheduling restart for port %d in %.0fs', port, delay)
        await asyncio.sleep(delay)
        try:
            await self._pm.restart_instance(resolve_host(), port)
            self._failure_counts[port] = 0
            logger.info('Restarted child process on port %d', port)
            # Immediately refresh registry so the recovered instance gets traffic again
            await self._registry.refresh()
        except Exception as exc:
            logger.error('Failed to restart child process on port %d: %s', port, exc)

    async def _update_child_status(
        self, port: int, status: Optional[str], failures: int
    ) -> None:
        now = datetime.now(timezone.utc)
        values: dict = {
            'failures': failures,
            'last_health_at': now,
            'updated_at': now,
        }
        if status is not None:
            values['status'] = status
        async with AsyncSessionLocal() as session:
            await session.execute(
                RouterChildProcess.__table__.update()
                .where(
                    RouterChildProcess.host == resolve_host(),
                    RouterChildProcess.port == port,
                    RouterChildProcess.instance_id == self._pm.instance_id,
                )
                .values(**values)
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
        await self._pm.ensure_instance_registered()
        now = datetime.now(timezone.utc)
        async with HeartbeatSessionLocal() as session:
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
        """Delete child_process records and instance records for stale instances.

        This process's own instance_id is excluded: the heartbeat loop is
        responsible for keeping it alive, and removing our own record would
        make _probe_all unable to find our child processes.
        """
        timeout_secs = config['router_instance_timeout']
        cutoff = datetime.now(timezone.utc) - timedelta(seconds=timeout_secs)
        own_id = self._pm.instance_id
        async with HeartbeatSessionLocal() as session:
            dead = await session.execute(
                select(RouterInstance.instance_id).where(
                    RouterInstance.last_heartbeat < cutoff,
                    RouterInstance.instance_id != own_id,
                )
            )
            dead_ids = [r.instance_id for r in dead]

        if not dead_ids:
            return

        logger.info('Cleaning up %d dead router instance(s): %s', len(dead_ids), dead_ids)
        async with HeartbeatSessionLocal() as session:
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
