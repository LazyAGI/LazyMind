"""Star contract tests: real connection ownership/storage, isolated GitHub HTTP."""
import importlib
import io
import json
import threading
from concurrent.futures import ThreadPoolExecutor
from types import SimpleNamespace
from urllib.error import HTTPError

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker

from api import cloud_oauth as cloud_api
from core.cloud_crypto import encrypt_json
from core.deps import current_user
from core.errors import AppException, ErrorCodes, raise_error
from main import app
from models import Base, CloudAuthConnection
from services.cloud_oauth_service import CloudOAuthService
from services.github_star_service import GitHubStarService


ROOT = '/api/authservice/v1/cloud/github/star'
REPO = 'LazyAGI/LazyMind'
STAR_PATH = '/user/starred/' + REPO
TOKEN = 'fixture-star-token-not-a-real-credential'
OWNER = 'star-owner'


class GitHubHTTP:
    def __init__(self):
        self.calls = []
        self.starred = False
        self.account_id = '10001'
        self.put_error = None
        self.get_error = None
        self.repo_error = None
        self.apply_before_error = False
        self.fail_read_after_put = False
        self.put_entered = threading.Event()
        self.release_put = None

    def __call__(self, req, *args, **kwargs):
        from urllib.parse import urlparse
        parsed = urlparse(req.full_url)
        assert parsed.scheme == 'https' and parsed.netloc == 'api.github.com'
        method, path = req.get_method(), parsed.path
        self.calls.append((method, path, req))
        assert req.get_header('Authorization') in ('Bearer ' + TOKEN, 'token ' + TOKEN)
        headers = {'X-OAuth-Scopes': 'repo', 'Content-Type': 'application/json'}
        if path == '/user' and method == 'GET':
            body = {'id': int(self.account_id), 'login': 'fixture-user'}
        elif path == '/repos/' + REPO and method == 'GET':
            if self.repo_error:
                raise self.repo_error
            body = {'id': 999, 'full_name': REPO, 'private': False}
        elif path == STAR_PATH and method == 'GET':
            if self.get_error or (self.fail_read_after_put and self.put_entered.is_set()):
                raise self.get_error or TimeoutError('fixture read timed out')
            if not self.starred:
                raise HTTPError(req.full_url, 404, 'Not Found', headers, io.BytesIO(b'{}'))
            return self.response(204, {}, headers)
        elif path == STAR_PATH and method == 'PUT':
            self.put_entered.set()
            if self.release_put is not None:
                assert self.release_put.wait(5), 'test did not release controlled PUT'
            assert req.data in (None, b'')
            assert req.get_header('Content-length') == '0'
            if self.apply_before_error:
                self.starred = True
            if self.put_error:
                raise self.put_error
            self.starred = True
            return self.response(204, {}, headers)
        else:
            raise AssertionError(f'unexpected GitHub request: {method} {path}')
        return self.response(200, body, headers)

    @staticmethod
    def response(status, body, headers):
        response = io.BytesIO(json.dumps(body).encode())
        response.status = status
        response.headers = headers
        response.getcode = lambda: status
        return response

    def writes(self):
        return [call for call in self.calls if call[0] != 'GET']


