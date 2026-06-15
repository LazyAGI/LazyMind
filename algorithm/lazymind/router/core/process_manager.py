from __future__ import annotations

import asyncio
import logging
import os
import subprocess
import sys
import time
import uuid
from typing import Optional

import httpx
from sqlalchemy import delete, select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.dialects.sqlite import insert as sqlite_insert

from lazymind.config import config
import lazymind.router.config  # noqa: F401 — registers router config keys
from lazymind.router.config import resolve_host
from lazymind.router.db.client import AsyncSessionLocal
from lazymind.router.db.models import (
    RouterAlgorithm,
    RouterChildProcess,
    RouterInstance,
)
from lazymind.router.db.client import get_engine

logger = logging.getLogger(__name__)


def _is_sqlite_engine() -> bool:
    """Return True if the current DB engine is SQLite."""
    return get_engine().dialect.name == 'sqlite'


def _upsert_instance(instance_id: str, host: str, pid: int,
                     port_range_start: int, port_range_end: int):
    """Build a dialect-appropriate INSERT ... ON CONFLICT DO NOTHING for RouterInstance."""
    values = dict(instance_id=instance_id, host=host, pid=pid,
                  port_range_start=port_range_start, port_range_end=port_range_end)
    if _is_sqlite_engine():
        return sqlite_insert(RouterInstance).values(**values).on_conflict_do_nothing(
            index_elements=['instance_id']
        )
    return pg_insert(RouterInstance).values(**values).on_conflict_do_nothing(
        index_elements=['instance_id']
    )


def _upsert_child_process(instance_id: str, algo_id: str, host: str, port: int, pid: int):
    """Build a dialect-appropriate UPSERT for RouterChildProcess."""
    values = dict(instance_id=instance_id, algorithm_id=algo_id,
                  host=host, port=port, pid=pid, status='starting', failures=0)
    set_vals = {'pid': pid, 'status': 'starting', 'failures': 0}
    if _is_sqlite_engine():
        return sqlite_insert(RouterChildProcess).values(**values).on_conflict_do_update(
            index_elements=['host', 'port'],
            set_=set_vals,
        )
    return pg_insert(RouterChildProcess).values(**values).on_conflict_do_update(
        constraint='uq_router_child_processes_host_port',
        set_=set_vals,
    )


