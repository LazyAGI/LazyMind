"""
Tests for algorithm management API routes.

Covers the full lifecycle:
  - Register a new algorithm version (simulating first deployment)
  - Code update: re-register existing algorithm with new code_path
  - Delete (disable) an algorithm
  - List / get detail
  - Restart local instances

All subprocess.Popen and httpx health checks are mocked so no real processes
are created.  This mirrors the "code update" scenario: evo calls
POST /inner/algorithm/register with a new code_path, the router stops old
instances and starts fresh ones from the updated path.
"""
from __future__ import annotations

import pytest
from httpx import AsyncClient, ASGITransport
from fastapi import FastAPI
from unittest.mock import AsyncMock, MagicMock, patch
from sqlalchemy import select

from lazymind.router.api.algorithm_routes import router as algo_router
from lazymind.router.core.process_manager import ProcessManager
from lazymind.router.core.registry import GlobalRegistry, ChildProcessInfo
from lazymind.router.db.models import RouterAlgorithm, RouterChildProcess, RouterInstance


# ── App and client fixtures ───────────────────────────────────────────────────

@pytest.fixture
def app():
    application = FastAPI()
    application.include_router(algo_router)
    return application


@pytest.fixture
def client(app):
    return AsyncClient(transport=ASGITransport(app=app), base_url='http://test')


# ── Mock ProcessManager factory ───────────────────────────────────────────────

def _make_mock_pm(instance_id: str = 'inst-1', host: str = '127.0.0.1',
                  ports: list[int] | None = None) -> MagicMock:
    """Return a ProcessManager mock that simulates starting N instances."""
    if ports is None:
        ports = [18000]
    pm = MagicMock(spec=ProcessManager)
    pm.instance_id = instance_id
    pm.host = host
    pm.start_algorithm = AsyncMock(return_value=ports)
    pm.wait_all_healthy = AsyncMock()
    pm.stop_algorithm = AsyncMock()
    pm.restart_instance = AsyncMock()
    return pm


def _make_mock_registry(algo_instances: dict[str, list[ChildProcessInfo]] | None = None) -> MagicMock:
    reg = MagicMock(spec=GlobalRegistry)
    if algo_instances is None:
        algo_instances = {}
    reg.get_all_instances = MagicMock(side_effect=lambda aid: algo_instances.get(aid, []))
    return reg


# ── Seed helpers ──────────────────────────────────────────────────────────────

async def _seed_instance(session_factory, instance_id: str = 'inst-1',
                          start: int = 18000, end: int = 18099) -> None:
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterInstance(
                instance_id=instance_id, host='127.0.0.1',
                pid=1, port_range_start=start, port_range_end=end,
            ))


# ── POST /inner/algorithm/register ───────────────────────────────────────────

@pytest.mark.asyncio
async def test_register_new_algorithm(session_factory, client):
    """Registering a brand-new algorithm should create a DB record and start instances."""
    await _seed_instance(session_factory)
    pm = _make_mock_pm(ports=[18000])

    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm):
        resp = await client.post('/inner/algorithm/register', json={
            'id': 'algo_v1',
            'name': 'Version 1',
            'code_path': '/opt/lazymind/v1',
            'instance_count': 1,
            'config': {},
        })

    assert resp.status_code == 200
    data = resp.json()
    assert data['algorithm_id'] == 'algo_v1'
    assert data['ports'] == [18000]

    async with session_factory() as s:
        algo = await s.get(RouterAlgorithm, 'algo_v1')

    assert algo is not None
    assert algo.status == 'active'
    assert algo.code_path == '/opt/lazymind/v1'
    pm.start_algorithm.assert_awaited_once()
    pm.wait_all_healthy.assert_awaited_once()


@pytest.mark.asyncio
async def test_register_auto_generates_id(session_factory, client):
    """When id is omitted, a UUID is auto-generated."""
    await _seed_instance(session_factory)
    pm = _make_mock_pm(ports=[18000])

    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm):
        resp = await client.post('/inner/algorithm/register', json={
            'name': 'Auto ID',
            'code_path': '/opt/lazymind/auto',
        })

    assert resp.status_code == 200
    algo_id = resp.json()['algorithm_id']
    assert len(algo_id) == 36  # UUID4 format


@pytest.mark.asyncio
async def test_register_multiple_instances(session_factory, client):
    """instance_count=2 should start 2 child processes."""
    await _seed_instance(session_factory)
    pm = _make_mock_pm(ports=[18000, 18001])

    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm):
        resp = await client.post('/inner/algorithm/register', json={
            'id': 'algo_v1',
            'name': 'v1',
            'code_path': '/opt/lazymind/v1',
            'instance_count': 2,
        })

    assert resp.status_code == 200
    assert resp.json()['ports'] == [18000, 18001]
    pm.start_algorithm.assert_awaited_once_with(
        algo_id='algo_v1', code_path='/opt/lazymind/v1', count=2, extra_env={}
    )


