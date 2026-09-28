from concurrent.futures import ThreadPoolExecutor
import json
import uuid

import httpx
import pytest


PREFIX = '/api/channel-gateway/v1'


class OneIteration:
    """Deterministic worker scheduling, without replacing delivery/storage behavior."""
    def __init__(self):
        self.visited = False

    def is_set(self):
        old = self.visited
        self.visited = True
        return old

    def wait(self, _timeout):
        return True

    def set(self):
        self.visited = True


def deliver_once(gateway, monkeypatch, *, error=None):
    sent = []
    provider = gateway.components.delivery_worker._providers.delivery('wechat')

    def send_text(**payload):
        sent.append(payload)
        if error:
            raise error

    monkeypatch.setattr(provider._client, 'send_text', send_text)
    worker = gateway.components.delivery_worker
    monkeypatch.setattr(worker, '_stop', OneIteration())
    worker._run('test-delivery')
    return sent


def notification(row, *, recipient='recipient-a', event_id=None):
    return {'event_id': event_id or uuid.uuid4().hex, 'task_id': 'scheduled-run',
            'schedule_id': 'daily-summary', 'event': 'succeeded', 'config_revision': 1,
            'title': '每日简报', 'body': '已整理三项进展，下一步进行联调。', 'content': 'summary',
            'channel': row['provider'], 'account_id': row['id'], 'recipient_id': recipient}


def enqueue(gateway, payload):
    gateway.core_events.publish(payload)
    response = gateway.client.post(f'{PREFIX}/task-notifications', json=payload)
    assert response.status_code in (200, 201), response.text
    assert response.json().get('notification_id')
    return response.json()


def outbox(gateway, outbox_id):
    with gateway.store._connect() as connection:
        return connection.execute('SELECT * FROM channel_outbox WHERE id = %s', (outbox_id,)).fetchone()


def test_existing_chat_reply_still_uses_shared_worker(gateway, account, queued_reply, monkeypatch):
    row = account()
    reply = queued_reply(row)
    sent = deliver_once(gateway, monkeypatch)
    assert len(sent) == 1
    assert sent[0]['text'] == '已完成简报'
    assert outbox(gateway, reply['id'])['status'] == 'sent'


def test_worker_claim_is_exclusive_and_stale_owner_cannot_complete(gateway, account, queued_reply):
    reply = queued_reply(account())

    def claim(owner):
        return gateway.store.claim_next_outbound(owner, lease_seconds=120)
    with ThreadPoolExecutor(max_workers=2) as executor:
        claimed = [item for item in executor.map(claim, ['worker-a', 'worker-b']) if item]
    assert len(claimed) == 1
    assert claimed[0].outbox_id == reply['id']
    assert not gateway.store.complete_outbound(reply['id'], 'stale-owner')
    assert outbox(gateway, reply['id'])['status'] == 'sending'


def test_scheduled_notification_reuses_outbox_and_latest_encrypted_target_context(
        gateway, account, incoming, monkeypatch):
    row, other = account(), account()
    for selected, recipient, token in [(row, 'recipient-a', 'old-context'), (row, 'recipient-b', 'other-target'),
                                       (other, 'recipient-a', 'other-account'), (row, 'recipient-a', 'latest-context')]:
        incoming(selected, recipient, token)
        claimed = gateway.store.claim_next_inbound('context-reader', lease_seconds=120)
        assert gateway.store.complete_inbound(claimed.inbox_id, 'context-reader', [], {})
    result = enqueue(gateway, notification(row))
    queued = outbox(gateway, result['outbox_id'])
    assert queued['purpose'] == 'notification'
    assert 'latest-context' not in json.dumps(dict(queued), default=str)
    sent = deliver_once(gateway, monkeypatch)
    assert len(sent) == 1
    assert sent[0]['to_user_id'] == 'recipient-a'
    assert sent[0]['context_token'] == 'latest-context'
    assert '已整理三项进展' in sent[0]['text']
    assert outbox(gateway, result['outbox_id'])['status'] == 'sent'


def test_event_replay_after_lost_enqueue_response_creates_one_outbox(gateway, account, incoming):
    row = account()
    incoming(row, context='context')
    payload = notification(row)
    first = enqueue(gateway, payload)
    with ThreadPoolExecutor(max_workers=4) as executor:
        results = list(executor.map(lambda _: enqueue(gateway, payload), range(4)))
    assert all(item['notification_id'] == first['notification_id'] for item in results)
    with gateway.store._connect() as connection:
        rows = connection.execute('SELECT id FROM channel_outbox WHERE account_id = %s', (row['id'],)).fetchall()
    assert len(rows) == 1


@pytest.mark.parametrize('case', ['wrong_owner', 'wrong_provider', 'wrong_recipient', 'no_context', 'disconnected'])
def test_target_binding_fails_closed_without_queueing(gateway, account, incoming, case):
    row = account(owner='other' if case == 'wrong_owner' else 'owner')
    if case != 'no_context':
        incoming(row, context='context')
    payload = notification(row)
    if case == 'wrong_provider':
        payload['channel'] = 'wecom'
    if case == 'wrong_recipient':
        payload['recipient_id'] = 'unseen-recipient'
    if case == 'disconnected':
        assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    response = gateway.client.post(f'{PREFIX}/task-notifications', json=payload)
    assert response.status_code == 422, response.text
    assert response.json()['error']['code'] == 'NOTIFICATION_TARGET_UNAVAILABLE'
    with gateway.store._connect() as connection:
        assert connection.execute('SELECT COUNT(*) AS n FROM channel_outbox').fetchone()['n'] == 0


