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
    listed = gateway.client.get(PREFIX, params={'provider': 'wecom'}).json()['items'][0]
    assert listed['capabilities']['authorization_required'] is True
    detail = gateway.client.get(f'{PREFIX}/{row["id"]}', params={'include_references': 'false'})
    assert detail.json()['capabilities']['authorization_required'] is True
    # Surfacing the error must not destroy history needed after reauthorization.
    cached = gateway.store.notification_targets('owner', row['id'])['items']
    assert len(cached) == int(has_cached_target)


def test_wecom_authorization_recovers_only_after_a_successful_permission_check(gateway, account, monkeypatch):
    row = account('wecom')
    gateway.store.mark_wecom_authorization_required(
        'owner', row['id'], row['credential_revision'], 'WECOM_CAPABILITY_REAUTH_REQUIRED')
    # A healthy socket does not prove that messaging permission has recovered.
    gateway.store.set_runtime_status(row['id'], 'running')
    assert gateway.client.get(PREFIX, params={'provider': 'wecom'}).json()['items'][0][
        'capabilities']['authorization_required'] is True
    service = gateway.components.delivery_worker._providers.delivery('wecom')
    monkeypatch.setattr(service, '_cli_call', lambda *_args, **_kwargs: {'sessions': []})
    response = gateway.client.get(f'{PREFIX}/{row["id"]}/notification-targets')
    assert response.status_code == 200
    assert gateway.client.get(PREFIX, params={'provider': 'wecom'}).json()['items'][0][
        'capabilities']['authorization_required'] is False


def test_wecom_authorization_checks_cannot_modify_another_owner_or_credential_revision(gateway, account):
    row = account('wecom')
    for owner, revision in [('other-owner', row['credential_revision']), ('owner', row['credential_revision'] + 1)]:
        gateway.store.mark_wecom_authorization_required(
            owner, row['id'], revision, 'WECOM_CAPABILITY_REAUTH_REQUIRED')
    assert gateway.client.get(PREFIX, params={'provider': 'wecom'}).json()['items'][0][
        'capabilities']['authorization_required'] is False


@pytest.mark.parametrize('has_cached_target', [False, True])
def test_wecom_temporary_session_outage_keeps_cached_target_fallback(
    gateway, account, incoming, monkeypatch, has_cached_target,
):
    row = account('wecom')
    gateway.store.mark_wecom_authorization_required(
        'owner', row['id'], row['credential_revision'], 'WECOM_CAPABILITY_REAUTH_REQUIRED')
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
    assert gateway.client.get(PREFIX, params={'provider': 'wecom'}).json()['items'][0][
        'capabilities']['authorization_required'] is True
