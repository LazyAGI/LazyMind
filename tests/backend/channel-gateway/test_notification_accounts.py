from concurrent.futures import ThreadPoolExecutor

import pytest

from channel_gateway.bootstrap import build_components


PREFIX = '/api/channel-gateway/v1/channel-accounts'


@pytest.mark.parametrize('provider', ['wechat', 'feishu'])
def test_disconnect_preserves_account_and_message_history(gateway, account, queued_reply, provider):
    row = account(provider)
    reply = queued_reply(row)
    response = gateway.client.delete(f'{PREFIX}/{row["id"]}')
    assert response.status_code == 204
    retained = gateway.store.get_account('owner', row['id'])
    assert retained is not None, 'Disconnect must retain the account referenced by task rules and history'
    assert retained['status'] == 'disconnected'
    with gateway.store._connect() as connection:
        assert connection.execute('SELECT id FROM channel_inbox WHERE id = %s',
                                  (reply['inbox_id'],)).fetchone()
        assert connection.execute('SELECT id FROM channel_outbox WHERE id = %s', (reply['id'],)).fetchone()
    assert gateway.store.claim_next_outbound('after-disconnect', lease_seconds=120) is None
    # Explicit disconnect is idempotent; closing twice must not remove history.
    assert gateway.client.delete(f'{PREFIX}/{row["id"]}').status_code == 204


@pytest.mark.parametrize('provider', ['wechat', 'feishu'])
def test_disconnect_blocks_an_already_claimed_delivery(gateway, account, queued_reply, provider):
    row = account(provider)
    queued_reply(row)
    claimed = gateway.store.claim_next_outbound('claimed-before-disconnect', lease_seconds=120)
    assert claimed is not None
    assert gateway.client.delete(f'{PREFIX}/{row["id"]}').status_code == 204
    assert not gateway.store.renew_outbound_lease(claimed.outbox_id, 'claimed-before-disconnect', lease_seconds=120)
    assert not gateway.store.complete_outbound(claimed.outbox_id, 'claimed-before-disconnect')


@pytest.mark.parametrize('provider', ['wechat', 'feishu'])
def test_reconnect_same_identity_preserves_id(gateway, account, provider):
    row = account(provider)
    assert gateway.client.delete(f'{PREFIX}/{row["id"]}').status_code == 204
    lease = gateway.store.acquire_runtime_lease('reconnect-' + row['id'])
    assert lease is not None
    try:
        connected = gateway.store.connect_referenced_account(
            owner_user_id='owner', provider=provider, external_id_hash=row['external_id_hash'],
            label='reconnected', credentials_ciphertext=row['credentials_ciphertext'],
            status='connected', runtime_fence=lease.fence,
        )
    finally:
        lease.close()
    assert connected is not None
    assert connected['id'] == row['id'], 'Reconnection must preserve existing task references'
    assert connected['credential_revision'] > row['credential_revision']


@pytest.mark.parametrize('provider', ['wechat', 'feishu'])
def test_disconnect_ownership_and_safe_account_view(gateway, account, provider):
    row = account(provider)
    assert gateway.client.delete(f'{PREFIX}/{row["id"]}', headers={'X-User-Id': 'other'}).status_code == 404
    assert gateway.store.get_account('owner', row['id'])['status'] == 'connected'
    view = gateway.client.get(PREFIX, params={'provider': provider})
    assert view.status_code == 200
    assert len(view.json()['items']) == 1
    item = view.json()['items'][0]
    assert item['capabilities']['notification_ready'] is (provider != 'wechat')
    assert 'credentials_ciphertext' not in view.text
    assert 'external_id_hash' not in view.text
    credentials = gateway.cipher.decrypt('owner', row['credentials_ciphertext'])
    # Application/user IDs are display metadata; authentication secrets must remain private.
    for key in ('token', 'app_secret', 'secret'):
        assert credentials[key] not in view.text
    assert gateway.client.get(PREFIX, params={'provider': provider},
                              headers={'X-User-Id': 'other'}).json()['items'] == []


def test_concurrent_binding_cannot_assign_identity_to_two_users(gateway, account):
    original = account()

    def bind(owner):
        return gateway.store.connect_referenced_account(
            owner_user_id=owner, provider='wechat', external_id_hash=original['external_id_hash'],
            label='account', credentials_ciphertext=gateway.cipher.encrypt(owner, {'token': 'fake'}),
            status='connected',
        )
    with ThreadPoolExecutor(max_workers=2) as executor:
        assert list(executor.map(bind, ['other-1', 'other-2'])) == [None, None]
    assert gateway.store.get_account('owner', original['id'])


@pytest.mark.parametrize('provider', ['wechat', 'feishu', 'wecom'])
def test_all_providers_use_existing_composition(gateway, provider):
    registry = gateway.components.delivery_worker._providers
    assert registry.delivery(provider) is not None, f'{provider} is missing from ProviderRegistry'
    assert registry.accounts(provider) is not None
    assert registry.connection(provider) is not None
    response = gateway.client.get(PREFIX, params={'provider': provider})
    assert response.status_code == 200
    assert response.json()['items'] == []


def test_reinitialize_preserves_accounts_and_outbox(gateway, account, queued_reply):
    row = account()
    reply = queued_reply(row)
    restored = build_components(gateway.settings)
    try:
        restored.store.initialize()
        assert restored.store.get_account('owner', row['id'])
        claimed = restored.store.claim_next_outbound('restored-worker', lease_seconds=120)
        assert claimed.outbox_id == reply['id']
    finally:
        restored.stop()
