"""Notification activation follows observed rejection and fenced inbound context."""
from dataclasses import replace
import datetime as dt

import httpx
import pytest

from channel_gateway.wechat.domain import WeChatRejectedError

from test_notification_delivery import OneIteration, PREFIX, deliver_once, enqueue, notification, outbox


def ready(gateway, row):
    response = gateway.client.get(f'{PREFIX}/channel-accounts/{row["id"]}', params={'include_references': 'false'})
    assert response.status_code == 200, response.text
    return response.json()['capabilities']['notification_ready']


def rejected_send():
    # This is an explicit business rejection, without assigning a particular
    # provider code a meaning such as "expired" or assuming a platform TTL.
    return WeChatRejectedError(operation='sendmessage', http_status=200, ret=-2)


def test_rejected_notice_becomes_pending_activation_and_new_inbound_restores_delivery(
        gateway, account, incoming, monkeypatch):
    row = account()
    incoming(row, context='rejected-context')
    queued = enqueue(gateway, notification(row))
    assert ready(gateway, row) is True

    deliver_once(gateway, monkeypatch, error=rejected_send())

    assert ready(gateway, row) is False
    retained = gateway.store.get_account('owner', row['id'])
    assert retained['status'] == 'connected'
    assert retained['credentials_ciphertext'] == row['credentials_ciphertext']
    assert gateway.store.notification_targets('owner', row['id'])['items'][0]['available'] is False
    assert outbox(gateway, queued['outbox_id'])['status'] == 'dead'
    assert outbox(gateway, queued['outbox_id'])['last_error'] == 'NOTIFICATION_DELIVERY_FAILED'

    incoming(row, context='renewed-context')
    assert ready(gateway, row) is True
    assert gateway.store.notification_targets('owner', row['id'])['items'][0]['available'] is True
    retried = gateway.client.post(f'{PREFIX}/task-notifications/{queued["notification_id"]}:retry',
                                  json={'idempotency_key': 'after-user-activation'})
    assert retried.status_code == 201, retried.text
    sent = deliver_once(gateway, monkeypatch)
    assert [item['context_token'] for item in sent] == ['renewed-context']
    assert outbox(gateway, retried.json()['outbox_id'])['status'] == 'sent'
    assert outbox(gateway, queued['outbox_id'])['status'] == 'dead'


@pytest.mark.parametrize('renewed_token', ['new-context', 'original-context'])
def test_late_rejection_does_not_clear_new_inbound_even_when_token_is_reused(
        gateway, account, incoming, monkeypatch, renewed_token):
    row = account()
    incoming(row, context='original-context')
    before = gateway.store.notification_context('owner', row['id'], 'recipient-a', 'wechat')
    queued = enqueue(gateway, notification(row))
    provider = gateway.components.delivery_worker._providers.delivery('wechat')

    def send(**payload):
        assert payload['context_token'] == 'original-context'
        incoming(row, context=renewed_token)
        raise rejected_send()

    monkeypatch.setattr(provider._client, 'send_text', send)
    monkeypatch.setattr(gateway.components.delivery_worker, '_stop', OneIteration())
    gateway.components.delivery_worker._run('late-rejection')

    after = gateway.store.notification_context('owner', row['id'], 'recipient-a', 'wechat')
    assert after['context_token'] == renewed_token
    assert after['_notification_context_version'] != before['_notification_context_version']
    assert ready(gateway, row) is True
    assert outbox(gateway, queued['outbox_id'])['status'] == 'dead'


@pytest.mark.parametrize('error', [
    httpx.ReadTimeout('uncertain-send'), httpx.ConnectError('not-sent'),
    WeChatRejectedError(operation='sendmessage', http_status=429, retryable=True),
    WeChatRejectedError(operation='sendmessage', http_status=401),
    WeChatRejectedError(operation='sendmessage', http_status=403),
    WeChatRejectedError(operation='getuploadurl', http_status=200, ret=-2),
])
def test_transport_rate_auth_and_upload_errors_do_not_invalidate_context(
        gateway, account, incoming, monkeypatch, error):
    row = account()
    incoming(row, context='retained-context')
    before = gateway.store.notification_context('owner', row['id'], 'recipient-a', 'wechat')
    enqueue(gateway, notification(row))
    deliver_once(gateway, monkeypatch, error=error)
    assert gateway.store.notification_context('owner', row['id'], 'recipient-a', 'wechat') == before
    assert ready(gateway, row) is True


