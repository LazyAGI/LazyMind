import os
import sqlite3
import threading
import time
from pathlib import Path
from typing import Protocol

from core.redis_client import redis_client, state_backend_error

STATE_BACKEND_ENV = 'LAZYMIND_STATE_BACKEND'
SQLITE_DIR_ENV = 'LAZYMIND_STATE_SQLITE_DIR'
SQLITE_PATH_ENV = 'LAZYMIND_STATE_SQLITE_PATH'
_STORE_LOCK = threading.Lock()


def _lazymind_config_value(key: str) -> str | None:
    try:
        from lazymind.config import config as _cfg
    except Exception:
        return None
    try:
        value = _cfg[key]
    except Exception:
        return None
    return str(value).strip() if value is not None else None


class StateStore(Protocol):
    def set(self, key: str, value: str, ex: int | None = None) -> None:
        ...

    def get(self, key: str) -> str | None:
        ...

    def delete(self, key: str) -> None:
        ...

    def zadd(self, key: str, mapping: dict[str, float], ex: int | None = None) -> None:
        ...

    def zremrangebyscore(self, key: str, min_score: float, max_score: float) -> None:
        ...

    def zcard(self, key: str) -> int:
        ...


class RedisStateStore:
    def _run(self, fn):
        try:
            return fn(redis_client())
        except Exception as exc:
            mapped = state_backend_error(exc)
            if mapped is exc:
                raise
            raise mapped from exc

    def set(self, key: str, value: str, ex: int | None = None) -> None:
        self._run(lambda r: r.set(key, value, ex=ex))

    def get(self, key: str) -> str | None:
        return self._run(lambda r: r.get(key))

    def delete(self, key: str) -> None:
        self._run(lambda r: r.delete(key))

    def zadd(self, key: str, mapping: dict[str, float], ex: int | None = None) -> None:
        def _zadd(r):
            r.zadd(key, mapping)
            if ex and ex > 0:
                r.expire(key, ex)

        self._run(_zadd)

    def zremrangebyscore(self, key: str, min_score: float, max_score: float) -> None:
        self._run(lambda r: r.zremrangebyscore(key, min_score, max_score))

    def zcard(self, key: str) -> int:
        return int(self._run(lambda r: r.zcard(key)))


class SQLiteStateStore:
    def __init__(self, path: str):
        self._path = path
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        self._lock = threading.RLock()
        self._conn = sqlite3.connect(path, check_same_thread=False, timeout=5)
        self._conn.execute('PRAGMA journal_mode=WAL')
        self._conn.execute('PRAGMA busy_timeout=5000')
        self._conn.execute(
            'CREATE TABLE IF NOT EXISTS state_kv '
            '(key TEXT PRIMARY KEY, value TEXT NOT NULL, expires_at INTEGER NOT NULL)'
        )
        self._conn.execute(
            'CREATE TABLE IF NOT EXISTS state_zset '
            '(key TEXT NOT NULL, member TEXT NOT NULL, score REAL NOT NULL, '
            'expires_at INTEGER NOT NULL, PRIMARY KEY (key, member))'
        )
        self._conn.commit()

    def _expires_at(self, ex: int | None) -> int:
        return int(time.time()) + int(ex) if ex and ex > 0 else 0

    def _cleanup_key(self, key: str) -> None:
        now = int(time.time())
        self._conn.execute(
            'DELETE FROM state_kv WHERE key = ? AND expires_at > 0 AND expires_at <= ?',
            (key, now),
        )
        self._conn.execute(
            'DELETE FROM state_zset WHERE key = ? AND expires_at > 0 AND expires_at <= ?',
            (key, now),
        )

    def set(self, key: str, value: str, ex: int | None = None) -> None:
        with self._lock:
            self._conn.execute(
                'INSERT INTO state_kv(key, value, expires_at) VALUES(?, ?, ?) '
                'ON CONFLICT(key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at',
                (key, value, self._expires_at(ex)),
            )
            self._conn.commit()

    def get(self, key: str) -> str | None:
        with self._lock:
            row = self._conn.execute(
                'SELECT value FROM state_kv WHERE key = ? AND (expires_at = 0 OR expires_at > ?)',
                (key, int(time.time())),
            ).fetchone()
            return row[0] if row else None

    def delete(self, key: str) -> None:
        with self._lock:
            self._conn.execute('DELETE FROM state_kv WHERE key = ?', (key,))
            self._conn.execute('DELETE FROM state_zset WHERE key = ?', (key,))
            self._conn.commit()

    def zadd(self, key: str, mapping: dict[str, float], ex: int | None = None) -> None:
        with self._lock:
            expires_at = self._expires_at(ex)
            for member, score in mapping.items():
                self._conn.execute(
                    'INSERT INTO state_zset(key, member, score, expires_at) VALUES(?, ?, ?, ?) '
                    'ON CONFLICT(key, member) DO UPDATE SET score = excluded.score, expires_at = excluded.expires_at',
                    (key, str(member), float(score), expires_at),
                )
            self._conn.commit()

    def zremrangebyscore(self, key: str, min_score: float, max_score: float) -> None:
        with self._lock:
            self._conn.execute(
                'DELETE FROM state_zset WHERE key = ? AND score >= ? AND score <= ?',
                (key, min_score, max_score),
            )
            self._conn.commit()

    def zcard(self, key: str) -> int:
        with self._lock:
            row = self._conn.execute(
                'SELECT COUNT(*) FROM state_zset WHERE key = ? AND (expires_at = 0 OR expires_at > ?)',
                (key, int(time.time())),
            ).fetchone()
            return int(row[0]) if row else 0


_STORE: StateStore | None = None


def _sqlite_path() -> str:
    path = (os.environ.get(SQLITE_PATH_ENV) or '').strip()
    if path:
        return path
    base = (os.environ.get(SQLITE_DIR_ENV) or '/data/sqlite').strip()
    return str(Path(base) / 'auth_state.db')


def state_backend() -> str:
    configured_backend = _lazymind_config_value('state_backend') or os.environ.get(STATE_BACKEND_ENV) or 'redis'
    return configured_backend.strip().lower() or 'redis'


def state_store() -> StateStore:
    global _STORE
    if _STORE is not None:
        return _STORE
    with _STORE_LOCK:
        if _STORE is not None:
            return _STORE
        if state_backend() == 'sqlite':
            _STORE = SQLiteStateStore(_sqlite_path())
        else:
            _STORE = RedisStateStore()
    return _STORE
