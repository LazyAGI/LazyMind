"""Real dual-database account metadata, removal, history and ownership checks."""
from concurrent.futures import ThreadPoolExecutor
import json

import pytest

import test_feishu_connection_reuse as reuse_tests
import test_feishu_reauthorization as reauth_tests
from test_feishu_connection_reuse import PREFIX, assert_error, connect, lifecycle
from test_notification_delivery import enqueue, notification
from channel_gateway.bootstrap import build_components

feishu = reuse_tests.feishu
registered = reauth_tests.registered


def rename(gateway, row, label, owner='owner'):
    return gateway.client.patch(f'{PREFIX}/channel-accounts/{row["id"]}', json={'label': label},
                                headers={'X-User-Id': owner})


def listed(gateway):
    response = gateway.client.get(f'{PREFIX}/channel-accounts', params={'provider': 'feishu'})
    assert response.status_code == 200
    return response.json()['items']


def test_identity_is_backfilled_and_retained_on_unbind_without_secrets(gateway, account, feishu):
    row = account('feishu', identity='app-one', display_name='Alice')
    credentials = gateway.cipher.decrypt('owner', row['credentials_ciphertext'])
    view = listed(gateway)[0]
    assert view['identity'] == {'app_id': 'app-one', 'authorized_name': 'Alice', 'authorized_id': 'app-one'}
    assert view['binding_status'] == 'connected'
    assert lifecycle(gateway, row, 'pause').status_code == 204
    assert listed(gateway)[0]['binding_status'] == 'paused'
    assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    unbound = listed(gateway)[0]
    assert unbound['binding_status'] == 'unbound'
    assert unbound['identity'] == view['identity']
    stored = gateway.store.get_account('owner', row['id'])
    assert stored['credentials_ciphertext'] == ''
    for key in ('app_secret', 'token', 'secret'):
        assert credentials[key] not in json.dumps(unbound)
        assert credentials[key] not in stored['identity_metadata']


def test_unbinding_without_first_listing_preserves_identity(gateway, account, feishu):
    row = account('feishu', identity='app-two', display_name='Bob')
    assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    assert listed(gateway)[0]['identity']['authorized_name'] == 'Bob'


def test_legacy_unbound_identity_is_not_invented_and_can_be_renamed(gateway, account, feishu):
    row = account('feishu')
    with gateway.store._connect() as connection:
        connection.execute("UPDATE channel_accounts SET status = 'disconnected', credentials_ciphertext = '' "
                           "WHERE id = %s", (row['id'],))
    view = listed(gateway)[0]
    assert view['identity'] == {'app_id': '', 'authorized_name': '', 'authorized_id': ''}
    renamed = rename(gateway, row, '  工作号 · 简报  ')
    assert renamed.status_code == 200
    assert renamed.json()['label'] == '工作号 · 简报'
    assert renamed.json()['identity'] == view['identity']


@pytest.mark.parametrize('label', ['', '  ', 'x' * 81, 'name\nother', 'name\x00other', 123, None])
def test_rename_rejects_invalid_input(gateway, account, label):
    row = account('feishu')
    assert_error(rename(gateway, row, label), 422, 'INVALID_REQUEST')
    assert gateway.store.get_account('owner', row['id'])['label'] == row['label']


@pytest.mark.parametrize('action', ['rename', 'archive'])
def test_management_rejects_other_owners(gateway, account, feishu, action):
    row = account('feishu')
    response = (rename(gateway, row, 'new', 'other') if action == 'rename'
                else lifecycle(gateway, row, 'archive', 'other'))
    assert_error(response, 404, 'ACCOUNT_NOT_FOUND')
    assert gateway.store.get_account('owner', row['id'])['label'] == row['label']


@pytest.mark.parametrize('provider', ['wechat', 'wecom'])
@pytest.mark.parametrize('action', ['rename', 'archive'])
def test_management_does_not_change_other_providers(gateway, account, provider, action):
    row = account(provider)
    response = rename(gateway, row, 'new') if action == 'rename' else lifecycle(gateway, row, 'archive')
    assert_error(response, 422, 'PROVIDER_NOT_SUPPORTED')
    assert gateway.store.get_account('owner', row['id'])['credentials_ciphertext'] == row['credentials_ciphertext']


@pytest.mark.parametrize('pause', [False, True])
def test_only_unbound_records_can_be_removed(gateway, account, feishu, pause):
    row = account('feishu')
    if pause:
        assert lifecycle(gateway, row, 'pause').status_code == 204
    assert_error(lifecycle(gateway, row, 'archive'), 409, 'ACCOUNT_UNBIND_REQUIRED')
    assert len(listed(gateway)) == 1


