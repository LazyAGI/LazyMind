"""
Login failure rate limiting (deny login for a period after N consecutive failures on the same account)
"""
import time

from core.state_store import state_store

LOGIN_MAX_ATTEMPTS = 3
LOGIN_TIME_WINDOW_SECONDS = 60


class LoginRateLimiter:
    """Per-user login failure rate limiter (state-store sliding window)"""

    def __init__(
        self,
        max_attempts: int = LOGIN_MAX_ATTEMPTS,
        time_window_seconds: int = LOGIN_TIME_WINDOW_SECONDS,
        *,
        key_prefix: str = 'login_rate_limiter',
    ):
        self._max_attempts = max_attempts
        self._time_window = time_window_seconds
        self._key_prefix = key_prefix

    def is_limited(self, user_id: int | str) -> bool:
        """Return True when failures for the same user reach the limit within the time window."""
        try:
            store = state_store()
            key = f'{self._key_prefix}:{user_id}'
            now = int(time.time())
            window_start_time = now - self._time_window

            store.zremrangebyscore(key, float('-inf'), window_start_time)
            attempts = store.zcard(key)

            try:
                return int(attempts) >= self._max_attempts
            except (TypeError, ValueError):
                return False
        except Exception:
            # Do not block login when state store is unavailable (only lose rate-limit protection)
            return False

    def record_failure(self, user_id: int | str) -> None:
        """Record one login failure."""
        try:
            store = state_store()
            key = f'{self._key_prefix}:{user_id}'
            now = int(time.time())

            store.zadd(key, {str(now): now}, ex=self._time_window * 2)
        except Exception:
            return


login_rate_limiter = LoginRateLimiter()
