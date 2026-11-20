"""Deferred Star HTTP contracts with real ownership, permissions and storage."""
import base64
import importlib
import importlib.util
import json
import os
from contextlib import contextmanager
from datetime import UTC, datetime, timedelta
from types import SimpleNamespace
from unittest.mock import Mock
from uuid import UUID, uuid4

import pytest
from core.cloud_crypto import encrypt_json
from core.deps import current_user_id
from core.errors import ErrorCodes, raise_error
from fastapi.testclient import TestClient
from main import app
from models import Base, CloudAuthConnection, PermissionGroup, Role, User
from services.cloud_oauth_service import CloudOAuthService
from services.github_star_service import GitHubStarService
from sqlalchemy import create_engine, text
from sqlalchemy.engine import make_url
from sqlalchemy.orm import sessionmaker
from test_github_star import REPO, TOKEN, GitHubHTTP

from api import cloud_oauth as cloud_api

ROOT = '/api/authservice/v1/cloud/github/star/deferred'
OWNER = UUID('d390cf35-74db-47f1-a603-1b36b1815325')
OTHER = UUID('f01ae10a-5036-42b2-9c4f-164e3e6f070f')
KEY = base64.urlsafe_b64encode(bytes(range(32))).decode()
START = datetime(2026, 11, 17, 0, 0, tzinfo=UTC)
LOCAL_BODY = {'confirmed': True, 'source': 'local'}


class Clock:
    def __init__(self):
        self.value = START

    def __call__(self):
        return self.value

    def advance(self, **kwargs):
        self.value += timedelta(**kwargs)


class ControlledGitHub(GitHubHTTP):
    """Only the external transport is fake; Star verification remains real."""

    def __init__(self):
        super().__init__()
        self.offline = False
        self.after_response = None

    def __call__(self, req, *args, **kwargs):
        if self.offline:
            self.calls.append((req.get_method(), req.full_url.removeprefix('https://api.github.com'), req))
            raise TimeoutError('fixture home network cannot reach GitHub')
        try:
            return super().__call__(req, *args, **kwargs)
        finally:
            if self.after_response:
                self.after_response(len(self.calls))


def deferred_module():
    """Resolve only at test execution, so absent feature cannot break collection."""
    name = 'services.github_star_deferred'
    assert importlib.util.find_spec(name) is not None, 'Deferred Star service is not implemented yet'
    return importlib.import_module(name)


def intent(result):
    assert result['repository'] == REPO
    assert isinstance(result['intent'], dict)
    return result['intent']


def as_utc(value):
    if isinstance(value, str):
        value = datetime.fromisoformat(value)
    return value.replace(tzinfo=UTC) if value.tzinfo is None else value.astimezone(UTC)


@pytest.fixture
def deferred_env(monkeypatch, tmp_path):
    with isolated_database_engine(tmp_path) as engine:
        yield from _deferred_environment(monkeypatch, engine)


@contextmanager
def isolated_database_engine(tmp_path):
    """Each contract run owns its SQLite file or one PostgreSQL schema."""
    driver = os.environ.get('AUTH_SERVICE_TEST_DB_DRIVER', 'sqlite')
    assert driver in {'sqlite', 'postgres'}, 'unsupported AuthService test DB driver'
    if driver == 'sqlite':
        engine = create_engine(f'sqlite:///{tmp_path / "deferred.db"}',
                               connect_args={'check_same_thread': False})
        try:
            yield engine
        finally:
            engine.dispose()
        return
    dsn = os.environ.get('AUTH_SERVICE_TEST_POSTGRES_DSN')
    assert dsn, 'explicit PostgreSQL run requires AUTH_SERVICE_TEST_POSTGRES_DSN'
    admin_url = make_url(dsn).set(drivername='postgresql+psycopg')
    admin = create_engine(admin_url, isolation_level='AUTOCOMMIT')
    schema = 'star_deferred_test_' + uuid4().hex
    with admin.connect() as connection:
        connection.execute(text(f'CREATE SCHEMA "{schema}"'))
    scoped = admin_url.set(query=dict(admin_url.query, options=f'-c search_path={schema}'))
    engine = create_engine(scoped)
    try:
        yield engine
    finally:
        engine.dispose()
        with admin.connect() as connection:
            connection.execute(text(f'DROP SCHEMA "{schema}" CASCADE'))
        admin.dispose()


