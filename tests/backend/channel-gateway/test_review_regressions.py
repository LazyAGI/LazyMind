from unittest.mock import Mock

import httpx
import pytest

from channel_gateway.common.application.notifications import NotificationService
from channel_gateway.common.errors import ProviderRejectedError
from channel_gateway.wecom.service import WeComService


def test_resolution_detail_does_not_call_core(gateway, account):
    row = account()
    core = Mock()
    service = NotificationService(gateway.store, core)
    view = service.account_detail('owner', row['id'], include_references=False)
    assert view['default_recipient'] is None
    core.notification_references.assert_not_called()


def test_stale_credentials_cannot_disconnect(gateway, account):
    row = account()
    assert not gateway.store.disconnect_account(
        'owner', row['id'], retain_credentials=True,
        expected_revision=row['credential_revision'] - 1,
    )
    assert gateway.store.get_account('owner', row['id'])['status'] == 'connected'


def test_auth_timeout_is_retryable_before_send(monkeypatch):
    service = WeComService(Mock(), Mock(), Mock())
    service._cipher.decrypt.return_value = {'bot_id': 'bot', 'secret': 'secret'}
    post = Mock(side_effect=httpx.ReadTimeout('auth timeout'))
    monkeypatch.setattr(httpx, 'post', post)
    with pytest.raises(ProviderRejectedError) as error:
        service._cli_call({'id': 'a', 'credential_revision': 1,
                           'owner_user_id': 'owner', 'credentials_ciphertext': 'cipher'},
                          '/message/aibot/send', {})
    assert error.value.retryable
    assert post.call_count == 1
    assert 'get_cli_config' in post.call_args.args[0]


def test_stale_runtime_fence_cannot_disconnect(gateway, account):
    from channel_gateway.common.errors import RuntimeLeaseLostError
    row = account()
    lease = gateway.store.acquire_runtime_lease(row['id'])
    lease.close()
    with pytest.raises(RuntimeLeaseLostError):
        gateway.store.disconnect_account(
            'owner', row['id'], expected_revision=row['credential_revision'], runtime_fence=lease.fence)
    assert gateway.store.get_account('owner', row['id'])['status'] == 'connected'


def test_qr_recovery_scans_durable_sessions():
    import datetime as dt
    store = Mock()
    store.recoverable_sessions.return_value = [{
        'id': 'session', 'qr_version': 2, 'owner_user_id': 'owner',
        'expires_at': dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=2),
        'provider_state_ciphertext': 'cipher',
    }]
    service = WeComService(store, Mock(), Mock())
    service._start_qr_worker = Mock()
    service._reconcile_sessions()
    service._start_qr_worker.assert_called_once_with('session', 2, 'owner')


def test_send_response_timeout_remains_unknown(monkeypatch):
    service = WeComService(Mock(), Mock(), Mock())
    service._token = Mock(return_value='token')
    monkeypatch.setattr(httpx, 'post', Mock(side_effect=httpx.ReadTimeout('send reply lost')))
    with pytest.raises(httpx.ReadTimeout):
        service._cli_call({}, '/message/aibot/send', {})


@pytest.mark.parametrize('status,retryable', [(429, True), (503, True), (401, False)])
def test_auth_http_errors_never_send(monkeypatch, status, retryable):
    service = WeComService(Mock(), Mock(), Mock())
    service._cipher.decrypt.return_value = {'bot_id': 'bot', 'secret': 'secret'}
    post = Mock(return_value=httpx.Response(status, request=httpx.Request('POST', 'https://auth.test')))
    monkeypatch.setattr(httpx, 'post', post)
    with pytest.raises(ProviderRejectedError) as error:
        service._cli_call({'id': 'a', 'credential_revision': 1,
                           'owner_user_id': 'owner', 'credentials_ciphertext': 'cipher'},
                          '/message/aibot/send', {})
    assert error.value.retryable is retryable
    assert post.call_count == 1


def test_history_returns_verified_source_id(gateway, account, incoming):
    from test_notification_delivery import notification, enqueue
    row = account()
    incoming(row, context='context')
    payload = notification(row)
    receipt = enqueue(gateway, payload)
    source = gateway.core_events.events[-1][1]['notification_id']
    assert receipt['source_notification_id'] == source
    assert 'notification_id' not in receipt['payload']
    history = gateway.store.notification_history('owner', payload['task_id'])
    assert history['items'][0]['source_notification_id'] == source