@pytest.fixture
def star_env(monkeypatch, tmp_path):
    monkeypatch.setenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'fixture-star-encryption-key')
    engine = create_engine(f'sqlite:///{tmp_path}/connections.db', connect_args={'check_same_thread': False})
    Base.metadata.create_all(engine)
    sessions = sessionmaker(bind=engine, expire_on_commit=False)
    connection_module = importlib.import_module('services.cloud_oauth_service')
    monkeypatch.setattr(connection_module, 'SessionLocal', sessions)
    connections = CloudOAuthService()
    # Cache infrastructure is unrelated; keep real ownership and decryption.
    monkeypatch.setattr(connections, '_cache_get', lambda *_: None)
    monkeypatch.setattr(connections, '_cache_set', lambda *_: None)

    def seed(connection_id='conn-1', owner=OWNER, provider='github', status='ACTIVE', account_id='10001'):
        with sessions() as db:
            db.add(CloudAuthConnection(
                connection_id=connection_id, owner_user_id=owner, tenant_id='',
                provider=provider, auth_mode='oauth_user', connection_method='legacy_byo',
                credential_location='local', provider_account_id=account_id,
                display_name='Fixture GitHub', scope='repo', status=status,
                credential_ciphertext=encrypt_json({'provider_options': {}}),
                auth_state_ciphertext=encrypt_json({
                    'token_source': 'github_cli', 'access_token': TOKEN,
                    'access_expires_at': '', 'refresh_token': '',
                }),
            ))
            db.commit()

    github = GitHubHTTP()
    star_module = importlib.import_module('services.github_star_service')
    monkeypatch.setattr(star_module.request, 'urlopen', github)
    service = GitHubStarService(connections=connections)
    viewer = SimpleNamespace(id=OWNER, role=SimpleNamespace(name='system-admin'))
    previous_overrides = dict(app.dependency_overrides)
    app.dependency_overrides[current_user] = lambda: viewer
    monkeypatch.setattr(cloud_api, 'github_star_service', service, raising=False)
    client = TestClient(app, raise_server_exceptions=False)
    yield SimpleNamespace(service=service, connections=connections, github=github,
                          seed=seed, sessions=sessions, client=client, viewer=viewer)
    client.close()
    app.dependency_overrides.clear()
    app.dependency_overrides.update(previous_overrides)
    engine.dispose()


def star(env, **overrides):
    args = dict(owner_user_id=OWNER, connection_id='conn-1', expected_account_id='10001', confirmed=True)
    args.update(overrides)
    return env.service.star(**args)


def test_connection_fixture_uses_real_decryption_and_owner_isolation(star_env):
    star_env.seed()
    assert star_env.connections.get_access_token('conn-1', user_id=OWNER)['access_token'] == TOKEN
    with pytest.raises(AppException) as error:
        star_env.connections.get_access_token('conn-1', user_id='other-owner')
    assert error.value.http_code == 403
    assert star_env.github.calls == []


def test_no_connection_is_read_only_and_does_not_start_authentication(star_env, monkeypatch):
    def forbidden(*args, **kwargs):
        pytest.fail('read-only Star status must not start authentication')
    monkeypatch.setattr(cloud_api.github_cli_auth_service, 'start', forbidden)
    assert star_env.service.status(owner_user_id=OWNER) == {'repository': REPO, 'accounts': []}
    assert star_env.github.calls == []


@pytest.mark.parametrize('already_starred', [False, True])
def test_status_uses_real_owned_connection_and_never_writes(star_env, already_starred):
    star_env.seed()
    star_env.github.starred = already_starred
    result = star_env.service.status(owner_user_id=OWNER, connection_id='conn-1')
    assert result['repository'] == REPO
    account = result['accounts'][0]
    assert (account['connection_id'], account['account_id'], account['login']) == ('conn-1', '10001', 'fixture-user')
    assert account['state'] == ('starred' if already_starred else 'not_starred')
    assert star_env.github.writes() == []
    assert TOKEN not in json.dumps(result)


def test_confirmed_star_is_get_put_get_and_returns_verified_success(star_env):
    star_env.seed()
    result = star(star_env)
    assert result['state'] == 'starred' and result['write_performed'] is True
    assert result['account_id'] == '10001' and result['repository'] == REPO
    assert [(method, path) for method, path, _ in star_env.github.calls if path == STAR_PATH] == [
        ('GET', STAR_PATH), ('PUT', STAR_PATH), ('GET', STAR_PATH),
    ]
    assert TOKEN not in json.dumps(result)


def test_existing_star_is_not_written_again(star_env):
    star_env.seed()
    star_env.github.starred = True
    result = star(star_env)
    assert result['state'] == 'starred' and result['write_performed'] is False
    assert star_env.github.writes() == []


@pytest.mark.parametrize('operation', ['status', 'star'])
def test_foreign_connection_is_denied_by_real_owner_check_before_http(star_env, operation):
    star_env.seed(owner='other-owner')
    with pytest.raises(AppException) as error:
        if operation == 'status':
            star_env.service.status(owner_user_id=OWNER, connection_id='conn-1')
        else:
            star(star_env)
    assert error.value.http_code == 403
    assert star_env.github.calls == []