def _deferred_environment(monkeypatch, engine):
    module_name = 'services.github_star_deferred'
    module = importlib.import_module(module_name) if importlib.util.find_spec(module_name) else None
    monkeypatch.setenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY', KEY)
    monkeypatch.setenv('LAZYMIND_RUNTIME_MODE', 'local')
    monkeypatch.setenv('WEB_CONCURRENCY', '1')
    Base.metadata.create_all(engine)
    sessions = sessionmaker(bind=engine, expire_on_commit=False)
    with sessions() as db:
        permissions = [PermissionGroup(code=code, module='model', action=code.split('.')[1])
                       for code in ('model.read', 'model.write')]
        role = Role(name='deferred-fixture-writer', permission_groups=permissions)
        db.add_all([role, User(id=OWNER, username='deferred-owner', password_hash='fixture', role=role),
                    User(id=OTHER, username='deferred-other', password_hash='fixture', role=role)])
        db.commit()
        role_id = role.id

    monkeypatch.setattr('core.deps.SessionLocal', sessions)
    monkeypatch.setattr(importlib.import_module('services.cloud_oauth_service'), 'SessionLocal', sessions)
    connections = CloudOAuthService()
    monkeypatch.setattr(connections, '_cache_get', lambda *_: None)
    monkeypatch.setattr(connections, '_cache_set', lambda *_: None)
    github = ControlledGitHub()
    monkeypatch.setattr('services.github_star_service.request.urlopen', github)
    star_service = GitHubStarService(connections=connections)
    monkeypatch.setattr(cloud_api, 'github_star_service', star_service)
    probe = Mock(return_value={'access_token': TOKEN})
    cli = cloud_api.github_cli_auth_service
    monkeypatch.setattr(cli, 'host_credentials_enabled', True)
    monkeypatch.setattr(cli.runner, 'probe', probe)

    def forbidden(*args, **kwargs):
        pytest.fail('deferred Star must not start OAuth or save a temporary local token')

    monkeypatch.setattr(cli.runner, 'authorize', forbidden)
    monkeypatch.setattr(connections, 'save_github_cli_token', forbidden)
    clock, identity = Clock(), {'user_id': OWNER}
    overrides = dict(app.dependency_overrides)
    app.dependency_overrides[current_user_id] = lambda: identity['user_id']

    def build():
        selected = module or deferred_module()
        return selected.GitHubStarDeferredService(
            session_factory=sessions, star_service=star_service, probe=probe,
            enabled=True, clock=clock,
        )

    service = build() if module else None
    if service:
        monkeypatch.setattr(cloud_api, 'github_star_deferred_service', service, raising=False)
    client = TestClient(app, raise_server_exceptions=False)

    def seed(connection_id='conn-deferred', owner=OWNER, account_id='10001', status='ACTIVE'):
        with sessions() as db:
            db.add(CloudAuthConnection(
                connection_id=connection_id, owner_user_id=str(owner), provider='github',
                auth_mode='oauth_user', provider_account_id=account_id, display_name='fixture-user',
                scope='repo', status=status,
                credential_ciphertext=encrypt_json({'provider_options': {}}),
                auth_state_ciphertext=encrypt_json({'token_source': 'github_cli', 'access_token': TOKEN}),
            ))
            db.commit()

    def set_permissions(codes):
        with sessions() as db:
            role = db.get(Role, role_id)
            role.permission_groups = db.query(PermissionGroup).filter(PermissionGroup.code.in_(codes)).all()
            db.commit()

    def row():
        with engine.connect() as connection:
            return dict(connection.execute(text('SELECT * FROM github_star_intents')).mappings().one())

    yield SimpleNamespace(service=service, build=build, github=github, probe=probe, clock=clock,
                          client=client, identity=identity, sessions=sessions, engine=engine,
                          seed=seed, set_permissions=set_permissions, row=row)
    client.close()
    app.dependency_overrides.clear()
    app.dependency_overrides.update(overrides)


def require_service(env):
    return env.service or env.build()


def receive(env, **overrides):
    body = dict(LOCAL_BODY, **overrides)
    response = env.client.post(ROOT, json=body)
    assert response.status_code == 200, response.text
    assert response.json()['data']['accepted'] is True
    return intent(response.json()['data'])


def current(env):
    return intent(require_service(env).status(owner_user_id=str(OWNER)))


def assert_no_history(env):
    with env.engine.connect() as connection:
        assert connection.execute(text('SELECT COUNT(*) FROM github_star_intents')).scalar_one() == 0


def test_real_http_accepts_offline_intent_without_claiming_remote_success(deferred_env, caplog):
    env = deferred_env
    env.github.offline = True
    accepted = receive(env)
    assert accepted['status'] == 'pending'
    assert accepted['remote_state'] == 'unknown'
    assert accepted['attempt_count'] == 0
    assert as_utc(accepted['expires_at']) - as_utc(accepted['created_at']) == timedelta(days=7)
    assert env.github.calls == []
    assert TOKEN not in json.dumps(accepted) and TOKEN not in caplog.text
    assert 'credential_fingerprint' not in accepted
    with env.sessions() as db:
        assert db.query(CloudAuthConnection).count() == 0


def test_real_http_get_is_read_only_and_exposes_owner_history(deferred_env):
    env = deferred_env
    response = env.client.get(ROOT)
    assert response.status_code == 200
    data = response.json()['data']
    assert data['repository'] == REPO and data['intent'] is None
    assert data['can_receive'] is True and data['local_supported'] is True
    assert data['accounts'] == []
    assert 'no-store' in response.headers.get('cache-control', '')
    env.probe.assert_not_called()
    assert env.github.calls == []


