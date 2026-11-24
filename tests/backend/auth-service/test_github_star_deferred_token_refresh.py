"""Supplemental read-only token retrieval regression; outside frozen R2."""
from datetime import UTC, datetime, timedelta
from unittest.mock import Mock

import pytest
from core.cloud_crypto import decrypt_json, encrypt_json
from core.errors import AppException
from models import CloudAuthConnection
from services.cloud_oauth_provider import CloudTokenPayload
from test_github_star_deferred import LOCAL_BODY, OWNER, TOKEN, current, intent, require_service
from test_github_star_deferred import deferred_env as _shared_deferred_env  # noqa: F401

from api import cloud_oauth as cloud_api


@pytest.fixture
def deferred_env(request):
    return request.getfixturevalue('_shared_deferred_env')


def oauth_connection(env, monkeypatch, *, token=TOKEN, expired=False):
    env.seed()
    now = datetime.now(UTC)
    with env.sessions() as db:
        row = db.get(CloudAuthConnection, 'conn-deferred')
        row.credential_ciphertext = encrypt_json({'client_id': 'fixture-client', 'client_secret': 'fixture-secret'})
        row.auth_state_ciphertext = encrypt_json({
            'access_token': token, 'refresh_token': 'fixture-refresh-token',
            'access_expires_at': (now + timedelta(days=-1 if expired else 1)).isoformat(),
        })
        db.commit()
        before = row.auth_state_ciphertext, row.updated_at, row.status
    connections = cloud_api.github_star_service.connections
    refresh = Mock(return_value=CloudTokenPayload('fixture-refreshed-token', now + timedelta(days=2)))
    monkeypatch.setattr(connections._provider('github'), 'refresh_access_token', refresh)
    cache = Mock()
    monkeypatch.setattr(connections, '_cache_set', cache)
    return connections, refresh, cache, before


def assert_unchanged(env, before):
    with env.sessions() as db:
        row = db.get(CloudAuthConnection, 'conn-deferred')
        assert (row.auth_state_ciphertext, row.updated_at, row.status) == before


def test_valid_oauth_token_read_without_refresh_has_no_storage_or_cache_side_effect(deferred_env, monkeypatch):
    env = deferred_env
    connections, refresh, cache, before = oauth_connection(env, monkeypatch)
    token = connections.get_access_token('conn-deferred', user_id=str(OWNER), allow_refresh=False)
    assert token['access_token'] == TOKEN
    refresh.assert_not_called()
    cache.assert_not_called()
    assert_unchanged(env, before)
    assert env.github.calls == []


@pytest.mark.parametrize('token,expired', [(TOKEN, True), ('', False)])
def test_unavailable_oauth_token_read_cannot_refresh_or_mutate(deferred_env, monkeypatch, token, expired):
    env = deferred_env
    connections, refresh, cache, before = oauth_connection(env, monkeypatch, token=token, expired=expired)
    with pytest.raises(AppException):
        connections.get_access_token('conn-deferred', user_id=str(OWNER), allow_refresh=False)
    refresh.assert_not_called()
    cache.assert_not_called()
    assert_unchanged(env, before)
    assert env.github.calls == []


def test_expired_saved_oauth_intent_stops_without_provider_refresh_or_local_fallback(deferred_env, monkeypatch):
    env = deferred_env
    _, refresh, cache, before = oauth_connection(env, monkeypatch, expired=True)
    service = require_service(env)
    accepted = intent(service.receive(owner_user_id=str(OWNER), source='connection', confirmed=True,
                                      connection_id='conn-deferred', expected_account_id='10001'))
    service.run_due_once()
    assert current(env)['intent_id'] == accepted['intent_id']
    assert current(env)['status'] == 'needs_attention'
    refresh.assert_not_called()
    cache.assert_not_called()
    env.probe.assert_not_called()
    assert_unchanged(env, before)
    assert env.github.calls == []
    assert intent(service.receive(owner_user_id=str(OWNER), **LOCAL_BODY))['intent_id'] == accepted['intent_id']


def test_default_getter_keeps_its_existing_oauth_refresh_behavior(deferred_env, monkeypatch):
    env = deferred_env
    connections, refresh, cache, _ = oauth_connection(env, monkeypatch, expired=True)
    result = connections.get_access_token('conn-deferred', user_id=str(OWNER))
    assert result['access_token'] == 'fixture-refreshed-token'
    refresh.assert_called_once()
    cache.assert_called_once()
    with env.sessions() as db:
        state = decrypt_json(db.get(CloudAuthConnection, 'conn-deferred').auth_state_ciphertext)
        assert state['access_token'] == 'fixture-refreshed-token'
