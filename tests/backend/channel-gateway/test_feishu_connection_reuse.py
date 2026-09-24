"""Approved Feishu reuse/pause semantics; only external runtime startup is replaced."""
from concurrent.futures import ThreadPoolExecutor

import pytest


PREFIX = '/api/channel-gateway/v1'


@pytest.fixture
def feishu(gateway, monkeypatch):
    registry = gateway.components.delivery_worker._providers
    connections = registry.connection('feishu')
    accounts = registry.accounts('feishu')
    calls = {'registration': [], 'started': [], 'stopped': []}
    monkeypatch.setattr(connections, '_start_worker', lambda *args: calls['registration'].append(args))
    monkeypatch.setattr(accounts, '_on_account_connected', calls['started'].append)
    monkeypatch.setattr(accounts, '_on_account_disconnected', calls['stopped'].append)
    return calls


def lifecycle(gateway, row, action, owner='owner'):
    return gateway.client.post(f'{PREFIX}/channel-accounts/{row["id"]}:{action}',
                               headers={'X-User-Id': owner})


def connect(gateway, *, key='reuse-test', **payload):
    return gateway.client.post(f'{PREFIX}/connection-sessions',
                               json={'provider': 'feishu', **payload},
                               headers={'Idempotency-Key': key})


def assert_error(response, status, code):
    assert response.status_code == status, response.text
    assert response.json()['error']['code'] == code


def test_pause_preserves_credentials_and_history_but_stops_delivery(gateway, account, queued_reply, feishu):
    row = account('feishu')
    reply = queued_reply(row)
    claimed = gateway.store.claim_next_outbound('pre-pause', lease_seconds=120)
    assert claimed is not None
    response = lifecycle(gateway, row, 'pause')
    assert response.status_code == 204, response.text
    paused = gateway.store.get_account('owner', row['id'])
    assert paused['status'] == 'disconnected'
    assert paused['runtime_status'] == 'stopped'
    assert paused['credentials_ciphertext'] == row['credentials_ciphertext']
    assert not gateway.store.renew_outbound_lease(claimed.outbox_id, 'pre-pause', lease_seconds=120)
    assert not gateway.store.complete_outbound(claimed.outbox_id, 'pre-pause')
    with gateway.store._connect() as connection:
        assert connection.execute('SELECT id FROM channel_inbox WHERE id = %s',
                                  (reply['inbox_id'],)).fetchone()
        assert connection.execute('SELECT id FROM channel_outbox WHERE id = %s',
                                  (reply['id'],)).fetchone()
    assert row['id'] in feishu['stopped']
    assert gateway.store.claim_next_outbound('after-pause', lease_seconds=120) is None


def test_pause_resume_is_idempotent_and_never_registers(gateway, account, queued_reply, feishu):
    row = account('feishu')
    queued_reply(row)
    for _ in range(2):
        assert lifecycle(gateway, row, 'pause').status_code == 204
    for _ in range(2):
        response = lifecycle(gateway, row, 'resume')
        assert response.status_code == 200, response.text
        assert response.json()['id'] == row['id']
        assert response.json()['status'] == 'connected'
        assert 'credentials_ciphertext' not in response.text
        assert row['credentials_ciphertext'] not in response.text
    resumed = gateway.store.get_account('owner', row['id'])
    assert resumed['credentials_ciphertext'] == row['credentials_ciphertext']
    assert len(gateway.store.list_accounts('owner', 'feishu')) == 1
    assert feishu['registration'] == []
    assert feishu['started'] == [row['id']]
    assert gateway.store.claim_next_outbound('no-replay', lease_seconds=120) is None


def test_concurrent_resume_starts_the_existing_runtime_once(gateway, account, feishu):
    row = account('feishu')
    assert lifecycle(gateway, row, 'pause').status_code == 204
    with ThreadPoolExecutor(max_workers=4) as executor:
        responses = list(executor.map(lambda _: lifecycle(gateway, row, 'resume'), range(4)))
    assert [r.status_code for r in responses] == [200] * 4
    assert feishu['started'] == [row['id']]
    assert feishu['registration'] == []


@pytest.mark.parametrize('action', ['pause', 'resume'])
def test_lifecycle_rejects_another_owner_without_side_effects(gateway, account, feishu, action):
    row = account('feishu')
    assert_error(lifecycle(gateway, row, action, 'other'), 404, 'ACCOUNT_NOT_FOUND')
    retained = gateway.store.get_account('owner', row['id'])
    assert retained['status'] == row['status']
    assert retained['credentials_ciphertext'] == row['credentials_ciphertext']
    assert not any(feishu.values())


