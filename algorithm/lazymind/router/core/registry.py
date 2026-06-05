from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass
from typing import Literal, Optional

from sqlalchemy import select

from lazymind.config import config
import lazymind.router.config  # noqa: F401 — registers router config keys
from lazymind.router.db.client import AsyncSessionLocal
from lazymind.router.db.models import RouterChildProcess

logger = logging.getLogger(__name__)


@dataclass
class ChildProcessInfo:
    instance_id: str
    algorithm_id: str
    host: str
    port: int
    status: Literal['starting', 'healthy', 'unhealthy', 'stopped']
    failures: int = 0

    @property
    def url(self) -> str:
        return f'http://{self.host}:{self.port}'


class GlobalRegistry:
    """In-memory cache of all healthy child processes across all router instances.

    Periodically refreshed from `router_child_processes` table so every router
    instance has a global view for cross-instance routing.
    """

    def __init__(self) -> None:
        # algo_id -> list of ChildProcessInfo (only healthy ones)
        self._global_instances: dict[str, list[ChildProcessInfo]] = {}
        # algo_id -> round-robin cursor
        self._rr_cursors: dict[str, int] = {}
        self._lock = asyncio.Lock()
        self._refresh_interval = config['router_registry_refresh_interval']

    # ------------------------------------------------------------------
    # Refresh
    # ------------------------------------------------------------------

    async def refresh(self) -> None:
        """Reload healthy child processes from DB into local cache."""
        async with AsyncSessionLocal() as session:
            rows = await session.execute(
                select(RouterChildProcess).where(
                    RouterChildProcess.status == 'healthy'
                )
            )
            children = rows.scalars().all()

        new_map: dict[str, list[ChildProcessInfo]] = {}
        for child in children:
            info = ChildProcessInfo(
                instance_id=child.instance_id,
                algorithm_id=child.algorithm_id,
                host=child.host,
                port=child.port,
                status=child.status,  # type: ignore[arg-type]
                failures=child.failures,
            )
            new_map.setdefault(child.algorithm_id, []).append(info)

        async with self._lock:
            self._global_instances = new_map

        logger.debug(
            'GlobalRegistry refreshed: %d algorithms, %d total instances',
            len(new_map),
            sum(len(v) for v in new_map.values()),
        )

    # ------------------------------------------------------------------
    # Query
    # ------------------------------------------------------------------

    def get_healthy_instance(self, algorithm_id: str) -> Optional[ChildProcessInfo]:
        """Return the next healthy instance for `algorithm_id` using round-robin."""
        instances = self._global_instances.get(algorithm_id, [])
        healthy = [i for i in instances if i.status == 'healthy']
        if not healthy:
            return None
        cursor = self._rr_cursors.get(algorithm_id, 0)
        chosen = healthy[cursor % len(healthy)]
        self._rr_cursors[algorithm_id] = (cursor + 1) % len(healthy)
        return chosen

    def get_all_instances(self, algorithm_id: str) -> list[ChildProcessInfo]:
        return list(self._global_instances.get(algorithm_id, []))

    def get_all_algorithms(self) -> list[str]:
        return list(self._global_instances.keys())

    def evict_instance(self, host: str, port: int) -> None:
        """Immediately remove a specific (host, port) from the in-memory cache.

        Called by HealthChecker as soon as a child process is found unhealthy,
        so no traffic is sent to it while it is being restarted.
        """
        changed = False
        for algo_id, instances in list(self._global_instances.items()):
            filtered = [i for i in instances if not (i.host == host and i.port == port)]
            if len(filtered) != len(instances):
                self._global_instances[algo_id] = filtered
                changed = True
                logger.info('Evicted unhealthy instance %s:%d from registry', host, port)
        if changed:
            # Reset round-robin cursors to avoid index-out-of-range on smaller lists
            self._rr_cursors.clear()

    def snapshot(self) -> dict[str, list[ChildProcessInfo]]:
        return {k: list(v) for k, v in self._global_instances.items()}

    # ------------------------------------------------------------------
    # Background loop (used by HealthChecker)
    # ------------------------------------------------------------------

    async def run_refresh_loop(self) -> None:
        while True:
            try:
                await self.refresh()
            except Exception as exc:
                logger.warning('GlobalRegistry.refresh() failed: %s', exc)
            await asyncio.sleep(self._refresh_interval)


# Module-level singleton
_global_registry: Optional[GlobalRegistry] = None


def get_global_registry() -> GlobalRegistry:
    global _global_registry
    if _global_registry is None:
        _global_registry = GlobalRegistry()
    return _global_registry
