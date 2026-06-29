import pytest

import core.redis_client as redis_client_module


class _FakeExceptions:
    RedisError = type('RedisError', (Exception,), {})
    AuthenticationError = type('AuthenticationError', (RedisError,), {})
    ReadOnlyError = type('ReadOnlyError', (RedisError,), {})
    ConnectionError = type('ConnectionError', (RedisError,), {})
    TimeoutError = type('TimeoutError', (RedisError,), {})


def test_redis_url_requires_env(monkeypatch):
    monkeypatch.delenv(redis_client_module.REDIS_URL_ENV, raising=False)

    with pytest.raises(RuntimeError, match='LAZYMIND_REDIS_URL is required'):
        redis_client_module.redis_url()


def test_redis_url_strips_env_value(monkeypatch):
    monkeypatch.setenv(redis_client_module.REDIS_URL_ENV, ' redis://localhost:6379/0 ')

    assert redis_client_module.redis_url() == 'redis://localhost:6379/0'


def test_redis_client_builds_pings_and_caches_client(monkeypatch):
    seen = {}

    class FakeClient:
        def __init__(self, url):
            self.url = url
            self.ping_count = 0

        def ping(self):
            self.ping_count += 1

    class _FakeRedisClient:
        @staticmethod
        def from_url(url, **kwargs):
            seen['url'] = url
            seen['kwargs'] = kwargs
            client = FakeClient(url)
            seen['client'] = client
            return client

    class FakeRedisModule:
        Redis = _FakeRedisClient
        exceptions = _FakeExceptions

    monkeypatch.setattr(redis_client_module, '_CLIENT', None)
    monkeypatch.setattr(redis_client_module, 'redis', FakeRedisModule)
    monkeypatch.setenv(redis_client_module.REDIS_URL_ENV, 'redis://localhost:6379/1')

    client = redis_client_module.redis_client()

    assert client is seen['client']
    assert client.url == 'redis://localhost:6379/1'
    assert client.ping_count == 1
    assert redis_client_module.redis_client() is client
    assert client.ping_count == 1
    assert seen['url'] == 'redis://localhost:6379/1'
    assert seen['kwargs']['decode_responses'] is True
    assert seen['kwargs']['socket_connect_timeout'] == 5
    assert seen['kwargs']['socket_timeout'] == 5
    assert seen['kwargs']['health_check_interval'] == 30
    assert seen['kwargs']['max_connections'] == 50
    assert seen['kwargs']['retry_on_error'] == [
        _FakeExceptions.ReadOnlyError,
        _FakeExceptions.ConnectionError,
        _FakeExceptions.TimeoutError,
    ]


def test_redis_client_retries_until_first_ping_succeeds(monkeypatch):
    clients = []
    sleeps = []

    class FakeClient:
        def __init__(self, should_fail):
            self.should_fail = should_fail

        def ping(self):
            if self.should_fail:
                raise _FakeExceptions.ConnectionError('not ready')

    class _FakeRedisClient:
        @staticmethod
        def from_url(url, **kwargs):
            client = FakeClient(should_fail=len(clients) < 2)
            clients.append(client)
            return client

    class FakeRedisModule:
        Redis = _FakeRedisClient
        exceptions = _FakeExceptions

    monkeypatch.setattr(redis_client_module, '_CLIENT', None)
    monkeypatch.setattr(redis_client_module, 'redis', FakeRedisModule)
    monkeypatch.setattr(redis_client_module, 'REDIS_CONNECT_RETRIES', 3)
    monkeypatch.setattr(redis_client_module.time, 'sleep', lambda seconds: sleeps.append(seconds))
    monkeypatch.setenv(redis_client_module.REDIS_URL_ENV, 'redis://localhost:6379/1')

    client = redis_client_module.redis_client()

    assert client is clients[-1]
    assert len(clients) == 3
    assert sleeps == [redis_client_module.REDIS_CONNECT_RETRY_INTERVAL_SECONDS] * 2
    assert redis_client_module.redis_client() is client
