import datetime as dt
import json

import httpx
import pytest

from test_notification_delivery import deliver_once, enqueue, notification, outbox, PREFIX


def retry_now(gateway, outbox_id):
    with gateway.store._connect() as connection:
        connection.execute('UPDATE channel_outbox SET next_attempt_at = %s WHERE id = %s',
                           (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=1), outbox_id))


def test_connection_failures_stop_after_five_attempts(gateway, account, incoming, monkeypatch):
    row = account()
    incoming(row, context='context')
    record = enqueue(gateway, notification(row))
    for attempt in range(5):
        deliver_once(gateway, monkeypatch, error=httpx.ConnectError('synthetic connection failure'))
        stored = outbox(gateway, record['outbox_id'])
        assert stored['attempt_count'] == attempt + 1
        assert stored['status'] == ('dead' if attempt == 4 else 'retry_wait')
        retry_now(gateway, record['outbox_id'])
    assert gateway.store.claim_next_outbound('sixth', lease_seconds=120) is None


def test_expired_sending_lease_becomes_unknown_after_restart(gateway, account, incoming):
    row = account()
    incoming(row, context='context')
    result = enqueue(gateway, notification(row))
    assert gateway.store.claim_next_outbound('crashed', lease_seconds=120)
    with gateway.store._connect() as connection:
        connection.execute('UPDATE channel_outbox SET lease_until = %s WHERE id = %s',
                           (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=1), result['outbox_id']))
    assert gateway.store.claim_next_outbound('replacement', lease_seconds=120) is None
    assert outbox(gateway, result['outbox_id'])['status'] == 'unknown'


def test_partial_retry_keeps_confirmed_parts_and_original_history(gateway, account, incoming, monkeypatch):
    row = account()
    incoming(row, context='context')
    payload = notification(row)
    payload['body'] = '前半段' * 700 + '后半段' * 700
    payload['content'] = 'full'
    original = enqueue(gateway, payload)
    provider = gateway.components.delivery_worker._providers.delivery('wechat')
    sent = []

    def interrupted(**message):
        sent.append(message['text'])
        if len(sent) == 2:
            raise httpx.ReadTimeout('synthetic ambiguous acknowledgement')

    from test_notification_delivery import OneIteration
    monkeypatch.setattr(provider._client, 'send_text', interrupted)
    worker = gateway.components.delivery_worker
    monkeypatch.setattr(worker, '_stop', OneIteration())
    worker._run('partial')
    before = outbox(gateway, original['outbox_id'])
    assert before['status'] == 'unknown' and before['next_part_index'] == 1
    response = gateway.client.post(f'{PREFIX}/task-notifications/{original["notification_id"]}:retry',
                                   json={'idempotency_key': 'partial-retry', 'confirm_duplicate_risk': True})
    assert response.status_code == 201
    retried = response.json()
    assert outbox(gateway, retried['outbox_id'])['next_part_index'] == 1
    new_sent = deliver_once(gateway, monkeypatch)
    parts = gateway.store._list(before['rendered_parts'])
    assert [item['text'] for item in new_sent] == [part['text'] for part in parts[1:]]
    assert outbox(gateway, original['outbox_id'])['status'] == 'unknown'


@pytest.mark.parametrize('provider', ['wechat', 'feishu', 'wecom'])
def test_reconnect_request_is_owned_and_identity_mismatch_preserves_account(gateway, account, provider):
    row = account(provider)
    now = dt.datetime.now(dt.timezone.utc)
    session, _ = gateway.store.reserve_session(session_id='reconnect', owner_user_id='owner', provider=provider,
                                               idempotency_key='key', expires_at=now + dt.timedelta(minutes=5),
                                               requested_account_id=row['id'])
    with pytest.raises(Exception) as error:
        gateway.store.validate_reconnect(session['id'], 'owner', provider, 'different-identity')
    assert error.value.code == 'ACCOUNT_IDENTITY_MISMATCH'
    assert gateway.store.get_session('owner', session['id'])['status'] == 'failed'
    assert gateway.store.get_account('owner', row['id'])['external_id_hash'] == row['external_id_hash']


def test_notification_requests_reject_duplicate_json_and_oversize_credentials(gateway):
    response = gateway.client.post(
        f'{PREFIX}/connection-sessions',
        content=json.dumps({'provider': 'wecom', 'credentials': {'bot_id': 'b', 'secret': 'x' * 17000}}))
    assert response.status_code == 413
    response = gateway.client.post(f'{PREFIX}/connection-sessions',
                                   content='{"provider":"wechat","provider":"wecom"}')
    assert response.status_code == 422


