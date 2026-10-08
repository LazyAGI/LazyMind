import pytest

from channel_gateway.common.errors import GatewayError


PREFIX = '/api/channel-gateway/v1/channel-accounts'


@pytest.mark.parametrize('code', ['WECOM_CAPABILITY_REAUTH_REQUIRED', 'WECOM_REAUTHORIZATION_REQUIRED'])
@pytest.mark.parametrize('has_cached_target', [False, True])
def test_wecom_authorization_errors_are_visible_even_with_cached_targets(
    gateway, account, incoming, monkeypatch, code, has_cached_target,
):
    row = account('wecom')
    if has_cached_target:
        incoming(row)
    service = gateway.components.delivery_worker._providers.delivery('wecom')

    def authorization_expired(*_args):
        raise GatewayError(409, code, '企业微信会话读取权限不可用，请重新授权机器人', retryable=False)

    monkeypatch.setattr(service, 'sync_notification_targets', authorization_expired)
    response = gateway.client.get(f'{PREFIX}/{row["id"]}/notification-targets')

    assert response.status_code == 409, response.text
    assert response.json()['error']['code'] == code
    assert response.json()['error']['retryable'] is False
    # Surfacing the error must not destroy history needed after reauthorization.
    cached = gateway.store.notification_targets('owner', row['id'])['items']
    assert len(cached) == int(has_cached_target)


@pytest.mark.parametrize('has_cached_target', [False, True])
def test_wecom_temporary_session_outage_keeps_cached_target_fallback(
    gateway, account, incoming, monkeypatch, has_cached_target,
):
    row = account('wecom')
    if has_cached_target:
        incoming(row)
    service = gateway.components.delivery_worker._providers.delivery('wecom')

    def temporarily_unavailable(*_args):
        raise GatewayError(503, 'WECOM_SESSIONS_UNAVAILABLE', '企业微信会话暂时无法读取', retryable=True)

    monkeypatch.setattr(service, 'sync_notification_targets', temporarily_unavailable)
    response = gateway.client.get(f'{PREFIX}/{row["id"]}/notification-targets')

    if has_cached_target:
        assert response.status_code == 200, response.text
        assert [item['recipient_id'] for item in response.json()['items']] == ['recipient-a']
    else:
        assert response.status_code == 503, response.text
        assert response.json()['error']['code'] == 'WECOM_SESSIONS_UNAVAILABLE'
        assert response.json()['error']['retryable'] is True