def test_notification_unknown_send_outcome_is_not_blindly_retried(gateway, account, incoming, monkeypatch):
    row = account()
    incoming(row, context='context')
    result = enqueue(gateway, notification(row))
    sent = deliver_once(gateway, monkeypatch, error=httpx.ReadTimeout('response lost after provider accepted'))
    assert len(sent) == 1
    record = gateway.client.get(f'{PREFIX}/task-notifications/{result["notification_id"]}')
    assert record.status_code == 200
    assert record.json()['status'] == 'unknown'
    assert record.json()['reason'] == 'NOTIFICATION_DELIVERY_UNKNOWN'
    assert gateway.store.claim_next_outbound('next-worker', lease_seconds=120) is None


def test_notification_error_response_does_not_expose_provider_secrets(
        gateway, account, incoming, monkeypatch, caplog):
    row = account()
    incoming(row, context='context')
    result = enqueue(gateway, notification(row))
    secret = gateway.cipher.decrypt('owner', row['credentials_ciphertext'])['token']
    deliver_once(gateway, monkeypatch, error=httpx.ReadTimeout('provider token=' + secret))
    response = gateway.client.get(f'{PREFIX}/task-notifications/{result["notification_id"]}')
    assert response.status_code == 200
    assert secret not in response.text
    assert secret not in caplog.text
    assert response.json().get('reason')


def test_disconnect_skips_pending_notification_and_reconnect_does_not_replay(gateway, account, incoming):
    row = account()
    incoming(row, context='context')
    result = enqueue(gateway, notification(row))
    assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    assert gateway.store.claim_next_outbound('closed-worker', lease_seconds=120) is None
    record = gateway.client.get(f'{PREFIX}/task-notifications/{result["notification_id"]}')
    assert record.status_code == 200
    assert record.json()['status'] == 'skipped'
    with gateway.store._connect() as connection:
        connection.execute("UPDATE channel_accounts SET status = 'connected' WHERE id = %s", (row['id'],))
    assert gateway.store.claim_next_outbound('reopened-worker', lease_seconds=120) is None


def test_retry_requires_confirmation_and_preserves_unknown_original(gateway, account, incoming, monkeypatch):
    row = account()
    incoming(row, context='context')
    result = enqueue(gateway, notification(row))
    deliver_once(gateway, monkeypatch, error=httpx.ReadTimeout('lost response'))
    path = f'{PREFIX}/task-notifications/{result["notification_id"]}:retry'
    response = gateway.client.post(path, json={'idempotency_key': 'retry-1'})
    assert response.status_code == 409
    assert response.json()['error']['code'] == 'NOTIFICATION_CONFIRMATION_REQUIRED'
    response = gateway.client.post(path, json={'idempotency_key': 'retry-1', 'confirm_duplicate_risk': True})
    assert response.status_code in (200, 201)
    retried = response.json()
    assert retried['notification_id'] != result['notification_id']
    assert retried['retry_of'] == result['notification_id']
    repeated = gateway.client.post(path, json={'idempotency_key': 'retry-1', 'confirm_duplicate_risk': True})
    assert repeated.json()['notification_id'] == retried['notification_id']
    assert gateway.client.get(f'{PREFIX}/task-notifications/{result["notification_id"]}').json()['status'] == 'unknown'


def test_notification_api_requires_identity(gateway):
    response = gateway.client.post(f'{PREFIX}/task-notifications', json={}, headers={'X-User-Id': ''})
    assert response.status_code == 401
    assert response.json()['error']['code'] == 'UNAUTHORIZED'


@pytest.mark.parametrize('field', [None, 'body', 'title', 'event_id', 'task_id', 'schedule_id', 'config_revision'])
def test_uncommitted_or_tampered_core_event_cannot_send(gateway, account, incoming, field):
    row = account()
    incoming(row, context='context')
    payload = notification(row)
    if field:
        gateway.core_events.publish(payload)
        payload[field] = 99 if field == 'config_revision' else 'tampered'
    response = gateway.client.post(f'{PREFIX}/task-notifications', json=payload)
    assert response.status_code == 422
    assert response.json()['error']['code'] == 'NOTIFICATION_EVENT_INVALID'
    assert gateway.store.claim_next_outbound('no-send', lease_seconds=120) is None


def test_core_verification_failure_cannot_send(gateway, account, incoming):
    row = account()
    incoming(row, context='context')
    gateway.core_events.error = httpx.ReadTimeout('synthetic-private-provider-details')
    response = gateway.client.post(f'{PREFIX}/task-notifications', json=notification(row))
    assert response.status_code == 503
    assert 'synthetic-private' not in response.text
    assert gateway.store.claim_next_outbound('no-send', lease_seconds=120) is None


def test_global_close_blocks_queue_and_reopen_does_not_replay(gateway, account, incoming, monkeypatch):
    row = account()
    incoming(row, context='context')
    result = enqueue(gateway, notification(row))
    gateway.core_events.enabled = False
    assert deliver_once(gateway, monkeypatch) == []
    assert outbox(gateway, result['outbox_id'])['status'] == 'skipped'
    gateway.core_events.enabled = True
    assert gateway.store.claim_next_outbound('reopen', lease_seconds=120) is None
