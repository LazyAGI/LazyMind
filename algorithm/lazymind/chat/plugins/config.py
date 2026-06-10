from __future__ import annotations

import threading
from contextlib import contextmanager
from typing import Any, Generator

from lazymind.config import config

PLUGIN_DIR: str = config['plugin_dir']
PLUGIN_WORKSPACE_BASE: str = config['plugin_workspace_base']

_engine_lock = threading.Lock()
_engine_cache: dict[str, Any] = {}


def _get_plugin_db_engine():
    from sqlalchemy import create_engine
    from lazymind.common.postgres import normalize_postgres_sqlalchemy_url

    db_url: str = (config['core_database_url'] or config['database_url'] or '').strip()
    if not db_url:
        return None

    try:
        db_url = normalize_postgres_sqlalchemy_url(db_url)
    except Exception:
        pass

    engine = _engine_cache.get(db_url)
    if engine is not None:
        return engine
    with _engine_lock:
        engine = _engine_cache.get(db_url)
        if engine is None:
            engine = create_engine(db_url, future=True, pool_pre_ping=True)
            _engine_cache[db_url] = engine
    return engine


@contextmanager
def _plugin_db_session() -> Generator[Any, None, None]:
    """Context manager that yields a SQLAlchemy connection for plugin dependency checks."""
    engine = _get_plugin_db_engine()
    if engine is None:
        yield None
        return
    with engine.connect() as conn:
        yield conn


def get_db_session_factory():
    """Return a callable context manager factory for use in plugin trigger tools.

    Returns None when no database URL is configured (tests / local dev without DB).
    """
    engine = _get_plugin_db_engine()
    if engine is None:
        return None
    return _plugin_db_session
