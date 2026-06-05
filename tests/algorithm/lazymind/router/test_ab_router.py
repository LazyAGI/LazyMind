"""
Tests for ABRouter: algorithm selection logic.

Covers:
- Explicit algorithm_id bypass
- Weighted random selection from active strategy
- Filtering out disabled / starting algorithms
- Filtering out algorithms without healthy instances
- Fallback to 'default'
"""
from __future__ import annotations

import pytest
from unittest.mock import patch

from lazymind.router.core.ab_router import ABRouter
from lazymind.router.core.registry import ChildProcessInfo, GlobalRegistry
from lazymind.router.db.models import RouterAbStrategy, RouterAlgorithm


# ── Helpers ───────────────────────────────────────────────────────────────────

async def _seed_algorithms(session_factory, statuses: dict[str, str]) -> None:
    """Insert RouterAlgorithm rows with the given {id: status} map."""
    async with session_factory() as s:
        async with s.begin():
            for algo_id, status in statuses.items():
                s.add(RouterAlgorithm(
                    id=algo_id, name=algo_id,
                    code_path='/tmp/fake', config={}, status=status,
                ))


async def _seed_strategy(session_factory, weights: dict[str, int]) -> None:
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAbStrategy(weights=weights, is_active=True))


def _registry_with_healthy(*algo_ids: str) -> GlobalRegistry:
    """Build a GlobalRegistry whose cache already has one healthy instance per algo."""
    registry = GlobalRegistry()
    for algo_id in algo_ids:
        registry._global_instances[algo_id] = [
            ChildProcessInfo(
                instance_id='inst-1', algorithm_id=algo_id,
                host='127.0.0.1', port=18001, status='healthy',
            )
        ]
    return registry


# ── Tests ─────────────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_explicit_algorithm_id_bypasses_strategy(session_factory):
    """If caller passes algorithm_id, it is returned immediately without DB access."""
    router = ABRouter()
    result = await router.select_algorithm(caller_algorithm_id='explicit_algo')
    assert result == 'explicit_algo'


@pytest.mark.asyncio
async def test_fallback_to_default_when_no_strategy(session_factory):
    """When no active strategy exists, select_algorithm returns 'default'."""
    router = ABRouter()
    result = await router.select_algorithm()
    assert result == 'default'


@pytest.mark.asyncio
async def test_fallback_to_default_when_strategy_empty_weights(session_factory):
    """A strategy with empty weights map falls back to 'default'."""
    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAbStrategy(weights={}, is_active=True))

    router = ABRouter()
    result = await router.select_algorithm()
    assert result == 'default'


@pytest.mark.asyncio
async def test_weighted_selection_respects_active_only(session_factory):
    """Only active algorithms are eligible; disabled ones are filtered out."""
    await _seed_algorithms(session_factory, {'algo_v1': 'active', 'algo_v2': 'disabled'})
    await _seed_strategy(session_factory, {'algo_v1': 60, 'algo_v2': 40})

    registry = _registry_with_healthy('algo_v1', 'algo_v2')
    router = ABRouter()

    with patch('lazymind.router.core.registry.get_global_registry', return_value=registry):
        results = {await router.select_algorithm() for _ in range(20)}

    assert 'algo_v2' not in results
    assert results == {'algo_v1'}


@pytest.mark.asyncio
async def test_weighted_selection_filters_no_healthy_instance(session_factory):
    """Algorithms with no healthy instances in the registry are excluded."""
    await _seed_algorithms(session_factory, {'algo_v1': 'active', 'algo_v2': 'active'})
    await _seed_strategy(session_factory, {'algo_v1': 50, 'algo_v2': 50})

    # Only algo_v1 has a healthy instance
    registry = _registry_with_healthy('algo_v1')
    router = ABRouter()

    with patch('lazymind.router.core.registry.get_global_registry', return_value=registry):
        results = {await router.select_algorithm() for _ in range(20)}

    assert results == {'algo_v1'}


@pytest.mark.asyncio
async def test_weighted_selection_all_filtered_falls_back_to_default(session_factory):
    """When all strategy algorithms are filtered out, fallback to 'default'."""
    await _seed_algorithms(session_factory, {'algo_v1': 'starting'})
    await _seed_strategy(session_factory, {'algo_v1': 100})

    registry = GlobalRegistry()  # Empty registry — no healthy instances
    router = ABRouter()

    with patch('lazymind.router.core.registry.get_global_registry', return_value=registry):
        result = await router.select_algorithm()

    assert result == 'default'


@pytest.mark.asyncio
async def test_weighted_selection_distribution(session_factory):
    """Weighted random selection should roughly match the weight ratio over many trials."""
    await _seed_algorithms(session_factory, {'algo_v1': 'active', 'algo_v2': 'active'})
    await _seed_strategy(session_factory, {'algo_v1': 70, 'algo_v2': 30})

    registry = _registry_with_healthy('algo_v1', 'algo_v2')
    router = ABRouter()

    counts: dict[str, int] = {'algo_v1': 0, 'algo_v2': 0}
    with patch('lazymind.router.core.registry.get_global_registry', return_value=registry):
        for _ in range(400):
            chosen = await router.select_algorithm()
            counts[chosen] = counts.get(chosen, 0) + 1

    # With 400 trials, tolerance of ±10% is reasonable
    v1_ratio = counts['algo_v1'] / 400
    assert 0.60 <= v1_ratio <= 0.80, f'algo_v1 ratio {v1_ratio:.2f} out of expected range'


@pytest.mark.asyncio
async def test_latest_active_strategy_is_used(session_factory):
    """When multiple rows exist, the one with highest id (latest insert) is used."""
    await _seed_algorithms(session_factory, {'algo_v1': 'active', 'algo_v2': 'active'})

    async with session_factory() as s:
        async with s.begin():
            # Old strategy: 100% algo_v1
            s.add(RouterAbStrategy(weights={'algo_v1': 100}, is_active=True))
    async with session_factory() as s:
        async with s.begin():
            # Newer strategy: 100% algo_v2
            s.add(RouterAbStrategy(weights={'algo_v2': 100}, is_active=True))

    registry = _registry_with_healthy('algo_v1', 'algo_v2')
    router = ABRouter()

    with patch('lazymind.router.core.registry.get_global_registry', return_value=registry):
        result = await router.select_algorithm()

    assert result == 'algo_v2'


@pytest.mark.asyncio
async def test_inactive_strategy_is_ignored(session_factory):
    """A strategy row with is_active=False should not be used."""
    await _seed_algorithms(session_factory, {'algo_v1': 'active'})

    async with session_factory() as s:
        async with s.begin():
            s.add(RouterAbStrategy(weights={'algo_v1': 100}, is_active=False))

    router = ABRouter()
    result = await router.select_algorithm()
    assert result == 'default'
