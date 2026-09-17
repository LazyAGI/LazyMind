import json
import uuid

import pytest

from channel_gateway.common.domain.channel import ClaimedOutbound


PREFIX = '/api/channel-gateway/v1'


@pytest.mark.parametrize('credentials', [None, {}, {'bot_id': 'bot'}, {'secret': 'test-only'},
                                         {'bot_id': 'a' * 257, 'secret': 'test-only'},
                                         {'bot_id': 123, 'secret': 'test-only'}])
def test_wecom_requires_valid_bot_credentials_before_connecting(gateway, credentials):
    response = gateway.client.post(f'{PREFIX}/connection-sessions', json={
        'provider': 'wecom', 'credentials': credentials,
    })
    assert response.status_code == 422, response.text
    assert response.json()['error']['code'] == 'INVALID_REQUEST'
    assert 'test-only' not in response.text
    assert gateway.store.list_accounts('owner', 'wecom') == []


def test_wecom_notification_rendering_uses_safe_actual_result(gateway, account):
    provider = gateway.components.delivery_worker._providers.delivery('wecom')
    assert provider is not None, 'Enterprise WeChat must use the shared provider composition'
    row = account('wecom')
    message = ClaimedOutbound(
        outbox_id=uuid.uuid4().hex, created_sequence=1, provider='wecom', account_id=row['id'],
        order_key='chat-id', recipient_id='chat-id', provider_context={},
        text='<think>private reasoning</think>每日简报：今天完成接口联调。', intent_kind='message',
        purpose='notification', metadata={'task_id': 'scheduled-run', 'event': 'succeeded'},
        rendered_parts=[], next_part_index=0, provider_state={}, attempt_count=1,
    )
    parts = provider.render(message)
    encoded = json.dumps(parts, ensure_ascii=False)
    assert parts
    assert '今天完成接口联调' in encoded
    assert 'private reasoning' not in encoded
    assert row['credentials_ciphertext'] not in encoded


@pytest.mark.parametrize('provider', ['wechat', 'feishu', 'wecom'])
def test_account_targets_are_owned_and_secrets_never_returned(gateway, account, incoming, provider):
    row = account(provider)
    incoming(row, context='synthetic-sensitive-context')
    path = f'{PREFIX}/channel-accounts/{row["id"]}/notification-targets'
    response = gateway.client.get(path)
    assert response.status_code == 200, response.text
    assert [item['recipient_id'] for item in response.json()['items']] == ['recipient-a']
    assert 'synthetic-sensitive-context' not in response.text
    assert gateway.client.get(path, headers={'X-User-Id': 'other'}).status_code == 404
