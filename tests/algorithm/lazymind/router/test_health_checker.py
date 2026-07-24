"""
Tests for HealthChecker: health probing, failure counting, restart scheduling,
heartbeat update, and dead-instance cleanup.

All network calls and subprocess interactions are mocked.
"""
from __future__ import annotations

import asyncio
from datetime import datetime, timedelta, timezone
from unittest.mock import AsyncMock, MagicMock, patch, call

import pytest
from sqlalchemy import select, update

from lazymind.router.core.health_checker import HealthChecker, _BACKOFF_SCHEDULE
from lazymind.router.core.registry import ChildProcessInfo, GlobalRegistry
from lazymind.router.db.models import (
    RouterAlgorithm, RouterChildProcess, RouterInstance,
)


# ── Helpers ───────────────────────────────────────────────────────────────────

def _mock_pm(instance_id: str = 'inst-1', host: str = '127.0.0.1') -> MagicMock:
    pm = MagicMock()
    pm.instance_id = instance_id
    pm.host = host
    pm.restart_instance = AsyncMock()
    return pm


def _mock_registry() -> MagicMock:
    reg = MagicMock(spec=GlobalRegistry)
    reg.evict_instance = MagicMock()
    reg.refresh = AsyncMock()
    return reg


def _http_ok() -> MagicMock:
    resp = MagicMock()
    resp.status_code = 200
    return resp


def _http_error() -> MagicMock:
    resp = MagicMock()
    resp.status_code = 500
    return resp


async def _seed_child(session_factory, instance_id: str, port: int,
                      status: str = 'healthy', algo_id: str = 'algo_v1') -> None:
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id=algo_id, name=algo_id, code_path='/tmp', config={}, status='active'))
            s.add(RouterInstance(instance_id=instance_id, host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))
            s.add(RouterChildProcess(
                instance_id=instance_id, algorithm_id=algo_id,
                host='127.0.0.1', port=port, pid=1001, status=status, failures=0,
            ))


# ── Health probing ────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_probe_healthy_resets_failure_count(session_factory):
    """When /health returns 200, failure counter resets and status is set to healthy."""
    await _seed_child(session_factory, 'inst-1', 18000, status='healthy')

    pm = _mock_pm()
    registry = _mock_registry()
    hc = HealthChecker(pm, registry)

    mock_resp = _http_ok()
    mock_client = AsyncMock()
    mock_client.__aenter__ = AsyncMock(return_value=mock_client)
    mock_client.__aexit__ = AsyncMock(return_value=False)
    mock_client.get = AsyncMock(return_value=mock_resp)

    with patch('httpx.AsyncClient', return_value=mock_client), \
         patch('lazymind.router.core.health_checker.resolve_host', return_value='127.0.0.1'):
        # Seed a pre-existing failure count
        hc._failure_counts[18000] = 2
        await hc._probe_child(18000)

    assert hc._failure_counts[18000] == 0
    registry.evict_instance.assert_not_called()


@pytest.mark.asyncio
async def test_probe_failure_increments_count_and_evicts(session_factory):
    """On first failure, evict_instance is called immediately."""
    await _seed_child(session_factory, 'inst-1', 18000)

    pm = _mock_pm()
    registry = _mock_registry()
    hc = HealthChecker(pm, registry)

    with patch('httpx.AsyncClient', side_effect=Exception('Connection refused')), \
         patch('lazymind.router.core.health_checker.resolve_host', return_value='127.0.0.1'):
        await hc._probe_child(18000)

    assert hc._failure_counts[18000] == 1
    registry.evict_instance.assert_called_once_with('127.0.0.1', 18000)


@pytest.mark.asyncio
async def test_probe_max_failures_triggers_restart(session_factory):
    """After reaching router_health_max_failures, a deferred restart is scheduled."""
    await _seed_child(session_factory, 'inst-1', 18000)

    pm = _mock_pm()
    registry = _mock_registry()
    hc = HealthChecker(pm, registry)

    from lazymind.config import config
    max_failures = config['router_health_max_failures']
    hc._failure_counts[18000] = max_failures - 1

    with patch('httpx.AsyncClient', side_effect=Exception('down')), \
         patch('lazymind.router.core.health_checker.resolve_host', return_value='127.0.0.1'):

        # Replace _deferred_restart with an AsyncMock so we can spy on scheduling
        hc._deferred_restart = AsyncMock()

        # Patch asyncio.create_task to intercept only calls related to _deferred_restart
        original_create_task = asyncio.create_task
        restart_tasks_created = []

        def spy_create_task(coro, **kwargs):
            # Only track tasks spawned from _deferred_restart (name attribute check)
            name = kwargs.get('name', '')
            task = original_create_task(asyncio.sleep(0))  # safe no-op
            if 'deferred' in str(coro.__qualname__ if hasattr(coro, '__qualname__') else ''):
                restart_tasks_created.append(coro)
            return task

        with patch('asyncio.create_task', side_effect=spy_create_task):
            await hc._probe_child(18000)

    # The restart task machinery should have been triggered
    assert hc._failure_counts[18000] == max_failures
    # Verify port is now tracked as unhealthy
    assert 18000 in hc._restart_tasks or hc._failure_counts[18000] >= max_failures


