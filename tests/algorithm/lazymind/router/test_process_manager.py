"""
Tests for ProcessManager: port-range claiming, algorithm start/stop/restart, shutdown.

All subprocess.Popen calls are mocked so no real child processes are created.
httpx health checks are also mocked.
"""
from __future__ import annotations

import asyncio
import os
import pytest
from unittest.mock import AsyncMock, MagicMock, patch

from sqlalchemy import select

from lazymind.router.core.process_manager import ProcessManager
from lazymind.router.db.models import RouterAlgorithm, RouterChildProcess, RouterInstance


# ── Helpers ───────────────────────────────────────────────────────────────────

def _mock_popen(pid: int = 12345) -> MagicMock:
    proc = MagicMock()
    proc.pid = pid
    proc.poll.return_value = None  # process is still running
    return proc


@pytest.fixture
def pm():
    """Return a fresh ProcessManager with host pinned to 127.0.0.1."""
    with patch('lazymind.router.core.process_manager.resolve_host', return_value='127.0.0.1'):
        manager = ProcessManager()
    return manager


# ── claim_port_range ──────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_claim_port_range_basic(pm, session_factory):
    """claim_port_range() should insert a RouterInstance and return the range."""
    start, end = await pm.claim_port_range()

    assert start == 18000
    assert end == 18099  # stride=100 default

    async with session_factory() as s:
        inst = await s.get(RouterInstance, pm.instance_id)

    assert inst is not None
    assert inst.port_range_start == 18000
    assert inst.host == '127.0.0.1'


@pytest.mark.asyncio
async def test_claim_port_range_skips_occupied(pm, session_factory):
    """claim_port_range() should skip already-occupied port ranges."""
    # Pre-insert another instance holding 18000–18099
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterInstance(
                instance_id='other-inst', host='10.0.0.2',
                pid=1, port_range_start=18000, port_range_end=18099,
            ))

    start, end = await pm.claim_port_range()
    assert start == 18100
    assert end == 18199


@pytest.mark.asyncio
async def test_claim_port_range_cleans_stale_records_for_same_host(session_factory):
    """On startup, any stale records for the same host are cleaned up first."""
    stale_inst_id = 'stale-inst'

    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/tmp', config={}, status='active'))
            s.add(RouterInstance(instance_id=stale_inst_id, host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))
            s.add(RouterChildProcess(
                instance_id=stale_inst_id, algorithm_id='algo_v1',
                host='127.0.0.1', port=18001, status='healthy', failures=0,
            ))

    with patch('lazymind.router.core.process_manager.resolve_host', return_value='127.0.0.1'):
        pm = ProcessManager()
    await pm.claim_port_range()

    async with session_factory() as s:
        # Stale instance should be gone
        stale = await s.get(RouterInstance, stale_inst_id)
        assert stale is None

        # Its child processes should also be gone
        result = await s.execute(
            select(RouterChildProcess).where(RouterChildProcess.instance_id == stale_inst_id)
        )
        assert result.scalars().all() == []


# ── start_algorithm / _spawn_child ────────────────────────────────────────────

@pytest.mark.asyncio
async def test_start_algorithm_creates_db_records(pm, session_factory):
    """start_algorithm() should insert child process records with status=starting."""
    # seed algorithm + instance
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/tmp', config={}, status='active'))
            s.add(RouterInstance(instance_id=pm.instance_id, host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))

    pm._port_range = (18000, 18099)
    pm._next_port = 18000

    with patch('subprocess.Popen', return_value=_mock_popen(pid=1001)) as mock_popen:
        ports = await pm.start_algorithm('algo_v1', '/tmp/algo_v1', count=2)

    assert ports == [18000, 18001]
    assert mock_popen.call_count == 2

    async with session_factory() as s:
        rows = await s.execute(
            select(RouterChildProcess).where(RouterChildProcess.instance_id == pm.instance_id)
        )
        children = rows.scalars().all()

    assert len(children) == 2
    assert all(c.status == 'starting' for c in children)
    assert sorted(c.port for c in children) == [18000, 18001]


@pytest.mark.asyncio
async def test_start_algorithm_sets_pythonpath(pm, session_factory):
    """_spawn_child should prepend the code_path grandparent to PYTHONPATH."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/opt/lazymind/chat', config={}, status='active'))
            s.add(RouterInstance(instance_id=pm.instance_id, host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))

    pm._port_range = (18000, 18099)
    pm._next_port = 18000

    captured_env = {}

    def fake_popen(cmd, env=None, **kwargs):
        captured_env.update(env or {})
        return _mock_popen()

    with patch('subprocess.Popen', side_effect=fake_popen):
        await pm.start_algorithm('algo_v1', '/opt/lazymind/chat', count=1)

    assert '/opt' in captured_env.get('PYTHONPATH', '')


# ── stop_algorithm ────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_stop_algorithm_marks_stopped_in_db(pm, session_factory):
    """stop_algorithm() should mark all matching child processes as stopped."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/tmp', config={}, status='active'))
            s.add(RouterInstance(instance_id=pm.instance_id, host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))
            s.add(RouterChildProcess(
                instance_id=pm.instance_id, algorithm_id='algo_v1',
                host='127.0.0.1', port=18000, pid=1001, status='healthy', failures=0,
            ))

    pm._port_range = (18000, 18099)
    pm._next_port = 18001

    # Register the process in internal state
    proc = _mock_popen(pid=1001)
    pm._procs[18000] = proc
    pm._port_algo[('127.0.0.1', 18000)] = 'algo_v1'

    await pm.stop_algorithm('algo_v1')

    async with session_factory() as s:
        result = await s.execute(
            select(RouterChildProcess).where(
                RouterChildProcess.instance_id == pm.instance_id,
                RouterChildProcess.algorithm_id == 'algo_v1',
            )
        )
        children = result.scalars().all()

    assert all(c.status == 'stopped' for c in children)
    proc.terminate.assert_called_once()


