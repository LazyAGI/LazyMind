"""Connection checks use synthetic getconfig responses and isolated fixture data."""
from concurrent.futures import ThreadPoolExecutor
import datetime as dt
import importlib.util
from pathlib import Path
import queue
import threading
from types import SimpleNamespace

import httpx
import pytest


PREFIX = '/api/channel-gateway/v1/channel-accounts'


@pytest.fixture
def platform(monkeypatch):
    boundary = SimpleNamespace(calls=[], status=200, payload={'ret': 0}, error=None, on_request=None)

    def post(url, **kwargs):
        # Any attempted message, typing notification, upload, or receiver call
        # fails here before it can reach the network.
        assert url == 'https://wechat.test.invalid/ilink/bot/getconfig'
        assert kwargs['timeout'] == 2.0
        boundary.calls.append(kwargs)
        if boundary.on_request:
            response = boundary.on_request(kwargs)
            if isinstance(response, httpx.Response):
                return response
        if boundary.error:
            raise boundary.error
        return httpx.Response(boundary.status, json=boundary.payload)

    monkeypatch.setattr(httpx, 'post', post)
    return boundary


def view(gateway, row):
    response = gateway.client.get(f'{PREFIX}/{row["id"]}', params={'include_references': 'false'})
    assert response.status_code == 200, response.text
    return response.json()


def connection_probe(gateway):
    service = gateway.components.delivery_worker._providers.connection('wechat')
    return service._connection_probe


def check(gateway, *, force=True, wait=True):
    rows = gateway.store.list_accounts('owner', 'wechat')
    if not force:
        return connection_probe(gateway).check_accounts(rows, force=False, wait=wait)
    for row in rows:
        if row['status'] == 'connected':
            connection_probe(gateway).check_target('owner', row['id'], 'recipient-a', wait=wait)


def context(gateway, row):
    return gateway.store.notification_context('owner', row['id'], 'recipient-a', 'wechat')


def allow_next_check(gateway, row):
    # Only the isolated test database is aged; no wall-clock sleep is needed.
    with gateway.store._connect() as connection:
        connection.execute('UPDATE channel_notification_targets SET connection_check_started_at=%s WHERE account_id=%s',
                           (dt.datetime.now(dt.timezone.utc) - dt.timedelta(seconds=11), row['id']))


def test_precise_target_contract_requires_confirmed_recipient_and_ownership(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='private-context')
    path = f'{PREFIX}/{row["id"]}/notification-targets'
    for params, owner, status in [
        ({'check_connection': 'true'}, 'owner', 422),
        ({'check_connection': 'true', 'recipient_id': 'unknown'}, 'owner', 422),
        ({'check_connection': 'true', 'recipient_id': 'recipient-a'}, 'other-owner', 404),
    ]:
        response = gateway.client.get(path, params=params, headers={'X-User-Id': owner})
        assert response.status_code == status, response.text
        assert platform.calls == []
    assert gateway.client.get(path).status_code == 200
    assert platform.calls == []


def test_confirming_one_target_probes_that_recipient_without_saved_schedule(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='context-a')
    incoming(row, recipient='recipient-b', context='context-b')
    assert gateway.core_events.wechat_targets == []
    response = gateway.client.get(f'{PREFIX}/{row["id"]}/notification-targets',
                                  params={'recipient_id': 'recipient-b', 'check_connection': 'true'})
    assert response.status_code == 200, response.text
    assert len(platform.calls) == 1
    assert platform.calls[0]['json']['ilink_user_id'] == 'recipient-b'
    assert platform.calls[0]['json']['context_token'] == 'context-b'
    target = response.json()['items'][0]
    assert target['connection_check_status'] == 'connected'
    assert target['connection_checked_at']
    assert target['connection_error_code'] == ''
    assert gateway.core_events.catalog_reads == 0