@pytest.mark.parametrize('provider', ['wechat', 'wecom'])
def test_pause_does_not_change_other_providers(gateway, account, provider):
    row = account(provider)
    assert_error(lifecycle(gateway, row, 'pause'), 422, 'PROVIDER_NOT_SUPPORTED')
    assert gateway.store.get_account('owner', row['id'])['credentials_ciphertext'] == row['credentials_ciphertext']


@pytest.mark.parametrize('provider', ['wechat', 'wecom'])
def test_resume_connected_account_is_idempotent(gateway, account, provider, monkeypatch):
    row = account(provider)
    adapter = gateway.components.delivery_worker._providers.accounts(provider)
    started = []
    if provider == 'wechat':
        monkeypatch.setattr(adapter, '_on_account_connected', started.append)
    else:
        monkeypatch.setattr(adapter._runtime, 'restart_account', started.append)

    for _ in range(2):
        response = lifecycle(gateway, row, 'resume')
        assert response.status_code == 200, response.text
        assert response.json()['id'] == row['id']
        assert response.json()['status'] == 'connected'
        assert 'credentials_ciphertext' not in response.text
        assert row['credentials_ciphertext'] not in response.text

    retained = gateway.store.get_account('owner', row['id'])
    assert retained['credentials_ciphertext'] == row['credentials_ciphertext']
    assert retained['credential_revision'] == row['credential_revision']
    assert started == []


def test_unbind_still_erases_credentials_and_cannot_resume(gateway, account, feishu):
    row = account('feishu')
    for _ in range(2):
        assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    retained = gateway.store.get_account('owner', row['id'])
    assert retained['credentials_ciphertext'] == ''
    assert_error(lifecycle(gateway, row, 'resume'), 409, 'FEISHU_REAUTHORIZATION_REQUIRED')
    assert feishu['registration'] == []
    assert feishu['started'] == []


@pytest.mark.parametrize('ciphertext', ['', 'invalid-synthetic-ciphertext'])
def test_missing_or_damaged_credentials_do_not_trigger_creation(gateway, account, feishu, ciphertext):
    row = account('feishu')
    with gateway.store._connect() as connection:
        connection.execute("UPDATE channel_accounts SET status = 'disconnected', "
                           'credentials_ciphertext = %s WHERE id = %s',
                           (ciphertext, row['id']))
    response = lifecycle(gateway, row, 'resume')
    assert_error(response, 409, 'FEISHU_REAUTHORIZATION_REQUIRED')
    assert 'invalid-synthetic-ciphertext' not in response.text
    assert feishu['registration'] == []
    assert feishu['started'] == []
    assert gateway.store.get_account('owner', row['id'])['status'] == 'disconnected'


def test_unbind_racing_resume_cannot_leave_a_connected_account_without_credentials(gateway, account, feishu):
    row = account('feishu')
    assert lifecycle(gateway, row, 'pause').status_code == 204
    with ThreadPoolExecutor(max_workers=2) as executor:
        restore = executor.submit(lifecycle, gateway, row, 'resume')
        erase = executor.submit(gateway.client.delete, f'{PREFIX}/channel-accounts/{row["id"]}')
        assert erase.result().status_code == 204
        assert restore.result().status_code in {200, 409}
    retained = gateway.store.get_account('owner', row['id'])
    assert retained['credentials_ciphertext'] == ''
    assert retained['status'] == 'disconnected'
    assert_error(lifecycle(gateway, row, 'resume'), 409, 'FEISHU_REAUTHORIZATION_REQUIRED')
    assert feishu['registration'] == []


def test_default_connection_reuses_single_existing_robot(gateway, account, feishu):
    row = account('feishu')
    response = connect(gateway)
    assert response.status_code == 201, response.text
    assert response.json()['status'] == 'connected'
    assert response.json()['account']['id'] == row['id']
    assert feishu['registration'] == []
    assert len(gateway.store.list_accounts('owner', 'feishu')) == 1
    again = connect(gateway)
    assert again.json()['id'] == response.json()['id']
    read = gateway.client.get(f'{PREFIX}/connection-sessions/{response.json()["id"]}')
    assert read.json()['account']['id'] == row['id']


def test_multiple_robots_require_selection_and_selected_id_is_reused(gateway, account, feishu):
    first, second = account('feishu'), account('feishu')
    assert_error(connect(gateway), 409, 'ACCOUNT_SELECTION_REQUIRED')
    response = connect(gateway, key='selected', account_id=second['id'])
    assert response.status_code == 201, response.text
    assert response.json()['status'] == 'connected'
    assert response.json()['account']['id'] == second['id']
    assert gateway.store.get_account('owner', first['id'])['status'] == 'connected'
    assert feishu['registration'] == []