# ── restart_instance ──────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_restart_instance_kills_old_and_spawns_new(pm, session_factory):
    """restart_instance() should kill the old process and spawn a replacement."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/tmp/v1', config={}, status='active'))
            s.add(RouterInstance(instance_id=pm.instance_id, host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))
            s.add(RouterChildProcess(
                instance_id=pm.instance_id, algorithm_id='algo_v1',
                host='127.0.0.1', port=18000, pid=1001, status='unhealthy', failures=3,
            ))

    pm._port_range = (18000, 18099)
    pm._next_port = 18001
    old_proc = _mock_popen(pid=1001)
    pm._procs[18000] = old_proc
    pm._port_algo[('127.0.0.1', 18000)] = 'algo_v1'

    new_proc = _mock_popen(pid=2002)

    with patch('subprocess.Popen', return_value=new_proc):
        await pm.restart_instance('127.0.0.1', 18000)

    old_proc.terminate.assert_called_once()
    assert pm._procs[18000] is new_proc


@pytest.mark.asyncio
async def test_restart_instance_ignores_remote_host(pm):
    """restart_instance() should do nothing for a non-local host."""
    # Should not raise
    await pm.restart_instance('10.99.99.99', 18000)
    assert len(pm._procs) == 0


# ── wait_all_healthy ──────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_wait_all_healthy_marks_healthy_in_db(pm, session_factory):
    """_wait_until_healthy() marks the DB record healthy when /health returns 200."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/tmp', config={}, status='active'))
            s.add(RouterInstance(instance_id=pm.instance_id, host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))
            s.add(RouterChildProcess(
                instance_id=pm.instance_id, algorithm_id='algo_v1',
                host='127.0.0.1', port=18000, pid=1001, status='starting', failures=0,
            ))

    pm._host = '127.0.0.1'

    mock_response = MagicMock()
    mock_response.status_code = 200

    mock_client = AsyncMock()
    mock_client.__aenter__ = AsyncMock(return_value=mock_client)
    mock_client.__aexit__ = AsyncMock(return_value=False)
    mock_client.get = AsyncMock(return_value=mock_response)

    with patch('httpx.AsyncClient', return_value=mock_client):
        await pm.wait_all_healthy([18000], timeout=5)

    async with session_factory() as s:
        result = await s.execute(
            select(RouterChildProcess).where(RouterChildProcess.port == 18000)
        )
        child = result.scalar_one()

    assert child.status == 'healthy'


# ── shutdown ──────────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_shutdown_removes_instance_and_children(pm, session_factory):
    """shutdown() should remove RouterInstance and RouterChildProcess records."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/tmp', config={}, status='active'))
            s.add(RouterInstance(instance_id=pm.instance_id, host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))
            s.add(RouterChildProcess(
                instance_id=pm.instance_id, algorithm_id='algo_v1',
                host='127.0.0.1', port=18000, pid=1001, status='healthy', failures=0,
            ))

    proc = _mock_popen(pid=1001)
    pm._procs[18000] = proc
    pm._port_algo[('127.0.0.1', 18000)] = 'algo_v1'

    await pm.shutdown()

    async with session_factory() as s:
        inst = await s.get(RouterInstance, pm.instance_id)
        assert inst is None

        result = await s.execute(
            select(RouterChildProcess).where(RouterChildProcess.instance_id == pm.instance_id)
        )
        assert result.scalars().all() == []

    proc.terminate.assert_called_once()


@pytest.mark.asyncio
async def test_port_range_exhausted_raises(pm, session_factory):
    """_allocate_port() raises RuntimeError when the port range is exhausted."""
    pm._port_range = (18000, 18000)  # Only one port
    pm._next_port = 18000

    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/tmp', config={}, status='active'))
            s.add(RouterInstance(instance_id=pm.instance_id, host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18000))

    with patch('subprocess.Popen', return_value=_mock_popen()):
        await pm.start_algorithm('algo_v1', '/tmp', count=1)  # Should succeed

    with pytest.raises(RuntimeError, match='exhausted'):
        with patch('subprocess.Popen', return_value=_mock_popen()):
            await pm.start_algorithm('algo_v1', '/tmp', count=1)  # Should fail