def test_account_check_and_startup_skip_all_unconfigured_accounts(gateway, account, incoming, platform, monkeypatch):
    row = account()
    incoming(row, context='historical-but-unconfirmed')
    response = gateway.client.get(PREFIX, params={'provider': 'wechat', 'check_connection': 'true'})
    assert response.status_code == 200, response.text
    supervisor = next(item for item in gateway.components.runtime_supervisors
                      if getattr(item, '_provider', None) == 'wechat')
    monkeypatch.setattr(supervisor._runtime, 'start_account', lambda *args, **kwargs: None)
    supervisor._runtime.reconcile_accounts(gateway.store.runtime_accounts('wechat'))
    assert platform.calls == []


@pytest.mark.parametrize('gate', ['global', 'wechat'])
def test_background_catalog_respects_notification_gates(gateway, account, incoming, platform, gate):
    row = account()
    incoming(row, context='configured-context')
    gateway.core_events.wechat_targets = [{'account_id': row['id'], 'recipient_id': 'recipient-a'}]
    if gate == 'global':
        gateway.core_events.enabled = False
    else:
        gateway.core_events.wechat_enabled = False
    response = gateway.client.get(PREFIX, params={'provider': 'wechat', 'check_connection': 'true'})
    assert response.status_code == 200, response.text
    assert platform.calls == []
    assert gateway.core_events.catalog_reads == 0


def test_background_catalog_only_probes_saved_precise_pairs(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='context-a')
    incoming(row, recipient='recipient-b', context='context-b')
    gateway.core_events.wechat_targets = [
        {'account_id': row['id'], 'recipient_id': 'recipient-b'},
        {'account_id': row['id'], 'recipient_id': 'recipient-b'},
        {'account_id': row['id'], 'recipient_id': 'unknown'},
    ]
    response = gateway.client.get(PREFIX, params={'provider': 'wechat', 'check_connection': 'true'})
    assert response.status_code == 200, response.text
    assert [call['json']['ilink_user_id'] for call in platform.calls] == ['recipient-b']


def test_recipient_checks_are_deduplicated_independently(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='context-a')
    incoming(row, recipient='recipient-b', context='context-b')
    probe = connection_probe(gateway)
    probe.check_target('owner', row['id'], 'recipient-a')
    probe.check_target('owner', row['id'], 'recipient-b')
    probe.check_target('owner', row['id'], 'recipient-a')
    probe.check_target('owner', row['id'], 'recipient-b')
    assert [call['json']['ilink_user_id'] for call in platform.calls] == ['recipient-a', 'recipient-b']


def test_new_credentials_refresh_core_gates_before_immediate_background_probe(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='context-a')
    gateway.core_events.wechat_targets = [{'account_id': row['id'], 'recipient_id': 'recipient-a'}]
    check(gateway, force=False)
    assert len(platform.calls) == 1
    gateway.core_events.enabled = False
    credentials = gateway.cipher.decrypt('owner', row['credentials_ciphertext'])
    assert gateway.store.update_account_credentials(
        row['id'], gateway.cipher.encrypt('owner', {**credentials, 'token': 'renewed-token'}),
        row['credential_revision'])
    check(gateway, force=False)
    assert len(platform.calls) == 1


def test_background_catalog_refreshes_saved_pairs_after_twenty_minutes(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='context-a')
    incoming(row, recipient='recipient-b', context='context-b')
    gateway.core_events.wechat_targets = [{'account_id': row['id'], 'recipient_id': 'recipient-a'}]
    check(gateway, force=False)
    gateway.core_events.wechat_targets = [{'account_id': row['id'], 'recipient_id': 'recipient-b'}]
    probe = connection_probe(gateway)
    checked_at, pairs, interval = probe._catalog['owner']
    probe._catalog['owner'] = (checked_at - 20 * 60 - 1, pairs, interval)
    check(gateway, force=False)
    assert gateway.core_events.catalog_reads == 2
    assert [call['json']['ilink_user_id'] for call in platform.calls] == ['recipient-a', 'recipient-b']


