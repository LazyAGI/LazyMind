"""
Tests for GlobalRegistry: refresh / round-robin / evict.
"""
from __future__ import annotations

import pytest
import pytest_asyncio

from lazymind.router.core.registry import ChildProcessInfo, GlobalRegistry
from lazymind.router.db.models import RouterAlgorithm, RouterChildProcess, RouterInstance


# ── Helpers ──────────────────────────────────────────────────────────────────

def _make_algo(session, algo_id: str = 'algo_v1') -> RouterAlgorithm:
    algo = RouterAlgorithm(id=algo_id, name=algo_id, code_path='/tmp/fake', status='active', config={})
    session.add(algo)
    return algo


def _make_instance(session, instance_id: str = 'inst-1', host: str = '127.0.0.1') -> RouterInstance:
    inst = RouterInstance(instance_id=instance_id, host=host, pid=9999,
                          port_range_start=18000, port_range_end=18099)
    session.add(inst)
    return inst


def _make_child(session, instance_id: str, algo_id: str, host: str, port: int,
                status: str = 'healthy') -> RouterChildProcess:
    child = RouterChildProcess(
        instance_id=instance_id, algorithm_id=algo_id,
        host=host, port=port, pid=None, status=status, failures=0,
    )
    session.add(child)
    return child


# ── Tests ─────────────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_refresh_loads_healthy_instances(session_factory):
    """refresh() should load only healthy child processes into the cache."""
    async with session_factory() as s:
        async with s.begin():
            _make_algo(s, 'algo_v1')
            _make_instance(s, 'inst-1')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18001, status='healthy')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18002, status='unhealthy')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18003, status='starting')

    registry = GlobalRegistry()
    await registry.refresh()

    instances = registry.get_all_instances('algo_v1')
    assert len(instances) == 1
    assert instances[0].port == 18001
    assert instances[0].status == 'healthy'


@pytest.mark.asyncio
async def test_refresh_multiple_algorithms(session_factory):
    """refresh() groups instances by algorithm_id correctly."""
    async with session_factory() as s:
        async with s.begin():
            _make_algo(s, 'algo_v1')
            _make_algo(s, 'algo_v2')
            _make_instance(s, 'inst-1')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18001, 'healthy')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18002, 'healthy')
            _make_child(s, 'inst-1', 'algo_v2', '127.0.0.1', 18011, 'healthy')

    registry = GlobalRegistry()
    await registry.refresh()

    assert set(registry.get_all_algorithms()) == {'algo_v1', 'algo_v2'}
    assert len(registry.get_all_instances('algo_v1')) == 2
    assert len(registry.get_all_instances('algo_v2')) == 1


@pytest.mark.asyncio
async def test_get_healthy_instance_round_robin(session_factory):
    """get_healthy_instance() should cycle through healthy instances in order."""
    async with session_factory() as s:
        async with s.begin():
            _make_algo(s, 'algo_v1')
            _make_instance(s, 'inst-1')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18001, 'healthy')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18002, 'healthy')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18003, 'healthy')

    registry = GlobalRegistry()
    await registry.refresh()

    ports_seen = [registry.get_healthy_instance('algo_v1').port for _ in range(6)]
    # Each port should appear exactly twice in 6 calls across 3 instances
    assert sorted(set(ports_seen)) == [18001, 18002, 18003]
    # Ensure rotation: no two consecutive calls return the same port
    for i in range(len(ports_seen) - 1):
        assert ports_seen[i] != ports_seen[i + 1]


@pytest.mark.asyncio
async def test_get_healthy_instance_returns_none_when_empty(session_factory):
    """get_healthy_instance() returns None if no instances exist."""
    registry = GlobalRegistry()
    await registry.refresh()
    assert registry.get_healthy_instance('nonexistent') is None


@pytest.mark.asyncio
async def test_evict_instance_removes_from_cache(session_factory):
    """evict_instance() immediately removes the target from the in-memory cache."""
    async with session_factory() as s:
        async with s.begin():
            _make_algo(s, 'algo_v1')
            _make_instance(s, 'inst-1')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18001, 'healthy')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18002, 'healthy')

    registry = GlobalRegistry()
    await registry.refresh()

    registry.evict_instance('127.0.0.1', 18001)

    instances = registry.get_all_instances('algo_v1')
    assert len(instances) == 1
    assert instances[0].port == 18002


@pytest.mark.asyncio
async def test_evict_resets_round_robin_cursor(session_factory):
    """After eviction, round-robin cursor resets to avoid IndexError."""
    async with session_factory() as s:
        async with s.begin():
            _make_algo(s, 'algo_v1')
            _make_instance(s, 'inst-1')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18001, 'healthy')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18002, 'healthy')

    registry = GlobalRegistry()
    await registry.refresh()

    # Advance cursor a few times
    for _ in range(3):
        registry.get_healthy_instance('algo_v1')

    # Evict one — cursor is reset
    registry.evict_instance('127.0.0.1', 18001)

    # Should not raise, and should always return the surviving instance
    for _ in range(4):
        inst = registry.get_healthy_instance('algo_v1')
        assert inst is not None
        assert inst.port == 18002


@pytest.mark.asyncio
async def test_evict_nonexistent_instance_is_noop(session_factory):
    """Evicting an instance that doesn't exist should not raise."""
    async with session_factory() as s:
        async with s.begin():
            _make_algo(s, 'algo_v1')
            _make_instance(s, 'inst-1')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18001, 'healthy')

    registry = GlobalRegistry()
    await registry.refresh()

    registry.evict_instance('10.0.0.99', 9999)  # Does not exist
    assert len(registry.get_all_instances('algo_v1')) == 1


@pytest.mark.asyncio
async def test_refresh_replaces_stale_cache(session_factory):
    """A second refresh() should replace the cache, not accumulate."""
    async with session_factory() as s:
        async with s.begin():
            _make_algo(s, 'algo_v1')
            _make_instance(s, 'inst-1')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18001, 'healthy')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18002, 'healthy')

    registry = GlobalRegistry()
    await registry.refresh()
    assert len(registry.get_all_instances('algo_v1')) == 2

    # Now simulate port 18002 going unhealthy in DB
    async with session_factory() as s:
        async with s.begin():
            from sqlalchemy import update
            from lazymind.router.db.models import RouterChildProcess
            await s.execute(
                update(RouterChildProcess)
                .where(RouterChildProcess.port == 18002)
                .values(status='unhealthy')
            )

    await registry.refresh()
    instances = registry.get_all_instances('algo_v1')
    assert len(instances) == 1
    assert instances[0].port == 18001


@pytest.mark.asyncio
async def test_snapshot_returns_copy(session_factory):
    """snapshot() returns a deep copy so mutations do not affect the registry."""
    async with session_factory() as s:
        async with s.begin():
            _make_algo(s, 'algo_v1')
            _make_instance(s, 'inst-1')
            _make_child(s, 'inst-1', 'algo_v1', '127.0.0.1', 18001, 'healthy')

    registry = GlobalRegistry()
    await registry.refresh()

    snap = registry.snapshot()
    snap['algo_v1'].clear()  # Mutate the snapshot

    # Original registry unchanged
    assert len(registry.get_all_instances('algo_v1')) == 1


@pytest.mark.asyncio
async def test_child_process_info_url_property():
    """ChildProcessInfo.url returns the correct http URL."""
    info = ChildProcessInfo(
        instance_id='i1', algorithm_id='algo_v1',
        host='10.0.0.5', port=18042, status='healthy',
    )
    assert info.url == 'http://10.0.0.5:18042'
