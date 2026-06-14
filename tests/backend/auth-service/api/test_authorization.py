import json
import uuid
from types import SimpleNamespace

import pytest
from starlette.requests import Request

import api.authorization as authorization_api
from core.errors import AppException
from schemas.auth import AuthorizeBody


def _request(headers=None):
    return Request({
        'type': 'http',
        'method': 'POST',
        'path': '/api/auth/authorize',
        'headers': [(key.lower().encode(), value.encode()) for key, value in (headers or {}).items()],
    })


class _Session:
    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return False


def test_normalize_path_and_pattern_matching():
    assert authorization_api._normalize_path('/api/user/') == '/api/user'
    assert authorization_api._normalize_path('') == '/'
    assert authorization_api._path_matches_pattern('/api/user/123', '/api/user/{user_id}') is True
    assert authorization_api._path_matches_pattern('/api/user/123', '/api/group/{group_id}') is False
    assert authorization_api._path_matches_pattern('/api/user/123', '/api/user/{user_id}/role') is False


def test_required_permissions_prefers_exact_match_then_pattern(monkeypatch):
    monkeypatch.setattr(
        authorization_api,
        'API_PERMISSIONS_MAP',
        {
            ('GET', '/api/user/me'): ['exact'],
            ('GET', '/api/user/{user_id}'): ['pattern'],
        },
    )

    assert authorization_api._required_permissions_for('GET', '/api/user/me') == ['exact']
    assert authorization_api._required_permissions_for('GET', '/api/user/123') == ['pattern']
    assert authorization_api._required_permissions_for('POST', '/api/user/123') is None


def test_load_api_permissions_reads_file_and_normalizes_entries(tmp_path, monkeypatch):
    path = tmp_path / 'api_permissions.json'
    path.write_text(
        json.dumps([
            {'method': 'get', 'path': '/api/user/', 'permissions': ['user.read']},
            {'path': '', 'permissions': []},
        ]),
        encoding='utf-8',
    )
    monkeypatch.setenv('LAZYMIND_AUTH_API_PERMISSIONS_FILE', str(path))

    authorization_api.load_api_permissions()

    assert authorization_api.API_PERMISSIONS_MAP == {
        ('GET', '/api/user'): ['user.read'],
        ('GET', '/'): [],
    }


def test_load_api_permissions_missing_or_invalid_file_allows_all(tmp_path, monkeypatch):
    monkeypatch.setenv('LAZYMIND_AUTH_API_PERMISSIONS_FILE', str(tmp_path / 'missing.json'))
    authorization_api.load_api_permissions()
    assert authorization_api.API_PERMISSIONS_MAP == {}

    bad = tmp_path / 'bad.json'
    bad.write_text('{bad json', encoding='utf-8')
    monkeypatch.setenv('LAZYMIND_AUTH_API_PERMISSIONS_FILE', str(bad))
    authorization_api.load_api_permissions()
    assert authorization_api.API_PERMISSIONS_MAP == {}


def test_user_id_from_token_decodes_subject(monkeypatch):
    user_id = uuid.uuid4()
    monkeypatch.setenv('LAZYMIND_JWT_SECRET', 'test-secret')
    token = authorization_api.jwt.encode({'sub': str(user_id)}, 'test-secret', algorithm='HS256')

    assert authorization_api._user_id_from_token(token) == user_id


def test_user_id_from_token_rejects_invalid_tokens(monkeypatch):
    monkeypatch.setenv('LAZYMIND_JWT_SECRET', 'test-secret')

    with pytest.raises(AppException) as exc:
        authorization_api._user_id_from_token('not-a-token')

    assert exc.value.code == 1000301


def test_user_id_from_token_rejects_missing_or_invalid_subject(monkeypatch):
    monkeypatch.setenv('LAZYMIND_JWT_SECRET', 'test-secret')
    token_without_sub = authorization_api.jwt.encode({}, 'test-secret', algorithm='HS256')
    token_with_bad_sub = authorization_api.jwt.encode({'sub': 'not-a-uuid'}, 'test-secret', algorithm='HS256')

    with pytest.raises(AppException) as missing_sub:
        authorization_api._user_id_from_token(token_without_sub)
    with pytest.raises(AppException) as bad_sub:
        authorization_api._user_id_from_token(token_with_bad_sub)

    assert missing_sub.value.code == 1000301
    assert bad_sub.value.code == 1000301