def test_bounded_background_batches_do_not_starve_later_recipients(gateway, account, incoming, platform):
    row = account()
    recipients = [f'recipient-{index:02d}' for index in range(20)]
    for recipient in recipients:
        incoming(row, recipient=recipient, context=f'context-{recipient}')
    gateway.core_events.wechat_targets = [
        {'account_id': row['id'], 'recipient_id': recipient} for recipient in recipients]
    check(gateway, force=False)
    assert len(platform.calls) == 16
    # The first batch is not due again for 20 minutes. Later recipients must
    # still get their immediate first check on the next supervisor pass.
    check(gateway, force=False)
    assert sorted(call['json']['ilink_user_id'] for call in platform.calls) == recipients


@pytest.mark.parametrize('change', ['global', 'wechat', 'rules', 'core_unavailable'])
def test_pending_background_batch_revalidates_current_gates_and_rules(
        gateway, account, incoming, platform, monkeypatch, change):
    row = account()
    recipients = [f'recipient-{index:02d}' for index in range(20)]
    for recipient in recipients:
        incoming(row, recipient=recipient, context=f'context-{recipient}')
    gateway.core_events.wechat_targets = [
        {'account_id': row['id'], 'recipient_id': recipient} for recipient in recipients]
    check(gateway, force=False)
    assert len(platform.calls) == 16
    if change == 'global':
        gateway.core_events.enabled = False
    elif change == 'wechat':
        gateway.core_events.wechat_enabled = False
    elif change == 'rules':
        # Deleted/disabled rules disappear from Core's authoritative catalog.
        gateway.core_events.wechat_targets = gateway.core_events.wechat_targets[:16]
    else:
        def unavailable(*args):
            raise RuntimeError('synthetic Core outage')
        monkeypatch.setattr(connection_probe(gateway)._core, 'wechat_notification_targets', unavailable)
    check(gateway, force=False)
    assert len(platform.calls) == 16


def test_completed_batch_does_not_poll_core_or_platform_until_due(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='context-a')
    gateway.core_events.wechat_targets = [{'account_id': row['id'], 'recipient_id': 'recipient-a'}]
    check(gateway, force=False)
    for _ in range(5):
        check(gateway, force=False)
    assert gateway.core_events.catalog_reads == 1
    assert len(platform.calls) == 1


def test_concurrent_recipient_results_keep_independent_states(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='context-a')
    incoming(row, recipient='recipient-b', context='context-b')
    entered, release = threading.Event(), threading.Event()

    def response(payload):
        if payload['json']['ilink_user_id'] == 'recipient-a':
            entered.set()
            assert release.wait(5)
            return httpx.Response(200, json={'ret': -2})
        return httpx.Response(200, json={'ret': 0})

    platform.on_request = response
    probe = connection_probe(gateway)
    probe.check_target('owner', row['id'], 'recipient-a', wait=False)
    try:
        assert entered.wait(3)
        probe.check_target('owner', row['id'], 'recipient-b')
        before = {item['recipient_id']: item for item in gateway.store.notification_targets('owner', row['id'])['items']}
        assert before['recipient-a']['connection_check_status'] == 'checking'
        assert before['recipient-b']['connection_check_status'] == 'connected'
    finally:
        release.set()
    probe.check_target('owner', row['id'], 'recipient-a')
    after = {item['recipient_id']: item for item in gateway.store.notification_targets('owner', row['id'])['items']}
    assert after['recipient-a']['connection_check_status'] == 'activation_required'
    assert after['recipient-b']['connection_check_status'] == 'connected'
    assert after['recipient-b']['available'] is True


def test_late_account_auth_rejection_cannot_clear_other_targets_fresh_inbound(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='retained-a')
    incoming(row, recipient='recipient-b', context='old-b')
    platform.status = 401
    platform.on_request = lambda payload: incoming(row, recipient='recipient-b', context='new-b')
    connection_probe(gateway).check_target('owner', row['id'], 'recipient-a')
    targets = gateway.store.notification_targets('owner', row['id'])['items']
    assert all(item['available'] for item in targets)
    assert gateway.store.notification_context('owner', row['id'], 'recipient-b', 'wechat')['context_token'] == 'new-b'


