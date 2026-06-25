import os
import threading
import time

import redis

from core.state_errors import StateBackendAuthenticationError, StateBackendError

_CLIENT: redis.Redis | None = None
_CLIENT_LOCK = threading.Lock()
REDIS_URL_ENV = 'LAZYMIND_REDIS_URL'
REDIS_CONNECT_RETRIES = 10
REDIS_CONNECT_RETRY_INTERVAL_SECONDS = 1.0


def redis_url() -> str:
    url = (os.environ.get(REDIS_URL_ENV) or '').strip()
    if not url:
        raise StateBackendError(f'{REDIS_URL_ENV} is required for auth-service state backend')
    return url


def state_backend_error(exc: Exception) -> Exception:
    if isinstance(exc, redis.exceptions.AuthenticationError):
        return StateBackendAuthenticationError('state backend authentication failed')
    if isinstance(exc, redis.exceptions.RedisError):
        return StateBackendError('state backend is unavailable')
    return exc


def _build_redis_client(url: str) -> redis.Redis:
    return redis.Redis.from_url(
        url,
        decode_responses=True,
        socket_connect_timeout=5,
        socket_timeout=5,
        health_check_interval=30,
        retry_on_error=[
            redis.exceptions.ReadOnlyError,
            redis.exceptions.ConnectionError,
            redis.exceptions.TimeoutError,
        ],
        max_connections=50,
    )


def redis_client() -> redis.Redis:
    global _CLIENT
    if _CLIENT is not None:
        return _CLIENT

    with _CLIENT_LOCK:
        if _CLIENT is not None:
            return _CLIENT

        url = redis_url()
        last_error: Exception | None = None
        for attempt in range(REDIS_CONNECT_RETRIES):
            client = _build_redis_client(url)
            try:
                client.ping()
            except (redis.exceptions.ConnectionError, redis.exceptions.TimeoutError) as exc:
                last_error = exc
                if attempt < REDIS_CONNECT_RETRIES - 1:
                    time.sleep(REDIS_CONNECT_RETRY_INTERVAL_SECONDS)
                    continue
                raise state_backend_error(exc) from exc
            except redis.exceptions.RedisError as exc:
                raise state_backend_error(exc) from exc
            _CLIENT = client
            return _CLIENT

        assert last_error is not None
        raise state_backend_error(last_error) from last_error
