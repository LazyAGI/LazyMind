import hashlib
from datetime import datetime, timezone

import pytest
from jose import jwt

from api.auth import on_startup
import core.security as security


def test_jwt_secret_requires_env(monkeypatch):
    monkeypatch.delenv('LAZYMIND_JWT_SECRET', raising=False)

    with pytest.raises(RuntimeError, match='LAZYMIND_JWT_SECRET is required'):
        security.jwt_secret()


def test_validate_jwt_secret_requires_minimum_length(monkeypatch):
    monkeypatch.setenv('LAZYMIND_JWT_SECRET', 'short-secret')

    with pytest.raises(RuntimeError, match='at least 32 bytes'):
        security.validate_jwt_secret()


def test_validate_jwt_secret_rejects_low_entropy(monkeypatch):
    monkeypatch.setenv('LAZYMIND_JWT_SECRET', 'a' * 32)

    with pytest.raises(RuntimeError, match='insufficient entropy'):
        security.validate_jwt_secret()


def test_validate_jwt_secret_accepts_random_like_value(monkeypatch):
    secret = 'test-secret-7f4b0f9a2c6d8e1b3a5c7d9f'
    monkeypatch.setenv('LAZYMIND_JWT_SECRET', secret)

    assert security.validate_jwt_secret() == secret


def test_auth_startup_rejects_invalid_jwt_secret_before_bootstrap(monkeypatch):
    monkeypatch.setenv('LAZYMIND_JWT_SECRET', 'a' * 32)

    with pytest.raises(RuntimeError, match='insufficient entropy'):
        on_startup()


def test_env_int_uses_default_for_missing_or_invalid_value(monkeypatch):
    monkeypatch.delenv('INT_ENV', raising=False)
    assert security._env_int('INT_ENV', 7) == 7

    monkeypatch.setenv('INT_ENV', 'bad')
    assert security._env_int('INT_ENV', 7) == 7

    monkeypatch.setenv('INT_ENV', '9')
    assert security._env_int('INT_ENV', 7) == 9


def test_ttl_helpers_read_env(monkeypatch):
    monkeypatch.setenv('LAZYMIND_JWT_TTL_MINUTES', '2')
    monkeypatch.setenv('LAZYMIND_JWT_REFRESH_TTL_DAYS', '3')

    assert isinstance(security.jwt_ttl_minutes(), int)
    assert isinstance(security.jwt_ttl_seconds(), int)
    assert isinstance(security.refresh_token_ttl_days(), int)
    assert isinstance(security.refresh_token_ttl_seconds(), int)
    assert security.jwt_ttl_minutes() == 2
    assert security.jwt_ttl_seconds() == 120
    assert security.refresh_token_ttl_days() == 3
    assert security.refresh_token_ttl_seconds() == 259200


def test_create_access_token_contains_expected_claims(monkeypatch):
    monkeypatch.setenv('LAZYMIND_JWT_SECRET', 'test-secret')
    monkeypatch.setenv('LAZYMIND_JWT_TTL_MINUTES', '5')

    token = security.create_access_token(
        subject='user-1',
        role='system-admin',
        tenant_id='tenant-a',
        username='alice',
        jti='jti-1',
    )
    assert isinstance(token, str)
    payload = jwt.decode(token, 'test-secret', algorithms=['HS256'])

    assert payload['sub'] == 'user-1'
    assert payload['user_id'] == 'user-1'
    assert payload['role'] == 'system-admin'
    assert payload['user_type'] == 'system-admin'
    assert payload['tenant_id'] == 'tenant-a'
    assert payload['tenant_code'] == 'default'
    assert payload['username'] == 'alice'
    assert payload['jti'] == 'jti-1'
    assert payload['exp'] > payload['iat']


def test_token_generators_and_hash(monkeypatch):
    monkeypatch.setattr(security.secrets, 'token_urlsafe', lambda size: f'url-{size}')
    monkeypatch.setattr(security.secrets, 'token_hex', lambda size: f'hex-{size}')

    refresh_token = security.generate_refresh_token()
    jti = security.generate_jti()
    token_hash = security.hash_refresh_token('refresh-token')

    assert isinstance(refresh_token, str)
    assert isinstance(jti, str)
    assert isinstance(token_hash, str)
    assert refresh_token == 'url-32'
    assert jti == 'hex-16'
    assert token_hash == hashlib.sha256(b'refresh-token').hexdigest()


def test_refresh_token_expires_at_uses_refresh_ttl(monkeypatch):
    class FakeDatetime:
        @classmethod
        def now(cls, tz):
            return datetime(2026, 1, 1, tzinfo=timezone.utc)

    monkeypatch.setattr(security, 'datetime', FakeDatetime)
    monkeypatch.setenv('LAZYMIND_JWT_REFRESH_TTL_DAYS', '2')

    result = security.refresh_token_expires_at()

    assert isinstance(result, datetime)
    assert result == datetime(2026, 1, 3, tzinfo=timezone.utc)