def test_connection_check_query_and_existing_read_permission_are_in_public_contract():
    from channel_gateway.app import app

    operation = app.openapi()['paths'][PREFIX]['get']
    query = next(parameter for parameter in operation['parameters'] if parameter['name'] == 'check_connection')
    assert query['in'] == 'query'
    assert query['schema']['type'] == 'boolean'
    assert query['schema']['default'] is False
    root = Path(__file__).resolve().parents[3]
    spec = importlib.util.spec_from_file_location(
        'wechat_probe_permission_extractor', root / 'backend/scripts/extract_api_permissions.py')
    extractor = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(extractor)
    permissions = extractor.extract_from_py_file(root / 'backend/channel-gateway/channel_gateway/app.py')
    entry = next(item for item in permissions if item['method'] == 'GET' and item['path'] == PREFIX)
    assert entry['permissions'] == ['qa.read']


def test_default_account_list_does_not_make_connection_requests(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='private-context')

    response = gateway.client.get(PREFIX, params={'provider': 'wechat'})

    assert response.status_code == 200, response.text
    assert len(response.json()['items']) == 1
    assert platform.calls == []


def test_explicit_check_only_probes_current_owners_connected_wechat_accounts(
        gateway, account, incoming, platform):
    first, second = account(), account()
    foreign, disconnected = account(owner='another-owner'), account()
    feishu, wecom = account('feishu'), account('wecom')
    for row in (first, second, foreign, disconnected, feishu, wecom):
        incoming(row, context=f'private-context-{row["id"]}')
    gateway.core_events.wechat_targets = [
        {'account_id': row['id'], 'recipient_id': 'recipient-a'} for row in (first, second, foreign, disconnected)
    ]
    with gateway.store._connect() as connection:
        connection.execute("UPDATE channel_accounts SET status='disconnected' WHERE id=%s", (disconnected['id'],))

    response = gateway.client.get(PREFIX, params={'provider': 'wechat', 'check_connection': 'true'})

    assert response.status_code == 200, response.text
    expected_tokens = {
        gateway.cipher.decrypt('owner', row['credentials_ciphertext'])['token']
        for row in (first, second)
    }
    assert len(platform.calls) == 2
    assert {call['headers']['Authorization'].removeprefix('Bearer ') for call in platform.calls} == expected_tokens
    items = {item['id']: item for item in response.json()['items']}
    for row in (first, second):
        capabilities = items[row['id']]['capabilities']
        assert capabilities['connection_check_status'] == 'connected'
        assert capabilities['connection_checked_at']
        assert capabilities['connection_error_code'] in ('', None)
        assert capabilities['notification_ready'] is True
    assert foreign['id'] not in items
    assert view(gateway, disconnected)['status'] == 'disconnected'
    for token in expected_tokens:
        assert token not in response.text
    assert 'private-context-' not in response.text


@pytest.mark.parametrize('provider', ['feishu', 'wecom'])
def test_connection_check_flag_does_not_probe_other_providers(
        gateway, account, incoming, platform, provider):
    incoming(account(), context='private-context')
    account(provider)

    response = gateway.client.get(PREFIX, params={'provider': provider, 'check_connection': 'true'})

    assert response.status_code == 200, response.text
    assert platform.calls == []


def test_connection_check_requires_current_user(gateway, account, platform):
    account()
    response = gateway.client.get(PREFIX, params={'provider': 'wechat', 'check_connection': 'true'},
                                  headers={'X-User-Id': ''})
    assert response.status_code == 401
    assert platform.calls == []


