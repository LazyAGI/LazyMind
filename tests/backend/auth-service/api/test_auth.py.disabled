"""
Unit tests for auth-service API endpoints.
Uses the isolated SQLite test DB configured via conftest - no external deps.
"""
import uuid

import pytest
from fastapi.testclient import TestClient


API_PREFIX = '/api/authservice/auth'


def _data(response):
    payload = response.json()
    return payload['data']


@pytest.fixture(autouse=True)
def _stub_redis_dependencies(monkeypatch):
    import api.auth as auth_api
    from services.auth_service import login_rate_limiter

    store = {}

    monkeypatch.setattr(login_rate_limiter, 'is_limited', lambda user_id: False)
    monkeypatch.setattr(login_rate_limiter, 'record_failure', lambda user_id: None)
    monkeypatch.setattr(auth_api, 'set_refresh_token', lambda token_hash, user_id: store.__setitem__(token_hash, user_id))
    monkeypatch.setattr(auth_api, 'get_user_id_by_token', lambda token_hash: store.get(token_hash))
    monkeypatch.setattr(auth_api, 'delete_refresh_token', lambda token_hash: store.pop(token_hash, None))


def test_health(client: TestClient):
    r = client.get(f'{API_PREFIX}/health')
    assert r.status_code == 200
    data = _data(r)
    assert isinstance(data, dict)
    assert data['status'] == 'ok'
    assert isinstance(data['timestamp'], float)
    assert 'timestamp' in data