@pytest.mark.parametrize('status', ['REVOKED', 'PENDING', 'FAILED'])
def test_inactive_connection_never_writes(star_env, status):
    star_env.seed(status=status)
    with pytest.raises(AppException):
        star(star_env)
    assert star_env.github.writes() == []


def test_non_github_connection_is_rejected_before_http(star_env):
    star_env.seed(provider='notion')
    with pytest.raises(AppException):
        star(star_env)
    assert star_env.github.calls == []


@pytest.mark.parametrize('expected_id,actual_id', [('20002', '10001'), ('10001', '20002')])
def test_confirmed_saved_and_actual_account_must_all_match(star_env, expected_id, actual_id):
    star_env.seed()
    star_env.github.account_id = actual_id
    with pytest.raises(AppException) as error:
        star(star_env, expected_account_id=expected_id)
    assert error.value.http_code == 409
    assert star_env.github.writes() == []


def test_unconfirmed_service_call_cannot_read_credentials_or_write(star_env, monkeypatch):
    star_env.seed()
    def forbidden(*args, **kwargs):
        pytest.fail('unconfirmed operation reached credentials')
    monkeypatch.setattr(star_env.connections, 'get_access_token', forbidden)
    with pytest.raises(AppException) as error:
        star(star_env, confirmed=False)
    assert error.value.http_code == 400
    assert star_env.github.calls == []


@pytest.mark.parametrize('status', [401, 403, 429, 500])
def test_failed_star_status_is_not_treated_as_unstarred(star_env, status):
    star_env.seed()
    star_env.github.get_error = HTTPError('https://api.github.com' + STAR_PATH, status, 'fixture', {}, io.BytesIO(b'{}'))
    result = star_env.service.status(owner_user_id=OWNER, connection_id='conn-1')
    assert result['accounts'][0]['state'] == 'unknown'
    assert result['accounts'][0]['eligible'] is False
    assert star_env.github.writes() == []


def test_timeout_after_applied_write_is_reconciled_without_replaying_put(star_env):
    star_env.seed()
    star_env.github.put_error = TimeoutError('fixture response lost')
    star_env.github.apply_before_error = True
    result = star(star_env)
    assert result['state'] == 'starred'
    assert len(star_env.github.writes()) == 1
    assert star_env.github.calls[-1][:2] == ('GET', STAR_PATH)


def test_unconfirmed_remote_result_is_not_reported_as_success(star_env):
    star_env.seed()
    star_env.github.put_error = TimeoutError('fixture response lost')
    star_env.github.fail_read_after_put = True
    with pytest.raises(AppException) as error:
        star(star_env)
    assert error.value.http_code == 503
    assert len(star_env.github.writes()) == 1


def test_repository_access_failure_is_not_confused_with_not_starred(star_env):
    star_env.seed()
    star_env.github.repo_error = HTTPError('https://api.github.com/repos/' + REPO, 404, 'fixture', {}, io.BytesIO(b'{}'))
    result = star_env.service.status(owner_user_id=OWNER, connection_id='conn-1')
    assert result['accounts'][0]['state'] == 'unknown'
    assert result['accounts'][0]['eligible'] is False
    with pytest.raises(AppException):
        star(star_env)
    assert star_env.github.writes() == []


def test_denied_write_is_not_reported_as_success_and_does_not_leak_error_details(star_env):
    star_env.seed()
    star_env.github.put_error = HTTPError('https://api.github.com' + STAR_PATH, 403, TOKEN, {}, io.BytesIO(TOKEN.encode()))
    with pytest.raises(AppException) as error:
        star(star_env)
    assert error.value.http_code == 403
    assert TOKEN not in str(error.value)
    assert TOKEN not in (error.value.extra or '')
    assert len(star_env.github.writes()) == 1