@pytest.mark.asyncio
async def test_register_code_update_replaces_existing(session_factory, client):
    """Re-registering an existing algorithm simulates a code update.

    The DB record should be updated with the new code_path, and fresh instances
    should be started (evo deploys new code → calls register again with new path).
    """
    await _seed_instance(session_factory)

    # Pre-insert the old version
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(
                id='algo_v1', name='Version 1',
                code_path='/opt/lazymind/v1_old', config={}, status='active',
            ))

    pm = _make_mock_pm(ports=[18000])

    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm):
        resp = await client.post('/inner/algorithm/register', json={
            'id': 'algo_v1',
            'name': 'Version 1 (updated)',
            'code_path': '/opt/lazymind/v1_new',  # updated code path
            'instance_count': 1,
        })

    assert resp.status_code == 200

    async with session_factory() as s:
        algo = await s.get(RouterAlgorithm, 'algo_v1')

    # Code path must be updated
    assert algo.code_path == '/opt/lazymind/v1_new'
    assert algo.name == 'Version 1 (updated)'
    assert algo.status == 'active'
    # New instances were started with the new path
    pm.start_algorithm.assert_awaited_once()
    call_kwargs = pm.start_algorithm.call_args
    assert call_kwargs.kwargs['code_path'] == '/opt/lazymind/v1_new'


@pytest.mark.asyncio
async def test_register_passes_config_as_env(session_factory, client):
    """Extra config fields should be passed as extra_env to start_algorithm."""
    await _seed_instance(session_factory)
    pm = _make_mock_pm(ports=[18000])

    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm):
        resp = await client.post('/inner/algorithm/register', json={
            'id': 'algo_v1',
            'name': 'v1',
            'code_path': '/opt/v1',
            'config': {'MODEL': 'gpt-4o', 'TEMPERATURE': '0.7'},
        })

    assert resp.status_code == 200
    _, call_kwargs = pm.start_algorithm.call_args
    assert call_kwargs['extra_env']['MODEL'] == 'gpt-4o'
    assert call_kwargs['extra_env']['TEMPERATURE'] == '0.7'


# ── DELETE /inner/algorithm/{algorithm_id} ────────────────────────────────────

@pytest.mark.asyncio
async def test_delete_algorithm_marks_disabled(session_factory, client):
    """DELETE should set status=disabled and stop local instances."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/tmp', config={}, status='active'))

    pm = _make_mock_pm()

    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm):
        resp = await client.delete('/inner/algorithm/algo_v1')

    assert resp.status_code == 200
    assert resp.json()['status'] == 'disabled'

    async with session_factory() as s:
        algo = await s.get(RouterAlgorithm, 'algo_v1')

    assert algo.status == 'disabled'
    pm.stop_algorithm.assert_awaited_once_with('algo_v1')


@pytest.mark.asyncio
async def test_delete_nonexistent_algorithm_returns_404(client):
    """Deleting an algorithm that doesn't exist should return 404."""
    pm = _make_mock_pm()

    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm):
        resp = await client.delete('/inner/algorithm/nonexistent')

    assert resp.status_code == 404


# ── GET /inner/algorithm ──────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_list_algorithms_returns_all(session_factory, client):
    """GET /inner/algorithm should list all algorithm records."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/v1', config={}, status='active'))
            s.add(RouterAlgorithm(id='algo_v2', name='v2', code_path='/v2', config={}, status='disabled'))

    resp = await client.get('/inner/algorithm')
    assert resp.status_code == 200

    algos = resp.json()['algorithms']
    assert len(algos) == 2
    ids = {a['id'] for a in algos}
    assert ids == {'algo_v1', 'algo_v2'}


@pytest.mark.asyncio
async def test_list_algorithms_empty(client):
    """GET with no algorithms in DB returns an empty list."""
    resp = await client.get('/inner/algorithm')
    assert resp.status_code == 200
    assert resp.json()['algorithms'] == []


# ── GET /inner/algorithm/{algorithm_id} ──────────────────────────────────────

@pytest.mark.asyncio
async def test_get_algorithm_detail_includes_instances(session_factory, client):
    """GET detail should include the global instance list from the registry."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/v1', config={}, status='active'))

    inst = ChildProcessInfo(instance_id='inst-1', algorithm_id='algo_v1',
                            host='127.0.0.1', port=18000, status='healthy')
    registry = _make_mock_registry({'algo_v1': [inst]})

    with patch('lazymind.router.api.algorithm_routes.get_global_registry', return_value=registry):
        resp = await client.get('/inner/algorithm/algo_v1')

    assert resp.status_code == 200
    data = resp.json()
    assert data['id'] == 'algo_v1'
    assert len(data['instances']) == 1
    assert data['instances'][0]['port'] == 18000
    assert data['instances'][0]['status'] == 'healthy'