def test_successful_getconfig_without_inbound_context_does_not_enable_notifications(
        gateway, account, incoming, platform):
    row = account()
    incoming(row, context='previous-context')
    with gateway.store._connect() as connection:
        connection.execute('UPDATE channel_notification_targets SET context_ciphertext=NULL WHERE account_id=%s',
                           (row['id'],))

    check(gateway)

    capabilities = view(gateway, row)['capabilities']
    assert len(platform.calls) == 1
    assert capabilities['connection_check_status'] == 'activation_required'
    assert capabilities['connection_checked_at']
    assert capabilities['notification_ready'] is False


@pytest.mark.parametrize('payload', [
    {'ret': 0, 'errmsg': 'ok'},
    {'errcode': 0, 'errmsg': 'ok'},
    {'ret': 0, 'errcode': 0, 'errmsg': 'ok'},
])
def test_probe_accepts_the_clients_explicit_numeric_success_contract(
        gateway, account, incoming, platform, payload):
    row = account()
    incoming(row, context='private-context')
    platform.payload = payload

    check(gateway)

    assert view(gateway, row)['capabilities']['connection_check_status'] == 'connected'
    assert view(gateway, row)['capabilities']['notification_ready'] is True


@pytest.mark.parametrize('kind', ['timeout', 'connect', 'rate_limit', 'server'])
def test_transient_failures_preserve_existing_notification_context(
        gateway, account, incoming, platform, kind):
    row = account()
    incoming(row, context='retained-private-context')
    before = context(gateway, row)
    if kind == 'timeout':
        platform.error = httpx.ReadTimeout('private-network-error')
    elif kind == 'connect':
        platform.error = httpx.ConnectError('private-network-error')
    else:
        platform.status = {'rate_limit': 429, 'server': 503}[kind]
        platform.payload = {'errmsg': 'private-provider-error'}

    check(gateway)

    public = view(gateway, row)
    capabilities = public['capabilities']
    assert len(platform.calls) == 1
    assert capabilities['connection_check_status'] == 'unavailable'
    assert capabilities['connection_checked_at']
    assert capabilities['connection_error_code']
    assert capabilities['connection_error_code'] == 'WECHAT_CONNECTION_UNAVAILABLE'
    assert context(gateway, row) == before
    assert capabilities['notification_ready'] is True
    assert gateway.store.get_account('owner', row['id'])['status'] == 'connected'
    for secret in ('private-network-error', 'private-provider-error', 'retained-private-context'):
        assert secret not in str(public)


@pytest.mark.parametrize('status', [401, 403])
def test_http_auth_rejection_blocks_all_account_targets_until_reactivation(
        gateway, account, incoming, platform, status):
    row, other = account(), account()
    incoming(row, context='rejected-account-context')
    incoming(row, recipient='recipient-b', context='rejected-account-other-recipient')
    incoming(other, context='retained-other-account-context')
    before = context(gateway, other)
    platform.status = status
    platform.payload = {'errmsg': 'private-provider-error'}

    connection_probe(gateway).check_target('owner', row['id'], 'recipient-a')

    capabilities = view(gateway, row)['capabilities']
    assert capabilities['connection_check_status'] == 'activation_required'
    assert capabilities['connection_error_code'] == 'WECHAT_REAUTHORIZATION_REQUIRED'
    assert capabilities['notification_ready'] is False
    assert all(not target['available'] for target in gateway.store.notification_targets('owner', row['id'])['items'])
    assert context(gateway, other) == before
    retained = gateway.store.get_account('owner', row['id'])
    assert retained['status'] == 'connected'
    assert retained['credentials_ciphertext'] == row['credentials_ciphertext']

    allow_next_check(gateway, row)
    platform.status, platform.payload = 200, {'ret': 0}
    connection_probe(gateway).check_target('owner', row['id'], 'recipient-a')
    capabilities = view(gateway, row)['capabilities']
    assert capabilities['connection_check_status'] == 'activation_required'
    assert capabilities['connection_error_code'] == 'WECHAT_NOTIFICATION_CONTEXT_REQUIRED'
    assert capabilities['notification_ready'] is False

    incoming(row, context='new-private-context')
    assert view(gateway, row)['capabilities']['connection_check_status'] == 'connected'
    assert view(gateway, row)['capabilities']['notification_ready'] is True


