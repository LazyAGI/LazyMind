"""Lifecycle regressions added during implementation; external Feishu registration only is replaced."""
import hashlib
import threading

import pytest

import test_feishu_connection_reuse as reuse_tests
from test_feishu_connection_reuse import PREFIX, assert_error, connect, lifecycle
from channel_gateway.feishu.connection import _RegistrationWorker
from channel_gateway.feishu.domain import FeishuAppRegistration


feishu = reuse_tests.feishu


@pytest.fixture
def registered(gateway, account, feishu):
    row = account('feishu', identity='synthetic-original-app')
    identity = hashlib.sha256(b'synthetic-original-app:synthetic-original-app').hexdigest()
    with gateway.store._connect() as connection:
        connection.execute('UPDATE channel_accounts SET external_id_hash = %s WHERE id = %s', (identity, row['id']))
    return gateway.store.get_account('owner', row['id'])


def run_registration(gateway, monkeypatch, session_id, effect=None, app_id='synthetic-original-app'):
    service = gateway.components.delivery_worker._providers.connection('feishu')
    options = []

    def register(**kwargs):
        options.append(kwargs)
        kwargs['on_qr_code']('https://feishu.test.invalid/authorize', 120)
        if effect:
            effect()
        return FeishuAppRegistration(app_id, 'synthetic-rotated-secret',
                                     'synthetic-original-app', 'Test user', '')

    monkeypatch.setattr(service._registrar, 'register', register)
    row = gateway.store.get_session_internal(session_id)
    service._run_worker(session_id, row['qr_version'], _RegistrationWorker(threading.Event()))
    return options


@pytest.mark.parametrize('unbind', [False, True])
def test_reauthorization_rotates_only_original_credentials_preserving_history(
    gateway, registered, queued_reply, feishu, monkeypatch, unbind,
):
    row = registered
    reply = queued_reply(row)
    if unbind:
        assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    session = connect(gateway, account_id=row['id'], reauthorize=True)
    assert session.status_code == 201
    assert session.json()['status'] == 'preparing'
    options = run_registration(gateway, monkeypatch, session.json()['id'])
    assert options[0]['create_new'] is False
    assert options[0]['app_id'] == 'synthetic-original-app'
    completed = gateway.client.get(f'{PREFIX}/connection-sessions/{session.json()["id"]}').json()
    assert completed['status'] == 'connected'
    assert completed['account']['id'] == row['id']
    current = gateway.store.get_account('owner', row['id'])
    assert current['credentials_ciphertext'] != row['credentials_ciphertext']
    credentials = gateway.cipher.decrypt('owner', current['credentials_ciphertext'])
    assert credentials['app_secret'] == 'synthetic-rotated-secret'
    assert 'synthetic-rotated-secret' not in str(completed)
    assert len(gateway.store.list_accounts('owner', 'feishu')) == 1
    with gateway.store._connect() as connection:
        assert connection.execute('SELECT id FROM channel_outbox WHERE id = %s', (reply['id'],)).fetchone()
        assert connection.execute('SELECT id FROM channel_inbox WHERE id = %s', (reply['inbox_id'],)).fetchone()
    assert feishu['started'] == [row['id']]


@pytest.mark.parametrize('action', ['pause', 'unbind', 'cancel', 'mismatch', 'failure'])
def test_late_or_failed_authorization_cannot_replace_or_delete_original(
    gateway, registered, feishu, monkeypatch, action,
):
    row = registered
    session = connect(gateway, account_id=row['id'], reauthorize=True).json()

    def effect():
        if action == 'pause':
            assert lifecycle(gateway, row, 'pause').status_code == 204
        elif action == 'unbind':
            assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
        elif action == 'cancel':
            assert gateway.client.delete(f'{PREFIX}/connection-sessions/{session["id"]}').status_code == 204
        elif action == 'failure':
            raise RuntimeError('synthetic registration failure')

    run_registration(gateway, monkeypatch, session['id'], effect,
                     app_id='different-app' if action == 'mismatch' else 'synthetic-original-app')
    retained = gateway.store.get_account('owner', row['id'])
    assert retained is not None
    assert retained['credentials_ciphertext'] == ('' if action == 'unbind' else row['credentials_ciphertext'])
    assert retained['status'] == ('disconnected' if action in {'pause', 'unbind'} else 'connected')
    assert feishu['started'] == []
    result = gateway.client.get(f'{PREFIX}/connection-sessions/{session["id"]}').json()
    assert result['status'] in {'failed', 'canceled'}
    if action == 'mismatch':
        assert result['error']['code'] == 'ACCOUNT_IDENTITY_MISMATCH'


