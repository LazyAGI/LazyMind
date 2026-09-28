"""Password ownership through real HTTP authentication and an isolated database."""
import importlib
from types import SimpleNamespace

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

import api.auth as auth_api
import core.deps as deps
from main import app
from models import Base, User
from repositories import PermissionGroupRepository, RoleRepository, UserRepository
from services.auth_service import auth_service, login_rate_limiter


PREFIX = '/api/authservice'
OLD_PASSWORD = 'Fixture1!old'
NEW_PASSWORD = 'Fixture2!new'


@pytest.fixture
def password_env(monkeypatch):
    engine = create_engine(
        'sqlite:///:memory:', connect_args={'check_same_thread': False},
        poolclass=StaticPool,
    )
    Base.metadata.create_all(engine)
    sessions = sessionmaker(bind=engine)
    user_service_module = importlib.import_module('services.user_service')
    for module in (auth_api, deps, user_service_module):
        monkeypatch.setattr(module, 'SessionLocal', sessions)
    # Only Redis-backed rate limits and refresh-token storage are stubbed.
    monkeypatch.setattr(login_rate_limiter, 'is_limited', lambda _: False)
    monkeypatch.setattr(login_rate_limiter, 'record_failure', lambda _: None)
    monkeypatch.setattr(auth_api, 'set_refresh_token', lambda *_: None)
    ids = {}
    with sessions() as db:
        permission = PermissionGroupRepository.create(db, code='user.admin')
        for name in ('system-admin', 'user', 'delegated-admin'):
            role = RoleRepository.create(db, name)
            if name == 'delegated-admin':
                RoleRepository.replace_permissions(db, role.id, {permission.id})
            user = UserRepository.create(
                db, username=name, role_id=role.id,
                password_hash=auth_service.hash_password(OLD_PASSWORD),
            )
            ids[name] = str(user.id)
        target = UserRepository.create(
            db, username='target-user',
            role_id=RoleRepository.get_by_name(db, 'user').id,
            password_hash=auth_service.hash_password(OLD_PASSWORD),
        )
        ids['target-user'] = str(target.id)
    # No lifespan: avoid migrations, bootstrap and background services.
    client = TestClient(app)
    yield SimpleNamespace(client=client, sessions=sessions, ids=ids)
    client.close()
    engine.dispose()


def _login(env, username, password=OLD_PASSWORD):
    return env.client.post(f'{PREFIX}/auth/login', json={
        'username': username, 'password': password,
    })


def _headers(env, username):
    response = _login(env, username)
    assert response.status_code == 200
    token = response.json()['data']['access_token']
    return {'Authorization': f'Bearer {token}'}


def _password_state(env):
    with env.sessions() as db:
        return {
            str(user.id): (user.password_hash, user.updated_pwd_time)
            for user in db.query(User).all()
        }


@pytest.mark.parametrize('actor', ['system-admin', 'user', 'delegated-admin'])
@pytest.mark.parametrize('target_self', [False, True], ids=['other', 'self'])
def test_legacy_reset_route_is_unavailable(password_env, actor, target_self):
    env = password_env
    headers = _headers(env, actor)
    target = env.ids[actor if target_self else 'target-user']
    before = _password_state(env)
    response = env.client.patch(f'{PREFIX}/user/{target}/reset_password',
                               headers=headers, json={'new_password': NEW_PASSWORD})
    after = _password_state(env)
    # Check persisted side effects even when the HTTP result is also incorrect.
    assert (response.status_code, after == before) == (404, True)


@pytest.mark.parametrize('actor', ['system-admin', 'user', 'delegated-admin'])
def test_change_password_only_updates_authenticated_user(password_env, actor):
    env = password_env
    headers = _headers(env, actor)
    before = _password_state(env)
    response = env.client.post(f'{PREFIX}/auth/change_password', headers=headers, json={
        'old_password': OLD_PASSWORD, 'new_password': NEW_PASSWORD,
        'user_id': env.ids['target-user'],
    })
    assert response.status_code == 200
    assert response.json()['data']['success'] is True
    after = _password_state(env)
    actor_id = env.ids[actor]
    assert after[actor_id][0] != before[actor_id][0]
    assert after[actor_id][1] is not None
    assert {uid: state for uid, state in after.items() if uid != actor_id} == {
        uid: state for uid, state in before.items() if uid != actor_id
    }
    assert _login(env, actor, NEW_PASSWORD).status_code == 200
    old_login = _login(env, actor, OLD_PASSWORD)
    assert old_login.status_code == 400
    assert old_login.json()['code'] == 1000105
    assert _login(env, 'target-user', OLD_PASSWORD).status_code == 200
    target_login = _login(env, 'target-user', NEW_PASSWORD)
    assert target_login.status_code == 400
    assert target_login.json()['code'] == 1000105


@pytest.mark.parametrize('old_password,new_password,code', [
    ('Fixture0!wrong', NEW_PASSWORD, 1000205),
    (OLD_PASSWORD, ' ', 1000206),
    (OLD_PASSWORD, OLD_PASSWORD, 1000208),
    (OLD_PASSWORD, 'weak', 1000103),
])
def test_invalid_password_change_preserves_passwords(password_env, old_password, new_password, code):
    env = password_env
    headers = _headers(env, 'system-admin')
    before = _password_state(env)
    response = env.client.post(f'{PREFIX}/auth/change_password', headers=headers, json={
        'old_password': old_password, 'new_password': new_password,
    })
    assert response.status_code == 400
    assert response.json()['code'] == code
    assert _password_state(env) == before


def test_password_change_requires_authentication(password_env):
    env = password_env
    before = _password_state(env)
    response = env.client.post(f'{PREFIX}/auth/change_password', json={
        'old_password': OLD_PASSWORD, 'new_password': NEW_PASSWORD,
    })
    assert response.status_code == 401
    assert _password_state(env) == before