def test_business_rejection_requires_new_inbound_and_getconfig_success_cannot_restore_context(
        gateway, account, incoming, platform):
    row = account()
    incoming(row, context='rejected-private-context')
    platform.payload = {'ret': -2, 'errmsg': 'private-provider-error'}

    check(gateway)

    capabilities = view(gateway, row)['capabilities']
    assert capabilities['connection_check_status'] == 'activation_required'
    assert capabilities['connection_error_code'] == 'WECHAT_NOTIFICATION_CONTEXT_REQUIRED'
    assert capabilities['notification_ready'] is False
    assert gateway.store.notification_targets('owner', row['id'])['items'][0]['available'] is False
    retained = gateway.store.get_account('owner', row['id'])
    assert retained['status'] == 'connected'
    assert retained['credentials_ciphertext'] == row['credentials_ciphertext']

    allow_next_check(gateway, row)
    platform.payload = {'ret': 0}
    check(gateway)
    assert len(platform.calls) == 2
    capabilities = view(gateway, row)['capabilities']
    assert capabilities['connection_check_status'] == 'activation_required'
    assert capabilities['notification_ready'] is False
    assert gateway.store.notification_targets('owner', row['id'])['items'][0]['available'] is False

    incoming(row, context='new-private-context')
    capabilities = view(gateway, row)['capabilities']
    assert capabilities['connection_check_status'] == 'connected'
    assert capabilities['notification_ready'] is True
    assert context(gateway, row)['context_token'] == 'new-private-context'


def test_probe_rejection_only_invalidates_the_probed_recipient(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='rejected-recipient-context')
    incoming(row, recipient='recipient-b', context='retained-other-recipient-context')
    before = gateway.store.notification_context('owner', row['id'], 'recipient-b', 'wechat')
    platform.payload = {'ret': -2}

    check(gateway)

    assert len(platform.calls) == 1
    assert platform.calls[0]['json']['ilink_user_id'] == 'recipient-a'
    targets = gateway.store.notification_targets('owner', row['id'])['items']
    assert {item['recipient_id']: item['available'] for item in targets} == {
        'recipient-a': False, 'recipient-b': True,
    }
    assert gateway.store.notification_context('owner', row['id'], 'recipient-b', 'wechat') == before
    capabilities = view(gateway, row)['capabilities']
    assert capabilities['connection_check_status'] == 'activation_required'
    assert capabilities['notification_ready'] is True


def test_one_unavailable_account_does_not_abort_checks_for_other_accounts(
        gateway, account, incoming, platform):
    failing, healthy = account(), account()
    for row in (failing, healthy):
        incoming(row, context=f'private-context-{row["id"]}')
    rejected_token = gateway.cipher.decrypt('owner', failing['credentials_ciphertext'])['token']

    def respond(payload):
        if payload['headers']['Authorization'] == f'Bearer {rejected_token}':
            return httpx.Response(429, json={'errmsg': 'private-provider-error'})
        return httpx.Response(200, json={'ret': 0})

    platform.on_request = respond

    check(gateway)

    assert len(platform.calls) == 2
    assert view(gateway, failing)['capabilities']['connection_check_status'] == 'unavailable'
    assert view(gateway, healthy)['capabilities']['connection_check_status'] == 'connected'
    assert context(gateway, failing)['context_token'] == f'private-context-{failing["id"]}'