def test_expired_reauthorization_refresh_still_authorizes_original(gateway, registered, monkeypatch):
    row = registered
    session = connect(gateway, account_id=row['id'], reauthorize=True).json()
    gateway.store.mark_expired(session['id'], 1)
    refreshed = gateway.client.post(f'{PREFIX}/connection-sessions/{session["id"]}:refresh')
    assert refreshed.status_code == 200
    assert refreshed.json()['status'] == 'preparing'
    options = run_registration(gateway, monkeypatch, session['id'])
    assert options[0]['create_new'] is False
    assert gateway.store.get_session_internal(session['id'])['account_id'] == row['id']


@pytest.mark.parametrize('payload', [
    {'reauthorize': 'true'}, {'reauthorize': 1}, {'reauthorize': True},
    {'reauthorize': True, 'account_id': 'account', 'create_new': True},
    {'provider': 'wechat', 'reauthorize': True, 'account_id': 'account'},
])
def test_reauthorization_intent_is_strict_and_requires_original(gateway, feishu, payload):
    assert_error(connect(gateway, **payload), 422, 'INVALID_REQUEST')
    assert not any(feishu.values())


def test_default_damaged_credentials_do_not_start_implicit_registration(gateway, registered, feishu):
    with gateway.store._connect() as connection:
        connection.execute("UPDATE channel_accounts SET credentials_ciphertext = 'invalid' WHERE id = %s",
                           (registered['id'],))
    assert_error(connect(gateway), 409, 'FEISHU_REAUTHORIZATION_REQUIRED')
    assert feishu['registration'] == []


def test_retry_after_creation_returns_original_session_even_when_account_now_exists(gateway, registered, feishu):
    response = connect(gateway, create_new=True)
    session_id = response.json()['id']
    with gateway.store._connect() as connection:
        connection.execute("UPDATE channel_connection_sessions SET status = 'connected', account_id = %s WHERE id = %s",
                           (registered['id'], session_id))
    assert connect(gateway, create_new=True).json()['id'] == session_id
    assert len(feishu['registration']) == 1


def test_stale_runtime_start_does_not_load_paused_account(gateway, registered):
    from channel_gateway.feishu.accounts import FeishuCredentialStore
    from channel_gateway.feishu.runtime import FeishuRuntime
    row = registered
    assert lifecycle(gateway, row, 'pause').status_code == 204
    # Instantiate the existing runtime without network factories; paused state must return before their use.
    from channel_gateway.feishu.domain import FeishuAddressFactory
    runtime = FeishuRuntime(store=gateway.store, credentials=FeishuCredentialStore(
        store=gateway.store, cipher=gateway.cipher), channels=None, addresses=FeishuAddressFactory())
    runtime.start_account(row['id'])
    assert runtime._accounts == {}
    assert runtime._workers == {}


def test_new_robot_authorization_cannot_overwrite_an_existing_robot(gateway, registered, feishu, monkeypatch):
    session = connect(gateway, create_new=True).json()
    run_registration(gateway, monkeypatch, session['id'])
    current = gateway.store.get_account('owner', registered['id'])
    assert current['status'] == registered['status']
    assert current['credentials_ciphertext'] == registered['credentials_ciphertext']
    assert current['credential_revision'] == registered['credential_revision']
    assert gateway.store.get_session_internal(session['id'])['status'] == 'failed'
    assert feishu['started'] == []


def test_lifecycle_permissions_and_request_schema_are_exposed(gateway):
    from channel_gateway.app import app, pause_channel_account, resume_channel_account
    assert pause_channel_account.__required_permissions__ == {'qa.write'}
    assert resume_channel_account.__required_permissions__ == {'qa.write'}
    schema = app.openapi()['components']['schemas']['ConnectionSessionCreate']
    assert schema['additionalProperties'] is False
    for field in ('create_new', 'reauthorize'):
        assert schema['properties'][field]['type'] == 'boolean'
        assert schema['properties'][field]['default'] is False
