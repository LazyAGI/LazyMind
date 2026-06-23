import core.rate_limit as rate_limit


class _StateStore:
    def __init__(self, attempts=0, fail=False):
        self.calls = []
        self.attempts = attempts
        self.fail = fail

    def zremrangebyscore(self, *args):
        if self.fail:
            raise RuntimeError('state down')
        self.calls.append(('zremrangebyscore', args))

    def zcard(self, *args):
        if self.fail:
            raise RuntimeError('state down')
        self.calls.append(('zcard', args))
        return self.attempts

    def zadd(self, *args, **kwargs):
        if self.fail:
            raise RuntimeError('state down')
        self.calls.append(('zadd', args, kwargs))


def test_is_limited_uses_sliding_window_and_threshold(monkeypatch):
    state = _StateStore(attempts=3)
    monkeypatch.setattr(rate_limit.time, 'time', lambda: 100)
    monkeypatch.setattr(rate_limit, 'state_store', lambda: state)
    limiter = rate_limit.LoginRateLimiter(max_attempts=3, time_window_seconds=60, key_prefix='login')

    assert limiter.is_limited('alice') is True
    assert state.calls == [
        ('zremrangebyscore', ('login:alice', float('-inf'), 40)),
        ('zcard', ('login:alice',)),
    ]


def test_is_limited_returns_false_for_bad_counts_or_state_errors(monkeypatch):
    state = _StateStore(attempts='bad')
    monkeypatch.setattr(rate_limit, 'state_store', lambda: state)
    assert rate_limit.LoginRateLimiter().is_limited('alice') is False

    monkeypatch.setattr(rate_limit, 'state_store', lambda: _StateStore(fail=True))
    assert rate_limit.LoginRateLimiter().is_limited('alice') is False


def test_record_failure_records_timestamp(monkeypatch):
    state = _StateStore()
    monkeypatch.setattr(rate_limit.time, 'time', lambda: 123)
    monkeypatch.setattr(rate_limit, 'state_store', lambda: state)
    limiter = rate_limit.LoginRateLimiter(time_window_seconds=60, key_prefix='login')

    limiter.record_failure('alice')

    assert state.calls == [('zadd', ('login:alice', {'123': 123}), {'ex': 120})]


def test_record_failure_ignores_state_errors(monkeypatch):
    monkeypatch.setattr(rate_limit, 'state_store', lambda: _StateStore(fail=True))

    rate_limit.LoginRateLimiter().record_failure('alice')