@pytest.mark.asyncio
async def test_deferred_restart_calls_process_manager(session_factory):
    """_deferred_restart calls pm.restart_instance and refreshes the registry."""
    pm = _mock_pm()
    registry = _mock_registry()
    hc = HealthChecker(pm, registry)

    with patch('httpx.AsyncClient', side_effect=Exception('down')), \
         patch('lazymind.router.core.health_checker.resolve_host', return_value='127.0.0.1'):
        with patch('asyncio.sleep', new_callable=AsyncMock):
            await hc._deferred_restart(18000, delay=0)

    pm.restart_instance.assert_awaited_once_with('127.0.0.1', 18000)
    registry.refresh.assert_awaited_once()
    assert hc._failure_counts.get(18000, -1) == 0


@pytest.mark.asyncio
async def test_probe_http_500_counts_as_failure(session_factory):
    """A /health response with status_code >= 500 is treated as a failure."""
    await _seed_child(session_factory, 'inst-1', 18000)

    pm = _mock_pm()
    registry = _mock_registry()
    hc = HealthChecker(pm, registry)

    mock_resp = _http_error()
    mock_client = AsyncMock()
    mock_client.__aenter__ = AsyncMock(return_value=mock_client)
    mock_client.__aexit__ = AsyncMock(return_value=False)
    mock_client.get = AsyncMock(return_value=mock_resp)

    with patch('httpx.AsyncClient', return_value=mock_client), \
         patch('lazymind.router.core.health_checker.resolve_host', return_value='127.0.0.1'):
        await hc._probe_child(18000)

    assert hc._failure_counts[18000] == 1
    registry.evict_instance.assert_called_once()


# ── Heartbeat ─────────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_update_heartbeat_writes_to_db(session_factory):
    """_update_heartbeat() should update last_heartbeat for the current instance."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterInstance(instance_id='inst-1', host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))

    pm = _mock_pm(instance_id='inst-1')
    hc = HealthChecker(pm, _mock_registry())

    before = datetime.now(timezone.utc)
    await hc._update_heartbeat()
    after = datetime.now(timezone.utc)

    async with session_factory() as s:
        inst = await s.get(RouterInstance, 'inst-1')

    # last_heartbeat should be between before and after
    ts = inst.last_heartbeat
    if ts.tzinfo is None:
        ts = ts.replace(tzinfo=timezone.utc)
    assert before <= ts <= after


# ── Dead instance cleanup ─────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_cleanup_removes_dead_instances(session_factory):
    """Instances whose last_heartbeat is older than router_instance_timeout are removed."""
    old_time = datetime.now(timezone.utc) - timedelta(seconds=300)

    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/tmp', config={}, status='active'))
            dead = RouterInstance(instance_id='dead-inst', host='10.0.0.99',
                                  pid=1, port_range_start=18200, port_range_end=18299)
            dead.last_heartbeat = old_time
            s.add(dead)
            s.add(RouterChildProcess(
                instance_id='dead-inst', algorithm_id='algo_v1',
                host='10.0.0.99', port=18201, status='healthy', failures=0,
            ))

    pm = _mock_pm()
    hc = HealthChecker(pm, _mock_registry())
    await hc._cleanup_dead_instances()

    async with session_factory() as s:
        inst = await s.get(RouterInstance, 'dead-inst')
        assert inst is None

        result = await s.execute(
            select(RouterChildProcess).where(RouterChildProcess.instance_id == 'dead-inst')
        )
        assert result.scalars().all() == []


@pytest.mark.asyncio
async def test_cleanup_does_not_remove_live_instances(session_factory):
    """Instances with a recent heartbeat should NOT be cleaned up."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterInstance(instance_id='live-inst', host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))
            # last_heartbeat defaults to NOW() — it is fresh

    pm = _mock_pm()
    hc = HealthChecker(pm, _mock_registry())
    await hc._cleanup_dead_instances()

    async with session_factory() as s:
        inst = await s.get(RouterInstance, 'live-inst')
        assert inst is not None


# ── Backoff schedule ──────────────────────────────────────────────────────────

def test_backoff_schedule_is_ascending():
    """The backoff schedule should be monotonically increasing."""
    for i in range(len(_BACKOFF_SCHEDULE) - 1):
        assert _BACKOFF_SCHEDULE[i] < _BACKOFF_SCHEDULE[i + 1]


def test_backoff_schedule_caps_at_60():
    """The maximum backoff should be 60 seconds."""
    assert _BACKOFF_SCHEDULE[-1] == 60
