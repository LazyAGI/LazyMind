"""Local Star capability checks validate permissions without performing a Star."""
import importlib
import io
from urllib.error import HTTPError

import pytest

from test_github_star import REPO, ROOT, STAR_PATH, TOKEN, star_env  # noqa: F401
from test_github_star_local import local_env  # noqa: F401
from api import cloud_oauth as cloud_api
from core.deps import current_user
from core.errors import ErrorCodes, raise_error
from main import app
from models import CloudAuthConnection
from services.github_cli_runner import GitHubCLIError


LOCAL_ROOT = ROOT + '/local'


def capability(env):
    return env.client.get(LOCAL_ROOT)


@pytest.mark.parametrize('already_starred', [False, True])
@pytest.mark.parametrize('scope', ['repo', 'read:user, public_repo'])
def test_capability_does_not_depend_on_star_state(local_env, monkeypatch, already_starred, scope, caplog):  # noqa: F811
    local_env.github.starred = already_starred

    def github(req, *args, **kwargs):
        result = local_env.github(req, *args, **kwargs)
        if req.full_url.endswith('/user'):
            result.headers['X-OAuth-Scopes'] = scope
        return result

    monkeypatch.setattr(importlib.import_module('services.github_star_service').request, 'urlopen', github)
    response = capability(local_env)
    assert response.status_code == 200
    assert response.json()['data'] == {'repository': REPO, 'eligible': True}
    assert 'no-store' in response.headers['cache-control']
    assert [(method, path) for method, path, _ in local_env.github.calls] == [
        ('GET', '/user'), ('GET', '/repos/' + REPO),
    ]
    assert local_env.github.writes() == []
    assert local_env.github.starred is already_starred
    assert TOKEN not in response.text and TOKEN not in caplog.text
    for field in ('account_id', 'login', 'connection_id', 'access_token'):
        assert field not in response.json()['data']
    with local_env.sessions() as db:
        assert db.query(CloudAuthConnection).count() == 0


def test_capability_leaves_an_existing_unusable_connection_unchanged(local_env):  # noqa: F811
    local_env.seed(status='EXPIRED')
    with local_env.sessions() as db:
        row = db.query(CloudAuthConnection).one()
        before = (row.status, row.credential_ciphertext, row.auth_state_ciphertext, row.updated_at)
    assert capability(local_env).json()['data'] == {'repository': REPO, 'eligible': True}
    with local_env.sessions() as db:
        row = db.query(CloudAuthConnection).one()
        assert (row.status, row.credential_ciphertext, row.auth_state_ciphertext, row.updated_at) == before
    assert local_env.github.writes() == []


@pytest.mark.parametrize('failure', [
    None, 'GITHUB_CREDENTIAL_UNREADABLE', 'GITHUB_ACCOUNT_SELECTION_REQUIRED', 'GITHUB_CLI_UNAVAILABLE',
])
def test_missing_or_unreadable_credentials_are_not_eligible(local_env, failure):  # noqa: F811
    local_env.probe.return_value = None
    if failure:
        local_env.probe.side_effect = GitHubCLIError(failure)
    response = capability(local_env)
    assert response.status_code == 200
    assert response.json()['data'] == {'repository': REPO, 'eligible': False}
    assert local_env.github.calls == []


def test_capability_host_gate_precedes_credential_access(local_env, monkeypatch):  # noqa: F811
    monkeypatch.setattr(cloud_api.github_cli_auth_service, 'host_credentials_enabled', False)
    response = capability(local_env)
    assert response.status_code == 200
    assert response.json()['data'] == {'repository': REPO, 'eligible': False}
    local_env.probe.assert_not_called()
    assert local_env.github.calls == []


@pytest.mark.parametrize('problem', ['invalid', 'forbidden', 'missing_scope', 'read_only_scope'])
def test_unverified_identity_or_scope_is_not_eligible(local_env, monkeypatch, problem):  # noqa: F811
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
    response = capability(local_env)
    assert response.status_code == 200
    assert response.json()['data'] == {'repository': REPO, 'eligible': False}
    assert local_env.github.writes() == []
    assert all(path != STAR_PATH for _, path, _ in local_env.github.calls)


@pytest.mark.parametrize('status,expected,reason', [(None, 503, 'unavailable'), (429, 429, 'rate_limited')])
def test_capability_network_and_rate_limits_remain_check_failures(local_env, status, expected, reason):  # noqa: F811
    local_env.github.repo_error = (
        TimeoutError('fixture repository check timed out') if status is None else
        HTTPError('https://api.github.com/repos/' + REPO, status, 'fixture', {}, io.BytesIO(b'{}'))
    )
    response = capability(local_env)
    assert response.status_code == expected
    assert response.json()['reason'] == reason
    assert local_env.github.writes() == []
    assert all(path != STAR_PATH for _, path, _ in local_env.github.calls)


def test_anonymous_capability_check_cannot_probe(local_env):  # noqa: F811
    def anonymous():
        raise_error(ErrorCodes.UNAUTHORIZED)

    app.dependency_overrides[current_user] = anonymous
    assert capability(local_env).status_code == 401
    local_env.probe.assert_not_called()
    assert local_env.github.calls == []


def test_read_only_user_cannot_probe_capability(local_env, monkeypatch):  # noqa: F811
    local_env.viewer.role.name = 'fixture-reader'
    monkeypatch.setattr('core.rbac.get_effective_permission_codes', lambda *_: {'model.read'})
    assert capability(local_env).status_code == 403
    local_env.probe.assert_not_called()
    assert local_env.github.calls == []