@pytest.mark.parametrize('body', [
    {}, {'source': 'local'}, {'source': 'local', 'confirmed': False},
    {'source': 'local', 'confirmed': 'true'}, {'source': 'local', 'confirmed': 1},
    dict(LOCAL_BODY, repository='other/repository'), dict(LOCAL_BODY, owner_user_id=str(OTHER)),
    dict(LOCAL_BODY, token=TOKEN), dict(LOCAL_BODY, source='untrusted'),
    dict(LOCAL_BODY, connection_id='conn-deferred'), dict(LOCAL_BODY, expected_account_id='10001'),
    {'confirmed': True, 'source': 'connection'},
])
def test_invalid_confirmation_or_client_target_cannot_probe_or_record(deferred_env, body):
    env = deferred_env
    response = env.client.post(ROOT, json=body)
    assert response.status_code in (400, 422)
    env.probe.assert_not_called()
    assert env.github.calls == []
    assert_no_history(env)


@pytest.mark.parametrize('method', ['get', 'post', 'delete'])
def test_http_requires_login_before_any_probe(deferred_env, method):
    env = deferred_env

    def anonymous():
        raise_error(ErrorCodes.UNAUTHORIZED)

    app.dependency_overrides[current_user_id] = anonymous
    kwargs = {'json': LOCAL_BODY} if method == 'post' else {'params': {'intent_id': 'other'}}
    response = getattr(env.client, method)(ROOT, **kwargs)
    assert response.status_code == 401
    env.probe.assert_not_called()
    assert env.github.calls == []
    assert_no_history(env)


@pytest.mark.parametrize('method', ['post', 'delete'])
def test_http_mutation_requires_real_write_permission(deferred_env, method):
    env = deferred_env
    env.set_permissions(['model.read'])
    kwargs = {'json': LOCAL_BODY} if method == 'post' else {'params': {'intent_id': 'fixture'}}
    assert getattr(env.client, method)(ROOT, **kwargs).status_code == 403
    env.probe.assert_not_called()
    assert_no_history(env)


def test_http_read_capability_does_not_grant_write_permission(deferred_env):
    env = deferred_env
    env.set_permissions(['model.read'])
    response = env.client.get(ROOT)
    assert response.status_code == 200
    assert response.json()['data']['can_receive'] is False
    env.probe.assert_not_called()


def test_unsupported_runtime_gate_prevents_local_credential_access(deferred_env, monkeypatch):
    env = deferred_env
    service = deferred_module().GitHubStarDeferredService(
        session_factory=env.sessions, star_service=cloud_api.github_star_service,
        probe=env.probe, enabled=False, clock=env.clock,
    )
    monkeypatch.setattr(cloud_api, 'github_star_deferred_service', service, raising=False)
    response = env.client.get(ROOT)
    assert response.status_code == 200
    assert response.json()['data']['can_receive'] is False
    assert response.json()['data']['local_supported'] is False
    assert env.client.post(ROOT, json=LOCAL_BODY).status_code in (403, 409, 503)
    env.probe.assert_not_called()
    assert env.github.calls == []


def test_saved_connection_uses_real_owner_check_and_stable_account(deferred_env):
    env = deferred_env
    env.seed(owner=OTHER)
    body = {'confirmed': True, 'source': 'connection', 'connection_id': 'conn-deferred',
            'expected_account_id': '10001'}
    assert env.client.post(ROOT, json=body).status_code == 403
    assert env.github.calls == []
    env.probe.assert_not_called()
    assert_no_history(env)


def test_new_saved_connection_consent_cannot_bind_a_different_account(deferred_env):
    env = deferred_env
    env.seed()
    response = env.client.post(ROOT, json={
        'confirmed': True, 'source': 'connection', 'connection_id': 'conn-deferred',
        'expected_account_id': '20002',
    })
    assert response.status_code == 409
    assert env.github.calls == []
    env.probe.assert_not_called()
    assert_no_history(env)


def test_history_is_private_and_foreign_cancel_does_not_modify_it(deferred_env):
    env = deferred_env
    accepted = receive(env)
    env.identity['user_id'] = OTHER
    response = env.client.get(ROOT)
    assert response.status_code == 200
    assert response.json()['data']['intent'] is None
    response = env.client.delete(ROOT, params={'intent_id': accepted['intent_id']})
    assert response.status_code in (403, 404)
    env.identity['user_id'] = OWNER
    assert env.client.get(ROOT).json()['data']['intent']['status'] == 'pending'


def test_cancellation_keeps_history_and_duplicate_post_cannot_restart_week(deferred_env):
    env = deferred_env
    accepted = receive(env)
    response = env.client.delete(ROOT, params={'intent_id': accepted['intent_id']})
    assert response.status_code == 200
    cancelled = intent(response.json()['data'])
    assert cancelled['status'] == 'cancelled'
    calls = env.probe.call_count
    env.clock.advance(days=8)
    repeated = receive(env)
    for field in ('intent_id', 'created_at', 'expires_at', 'attempt_count'):
        assert repeated[field] == accepted[field]
    assert repeated['status'] == 'cancelled'
    assert env.probe.call_count == calls
    require_service(env).run_due_once()
    assert env.github.calls == []