@pytest.mark.asyncio
async def test_get_algorithm_detail_404(client):
    """GET on unknown algorithm_id should return 404."""
    registry = _make_mock_registry()
    with patch('lazymind.router.api.algorithm_routes.get_global_registry', return_value=registry):
        resp = await client.get('/inner/algorithm/ghost')

    assert resp.status_code == 404


# ── POST /inner/algorithm/{algorithm_id}/restart ──────────────────────────────

@pytest.mark.asyncio
async def test_restart_algorithm_restarts_all_local_instances(session_factory, client):
    """POST /restart should restart every local instance for that algorithm."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v1', name='v1', code_path='/v1', config={}, status='active'))
            s.add(RouterInstance(instance_id='inst-1', host='127.0.0.1',
                                 pid=1, port_range_start=18000, port_range_end=18099))
            s.add(RouterChildProcess(
                instance_id='inst-1', algorithm_id='algo_v1',
                host='127.0.0.1', port=18000, status='unhealthy', failures=3,
            ))
            s.add(RouterChildProcess(
                instance_id='inst-1', algorithm_id='algo_v1',
                host='127.0.0.1', port=18001, status='unhealthy', failures=3,
            ))

    pm = _make_mock_pm(instance_id='inst-1')

    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm):
        resp = await client.post('/inner/algorithm/algo_v1/restart')

    assert resp.status_code == 200
    restarted = resp.json()['restarted_ports']
    assert sorted(restarted) == [18000, 18001]
    assert pm.restart_instance.await_count == 2


@pytest.mark.asyncio
async def test_restart_algorithm_404_when_no_local_instances(session_factory, client):
    """POST /restart returns 404 when this instance has no child processes for the algo."""
    pm = _make_mock_pm()

    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm):
        resp = await client.post('/inner/algorithm/algo_nonexistent/restart')

    assert resp.status_code == 404


# ── Code-update simulation scenario ──────────────────────────────────────────

@pytest.mark.asyncio
async def test_full_code_update_lifecycle(session_factory, client):
    """End-to-end simulation of an evo code-update cycle:

    1. Deploy v1  → register algo_v1 with code path v1
    2. Deploy v2  → register algo_v2 with code path v2
    3. Update AB  → (tested in test_strategy_routes, assumed here)
    4. Retire v1  → DELETE algo_v1, verify it is disabled but record kept
    5. Hot-update → re-register algo_v2 with new code path (in-place update)
    """
    await _seed_instance(session_factory)
    pm = _make_mock_pm(ports=[18000])

    # Step 1: Deploy v1
    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm):
        r1 = await client.post('/inner/algorithm/register', json={
            'id': 'algo_v1', 'name': 'v1', 'code_path': '/deploy/v1',
        })
    assert r1.status_code == 200

    # Step 2: Deploy v2 (new algorithm)
    pm2 = _make_mock_pm(ports=[18010])
    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm2):
        r2 = await client.post('/inner/algorithm/register', json={
            'id': 'algo_v2', 'name': 'v2', 'code_path': '/deploy/v2',
        })
    assert r2.status_code == 200

    # Step 4: Retire v1
    pm_stop = _make_mock_pm()
    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm_stop):
        r_del = await client.delete('/inner/algorithm/algo_v1')
    assert r_del.status_code == 200

    async with session_factory() as s:
        v1 = await s.get(RouterAlgorithm, 'algo_v1')
    assert v1.status == 'disabled'   # record kept for audit
    assert v1.code_path == '/deploy/v1'

    # Step 5: Hot-update v2 with a new code path
    pm_update = _make_mock_pm(ports=[18010])
    with patch('lazymind.router.api.algorithm_routes.get_process_manager', return_value=pm_update):
        r_update = await client.post('/inner/algorithm/register', json={
            'id': 'algo_v2', 'name': 'v2 (hotfix)', 'code_path': '/deploy/v2_hotfix',
        })
    assert r_update.status_code == 200

    async with session_factory() as s:
        v2 = await s.get(RouterAlgorithm, 'algo_v2')
    assert v2.code_path == '/deploy/v2_hotfix'
    assert v2.status == 'active'
    # Fresh processes started from the new path
    assert pm_update.start_algorithm.await_count == 1
    call_kw = pm_update.start_algorithm.call_args.kwargs
    assert call_kw['code_path'] == '/deploy/v2_hotfix'
