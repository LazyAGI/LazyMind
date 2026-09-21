from dataclasses import replace

import pytest

from channel_gateway.common.domain.channel import InboundEnvelope


def receive(gateway, row, *, expected_revision=0, **overrides):
    envelope = InboundEnvelope(provider='feishu', account_id=row['id'], message_key='message-one',
                               order_key='address-one', external_address_hash='address-one',
                               owner_user_id='owner', recipient_id='oc_known_chat', text='hello')
    envelope = replace(envelope, **overrides)
    lease = gateway.store.acquire_runtime_lease(row['id'])
    assert lease is not None
    try:
        return gateway.store.claim_feishu_workspace_and_ingest(
            row['id'], 'address-one', {'revision': 1}, expected_revision, '', '', envelope, lease.fence,
        )
    finally:
        lease.close()


def test_real_feishu_ingestion_registers_selectable_target_once(gateway, account):
    row = account('feishu')
    assert receive(gateway, row)
    assert not receive(gateway, row)
    response = gateway.client.get(f'/api/channel-gateway/v1/channel-accounts/{row["id"]}/notification-targets')
    assert response.status_code == 200
    assert response.json()['items'] == [
        {'recipient_id': 'oc_known_chat', 'label': 'oc_known_chat', 'kind': 'conversation', 'available': True},
    ]
    assert gateway.store.notification_context('owner', row['id'], 'oc_known_chat', 'feishu') == {}
    assert gateway.client.get(f'/api/channel-gateway/v1/channel-accounts/{row["id"]}/notification-targets',
                              headers={'X-User-Id': 'other'}).status_code == 404


@pytest.mark.parametrize('overrides', [
    {'owner_user_id': 'other'}, {'provider': 'wechat'}, {'account_id': 'another-account'},
])
def test_feishu_target_requires_matching_inbound_identity(gateway, account, overrides):
    row = account('feishu')
    with pytest.raises(RuntimeError, match='binding is invalid'):
        receive(gateway, row, **overrides)
    assert gateway.store.notification_targets('owner', row['id'])['items'] == []


def test_rejected_workspace_claim_does_not_add_recipient(gateway, account):
    row = account('feishu')
    assert not receive(gateway, row, expected_revision=9)
    assert gateway.store.notification_targets('owner', row['id'])['items'] == []


def test_upgrade_backfills_only_connected_feishu_history_idempotently(gateway, account, incoming):
    rows = [account('feishu'), account('feishu'), account('wechat'), account('wecom')]
    for row in rows:
        incoming(row, recipient='known-recipient', context='existing-context')
    with gateway.store._connect() as connection:
        connection.execute('DELETE FROM channel_notification_targets')
        connection.execute("UPDATE channel_accounts SET status='disconnected', credentials_ciphertext='' WHERE id=%s",
                           (rows[1]['id'],))
    gateway.store.initialize()
    gateway.store.initialize()
    for index, row in enumerate(rows):
        items = gateway.store.notification_targets('owner', row['id'])['items']
        assert len(items) == (1 if index == 0 else 0)
    with gateway.store._connect() as connection:
        assert connection.execute('SELECT COUNT(*) AS n FROM channel_inbox').fetchone()['n'] == 4


def test_backfill_does_not_trust_mismatched_historical_owner(gateway, account, incoming):
    row = account('feishu')
    incoming(row)
    with gateway.store._connect() as connection:
        connection.execute('DELETE FROM channel_notification_targets')
        connection.execute("UPDATE channel_inbox SET owner_user_id='other'")
    gateway.store.initialize()
    assert gateway.store.notification_targets('owner', row['id'])['items'] == []
