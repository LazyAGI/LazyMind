"""Temporary host credentials can Star without creating an App connection."""
import importlib
import io
import threading
from concurrent.futures import ThreadPoolExecutor
from unittest.mock import Mock
from urllib.error import HTTPError

import pytest

from test_github_star import REPO, ROOT, STAR_PATH, TOKEN, star_env  # noqa: F401
from api import cloud_oauth as cloud_api
from core.deps import current_user
from core.errors import ErrorCodes, raise_error
from main import app
from models import CloudAuthConnection
from services.github_cli_runner import GitHubCLIError


LOCAL_ROOT = ROOT + '/local'


@pytest.fixture
def local_env(star_env, monkeypatch):  # noqa: F811
    cli = cloud_api.github_cli_auth_service
    monkeypatch.setattr(cli, 'host_credentials_enabled', True)
    star_env.probe = Mock(return_value={'access_token': TOKEN})
    monkeypatch.setattr(cli.runner, 'probe', star_env.probe)

    def forbidden(*args, **kwargs):
        pytest.fail('temporary Star must not save credentials or start authorization')

    monkeypatch.setattr(star_env.connections, 'save_github_cli_token', forbidden)
    monkeypatch.setattr(cli.runner, 'authorize', forbidden)
    return star_env


def local_star(env):
    return env.client.put(LOCAL_ROOT, json={'confirmed': True})


@pytest.mark.parametrize('already_starred', [False, True])
def test_local_star_uses_host_token_without_persisting_or_returning_it(local_env, already_starred, caplog):
    local_env.github.starred = already_starred
    response = local_star(local_env)
    assert response.status_code == 200
    assert response.json()['data'] == {
        'repository': REPO, 'state': 'starred', 'account_id': '10001',
        'login': 'fixture-user', 'write_performed': not already_starred,
    }
    assert 'no-store' in response.headers['cache-control']
    assert TOKEN not in response.text and 'connection_id' not in response.text
    assert TOKEN not in caplog.text
    with local_env.sessions() as db:
        assert db.query(CloudAuthConnection).count() == 0
    assert len(local_env.github.writes()) == (0 if already_starred else 1)
    assert [method for method, path, _ in local_env.github.calls if path == STAR_PATH] == (
        ['GET'] if already_starred else ['GET', 'PUT', 'GET']
    )


def test_local_star_does_not_update_an_existing_unusable_connection(local_env):
    local_env.seed(status='EXPIRED')
    with local_env.sessions() as db:
        row = db.query(CloudAuthConnection).one()
        before = (row.status, row.credential_ciphertext, row.auth_state_ciphertext, row.updated_at)
    assert local_star(local_env).json()['data']['state'] == 'starred'
    with local_env.sessions() as db:
        row = db.query(CloudAuthConnection).one()
        assert (row.status, row.credential_ciphertext, row.auth_state_ciphertext, row.updated_at) == before


@pytest.mark.parametrize('failure', [
    None, 'GITHUB_CREDENTIAL_UNREADABLE', 'GITHUB_ACCOUNT_SELECTION_REQUIRED', 'GITHUB_CLI_UNAVAILABLE',
])
def test_missing_or_unreadable_local_credentials_require_connection(local_env, failure):
    local_env.probe.return_value = None
    if failure:
        local_env.probe.side_effect = GitHubCLIError(failure)
    response = local_star(local_env)
    assert response.status_code == 200
    assert response.json()['data'] == {
        'repository': REPO, 'state': 'connection_required', 'account_id': '',
        'login': '', 'write_performed': False,
    }
    assert local_env.github.calls == []


def test_host_gate_precedes_any_credential_access(local_env, monkeypatch):
    monkeypatch.setattr(cloud_api.github_cli_auth_service, 'host_credentials_enabled', False)
    assert local_star(local_env).json()['data']['state'] == 'connection_required'
    local_env.probe.assert_not_called()
    assert local_env.github.calls == []


@pytest.mark.parametrize('problem', ['invalid', 'forbidden', 'missing_scope', 'read_only_scope'])
def test_unusable_local_identity_or_scope_requires_connection(local_env, monkeypatch, problem):
    def github(req, *args, **kwargs):
        if req.full_url.endswith('/user') and problem in {'invalid', 'forbidden'}:
            status = 401 if problem == 'invalid' else 403
            raise HTTPError(req.full_url, status, 'fixture', {}, io.BytesIO(b'{}'))
        result = local_env.github(req, *args, **kwargs)
        if req.full_url.endswith('/user'):
            if problem == 'missing_scope':
                result.headers.pop('X-OAuth-Scopes', None)
            elif problem == 'read_only_scope':
                result.headers['X-OAuth-Scopes'] = 'read:user'
        return result
    monkeypatch.setattr(importlib.import_module('services.github_star_service').request, 'urlopen', github)
    response = local_star(local_env)
    assert response.status_code == 200
    assert response.json()['data']['state'] == 'connection_required'
    assert local_env.github.writes() == []