@pytest.mark.parametrize('status', [200, 401])
@pytest.mark.parametrize('new_context', ['renewed-private-context', 'original-private-context'])
def test_late_probe_rejection_does_not_override_new_inbound_even_with_same_token(
        gateway, account, incoming, platform, new_context, status):
    row = account()
    incoming(row, context='original-private-context')
    before = context(gateway, row)
    platform.on_request = lambda payload: incoming(row, context=new_context)
    platform.status = status
    platform.payload = {'ret': -2}

    check(gateway)

    after = context(gateway, row)
    assert after['context_token'] == new_context
    assert after['_notification_context_version'] != before['_notification_context_version']
    capabilities = view(gateway, row)['capabilities']
    assert capabilities['connection_check_status'] == 'connected'
    assert capabilities['notification_ready'] is True


def test_late_probe_rejection_cannot_invalidate_context_after_credential_rotation(
        gateway, account, incoming, platform):
    row = account()
    incoming(row, context='retained-private-context')
    before = context(gateway, row)

    def rotate_credentials(payload):
        old = gateway.cipher.decrypt('owner', row['credentials_ciphertext'])
        changed = gateway.cipher.encrypt('owner', {**old, 'token': 'new-private-token'})
        assert gateway.store.update_account_credentials(row['id'], changed, row['credential_revision'])

    platform.on_request = rotate_credentials
    platform.payload = {'ret': -2}

    check(gateway)

    after = context(gateway, row)
    assert after['context_token'] == before['context_token']
    assert after['_notification_context_version'] == before['_notification_context_version']
    assert after['_notification_credential_revision'] > before['_notification_credential_revision']
    capabilities = view(gateway, row)['capabilities']
    assert capabilities['connection_check_status'] != 'activation_required'
    assert capabilities['notification_ready'] is True


def test_force_check_remains_deduplicated_for_ten_seconds(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='private-context')

    check(gateway)
    check(gateway)
    assert len(platform.calls) == 1

    allow_next_check(gateway, row)
    check(gateway, force=False)
    assert len(platform.calls) == 1
    check(gateway)
    assert len(platform.calls) == 2


def test_concurrent_checks_share_one_provider_request(gateway, account, incoming, platform):
    row = account()
    incoming(row, context='private-context')
    entered, release = threading.Event(), threading.Event()

    def block_request(payload):
        entered.set()
        assert release.wait(5), 'Test did not release the synthetic provider request'

    platform.on_request = block_request
    with ThreadPoolExecutor(max_workers=2) as executor:
        first = executor.submit(check, gateway)
        try:
            assert entered.wait(3), 'Connection check did not reach the mocked provider'
            duplicate = executor.submit(check, gateway, wait=False)
            duplicate.result(timeout=3)
            assert len(platform.calls) == 1
            assert view(gateway, row)['capabilities']['connection_check_status'] == 'checking'
        finally:
            release.set()
        first.result(timeout=5)

    assert len(platform.calls) == 1
    assert view(gateway, row)['capabilities']['connection_check_status'] == 'connected'


def test_runtime_supervisor_probes_on_startup_and_later_timer_without_receiver_threads(
        gateway, account, incoming, platform, monkeypatch):
    row = account()
    incoming(row, context='private-context')
    gateway.core_events.wechat_targets = [{'account_id': row['id'], 'recipient_id': 'recipient-a'}]
    supervisor = next(item for item in gateway.components.runtime_supervisors
                      if getattr(item, '_provider', None) == 'wechat')
    runtime = supervisor._runtime
    # The receiver is intentionally not started. The real supervisor, timer,
    # probe, credentials, and persistence still run against the fixture DB.
    monkeypatch.setattr(runtime, 'start_account', lambda *args, **kwargs: None)
    supervisor._interval_seconds = 0.02
    observed = queue.Queue()
    platform.on_request = lambda payload: observed.put(True)
    # A previous process's persisted probe must not delay this process's first
    # check until the new 20-minute background interval has elapsed.
    with gateway.store._connect() as connection:
        connection.execute(
            'UPDATE channel_notification_targets SET connection_check_started_at=%s WHERE account_id=%s',
            (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=1), row['id']))

    supervisor.start()
    try:
        observed.get(timeout=3)
        check(gateway, force=False)
        assert view(gateway, row)['capabilities']['connection_check_status'] == 'connected'
        assert len(platform.calls) == 1
        with gateway.store._connect() as connection:
            connection.execute(
                'UPDATE channel_notification_targets SET connection_check_started_at=%s WHERE account_id=%s',
                (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=19, seconds=59), row['id']))
        check(gateway, force=False)
        assert len(platform.calls) == 1
        with gateway.store._connect() as connection:
            connection.execute(
                'UPDATE channel_notification_targets SET connection_check_started_at=%s WHERE account_id=%s',
                (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=20, seconds=1), row['id']))
        # Advance the in-process scheduling clock as well as persisted time.
        probe = connection_probe(gateway)
        with probe._lock:
            probe._scheduled_at[(row['id'], 'recipient-a')] -= 20 * 60 + 1
        observed.get(timeout=3)
        check(gateway, force=False)
        assert len(platform.calls) == 2
        assert view(gateway, row)['capabilities']['connection_check_status'] == 'connected'
    finally:
        supervisor.stop()