def test_concurrent_requests_for_same_account_do_not_issue_two_puts(star_env):
    star_env.seed()
    star_env.github.release_put = threading.Event()
    with ThreadPoolExecutor(max_workers=2) as pool:
        first = pool.submit(star, star_env)
        try:
            assert star_env.github.put_entered.wait(2), 'Star never reached the controlled HTTP boundary'
            second = pool.submit(star, star_env)
        finally:
            star_env.github.release_put.set()
        results = [first.result(timeout=5), second.result(timeout=5)]
    assert [result['state'] for result in results] == ['starred', 'starred']
    assert len(star_env.github.writes()) == 1


def test_real_http_route_returns_safe_star_status(star_env):
    star_env.seed()
    response = star_env.client.get(ROOT, params={'connection_id': 'conn-1'})
    assert response.status_code == 200
    assert 'no-store' in response.headers.get('cache-control', '')
    assert 'not_starred' in response.text
    assert TOKEN not in response.text
    assert star_env.github.writes() == []


def test_real_http_put_uses_authenticated_owner_and_fixed_target(star_env):
    star_env.seed()
    response = star_env.client.put(ROOT, json={
        'connection_id': 'conn-1', 'expected_account_id': '10001', 'confirmed': True,
    })
    assert response.status_code == 200
    assert 'no-store' in response.headers.get('cache-control', '')
    assert response.json()['data']['state'] == 'starred'
    assert TOKEN not in response.text


@pytest.mark.parametrize('body', [
    {}, {'connection_id': 'conn-1', 'expected_account_id': '10001'},
    {'connection_id': 'conn-1', 'expected_account_id': '10001', 'confirmed': False},
    {'connection_id': 'conn-1', 'expected_account_id': '10001', 'confirmed': 'true'},
    {'connection_id': 'conn-1', 'expected_account_id': '10001', 'confirmed': True, 'repository': 'other/repo'},
    {'connection_id': 'conn-1', 'expected_account_id': '10001', 'confirmed': True, 'owner_user_id': 'other'},
])
def test_invalid_http_confirmation_cannot_write(star_env, body):
    star_env.seed()
    response = star_env.client.put(ROOT, json=body)
    assert response.status_code in (400, 422)
    assert star_env.github.writes() == []


@pytest.mark.parametrize('method', ['get', 'put'])
def test_http_route_requires_login(star_env, method):
    def anonymous():
        raise_error(ErrorCodes.UNAUTHORIZED)
    app.dependency_overrides[current_user] = anonymous
    kwargs = {'json': {'connection_id': 'conn-1', 'expected_account_id': '10001', 'confirmed': True}} if method == 'put' else {}
    response = getattr(star_env.client, method)(ROOT, **kwargs)
    assert response.status_code == 401
    assert star_env.github.calls == []


def test_http_put_requires_write_permission(star_env, monkeypatch):
    star_env.viewer.role.name = 'fixture-reader'
    monkeypatch.setattr('core.rbac.get_effective_permission_codes', lambda *_: {'model.read'})
    response = star_env.client.put(ROOT, json={'connection_id': 'conn-1', 'expected_account_id': '10001', 'confirmed': True})
    assert response.status_code == 403
    assert star_env.github.writes() == []


def test_http_get_requires_read_permission(star_env, monkeypatch):
    star_env.viewer.role.name = 'fixture-no-permission'
    monkeypatch.setattr('core.rbac.get_effective_permission_codes', lambda *_: set())
    assert star_env.client.get(ROOT).status_code == 403
    assert star_env.github.calls == []


def test_http_unknown_result_has_explicit_feedback_without_false_success(star_env):
    star_env.seed()
    star_env.github.put_error = TimeoutError('fixture response lost: ' + TOKEN)
    star_env.github.fail_read_after_put = True
    response = star_env.client.put(ROOT, json={'connection_id': 'conn-1', 'expected_account_id': '10001', 'confirmed': True})
    assert response.status_code == 503
    assert response.json()['reason'] == 'result_unknown'
    assert TOKEN not in response.text
    assert response.json().get('state') != 'starred'


def test_no_unstar_route_is_exposed(star_env):
    response = star_env.client.delete(ROOT)
    assert response.status_code == 405
    assert star_env.github.writes() == []
