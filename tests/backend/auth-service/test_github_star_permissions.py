"""The read-only status route also reports whether its caller can confirm a Star."""
from test_github_star import ROOT, STAR_PATH, star_env  # noqa: F401
import importlib
import io
from urllib.error import HTTPError
import pytest
from core.cloud_crypto import encrypt_json
from core.errors import AppException
from models import CloudAuthConnection


def test_read_only_user_has_no_star_capability(star_env, monkeypatch):
    star_env.seed()
    star_env.viewer.role.name = 'fixture-reader'
    monkeypatch.setattr('core.rbac.get_effective_permission_codes', lambda *_: {'model.read'})
    response = star_env.client.get(ROOT)
    assert response.status_code == 200
    assert response.json()['data']['can_star'] is False
    assert star_env.github.writes() == []


def test_administrator_receives_star_capability(star_env):
    response = star_env.client.get(ROOT)
    assert response.status_code == 200
    assert response.json()['data']['can_star'] is True
    assert star_env.github.writes() == []


@pytest.mark.parametrize('scope', [None, 'read:user'])
def test_unknown_or_read_only_github_scope_is_not_eligible(star_env, monkeypatch, scope):
    star_env.seed()
    def response(req, *args, **kwargs):
        result = star_env.github(req, *args, **kwargs)
        if req.full_url.endswith('/user'):
            if scope is None:
                result.headers.pop('X-OAuth-Scopes', None)
            else:
                result.headers['X-OAuth-Scopes'] = scope
        return result
    monkeypatch.setattr(importlib.import_module('services.github_star_service').request, 'urlopen', response)
    status = star_env.service.status(owner_user_id='star-owner', connection_id='conn-1')
    assert status['accounts'][0]['eligible'] is False
    assert status['accounts'][0]['state'] == 'unknown'
    with pytest.raises(AppException) as error:
        star_env.service.star(owner_user_id='star-owner', connection_id='conn-1', expected_account_id='10001', confirmed=True)
    assert error.value.http_code == 403
    assert star_env.github.writes() == []


def test_public_repo_scope_is_sufficient_for_the_fixed_public_project(star_env, monkeypatch):
    star_env.seed()
    def response(req, *args, **kwargs):
        result = star_env.github(req, *args, **kwargs)
        result.headers['X-OAuth-Scopes'] = 'read:user, public_repo'
        return result
    monkeypatch.setattr(importlib.import_module('services.github_star_service').request, 'urlopen', response)
    result = star_env.service.star(owner_user_id='star-owner', connection_id='conn-1', expected_account_id='10001', confirmed=True)
    assert result['state'] == 'starred'
    assert len(star_env.github.writes()) == 1


def test_missing_stored_cli_token_uses_the_isolated_star_reconnect_code(star_env):
    star_env.seed()
    with star_env.sessions() as db:
        row = db.query(CloudAuthConnection).filter_by(connection_id='conn-1').one()
        row.auth_state_ciphertext = encrypt_json({'token_source': 'github_cli', 'access_token': ''})
        db.commit()
    response = star_env.client.put(ROOT, json={
        'connection_id': 'conn-1', 'expected_account_id': '10001', 'confirmed': True,
    })
    assert response.status_code == 401
    assert response.json()['code'] == 1001306
    assert response.json()['reason'] == 'reauthorize'
    assert star_env.github.calls == []


@pytest.mark.parametrize('status,headers,body,expected,reason', [
    (403, {}, b'{}', 403, 'permission_denied'),
    (403, {'X-RateLimit-Remaining': '0'}, b'{}', 429, 'rate_limited'),
    (403, {'Retry-After': '60'}, b'{}', 429, 'rate_limited'),
    (403, {}, b'{"message":"You have exceeded a secondary rate limit"}', 429, 'rate_limited'),
    (429, {}, b'{}', 429, 'rate_limited'),
])
def test_permission_denial_and_rate_limits_stay_distinct_without_replaying_put(star_env, status, headers, body, expected, reason):
    star_env.seed()
    star_env.github.put_error = HTTPError('https://api.github.com' + STAR_PATH, status, 'fixture', headers, io.BytesIO(body))
    response = star_env.client.put(ROOT, json={
        'connection_id': 'conn-1', 'expected_account_id': '10001', 'confirmed': True,
    })
    assert response.status_code == expected
    assert response.json()['reason'] == reason
    assert len(star_env.github.writes()) == 1
