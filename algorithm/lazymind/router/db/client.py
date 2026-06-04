from __future__ import annotations

from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine

from lazymind.config import config
import lazymind.router.config  # noqa: F401 — registers router config keys


def _make_async_url(url: str) -> str:
    """Normalise a DB URL to an async-driver URL."""
    if url.startswith('postgresql://'):
        return url.replace('postgresql://', 'postgresql+asyncpg://', 1)
    if url.startswith('postgres://'):
        return url.replace('postgres://', 'postgresql+asyncpg://', 1)
    if url.startswith('sqlite://') and '+' not in url.split('://')[0]:
        # sqlite:///path  →  sqlite+aiosqlite:///path
        return url.replace('sqlite://', 'sqlite+aiosqlite://', 1)
    return url


def _is_sqlite(url: str) -> bool:
    return url.startswith('sqlite')


def _build_engine(raw_url: str):
    async_url = _make_async_url(raw_url)
    if _is_sqlite(async_url):
        # SQLite does not support connection pooling parameters
        return create_async_engine(async_url, echo=False)
    return create_async_engine(
        async_url,
        pool_pre_ping=True,
        pool_size=5,
        max_overflow=10,
        echo=False,
    )


_engine = _build_engine(config['core_database_url'] or '')

AsyncSessionLocal: async_sessionmaker[AsyncSession] = async_sessionmaker(
    bind=_engine,
    class_=AsyncSession,
    expire_on_commit=False,
)


def get_engine():
    return _engine
