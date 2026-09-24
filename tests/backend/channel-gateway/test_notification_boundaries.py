"""Remaining failure boundaries, using the real stores and delivery worker."""
import datetime as dt
import threading
import uuid

import pytest
from aibot import ws

from channel_gateway.common.application.workers import _LeaseHeartbeat, LeaseLostError
from channel_gateway.common.errors import ProviderRejectedError
from channel_gateway.feishu.sdk import LarkChannelClient
from test_notification_delivery import OneIteration, deliver_once, enqueue, notification, outbox
from test_wecom_protocol import BotSocket


def test_failed_channel_does_not_block_other_channels(gateway, account, incoming, monkeypatch):
    rows = [account(provider) for provider in ('wechat', 'feishu', 'wecom')]
    records = []
    for row in rows:
        incoming(row, context='synthetic-context')
        records.append(enqueue(gateway, notification(row)))
    worker = gateway.components.delivery_worker
    runtime = worker._providers.delivery('wecom')._runtime
    ready = threading.Event()
    sockets, feishu_sent = [], []
    original_status = gateway.store.set_runtime_status

    async def connect(_url, **_kwargs):
        socket = BotSocket()
        sockets.append(socket)
        return socket

    def status(*args, **kwargs):
        result = original_status(*args, **kwargs)
        if args[0] == rows[2]['id'] and args[1] == 'running':
            ready.set()
        return result

    def reject(**_kwargs):
        raise ProviderRejectedError()

    def feishu_send(_self, **payload):
        feishu_sent.append(payload)
        return 'synthetic-message-id'

    monkeypatch.setattr(ws.websockets, 'connect', connect)
    monkeypatch.setattr(gateway.store, 'set_runtime_status', status)
    monkeypatch.setattr(worker._providers.delivery('wechat')._client, 'send_text', reject)
    monkeypatch.setattr(LarkChannelClient, '_send', feishu_send)
    runtime.restart_account(rows[2]['id'])
    try:
        assert ready.wait(5)
        for index in range(3):
            monkeypatch.setattr(worker, '_stop', OneIteration())
            worker._run(f'channel-isolation-{index}')
        assert [outbox(gateway, item['outbox_id'])['status'] for item in records] == ['dead', 'sent', 'sent']
        assert len(feishu_sent) == 1
        assert feishu_sent[0]['chat_id'] == 'recipient-a'
        assert len([frame for frame in sockets[0].sent if frame['cmd'] == 'aibot_send_msg']) == 1
        assert gateway.store.claim_next_outbound('no-replay', lease_seconds=120) is None
    finally:
        runtime.stop_account(rows[2]['id'])


@pytest.mark.parametrize('action', ['close', 'disconnect'])
def test_control_change_between_parts_prevents_next_send(gateway, account, incoming, monkeypatch, action):
    row = account()
    incoming(row, context='synthetic-context')
    payload = notification(row)
    payload.update(body='notification result ' * 800, content='full')
    record = enqueue(gateway, payload)
    worker = gateway.components.delivery_worker
    sent = []

    def send(**message):
        sent.append(message)
        if action == 'close':
            gateway.core_events.enabled = False
        else:
            assert gateway.store.disconnect_account('owner', row['id'])

    monkeypatch.setattr(worker._providers.delivery('wechat')._client, 'send_text', send)
    monkeypatch.setattr(worker, '_stop', OneIteration())
    worker._run('control-boundary')
    assert len(sent) == 1
    stored = outbox(gateway, record['outbox_id'])
    assert len(gateway.store._list(stored['rendered_parts'])) > 1
    # Disconnect during the request preserves uncertainty of that in-flight part.
    assert stored['status'] == ('skipped' if action == 'close' else 'unknown')
    gateway.core_events.enabled = True
    assert deliver_once(gateway, monkeypatch) == []


def test_lease_renewal_and_loss_fence_storage_with_controlled_ticks(gateway, account, incoming):
    row = account()
    incoming(row, context='synthetic-context')
    record = enqueue(gateway, notification(row))
    owner = 'lease-owner'
    assert gateway.store.claim_next_outbound(owner, lease_seconds=120)
    calls, waits = [], []

    def renew():
        result = gateway.store.renew_outbound_lease(record['outbox_id'], owner, lease_seconds=120)
        calls.append(result)
        return result

    class Ticks:
        def wait(self, seconds):
            waits.append(seconds)
            if len(waits) == 2:
                with gateway.store._connect() as connection:
                    connection.execute('UPDATE channel_outbox SET lease_until = %s WHERE id = %s',
                                       (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=1),
                                        record['outbox_id']))
            assert len(waits) <= 2, 'heartbeat did not stop after losing its lease'
            return False

    heartbeat = _LeaseHeartbeat(renew, name='controlled-lease')
    heartbeat._stop = Ticks()
    heartbeat._run()
    assert waits == [30, 30] and calls == [True, False]
    with pytest.raises(LeaseLostError):
        heartbeat.ensure_owned()
    assert not gateway.store.advance_outbound(record['outbox_id'], owner, 1)
    assert not gateway.store.complete_outbound(record['outbox_id'], owner)
    assert gateway.store.claim_next_outbound('replacement', lease_seconds=120) is None
    assert outbox(gateway, record['outbox_id'])['status'] == 'unknown'


@pytest.mark.parametrize('provider', ['wechat', 'feishu'])
@pytest.mark.parametrize('action', ['cancel', 'expire', 'refresh'])
def test_stale_qr_result_cannot_create_or_replace_account(gateway, account, provider, action):
    row = account(provider)
    expires = dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=5)
    session, _ = gateway.store.reserve_session(
        session_id=uuid.uuid4().hex, owner_user_id='owner', provider=provider,
        idempotency_key=uuid.uuid4().hex, expires_at=expires, requested_account_id=row['id'])
    if action == 'cancel':
        assert gateway.store.cancel_session('owner', session['id'])
    else:
        assert gateway.store.mark_expired(session['id'], session['qr_version'])
        if action == 'refresh':
            refreshed = gateway.store.refresh_session(
                owner_user_id='owner', session_id=session['id'], state_ciphertext='',
                expires_at=expires, message='new QR')
            assert refreshed['qr_version'] == session['qr_version'] + 1
    assert gateway.store.save_connected_account(
        session_id=session['id'], qr_version=session['qr_version'], expected_revision=session['revision'],
        owner_user_id='owner', provider=provider, external_id_hash=row['external_id_hash'],
        label='stale QR result', credentials_ciphertext=row['credentials_ciphertext'],
        conflict_message='conflict', connected_message='connected') is None
    current = gateway.store.get_account('owner', row['id'])
    assert current['credential_revision'] == row['credential_revision']
    assert current['label'] == row['label']
    assert len(gateway.store.list_accounts('owner', provider)) == 1