class ProcessManager:
    """Manages child process lifecycle for the local router instance only."""

    def __init__(self) -> None:
        self._instance_id: str = str(uuid.uuid4())
        self._host: str = resolve_host()
        self._port_range: tuple[int, int] = (0, -1)
        self._next_port: int = 0
        # pid -> subprocess.Popen
        self._procs: dict[int, subprocess.Popen] = {}
        # (host, port) -> algo_id, for local awareness
        self._port_algo: dict[tuple[str, int], str] = {}

    @property
    def instance_id(self) -> str:
        return self._instance_id

    @property
    def host(self) -> str:
        return self._host

    @property
    def port_range(self) -> tuple[int, int]:
        return self._port_range

    # ------------------------------------------------------------------
    # Startup
    # ------------------------------------------------------------------

    async def claim_port_range(self) -> tuple[int, int]:
        """Atomically claim an unused port segment and register this instance."""
        pool_start = config['router_port_pool_start']
        pool_end = config['router_port_pool_end']
        stride = config['router_ports_per_instance']

        # Clean up stale instances for this host on startup (crash recovery)
        async with AsyncSessionLocal() as session:
            stale_instances = await session.execute(
                select(RouterInstance.instance_id).where(
                    RouterInstance.host == self._host
                )
            )
            stale_ids = [r.instance_id for r in stale_instances]
            if stale_ids:
                await session.execute(
                    delete(RouterChildProcess).where(
                        RouterChildProcess.instance_id.in_(stale_ids)
                    )
                )
                await session.execute(
                    delete(RouterInstance).where(
                        RouterInstance.instance_id.in_(stale_ids)
                    )
                )
                logger.info(
                    'Cleaned up %d stale instance(s) for host %s on startup',
                    len(stale_ids), self._host,
                )
            await session.commit()

        for attempt in range(5):
            # Find all occupied ranges
            async with AsyncSessionLocal() as session:
                rows = await session.execute(
                    select(RouterInstance.port_range_start, RouterInstance.port_range_end)
                )
                occupied: list[tuple[int, int]] = [(r.port_range_start, r.port_range_end) for r in rows]

            occupied_starts = {s for s, _ in occupied}
            claimed_start = -1
            candidate = pool_start
            while candidate + stride - 1 <= pool_end:
                if candidate not in occupied_starts:
                    claimed_start = candidate
                    break
                candidate += stride

            if claimed_start < 0:
                raise RuntimeError(
                    f'No free port range available in [{pool_start}, {pool_end}] '
                    f'with stride {stride}'
                )

            claimed_end = claimed_start + stride - 1

            try:
                async with AsyncSessionLocal() as session:
                    stmt = _upsert_instance(
                        instance_id=self._instance_id,
                        host=self._host,
                        pid=os.getpid(),
                        port_range_start=claimed_start,
                        port_range_end=claimed_end,
                    )
                    await session.execute(stmt)
                    await session.commit()

                self._port_range = (claimed_start, claimed_end)
                self._next_port = claimed_start
                logger.info(
                    'Router instance %s claimed port range [%d, %d]',
                    self._instance_id,
                    claimed_start,
                    claimed_end,
                )
                return self._port_range
            except Exception as exc:
                logger.warning(
                    'Failed to claim port range %d-%d (attempt %d/5): %s',
                    claimed_start, claimed_end, attempt + 1, exc,
                )
                await asyncio.sleep(0.5 * (attempt + 1))

        raise RuntimeError('Failed to claim a unique port range after 5 attempts')

    def _allocate_port(self) -> int:
        start, end = self._port_range
        if self._next_port > end:
            raise RuntimeError(
                f'Port range [{start}, {end}] exhausted for instance {self._instance_id}'
            )
        port = self._next_port
        self._next_port += 1
        return port

    # ------------------------------------------------------------------
    # Default algorithm bootstrap
    # ------------------------------------------------------------------

    async def ensure_default_algorithm(self) -> None:
        """Register the default algorithm (id=default) if it doesn't exist yet."""
        async with AsyncSessionLocal() as session:
            row = await session.get(RouterAlgorithm, 'default')
            if row is None:
                algo = RouterAlgorithm(
                    id='default',
                    name='default',
                    code_path=config['router_default_algo_path'],
                    config={},
                    status='active',
                )
                session.add(algo)
                await session.commit()
                logger.info('Registered default algorithm at %s', config['router_default_algo_path'])
            elif row.status != 'active':
                row.status = 'active'
                await session.commit()

    # ------------------------------------------------------------------
    # Start / stop algorithms
    # ------------------------------------------------------------------

    async def start_algorithm(
        self,
        algo_id: str,
        code_path: str,
        count: int,
        extra_env: Optional[dict] = None,
    ) -> list[int]:
        """Start `count` child processes for the given algorithm. Returns list of ports."""
        ports: list[int] = []
        for _ in range(count):
            port = self._allocate_port()
            await self._spawn_child(algo_id, code_path, port, extra_env)
            ports.append(port)
        return ports

    async def _spawn_child(
        self,
        algo_id: str,
        code_path: str,
        port: int,
        extra_env: Optional[dict] = None,
    ) -> None:
        env = {**os.environ}
        # Prepend the grandparent of code_path (parent of the lazymind package) to PYTHONPATH
        code_parent = str(os.path.dirname(os.path.dirname(code_path.rstrip('/'))))
        existing_pp = env.get('PYTHONPATH', '')
        env['PYTHONPATH'] = f'{code_parent}:{existing_pp}' if existing_pp else code_parent
        # Child processes are plain chat servers behind the router proxy. Force router mode
        # off so they mount chat_routes (/api/chat/stream); otherwise the proxy would forward
        # to a child that has no chat route and get a 404. They only serve proxied request
        # types (chat/subagent); the main process serves the stateless shared endpoints.
        env['LAZYMIND_ENABLE_ROUTER'] = 'false'
        env['LAZYMIND_ROUTER_CHILD_PROXIED_ONLY'] = 'true'
        if extra_env:
            env.update(extra_env)

        cmd = [sys.executable, '-m', 'lazymind.chat.app', '--port', str(port)]
        proc = subprocess.Popen(cmd, env=env)
        self._procs[port] = proc
        self._port_algo[(self._host, port)] = algo_id

        await self.ensure_instance_registered()
        async with AsyncSessionLocal() as session:
            stmt = _upsert_child_process(
                instance_id=self._instance_id,
                algo_id=algo_id,
                host=self._host,
                port=port,
                pid=proc.pid,
            )
            await session.execute(stmt)
            await session.commit()

        logger.info('Spawned child process for algo=%s port=%d pid=%d', algo_id, port, proc.pid)

    async def ensure_instance_registered(self) -> None:
        start, end = self._port_range
        if start > end:
            return
        async with AsyncSessionLocal() as session:
            await session.execute(_upsert_instance(self._instance_id, self._host, os.getpid(), start, end))
            await session.commit()

    async def stop_algorithm(self, algo_id: str) -> None:
        """Terminate all local child processes belonging to `algo_id`."""
        to_kill = [
            (h, p) for (h, p), a in self._port_algo.items() if a == algo_id and h == self._host
        ]
        for _, port in to_kill:
            await self._kill_child(port)

        async with AsyncSessionLocal() as session:
            await session.execute(
                RouterChildProcess.__table__.update()
                .where(
                    RouterChildProcess.instance_id == self._instance_id,
                    RouterChildProcess.algorithm_id == algo_id,
                )
                .values(status='stopped')
            )
            await session.commit()

    async def _kill_child(self, port: int) -> None:
        proc = self._procs.pop(port, None)
        self._port_algo.pop((self._host, port), None)
        if proc is None:
            return
        try:
            proc.terminate()
            await asyncio.sleep(2)
            if proc.poll() is None:
                proc.kill()
        except Exception as exc:
            logger.warning('Error terminating child process on port %d: %s', port, exc)

    async def restart_instance(self, host: str, port: int) -> None:
        """Restart a local child process on the given port."""
        if host != self._host:
            logger.warning('restart_instance called for non-local host %s, ignoring', host)
            return

        # Find algo for this port
        algo_id = self._port_algo.get((host, port))
        if algo_id is None:
            # Try to look up from DB
            async with AsyncSessionLocal() as session:
                row = await session.execute(
                    select(RouterChildProcess).where(
                        RouterChildProcess.host == host,
                        RouterChildProcess.port == port,
                        RouterChildProcess.instance_id == self._instance_id,
                    )
                )
                child = row.scalar_one_or_none()
            if child is None:
                logger.error('Cannot restart: no child process found at %s:%d', host, port)
                return
            algo_id = child.algorithm_id

        async with AsyncSessionLocal() as session:
            row = await session.get(RouterAlgorithm, algo_id)
            if row is None:
                logger.error('Cannot restart: algorithm %s not found', algo_id)
                return
            code_path = row.code_path
            extra_env = dict(row.config or {})

        await self._kill_child(port)
        await self._spawn_child(algo_id, code_path, port, extra_env or None)

    # ------------------------------------------------------------------
    # Health wait
    # ------------------------------------------------------------------

    async def _wait_until_healthy(self, port: int, timeout: int = -1) -> bool:
        if timeout < 0:
            timeout = config['router_startup_timeout']
        url = f'http://127.0.0.1:{port}/health'
        deadline = time.monotonic() + timeout
        async with httpx.AsyncClient(timeout=2.0) as client:
            while time.monotonic() < deadline:
                try:
                    resp = await client.get(url)
                    if resp.status_code < 500:
                        async with AsyncSessionLocal() as session:
                            await session.execute(
                                RouterChildProcess.__table__.update()
                                .where(
                                    RouterChildProcess.host == self._host,
                                    RouterChildProcess.port == port,
                                )
                                .values(status='healthy')
                            )
                            await session.commit()
                        return True
                except Exception:
                    pass
                await asyncio.sleep(1)
        logger.warning('Child process on port %d did not become healthy within %ds', port, timeout)
        return False

    async def wait_all_healthy(self, ports: list[int], timeout: int = -1) -> None:
        if timeout < 0:
            timeout = config['router_startup_timeout']
        tasks = [self._wait_until_healthy(p, timeout) for p in ports]
        results = await asyncio.gather(*tasks)
        failed = [port for port, ok in zip(ports, results) if not ok]
        if failed:
            raise RuntimeError(f'child process healthcheck failed for ports: {failed}')

    # ------------------------------------------------------------------
    # Cleanup
    # ------------------------------------------------------------------

    async def shutdown(self) -> None:
        """Gracefully stop all local child processes and remove instance record."""
        for port in list(self._procs.keys()):
            await self._kill_child(port)

        async with AsyncSessionLocal() as session:
            await session.execute(
                delete(RouterChildProcess).where(
                    RouterChildProcess.instance_id == self._instance_id
                )
            )
            await session.execute(
                delete(RouterInstance).where(
                    RouterInstance.instance_id == self._instance_id
                )
            )
            await session.commit()
        logger.info('ProcessManager shutdown complete for instance %s', self._instance_id)


# Module-level singleton, created lazily in app startup
_process_manager: Optional[ProcessManager] = None


def get_process_manager() -> ProcessManager:
    global _process_manager
    if _process_manager is None:
        _process_manager = ProcessManager()
    return _process_manager