def test_default_cannot_treat_an_unbound_robot_as_first_connection(gateway, account, feishu):
    row = account('feishu')
    assert gateway.client.delete(f'{PREFIX}/channel-accounts/{row["id"]}').status_code == 204
    assert_error(connect(gateway), 409, 'FEISHU_REAUTHORIZATION_REQUIRED')
    assert feishu['registration'] == []


def test_selected_paused_robot_restores_without_qr(gateway, account, feishu):
    row = account('feishu')
    assert lifecycle(gateway, row, 'pause').status_code == 204
    response = connect(gateway, account_id=row['id'])
    assert response.status_code == 201, response.text
    assert response.json()['account']['id'] == row['id']
    assert response.json()['status'] == 'connected'
    assert feishu['registration'] == []


@pytest.mark.parametrize('explicit_new', [False, True])
def test_first_connection_and_explicit_new_deduplicate_registration(gateway, account, feishu, explicit_new):
    if explicit_new:
        account('feishu')
    payload = {'create_new': True} if explicit_new else {}
    with ThreadPoolExecutor(max_workers=3) as executor:
        responses = list(executor.map(lambda _: connect(gateway, **payload), range(3)))
    assert [r.status_code for r in responses] == [201] * 3
    assert len({r.json()['id'] for r in responses}) == 1
    assert len(feishu['registration']) == 1


def test_same_idempotency_key_cannot_switch_from_reuse_to_creation(gateway, account, feishu):
    account('feishu')
    assert connect(gateway).status_code == 201
    assert_error(connect(gateway, create_new=True), 409, 'ACCOUNT_STATE_CHANGED')
    assert feishu['registration'] == []


@pytest.mark.parametrize('payload', [
    {'create_new': 'true'}, {'create_new': 1}, {'create_new': True, 'account_id': 'existing'},
    {'provider': 'wechat', 'create_new': True},
    {'provider': 'wecom', 'create_new': True, 'credentials': {'bot_id': 'bot', 'secret': 'synthetic'}},
])
def test_new_robot_intent_is_strict_and_feishu_only(gateway, feishu, payload):
    assert_error(connect(gateway, **payload), 422, 'INVALID_REQUEST')
    assert feishu['registration'] == []


def test_reuse_session_cannot_be_read_or_retargeted_by_other_owner(gateway, account, feishu):
    row = account('feishu')
    response = connect(gateway, account_id=row['id'])
    assert response.status_code == 201
    session_id = response.json()['id']
    hidden = gateway.client.get(f'{PREFIX}/connection-sessions/{session_id}', headers={'X-User-Id': 'other'})
    assert hidden.status_code == 404
    forbidden = gateway.client.post(f'{PREFIX}/connection-sessions',
                                    json={'provider': 'feishu', 'account_id': row['id']},
                                    headers={'X-User-Id': 'other'})
    assert_error(forbidden, 404, 'ACCOUNT_NOT_FOUND')
    assert feishu['registration'] == []


def test_resume_does_not_rotate_or_erase_credentials_from_a_later_pause(gateway, account, feishu):
    row = account('feishu')
    assert lifecycle(gateway, row, 'pause').status_code == 204
    assert lifecycle(gateway, row, 'resume').status_code == 200
    assert lifecycle(gateway, row, 'pause').status_code == 204
    retained = gateway.store.get_account('owner', row['id'])
    assert retained['credentials_ciphertext'] == row['credentials_ciphertext']
    assert retained['status'] == 'disconnected'
    assert feishu['registration'] == []


@pytest.mark.parametrize('create_new,app_id', [(False, 'cli_existing'), (True, None)])
def test_registration_adapter_distinguishes_reauthorization_from_creation(monkeypatch, create_new, app_id):
    import threading
    from channel_gateway.feishu import registration
    calls = []

    def register(**kwargs):
        calls.append(kwargs)
        return {'client_id': app_id or 'cli_new', 'client_secret': 'synthetic-only',
                'user_info': {'open_id': 'owner-open'}}

    monkeypatch.setattr(registration.lark_oapi, 'register_app', register)
    result = registration.LarkAppRegistrar().register(
        on_qr_code=lambda *_: None, on_status_change=lambda *_: None,
        cancel_event=threading.Event(), create_new=create_new, app_id=app_id,
    )
    assert calls[0]['create_only'] is create_new
    assert calls[0].get('app_id') == app_id
    assert result.app_id == (app_id or 'cli_new')
