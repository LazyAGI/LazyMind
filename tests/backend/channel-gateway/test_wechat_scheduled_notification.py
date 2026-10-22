"""Exercise the iLink ingress and HTTP delivery boundaries for scheduled notices."""
import json

import httpx
import pytest

from channel_gateway.app import app
from channel_gateway.bootstrap import build_components
from channel_gateway.wechat.domain import WeChatAddressFactory, WeChatConfig
from channel_gateway.wechat.inbound import WeChatInboundNormalizer

from test_notification_delivery import OneIteration, PREFIX, notification, outbox


@pytest.mark.parametrize('event', ['succeeded', 'failed'])
def test_wechat_scheduled_notice_uses_real_ingress_context_and_survives_restart(
        gateway, account, monkeypatch, event):
    row = account()
    delivery = gateway.components.delivery_worker._providers.delivery('wechat')
    credentials = gateway.cipher.decrypt('owner', row['credentials_ciphertext'])
    normalizer = WeChatInboundNormalizer(
        config=WeChatConfig('https://wechat.test.invalid', 480, 40, 3, 1800, '/tmp', 1024),
        store=gateway.store, client=delivery._client, addresses=WeChatAddressFactory(),
    )
    # Real iLink ingress carries the reply token in provider_context. The generic
    # incoming fixture supplies sensitive_context instead, so cover both paths.
    envelope = normalizer.normalize(row, credentials, {
        'message_type': 1, 'message_id': 'activate-notifications',
        'from_user_id': 'recipient-a', 'context_token': 'initial-context',
        'item_list': [{'type': 1, 'text_item': {'text': '接收定时任务通知'}}],
    })
    assert envelope is not None
    assert gateway.store.ingest_batch(row['id'], [envelope], None) == 1
    endpoint = f'{PREFIX}/channel-accounts/{row["id"]}'
    response = gateway.client.put(endpoint + '/default-recipient', json={'recipient_id': 'recipient-a'})
    assert response.status_code == 200, response.text

    restored = build_components(gateway.settings)
    try:
        restored.store.initialize()
        monkeypatch.setattr(app.state, 'components', restored)
        detail = gateway.client.get(endpoint, params={'include_references': 'false'})
        assert detail.status_code == 200, detail.text
        assert detail.json()['capabilities']['notification_ready'] is True
        assert detail.json()['default_recipient']['available'] is True

        payload = {**notification(row), 'event': event}
        gateway.core_events.publish(payload)
        # Account-only rules resolve to the explicit default recipient before
        # Core verifies the committed scheduled event.
        response = gateway.client.post(f'{PREFIX}/task-notifications', json={**payload, 'recipient_id': ''})
        assert response.status_code == 201, response.text
        queued = response.json()

        refreshed = normalizer.normalize(row, credentials, {
            'message_type': 1, 'message_id': 'refresh-notifications',
            'from_user_id': 'recipient-a', 'context_token': 'latest-context',
            'item_list': [{'type': 1, 'text_item': {'text': '继续接收通知'}}],
        })
        assert refreshed is not None
        assert restored.store.ingest_batch(row['id'], [refreshed], None) == 1
        assert 'latest-context' not in json.dumps(dict(outbox(gateway, queued['outbox_id'])), default=str)

        sent = []

        def post(url, **kwargs):
            assert url == 'https://wechat.test.invalid/ilink/bot/sendmessage'
            assert kwargs['headers']['Authorization'] == 'Bearer ' + credentials['token']
            sent.append(kwargs['json']['msg'])
            return httpx.Response(200, json={'ret': 0})

        monkeypatch.setattr(httpx, 'post', post)
        monkeypatch.setattr(restored.delivery_worker, '_stop', OneIteration())
        restored.delivery_worker._run('scheduled-wechat-after-restart')
        assert len(sent) == 1
        assert sent[0]['to_user_id'] == 'recipient-a'
        assert sent[0]['context_token'] == 'latest-context'
        assert payload['body'] in sent[0]['item_list'][0]['text_item']['text']
        assert outbox(gateway, queued['outbox_id'])['status'] == 'sent'
    finally:
        restored.stop()