def test_register(client: TestClient):
    r = client.post(f'{API_PREFIX}/register', json={
        'username': 'testuser',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    assert r.status_code == 200
    data = _data(r)
    assert isinstance(data, dict)
    assert data['success'] is True
    assert 'user_id' in data
    assert isinstance(data['user_id'], str)
    assert data['role'] == 'user'


def test_register_rejects_password_confirmation_mismatch(client: TestClient):
    r = client.post(f'{API_PREFIX}/register', json={
        'username': 'mismatch',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Bb2@bbbb',
    })

    assert r.status_code == 400
    payload = r.json()
    assert payload['code'] == 1000204
    assert payload['message'] == 'Password confirmation does not match'


def test_register_duplicate(client: TestClient):
    client.post(f'{API_PREFIX}/register', json={
        'username': 'dup',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    r = client.post(f'{API_PREFIX}/register', json={
        'username': 'dup',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    assert r.status_code == 400
    assert 'already exists' in r.json()['message'].lower()


def test_login(client: TestClient):
    client.post(f'{API_PREFIX}/register', json={
        'username': 'logintest',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    r = client.post(f'{API_PREFIX}/login', json={'username': 'logintest', 'password': 'Aa1!aaaa'})
    assert r.status_code == 200
    data = _data(r)
    assert isinstance(data, dict)
    assert 'access_token' in data
    assert 'refresh_token' in data
    assert isinstance(data['access_token'], str)
    assert isinstance(data['refresh_token'], str)
    assert data['role'] == 'user'
    assert data['expires_in'] > 0


def test_login_invalid(client: TestClient):
    r = client.post(f'{API_PREFIX}/login', json={'username': 'nonexistent', 'password': 'wrong'})
    assert r.status_code == 400


def test_validate_no_token(client: TestClient):
    r = client.post(f'{API_PREFIX}/validate')
    assert r.status_code in {401, 403}


def test_validate_with_token(client: TestClient):
    reg = client.post(f'{API_PREFIX}/register', json={
        'username': 'valuser',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    assert reg.status_code == 200
    user_id = _data(reg)['user_id']
    login = client.post(f'{API_PREFIX}/login', json={'username': 'valuser', 'password': 'Aa1!aaaa'})
    token = _data(login)['access_token']
    r = client.post(f'{API_PREFIX}/validate', headers={'Authorization': f'Bearer {token}'})
    assert r.status_code == 200
    data = _data(r)
    assert isinstance(data, dict)
    assert data['sub'] == str(user_id)
    assert isinstance(data['permissions'], list)
    assert 'role' in data
    assert 'permissions' in data


def test_refresh(client: TestClient):
    client.post(f'{API_PREFIX}/register', json={
        'username': 'refuser',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    login = client.post(f'{API_PREFIX}/login', json={'username': 'refuser', 'password': 'Aa1!aaaa'})
    refresh = _data(login)['refresh_token']
    r = client.post(f'{API_PREFIX}/refresh', json={'refresh_token': refresh})
    assert r.status_code == 200
    data = _data(r)
    assert isinstance(data, dict)
    assert 'access_token' in data
    assert 'refresh_token' in data
    assert data['refresh_token'] != refresh  # new token


def test_refresh_requires_token(client: TestClient):
    r = client.post(f'{API_PREFIX}/refresh', json={'refresh_token': '   '})

    assert r.status_code == 401
    payload = r.json()
    assert payload['code'] == 1000203
    assert payload['message'] == 'refresh_token is required'


def test_refresh_invalid(client: TestClient):
    r = client.post(f'{API_PREFIX}/refresh', json={'refresh_token': 'invalid-token'})
    assert r.status_code == 401


def test_logout_rejects_other_users_refresh_token(client: TestClient):
    client.post(f'{API_PREFIX}/register', json={
        'username': 'logout_a',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    client.post(f'{API_PREFIX}/register', json={
        'username': 'logout_b',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })

    login_a = client.post(f'{API_PREFIX}/login', json={'username': 'logout_a', 'password': 'Aa1!aaaa'})
    login_b = client.post(f'{API_PREFIX}/login', json={'username': 'logout_b', 'password': 'Aa1!aaaa'})

    access_token_a = _data(login_a)['access_token']
    refresh_token_b = _data(login_b)['refresh_token']

    logout = client.post(
        f'{API_PREFIX}/logout',
        json={'refresh_token': refresh_token_b},
        headers={'Authorization': f'Bearer {access_token_a}'},
    )
    assert logout.status_code == 401

    refresh = client.post(f'{API_PREFIX}/refresh', json={'refresh_token': refresh_token_b})
    assert refresh.status_code == 200


def test_logout_without_refresh_token_returns_success(client: TestClient):
    client.post(f'{API_PREFIX}/register', json={
        'username': 'logout_none',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    login = client.post(f'{API_PREFIX}/login', json={'username': 'logout_none', 'password': 'Aa1!aaaa'})
    token = _data(login)['access_token']

    resp = client.post(
        f'{API_PREFIX}/logout',
        json={},
        headers={'Authorization': f'Bearer {token}'},
    )

    assert resp.status_code == 200
    assert _data(resp) == {'success': True}


def test_authorize_no_required_permission(client: TestClient):
    """When API_PERMISSIONS_MAP has no entry, allow all."""
    r = client.post(f'{API_PREFIX}/authorize', json={'method': 'GET', 'path': '/unknown'})
    assert r.status_code == 200
    assert isinstance(_data(r), dict)
    assert _data(r)['allowed'] is True


def test_authorize_with_token(client: TestClient):
    """With valid token and user.read permission for /api/hello, authorize returns allowed."""
    client.post(f'{API_PREFIX}/register', json={
        'username': 'authuser',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    login = client.post(f'{API_PREFIX}/login', json={'username': 'authuser', 'password': 'Aa1!aaaa'})
    token = _data(login)['access_token']
    r = client.post(
        f'{API_PREFIX}/authorize',
        json={'method': 'GET', 'path': '/api/hello'},
        headers={'Authorization': f'Bearer {token}'},
    )
    assert r.status_code == 200
    assert isinstance(_data(r), dict)
    assert _data(r)['allowed'] is True



def test_list_groups_scope_for_admin_and_normal_user(client: TestClient):
    from core.database import SessionLocal
    from repositories import GroupRepository, RoleRepository, UserGroupRepository, UserRepository

    admin_reg = client.post('/api/authservice/auth/register', json={
        'username': 'scope_admin',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    assert admin_reg.status_code == 200

    user_reg = client.post('/api/authservice/auth/register', json={
        'username': 'scope_user',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    assert user_reg.status_code == 200

    with SessionLocal() as db:
        admin = UserRepository.get_by_username(db, 'scope_admin')
        normal_user = UserRepository.get_by_username(db, 'scope_user')
        admin_role = RoleRepository.get_by_name(db, 'system-admin')
        admin.role_id = admin_role.id
        db.commit()

        g1 = GroupRepository.create(db, tenant_id=normal_user.tenant_id or '', group_name='team-a', remark='A组')
        g2 = GroupRepository.create(db, tenant_id=normal_user.tenant_id or '', group_name='team-b', remark='B组')
        UserGroupRepository.add(db, tenant_id=normal_user.tenant_id or '', user_id=normal_user.id, group_id=g1.id, role='member')

    admin_login = client.post(f'{API_PREFIX}/login', json={'username': 'scope_admin', 'password': 'Aa1!aaaa'})
    assert admin_login.status_code == 200
    admin_token = _data(admin_login)['access_token']

    user_login = client.post(f'{API_PREFIX}/login', json={'username': 'scope_user', 'password': 'Aa1!aaaa'})
    assert user_login.status_code == 200
    user_token = _data(user_login)['access_token']

    admin_resp = client.get('/api/authservice/group', headers={'Authorization': f'Bearer {admin_token}'})
    assert admin_resp.status_code == 200
    admin_groups = admin_resp.json()['data']['groups']
    assert isinstance(admin_groups, list)
    admin_names = {g['group_name'] for g in admin_groups}
    assert 'team-a' in admin_names
    assert 'team-b' in admin_names

    user_resp = client.get('/api/authservice/group', headers={'Authorization': f'Bearer {user_token}'})
    assert user_resp.status_code == 200
    user_groups = user_resp.json()['data']['groups']
    assert isinstance(user_groups, list)
    assert len(user_groups) == 1
    assert user_groups[0]['group_name'] == 'team-a'


def test_update_me_invalid_phone_format(client: TestClient):
    reg = client.post('/api/authservice/auth/register', json={
        'username': 'phoneuser',
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    assert reg.status_code == 200

    login = client.post(f'{API_PREFIX}/login', json={'username': 'phoneuser', 'password': 'Aa1!aaaa'})
    assert login.status_code == 200
    token = _data(login)['access_token']

    resp = client.patch(
        '/api/authservice/auth/me',
        json={'phone': 'abc-123'},
        headers={'Authorization': f'Bearer {token}'},
    )
    assert resp.status_code == 400
    payload = resp.json()
    assert payload['code'] == 1000209
    assert payload['message'] == 'Invalid phone format'