def test_rejection_invalidates_only_its_account_and_recipient(gateway, account, incoming, monkeypatch):
    row, other, feishu = account(), account(), account('feishu')
    incoming(row, context='rejected-context')
    incoming(row, recipient='recipient-b', context='other-recipient-context')
    incoming(other, context='other-account-context')
    incoming(feishu)
    enqueue(gateway, notification(row))

    deliver_once(gateway, monkeypatch, error=rejected_send())

    targets = gateway.store.notification_targets('owner', row['id'])['items']
    assert {target['recipient_id']: target['available'] for target in targets} == {
        'recipient-a': False, 'recipient-b': True,
    }
    assert gateway.store.notification_targets('owner', other['id'])['items'][0]['available'] is True
    assert gateway.store.notification_targets('owner', feishu['id'])['items'][0]['available'] is True


@pytest.mark.parametrize('case', [
    'owner', 'account', 'provider', 'recipient', 'token', 'version', 'credentials',
    'lease_owner', 'expired_lease', 'finished', 'disconnected',
])
def test_context_invalidation_rejects_stale_or_mismatched_delivery_fences(gateway, account, incoming, case):
    row, other = account(), account()
    incoming(row, context='current-context')
    queued = enqueue(gateway, notification(row))
    claimed = gateway.store.claim_next_outbound('fenced-worker', lease_seconds=120)
    prepared = gateway.components.notifications.prepare(claimed)
    original = dict(prepared.provider_context)
    owner = 'fenced-worker'
    if case == 'owner':
        prepared = replace(prepared, metadata={**prepared.metadata, 'owner_user_id': 'other-owner'})
    elif case == 'account':
        prepared = replace(prepared, account_id=other['id'])
    elif case == 'provider':
        prepared = replace(prepared, provider='feishu')
    elif case == 'recipient':
        prepared = replace(prepared, recipient_id='other-recipient')
    elif case in {'token', 'version'}:
        key, value = ('context_token', 'wrong-token') if case == 'token' else ('_notification_context_version', '0' * 64)
        prepared = replace(prepared, provider_context={**original, key: value})
    elif case == 'lease_owner':
        owner = 'another-worker'
    else:
        with gateway.store._connect() as connection:
            if case == 'credentials':
                connection.execute('UPDATE channel_accounts SET credential_revision=credential_revision+1 WHERE id=%s',
                                   (row['id'],))
            elif case == 'expired_lease':
                connection.execute('UPDATE channel_outbox SET lease_until=%s WHERE id=%s',
                                   (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=1), queued['outbox_id']))
            elif case == 'finished':
                connection.execute("UPDATE channel_outbox SET status='dead' WHERE id=%s", (queued['outbox_id'],))
            elif case == 'disconnected':
                connection.execute("UPDATE channel_accounts SET status='disconnected' WHERE id=%s", (row['id'],))

    assert gateway.store.invalidate_wechat_notification_context(prepared, owner) is False
    with gateway.store._connect() as connection:
        assert connection.execute('SELECT context_ciphertext FROM channel_notification_targets WHERE account_id=%s',
                                  (row['id'],)).fetchone()['context_ciphertext']


def test_internal_context_fences_are_not_exposed_in_public_views(gateway, account, incoming):
    row = account()
    incoming(row, context='private-context')
    for path in (f'{PREFIX}/channel-accounts?provider=wechat',
                 f'{PREFIX}/channel-accounts/{row["id"]}?include_references=false',
                 f'{PREFIX}/channel-accounts/{row["id"]}/notification-targets'):
        response = gateway.client.get(path)
        assert response.status_code == 200, response.text
        for secret in ('private-context', '_notification_context_version', '_notification_credential_revision'):
            assert secret not in response.text