def test_archive_preserves_notification_and_chat_history_and_blocks_reuse(gateway, account, queued_reply, feishu):
    row = account('feishu')
    reply = queued_reply(row)
    notice = enqueue(gateway, notification(row))
    assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    session = connect(gateway, account_id=row['id'], reauthorize=True).json()
    for _ in range(2):
        assert lifecycle(gateway, row, 'archive').status_code == 204
    assert listed(gateway) == []
    assert gateway.store.get_account('owner', row['id']) is None
    retained = gateway.store.get_account_internal(row['id'])
    assert retained['archived_at'] is not None and retained['credentials_ciphertext'] == ''
    assert gateway.store.get_session_internal(session['id'])['status'] == 'canceled'
    assert_error(lifecycle(gateway, row, 'resume'), 404, 'ACCOUNT_NOT_FOUND')
    assert_error(connect(gateway, key='late', account_id=row['id'], reauthorize=True), 404, 'ACCOUNT_NOT_FOUND')
    assert_error(rename(gateway, row, 'new'), 404, 'ACCOUNT_NOT_FOUND')
    assert gateway.client.get(f'{PREFIX}/task-notifications/{notice["notification_id"]}').status_code == 200
    history = gateway.client.get(f'{PREFIX}/task-notifications', params={'task_id': 'scheduled-run'}).json()
    assert history['items'][0]['notification_id'] == notice['notification_id']
    assert gateway.store.notification_history('other', 'scheduled-run')['items'] == []
    with gateway.store._connect() as connection:
        assert connection.execute('SELECT id FROM channel_inbox WHERE id = %s', (reply['inbox_id'],)).fetchone()
        assert connection.execute('SELECT id FROM channel_outbox WHERE id = %s', (reply['id'],)).fetchone()
    assert gateway.store.claim_next_outbound('after-archive', lease_seconds=20) is None


def test_archiving_does_not_remove_other_same_name_robots(gateway, account, feishu):
    first, second = account('feishu'), account('feishu')
    for row in (first, second):
        assert rename(gateway, row, 'Same name').status_code == 200
    assert gateway.client.delete(f'{PREFIX}/channel-accounts/{first["id"]}').status_code == 204
    assert lifecycle(gateway, first, 'archive').status_code == 204
    assert [row['id'] for row in listed(gateway)] == [second['id']]
    assert connect(gateway).json()['account']['id'] == second['id']


def test_concurrent_archive_and_reauthorization_cannot_revive_removed_record(gateway, account, feishu):
    row = account('feishu')
    assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    with ThreadPoolExecutor(max_workers=2) as executor:
        start = executor.submit(connect, gateway, account_id=row['id'], reauthorize=True)
        remove = executor.submit(lifecycle, gateway, row, 'archive')
        assert remove.result().status_code == 204
        assert start.result().status_code in {201, 404}
    assert gateway.store.get_account_internal(row['id'])['archived_at'] is not None
    assert gateway.store.get_account_internal(row['id'])['credentials_ciphertext'] == ''
    assert listed(gateway) == []


def test_upgrade_and_repeated_initialization_preserve_legacy_data(gateway, account, queued_reply, feishu):
    row = account('feishu', display_name='Legacy owner')
    reply = queued_reply(row)
    # Simulate the previous schema only inside this test's isolated file/schema.
    with gateway.store._connect() as connection:
        connection.execute('ALTER TABLE channel_accounts DROP COLUMN identity_metadata')
        connection.execute('ALTER TABLE channel_accounts DROP COLUMN archived_at')
    restored = build_components(gateway.settings)
    try:
        restored.store.initialize()
        restored.store.initialize()
        old = restored.store.get_account('owner', row['id'])
        assert old['label'] == row['label']
        assert old['credentials_ciphertext'] == row['credentials_ciphertext']
        assert old['identity_metadata'] == '{}'
        assert old['archived_at'] is None
        assert listed(gateway)[0]['identity']['authorized_name'] == 'Legacy owner'
        with restored.store._connect() as connection:
            assert connection.execute('SELECT id FROM channel_outbox WHERE id = %s', (reply['id'],)).fetchone()
    finally:
        restored.stop()


def test_archived_record_stays_removed_across_restart(gateway, account, feishu):
    row = account('feishu')
    assert rename(gateway, row, 'Personal assistant').status_code == 200
    assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    assert lifecycle(gateway, row, 'archive').status_code == 204
    restored = build_components(gateway.settings)
    try:
        restored.store.initialize()
        assert restored.store.list_accounts('owner', 'feishu') == []
        assert restored.store.get_account_internal(row['id'])['label'] == 'Personal assistant'
        assert restored.store.get_account_internal(row['id'])['archived_at'] is not None
    finally:
        restored.stop()


def test_authorization_callback_after_archiving_cannot_restore_record(gateway, registered, feishu, monkeypatch):
    row = registered
    assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    session = connect(gateway, account_id=row['id'], reauthorize=True).json()

    def archive_during_scan():
        assert lifecycle(gateway, row, 'archive').status_code == 204

    reauth_tests.run_registration(gateway, monkeypatch, session['id'], archive_during_scan)
    assert gateway.store.get_account_internal(row['id'])['archived_at'] is not None
    assert gateway.store.get_account_internal(row['id'])['credentials_ciphertext'] == ''
    assert gateway.store.get_session_internal(session['id'])['status'] == 'canceled'
    assert feishu['started'] == []


def test_client_cannot_spoof_display_identity_when_renaming(gateway, account):
    row = account('feishu')
    response = gateway.client.patch(f'{PREFIX}/channel-accounts/{row["id"]}',
                                    json={'label': 'Fake owner', 'identity': {'authorized_name': 'Other person'}})
    assert_error(response, 422, 'INVALID_REQUEST')
    assert gateway.store.get_account('owner', row['id'])['label'] == row['label']