def test_empty_successful_poll_recovers_runtime_but_requires_new_message_for_activation(
        gateway, account, incoming, platform):
    from channel_gateway.common.domain.channel import ReceiverCheckpoint

    row = account()
    incoming(row, context='rejected-private-context')
    platform.payload = {'ret': -2}
    check(gateway)
    gateway.store.set_runtime_status(row['id'], 'degraded', 'Synthetic transient receive failure')

    assert gateway.store.ingest_batch(row['id'], [], ReceiverCheckpoint(cursor='synthetic-cursor')) == 0

    current = view(gateway, row)
    assert current['runtime_status'] == 'running'
    assert current['last_error'] is None
    assert current['last_poll_at']
    assert current['capabilities']['connection_check_status'] == 'activation_required'
    assert current['capabilities']['notification_ready'] is False

    incoming(row, context='fresh-private-context')
    assert view(gateway, row)['capabilities']['connection_check_status'] == 'connected'
    assert view(gateway, row)['capabilities']['notification_ready'] is True


@pytest.mark.parametrize('status,payload', [(200, {'ret': -2}), (401, {'errmsg': 'private-provider-error'})])
def test_probe_rejection_blocks_already_queued_notification_before_any_send(
        gateway, account, incoming, platform, monkeypatch, status, payload):
    from test_notification_delivery import deliver_once, enqueue, notification, outbox

    row = account()
    incoming(row, context='original-private-context')
    queued = enqueue(gateway, notification(row))
    assert outbox(gateway, queued['outbox_id'])['status'] == 'pending'
    platform.status, platform.payload = status, payload

    check(gateway)
    sent = deliver_once(gateway, monkeypatch)

    assert sent == []
    assert len(platform.calls) == 1
    blocked = outbox(gateway, queued['outbox_id'])
    assert blocked['status'] == 'skipped'
    assert blocked['last_error'] == 'NOTIFICATION_TARGET_UNAVAILABLE'

    allow_next_check(gateway, row)
    platform.status, platform.payload = 200, {'ret': 0}
    check(gateway)
    assert len(platform.calls) == 2
    assert view(gateway, row)['capabilities']['notification_ready'] is False
    still_unavailable = gateway.client.post('/api/channel-gateway/v1/task-notifications', json=notification(row))
    assert still_unavailable.status_code == 422
    assert still_unavailable.json()['error']['code'] == 'NOTIFICATION_TARGET_UNAVAILABLE'

    incoming(row, context='renewed-private-context')
    renewed = enqueue(gateway, notification(row))
    sent = deliver_once(gateway, monkeypatch)
    assert len(sent) == 1
    assert sent[0]['context_token'] == 'renewed-private-context'
    assert outbox(gateway, renewed['outbox_id'])['status'] == 'sent'
    assert outbox(gateway, queued['outbox_id'])['status'] == 'skipped'
