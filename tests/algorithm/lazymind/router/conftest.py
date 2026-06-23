"""
Shared fixtures for router module tests.

Relies on tests/algorithm/lazymind/conftest.py (parent conftest) having already
installed the lazymind package stubs before this file is loaded.

Uses an in-memory SQLite DB (aiosqlite) for full isolation per test.
"""
from __future__ import annotations

import pytest
import pytest_asyncio
from unittest.mock import patch

from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine

from lazymind.router.db.models import Base


# ── Per-test in-memory SQLite engine ─────────────────────────────────────────

@pytest_asyncio.fixture
async def engine():
    """Brand-new in-memory SQLite engine with all tables for each test."""
    eng = create_async_engine('sqlite+aiosqlite://', echo=False)
    async with eng.begin() as conn:
        await conn.run_sync(Base.metadata.create_all)
    yield eng
    await eng.dispose()


@pytest_asyncio.fixture
async def session_factory(engine):
    """Async session factory bound to the per-test engine."""
    return async_sessionmaker(bind=engine, class_=AsyncSession, expire_on_commit=False)


@pytest_asyncio.fixture(autouse=True)
async def patch_db(session_factory):
    """Redirect all AsyncSessionLocal usages in the router package to the test DB."""
    targets = [
        'lazymind.router.db.client.AsyncSessionLocal',
        'lazymind.router.core.registry.AsyncSessionLocal',
        'lazymind.router.core.ab_router.AsyncSessionLocal',
        'lazymind.router.core.process_manager.AsyncSessionLocal',
        'lazymind.router.core.health_checker.AsyncSessionLocal',
        'lazymind.router.core.health_checker.HeartbeatSessionLocal',
        'lazymind.router.api.algorithm_routes.AsyncSessionLocal',
        'lazymind.router.api.strategy_routes.AsyncSessionLocal',
    ]
    patches = [patch(t, session_factory) for t in targets]
    for p in patches:
        p.start()
    yield
    for p in patches:
        p.stop()


@pytest.fixture(autouse=True)
def reset_singletons():
    """Reset module-level singletons so each test gets a fresh instance."""
    import lazymind.router.core.registry as _reg
    import lazymind.router.core.process_manager as _pm
    import lazymind.router.core.ab_router as _abr

    _reg._global_registry = None
    _pm._process_manager = None
    _abr._ab_router = None
    yield
    _reg._global_registry = None
    _pm._process_manager = None
    _abr._ab_router = None