def test_authorize_allows_when_no_permission_is_required(monkeypatch):
    monkeypatch.setattr(authorization_api, 'API_PERMISSIONS_MAP', {})

    assert authorization_api.authorize(AuthorizeBody(method='GET', path='/unknown'), _request()) == {'allowed': True}


def test_authorize_checks_bearer_token_and_effective_permissions(monkeypatch):
    user_id = uuid.uuid4()
    user = SimpleNamespace(
        id=user_id,
        disabled=False,
        role=SimpleNamespace(name='member'),
    )
    monkeypatch.setattr(authorization_api, 'API_PERMISSIONS_MAP', {('GET', '/api/user'): ['user.read']})
    monkeypatch.setattr(authorization_api, '_user_id_from_token', lambda token: user_id)
    monkeypatch.setattr(authorization_api, 'SessionLocal', lambda: _Session())
    monkeypatch.setattr(authorization_api.UserRepository, 'get_by_id', lambda db, uid, **kwargs: user)

    import core.permissions

    monkeypatch.setattr(core.permissions, 'get_effective_permission_codes', lambda row: {'user.read'})

    result = authorization_api.authorize(
        AuthorizeBody(method='GET', path='/api/user'),
        _request({'authorization': 'Bearer token-value'}),
    )

    assert isinstance(result, dict)
    assert result == {'allowed': True, 'role': 'member'}


def test_authorize_allows_builtin_admin_without_permission_match(monkeypatch):
    user_id = uuid.uuid4()
    user = SimpleNamespace(id=user_id, disabled=False, role=SimpleNamespace(name=authorization_api.BUILTIN_ADMIN_ROLE))
    monkeypatch.setattr(authorization_api, 'API_PERMISSIONS_MAP', {('DELETE', '/api/user'): ['user.admin']})
    monkeypatch.setattr(authorization_api, '_user_id_from_token', lambda token: user_id)
    monkeypatch.setattr(authorization_api, 'SessionLocal', lambda: _Session())
    monkeypatch.setattr(authorization_api.UserRepository, 'get_by_id', lambda db, uid, **kwargs: user)

    assert authorization_api.authorize(
        AuthorizeBody(method='DELETE', path='/api/user'),
        _request({'authorization': 'token-value'}),
    ) == {'allowed': True, 'role': authorization_api.BUILTIN_ADMIN_ROLE}


def test_authorize_rejects_missing_token_disabled_user_and_missing_permission(monkeypatch):
    user_id = uuid.uuid4()
    monkeypatch.setattr(authorization_api, 'API_PERMISSIONS_MAP', {('GET', '/api/user'): ['user.read']})

    with pytest.raises(AppException) as missing_token:
        authorization_api.authorize(AuthorizeBody(method='GET', path='/api/user'), _request())
    assert missing_token.value.code == 1000301

    monkeypatch.setattr(authorization_api, '_user_id_from_token', lambda token: user_id)
    monkeypatch.setattr(authorization_api, 'SessionLocal', lambda: _Session())
    monkeypatch.setattr(
        authorization_api.UserRepository,
        'get_by_id',
        lambda db, uid, **kwargs: SimpleNamespace(disabled=True, role=SimpleNamespace(name='member')),
    )
    with pytest.raises(AppException) as disabled:
        authorization_api.authorize(AuthorizeBody(method='GET', path='/api/user'), _request({'authorization': 'token'}))
    assert disabled.value.code == 1000106

    monkeypatch.setattr(
        authorization_api.UserRepository,
        'get_by_id',
        lambda db, uid, **kwargs: SimpleNamespace(disabled=False, role=SimpleNamespace(name='member')),
    )
    import core.permissions

    monkeypatch.setattr(core.permissions, 'get_effective_permission_codes', lambda row: set())
    with pytest.raises(AppException) as forbidden:
        authorization_api.authorize(AuthorizeBody(method='GET', path='/api/user'), _request({'authorization': 'token'}))
    assert forbidden.value.code == 1000302
