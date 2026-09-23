import uuid
from types import SimpleNamespace

import pytest
from starlette.requests import Request
from fastapi.security import HTTPAuthorizationCredentials
from jose import JWTError

import core.deps as deps
from core.errors import AppException


class _Session:
    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return False


def test_user_id_from_token_decodes_uuid(monkeypatch):
    user_id = uuid.uuid4()
    monkeypatch.setattr(deps, 'jwt_secret', lambda: 'secret')
    monkeypatch.setattr(deps.jwt, 'decode', lambda token, secret, algorithms: {'sub': str(user_id)})

    assert deps._user_id_from_token('token') == user_id


def test_user_id_from_token_rejects_invalid_token_or_subject(monkeypatch):
    monkeypatch.setattr(deps, 'jwt_secret', lambda: 'secret')
    monkeypatch.setattr(deps.jwt, 'decode', lambda token, secret, algorithms: (_ for _ in ()).throw(JWTError()))
    with pytest.raises(AppException) as bad_token:
        deps._user_id_from_token('token')
    assert bad_token.value.code == 1000301

    monkeypatch.setattr(deps.jwt, 'decode', lambda token, secret, algorithms: {})
    with pytest.raises(AppException) as missing_sub:
        deps._user_id_from_token('token')
    assert missing_sub.value.code == 1000301

    monkeypatch.setattr(deps.jwt, 'decode', lambda token, secret, algorithms: {'sub': 'bad-uuid'})
    with pytest.raises(AppException) as bad_sub:
        deps._user_id_from_token('token')
    assert bad_sub.value.code == 1000301


def test_current_user_id_requires_credentials(monkeypatch):
    user_id = uuid.uuid4()
    monkeypatch.setattr(deps, '_user_id_from_token', lambda token: user_id)

    credentials = HTTPAuthorizationCredentials(scheme='Bearer', credentials='token')
    assert deps.current_user_id(credentials) == user_id

    with pytest.raises(AppException) as exc:
        deps.current_user_id(None)
    assert exc.value.code == 1000301

    empty_credentials = HTTPAuthorizationCredentials(scheme='Bearer', credentials='')
    with pytest.raises(AppException) as empty_exc:
        deps.current_user_id(empty_credentials)
    assert empty_exc.value.code == 1000301


def test_current_user_loads_enabled_user(monkeypatch):
    user_id = uuid.uuid4()
    user = SimpleNamespace(id=user_id, disabled=False)
    monkeypatch.setattr(deps, 'SessionLocal', lambda: _Session())
    monkeypatch.setattr(deps.UserRepository, 'get_by_id', lambda db, uid, **kwargs: user)

    assert deps.current_user(user_id) is user


def test_current_user_rejects_missing_or_disabled_user(monkeypatch):
    user_id = uuid.uuid4()
    monkeypatch.setattr(deps, 'SessionLocal', lambda: _Session())
    monkeypatch.setattr(deps.UserRepository, 'get_by_id', lambda db, uid, **kwargs: None)
    with pytest.raises(AppException) as missing:
        deps.current_user(user_id)
    assert missing.value.code == 1000301

    monkeypatch.setattr(
        deps.UserRepository,
        'get_by_id',
        lambda db, uid, **kwargs: SimpleNamespace(disabled=True),
    )
    with pytest.raises(AppException) as disabled:
        deps.current_user(user_id)
    assert disabled.value.code == 1000106


def test_require_admin_allows_only_system_admin():
    admin = SimpleNamespace(role=SimpleNamespace(name='system-admin'))
    member = SimpleNamespace(role=SimpleNamespace(name='member'))

    assert deps.require_admin(admin) is admin
    with pytest.raises(AppException) as exc:
        deps.require_admin(member)
    assert exc.value.code == 1000303


def _request(headers=None):
    return Request({
        'type': 'http',
        'method': 'GET',
        'path': '/internal',
        'headers': [(key.lower().encode(), value.encode()) for key, value in (headers or {}).items()],
    })


def test_require_internal_service_token_all_branches(monkeypatch):
    monkeypatch.delenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', raising=False)
    with pytest.raises(AppException) as missing_expected:
        deps.require_internal_service_token(_request({'x-lazymind-internal-token': 'token'}))
    assert missing_expected.value.code == 1000302

    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'expected-token')
    with pytest.raises(AppException) as missing_header:
        deps.require_internal_service_token(_request())
    assert missing_header.value.code == 1000301

    with pytest.raises(AppException) as wrong_header:
        deps.require_internal_service_token(_request({'x-lazymind-internal-token': 'wrong-token'}))
    assert wrong_header.value.code == 1000301

    assert deps.require_internal_service_token(
        _request({'x-lazymind-internal-token': 'expected-token'})
    ) is None


def test_expected_internal_service_token_supports_secret_file(monkeypatch, tmp_path):
    monkeypatch.delenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', raising=False)
    token_file = tmp_path / 'internal-token'
    token_file.write_text('test-only-internal-token', encoding='utf-8')
    token_file.chmod(0o600)
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE', str(token_file))

    assert deps._expected_internal_service_token() == 'test-only-internal-token'


def test_expected_internal_service_token_prefers_direct_and_rejects_unsafe_file(
    monkeypatch, tmp_path
):
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'direct-test-token')
    monkeypatch.setenv(
        'LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE', str(tmp_path / 'missing')
    )
    assert deps._expected_internal_service_token() == 'direct-test-token'

    monkeypatch.delenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', raising=False)
    token_file = tmp_path / 'unsafe-token'
    token_file.write_text('test-only-internal-token', encoding='utf-8')
    token_file.chmod(0o644)
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE', str(token_file))
    assert deps._expected_internal_service_token() == ''