def test_refresh_qr_returns_public_session_view():
    store = Mock()
    store.restart_connection_session.return_value = {'id': 'session', 'qr_version': 2}
    service = WeComService(store, Mock(), Mock())
    service._prepare_qr = Mock()
    service.get_session = Mock(return_value={'id': 'session', 'status': 'waiting_scan'})
    assert service.refresh_session('owner', 'session')['status'] == 'waiting_scan'
    service.get_session.assert_called_once_with('owner', 'session')


def test_qr_worker_without_lease_does_not_poll():
    store = Mock()
    store.acquire_runtime_lease.return_value = None
    service = WeComService(store, Mock(), Mock())
    service._poll_qr = Mock()
    service._run_qr_worker('session', 2, 'owner')
    service._poll_qr.assert_not_called()


def test_qr_worker_releases_lease_after_temporary_failure():
    store = Mock()
    service = WeComService(store, Mock(), Mock())
    service._poll_qr = Mock(side_effect=RuntimeError('temporary store failure'))
    service._qr_workers[('session', 2)] = Mock()
    service._run_qr_worker('session', 2, 'owner')
    store.acquire_runtime_lease.return_value.close.assert_called_once()
    assert not service._qr_workers
    store.mark_failed.assert_not_called()


@pytest.mark.parametrize('status,body,transport_error,expected,reason', [
    (401, {}, None, 'dead', 'NOTIFICATION_DELIVERY_FAILED'),
    (429, {}, None, 'retry_wait', 'NOTIFICATION_DELIVERY_FAILED'),
    (503, {}, None, 'retry_wait', 'NOTIFICATION_DELIVERY_FAILED'),
    (200, {'errcode': 40001, 'errmsg': 'private diagnostic'}, None, 'dead', 'NOTIFICATION_DELIVERY_FAILED'),
    (200, {'results_json': '{"error":{"code":40001,"message":"private diagnostic"}}'}, None, 'dead', 'NOTIFICATION_DELIVERY_FAILED'),
    (200, {'errcode': 853004}, None, 'dead', 'NOTIFICATION_DELIVERY_FAILED'),
    (200, {'errcode': 850003, 'errmsg': 'private diagnostic'}, None, 'dead', 'WECOM_CAPABILITY_REAUTH_REQUIRED'),
    (200, {'results_json': '{"error":{"code":850003,"message":"private diagnostic"}}'}, None, 'dead', 'WECOM_CAPABILITY_REAUTH_REQUIRED'),
    (200, {}, httpx.ReadTimeout('reply lost'), 'unknown', 'NOTIFICATION_DELIVERY_UNKNOWN'),
    (200, {}, httpx.ConnectError('not submitted'), 'retry_wait', 'NOTIFICATION_DELIVERY_FAILED'),
])
def test_wecom_cli_delivery_outcome(gateway, account, monkeypatch, status, body, transport_error, expected, reason):
    from test_notification_delivery import OneIteration, enqueue, notification, outbox, PREFIX
    row = account('wecom')
    gateway.store.cache_wecom_notification_sessions('owner', row['id'], row['credential_revision'], [
        {'recipient_id': 'recipient-a', 'label': 'recipient', 'kind': 'conversation'},
    ])
    record = enqueue(gateway, notification(row))
    worker = gateway.components.delivery_worker
    service = worker._providers.delivery('wecom')
    monkeypatch.setattr(service, '_token', Mock(return_value='token'))
    post = Mock(side_effect=transport_error, return_value=httpx.Response(
        status, json=body, request=httpx.Request('POST', 'https://provider.test/message/aibot/send')))
    monkeypatch.setattr(httpx, 'post', post)
    monkeypatch.setattr(worker, '_stop', OneIteration())
    worker._run('wecom-cli-review')
    stored = outbox(gateway, record['outbox_id'])
    assert stored['status'] == expected
    assert stored['last_error'] == reason
    if body.get('errcode') == 853004:
        assert post.call_count == 2  # Exactly one refresh retry, then a definite rejection.
    if reason == 'WECOM_CAPABILITY_REAUTH_REQUIRED':
        assert post.call_count == 1  # Capability authorization cannot be fixed by refreshing the token.
    if expected in ('dead', 'unknown'):
        view = gateway.client.get(f'{PREFIX}/task-notifications/{record["notification_id"]}').json()
        assert view['status'] == ('failed' if expected == 'dead' else 'unknown')
        assert view['reason'] == reason
        assert 'private diagnostic' not in str(view)