@pytest.mark.parametrize('error,status,reason', [
    (TimeoutError('fixture network failure'), 503, 'unavailable'),
    (HTTPError('https://api.github.com' + STAR_PATH, 429, 'fixture', {}, io.BytesIO(b'{}')), 429, 'rate_limited'),
])
def test_network_and_rate_limits_do_not_masquerade_as_missing_credentials(local_env, error, status, reason):
    local_env.github.get_error = error
    response = local_star(local_env)
    assert response.status_code == status
    assert response.json()['reason'] == reason
    assert local_env.github.writes() == []


@pytest.mark.parametrize('status,headers,expected,reason', [
    (401, {}, 200, None), (403, {}, 200, None),
    (403, {'Retry-After': '60'}, 429, 'rate_limited'),
])
def test_local_put_denial_and_rate_limit_stay_distinct(local_env, status, headers, expected, reason):
    local_env.github.put_error = HTTPError(
        'https://api.github.com' + STAR_PATH, status, 'fixture', headers, io.BytesIO(b'{}'),
    )
    response = local_star(local_env)
    assert response.status_code == expected
    if reason:
        assert response.json()['reason'] == reason
    else:
        assert response.json()['data']['state'] == 'connection_required'
    assert len(local_env.github.writes()) == 1


@pytest.mark.parametrize('applied,read_fails', [(True, False), (False, False), (True, True)])
def test_lost_local_write_response_is_read_back_without_replaying(local_env, applied, read_fails):
    local_env.github.put_error = TimeoutError('fixture lost write response')
    local_env.github.apply_before_error = applied
    local_env.github.fail_read_after_put = read_fails
    response = local_star(local_env)
    assert response.status_code == (200 if applied and not read_fails else 503)
    if response.status_code == 200:
        assert response.json()['data']['state'] == 'starred'
    else:
        assert response.json()['reason'] == 'result_unknown'
    assert len(local_env.github.writes()) == 1


def test_concurrent_local_confirmations_do_not_duplicate_put(local_env):
    local_env.github.release_put = threading.Event()
    with ThreadPoolExecutor(max_workers=2) as pool:
        first = pool.submit(local_star, local_env)
        try:
            assert local_env.github.put_entered.wait(2), 'local Star never reached HTTP PUT'
            second = pool.submit(local_star, local_env)
        finally:
            local_env.github.release_put.set()
        responses = [first.result(timeout=5), second.result(timeout=5)]
    assert [response.status_code for response in responses] == [200, 200]
    assert len(local_env.github.writes()) == 1


@pytest.mark.parametrize('body', [
    {}, {'confirmed': False}, {'confirmed': 'true'}, {'confirmed': 1},
    {'confirmed': True, 'token': TOKEN}, {'confirmed': True, 'repository': 'other/repo'},
    {'confirmed': True, 'owner_user_id': 'other-owner'},
])
def test_local_confirmation_and_fixed_target_are_strict(local_env, body):
    response = local_env.client.put(LOCAL_ROOT, json=body)
    assert response.status_code in (400, 422)
    local_env.probe.assert_not_called()
    assert local_env.github.calls == []


def test_anonymous_local_star_cannot_probe(local_env):
    def anonymous():
        raise_error(ErrorCodes.UNAUTHORIZED)
    app.dependency_overrides[current_user] = anonymous
    assert local_star(local_env).status_code == 401
    local_env.probe.assert_not_called()


def test_read_only_user_cannot_probe_for_a_local_star(local_env, monkeypatch):
    local_env.viewer.role.name = 'fixture-reader'
    monkeypatch.setattr('core.rbac.get_effective_permission_codes', lambda *_: {'model.read'})
    assert local_star(local_env).status_code == 403
    local_env.probe.assert_not_called()


def test_status_does_not_probe_local_credentials(local_env):
    assert local_env.client.get(ROOT).status_code == 200
    local_env.probe.assert_not_called()
    assert local_env.github.calls == []