@pytest.mark.parametrize('status,body,retryable', [
    (200, {'ret': -1, 'errmsg': 'synthetic-private-provider-detail'}, False),
    (401, {}, False), (429, {}, True),
])
def test_explicit_provider_rejections_are_safe_and_distinct_from_unknown(status, body, retryable):
    from channel_gateway.wechat.client import WeChatClient
    from channel_gateway.common.errors import ProviderRejectedError
    with pytest.raises(ProviderRejectedError) as error:
        payload = WeChatClient._decode_response(httpx.Response(status, json=body))
        WeChatClient._raise_provider_error(payload, 'send')
    assert error.value.retryable is retryable
    assert 'synthetic-private' not in str(error.value)


def test_rejected_delivery_fails_without_unknown_state(gateway, account, incoming, monkeypatch):
    from channel_gateway.common.errors import ProviderRejectedError
    row = account()
    incoming(row, context='context')
    record = enqueue(gateway, notification(row))
    deliver_once(gateway, monkeypatch, error=ProviderRejectedError())
    stored = outbox(gateway, record['outbox_id'])
    assert stored['status'] == 'dead'
    assert stored['last_error'] == 'NOTIFICATION_DELIVERY_FAILED'


def test_disconnect_preserves_uncertainty_of_inflight_delivery(gateway, account, incoming):
    row = account()
    incoming(row, context='context')
    record = enqueue(gateway, notification(row))
    assert gateway.store.claim_next_outbound('inflight', lease_seconds=120)
    assert gateway.store.disconnect_account('owner', row['id'])
    stored = outbox(gateway, record['outbox_id'])
    assert stored['status'] == 'unknown'
    assert stored['last_error'] == 'NOTIFICATION_DELIVERY_UNKNOWN'


def test_account_detail_is_owned_and_does_not_guess_between_recipients(gateway, account, incoming, monkeypatch):
    row = account()
    incoming(row, context='context')
    original_request = gateway.core_events.request

    def request(method, url, **kwargs):
        if '/notification-account-references/' in url:
            return httpx.Response(200, json={'data': {'total': 3, 'items': [], 'next_cursor': ''}})
        return original_request(method, url, **kwargs)

    monkeypatch.setattr(httpx, 'request', request)
    response = gateway.client.get(f'{PREFIX}/channel-accounts/{row["id"]}')
    assert response.status_code == 200, response.text
    assert response.json()['primary_recipient']['recipient_id'] == 'recipient-a'
    assert response.json()['notification_reference_count'] == 3
    assert 'context_token' not in response.text and 'ciphertext' not in response.text
    incoming(row, recipient='recipient-b', context='other-context')
    assert gateway.client.get(f'{PREFIX}/channel-accounts/{row["id"]}').json()['primary_recipient'] is None
    forbidden = gateway.client.get(f'{PREFIX}/channel-accounts/{row["id"]}', headers={'X-User-Id': 'other'})
    assert forbidden.status_code == 404


def test_retry_history_survives_refresh_with_owned_stable_pagination(gateway, account, incoming, monkeypatch):
    row = account()
    incoming(row, context='context')
    original = enqueue(gateway, notification(row))
    deliver_once(gateway, monkeypatch, error=httpx.ReadTimeout('synthetic lost reply'))
    retried = gateway.client.post(f'{PREFIX}/task-notifications/{original["notification_id"]}:retry',
                                  json={'idempotency_key': 'history', 'confirm_duplicate_risk': True})
    assert retried.status_code == 201
    query = {'task_id': original['payload']['task_id'], 'limit': 1}
    first = gateway.client.get(f'{PREFIX}/task-notifications', params=query).json()
    assert [item['notification_id'] for item in first['items']] == [original['notification_id']]
    query['cursor'] = first['next_cursor']
    second = gateway.client.get(f'{PREFIX}/task-notifications', params=query).json()
    assert second['next_cursor'] == ''
    assert second['items'][0]['notification_id'] == retried.json()['notification_id']
    assert second['items'][0]['retry_of'] == original['notification_id']
    forbidden = gateway.client.get(f'{PREFIX}/task-notifications', params=query, headers={'X-User-Id': 'other'})
    assert forbidden.json()['items'] == []
