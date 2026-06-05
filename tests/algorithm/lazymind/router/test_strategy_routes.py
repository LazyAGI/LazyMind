"""
Tests for AB strategy API routes: PUT/GET/DELETE /inner/ab/strategy.

Covers weight normalization, validation, update-replaces-old-strategy,
and clear strategy.
"""
from __future__ import annotations

import pytest
from httpx import AsyncClient, ASGITransport
from fastapi import FastAPI
from sqlalchemy import select

from lazymind.router.api.strategy_routes import router as strategy_router
from lazymind.router.db.models import RouterAbStrategy, RouterAlgorithm


# ── App fixture ───────────────────────────────────────────────────────────────

@pytest.fixture
def app():
    application = FastAPI()
    application.include_router(strategy_router)
    return application


@pytest.fixture
def client(app):
    return AsyncClient(transport=ASGITransport(app=app), base_url='http://test')


# ── Seed helpers ──────────────────────────────────────────────────────────────

async def _seed_active_algorithm(session_factory, algo_id: str) -> None:
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(
                id=algo_id, name=algo_id, code_path='/tmp', config={}, status='active',
            ))


# ── PUT /inner/ab/strategy ────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_update_strategy_creates_new_active_row(session_factory, client):
    """PUT should insert a new is_active=True row."""
    await _seed_active_algorithm(session_factory, 'algo_v1')
    await _seed_active_algorithm(session_factory, 'algo_v2')

    resp = await client.put('/inner/ab/strategy', json={'weights': {'algo_v1': 70, 'algo_v2': 30}})
    assert resp.status_code == 200

    async with session_factory() as s:
        result = await s.execute(
            select(RouterAbStrategy).where(RouterAbStrategy.is_active.is_(True))
        )
        strategies = result.scalars().all()

    assert len(strategies) == 1
    assert strategies[0].weights == {'algo_v1': 70, 'algo_v2': 30}


@pytest.mark.asyncio
async def test_update_strategy_deactivates_old_rows(session_factory, client):
    """PUT should mark old is_active=True rows as False."""
    await _seed_active_algorithm(session_factory, 'algo_v1')
    await _seed_active_algorithm(session_factory, 'algo_v2')

    # Insert an existing active strategy
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAbStrategy(weights={'algo_v1': 100}, is_active=True))

    resp = await client.put('/inner/ab/strategy', json={'weights': {'algo_v1': 50, 'algo_v2': 50}})
    assert resp.status_code == 200

    async with session_factory() as s:
        result = await s.execute(select(RouterAbStrategy))
        all_strategies = result.scalars().all()

    active = [s for s in all_strategies if s.is_active]
    inactive = [s for s in all_strategies if not s.is_active]
    assert len(active) == 1
    assert len(inactive) == 1
    assert active[0].weights == {'algo_v1': 50, 'algo_v2': 50}


@pytest.mark.asyncio
async def test_update_strategy_normalizes_weights(session_factory, client):
    """Weights that don't sum to 100 should be normalized automatically."""
    await _seed_active_algorithm(session_factory, 'algo_v1')
    await _seed_active_algorithm(session_factory, 'algo_v2')

    # algo_v1:1, algo_v2:1 → should become 50/50
    resp = await client.put('/inner/ab/strategy', json={'weights': {'algo_v1': 1, 'algo_v2': 1}})
    assert resp.status_code == 200

    data = resp.json()
    assert sum(data['weights'].values()) == 100


@pytest.mark.asyncio
async def test_update_strategy_normalization_large_remainder(session_factory, client):
    """Normalization remainder goes onto the largest-weight entry."""
    await _seed_active_algorithm(session_factory, 'algo_v1')
    await _seed_active_algorithm(session_factory, 'algo_v2')
    await _seed_active_algorithm(session_factory, 'algo_v3')

    # 1:1:1 → normalize: each ~33, remainder 1 onto any
    resp = await client.put(
        '/inner/ab/strategy',
        json={'weights': {'algo_v1': 1, 'algo_v2': 1, 'algo_v3': 1}},
    )
    assert resp.status_code == 200
    assert sum(resp.json()['weights'].values()) == 100


@pytest.mark.asyncio
async def test_update_strategy_rejects_zero_weight(session_factory, client):
    """Weights with zero or negative values should be rejected with 422."""
    await _seed_active_algorithm(session_factory, 'algo_v1')
    await _seed_active_algorithm(session_factory, 'algo_v2')

    resp = await client.put('/inner/ab/strategy', json={'weights': {'algo_v1': 70, 'algo_v2': 0}})
    assert resp.status_code == 422


@pytest.mark.asyncio
async def test_update_strategy_rejects_missing_algorithm(session_factory, client):
    """Referencing a non-existent or non-active algorithm should return 422."""
    await _seed_active_algorithm(session_factory, 'algo_v1')
    # algo_v2 does NOT exist

    resp = await client.put('/inner/ab/strategy', json={'weights': {'algo_v1': 50, 'algo_v2': 50}})
    assert resp.status_code == 422


@pytest.mark.asyncio
async def test_update_strategy_rejects_disabled_algorithm(session_factory, client):
    """A disabled algorithm cannot be included in a strategy."""
    await _seed_active_algorithm(session_factory, 'algo_v1')

    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAlgorithm(id='algo_v2', name='v2', code_path='/tmp', config={}, status='disabled'))

    resp = await client.put('/inner/ab/strategy', json={'weights': {'algo_v1': 50, 'algo_v2': 50}})
    assert resp.status_code == 422


# ── GET /inner/ab/strategy ────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_get_strategy_returns_active(session_factory, client):
    """GET should return the currently active strategy."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAbStrategy(weights={'algo_v1': 80, 'algo_v2': 20}, is_active=True))

    resp = await client.get('/inner/ab/strategy')
    assert resp.status_code == 200

    data = resp.json()
    assert data['strategy'] is not None
    assert data['strategy']['weights'] == {'algo_v1': 80, 'algo_v2': 20}
    assert data['strategy']['is_active'] is True


@pytest.mark.asyncio
async def test_get_strategy_returns_none_when_no_active(client):
    """GET returns strategy=null when no active strategy exists."""
    resp = await client.get('/inner/ab/strategy')
    assert resp.status_code == 200
    assert resp.json()['strategy'] is None


@pytest.mark.asyncio
async def test_get_strategy_returns_latest_when_multiple_active(session_factory, client):
    """GET should return the row with the highest id (latest insert)."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAbStrategy(weights={'algo_v1': 100}, is_active=True))
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAbStrategy(weights={'algo_v2': 100}, is_active=True))

    resp = await client.get('/inner/ab/strategy')
    assert resp.status_code == 200
    assert resp.json()['strategy']['weights'] == {'algo_v2': 100}


# ── DELETE /inner/ab/strategy ─────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_delete_strategy_deactivates_all(session_factory, client):
    """DELETE should mark all active strategies as inactive."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAbStrategy(weights={'algo_v1': 100}, is_active=True))

    resp = await client.delete('/inner/ab/strategy')
    assert resp.status_code == 200
    assert resp.json() == {'status': 'cleared'}

    async with session_factory() as s:
        result = await s.execute(
            select(RouterAbStrategy).where(RouterAbStrategy.is_active.is_(True))
        )
        assert result.scalars().all() == []


@pytest.mark.asyncio
async def test_delete_strategy_is_idempotent(client):
    """DELETE on an already-empty strategy set should succeed."""
    resp = await client.delete('/inner/ab/strategy')
    assert resp.status_code == 200
    assert resp.json() == {'status': 'cleared'}
