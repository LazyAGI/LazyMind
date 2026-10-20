"""WeCom account identity and readable naming on both gateway database drivers."""
import datetime as dt
import hashlib
import json
from concurrent.futures import ThreadPoolExecutor
import uuid

import pytest


PREFIX = '/api/channel-gateway/v1'


@pytest.fixture
def wecom(gateway, monkeypatch):
    service = gateway.components.delivery_worker._providers.delivery('wecom')

    async def verify(_credentials):
        return None

    monkeypatch.setattr('channel_gateway.wecom.service.verify_credentials', verify)
    monkeypatch.setattr(service._runtime, 'restart_account', lambda _account_id: None)
    return service


def credentials_connect(gateway, bot_id, account_id=None):
    payload = {'provider': 'wecom', 'credentials': {'bot_id': bot_id, 'secret': 'new-test-secret'}}
    if account_id:
        payload['account_id'] = account_id
    response = gateway.client.post(f'{PREFIX}/connection-sessions', json=payload)
    assert response.status_code == 201, response.text
    assert response.json()['status'] == 'connected', response.text
    return response.json()['account']


def qr_connect(gateway, wecom, monkeypatch, bot_id, bot_name, account_id=None):
    session, _ = gateway.store.reserve_session(
        session_id=f'cs_{uuid.uuid4().hex}', owner_user_id='owner', provider='wecom',
        idempotency_key=None, expires_at=dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=5),
        requested_account_id=account_id,
    )
    session = gateway.store.set_qr_ready(
        session['id'], gateway.cipher.encrypt('owner', {'scode': 'test-scan'}),
        session['expires_at'], '等待扫码',
    )

    class Response:
        def raise_for_status(self):
            return None

        def json(self):
            return {'data': {'status': 'success', 'bot_info': {
                'botid': bot_id, 'secret': 'new-test-secret', 'bot_name': bot_name,
            }}}

    monkeypatch.setattr('channel_gateway.wecom.service.httpx.get', lambda *_args, **_kwargs: Response())
    wecom._poll_qr(session['id'], session['qr_version'], 'owner')
    view = wecom.get_session('owner', session['id'])
    assert view['status'] == 'connected', view
    return view['account']


@pytest.mark.parametrize('requested', [False, True])
@pytest.mark.parametrize('disconnect', [False, True])
def test_credentials_reauthorization_reuses_identity_and_keeps_custom_name(
        gateway, account, wecom, requested, disconnect):
    old = account('wecom', identity='same-bot')
    with gateway.store._connect() as connection:
        connection.execute('UPDATE channel_accounts SET label = %s, label_custom = TRUE WHERE id = %s',
                           ('产品日报助手', old['id']))
    if disconnect:
        assert gateway.store.disconnect_account('owner', old['id'], retain_credentials=True)

    restored = credentials_connect(gateway, 'same-bot', old['id'] if requested else None)

    assert restored['id'] == old['id']
    assert restored['label'] == '产品日报助手'
    assert len(gateway.store.list_accounts('owner', 'wecom')) == 1
    stored = gateway.store.get_account('owner', old['id'])
    assert stored['credential_revision'] == old['credential_revision'] + (2 if disconnect else 1)
    assert stored['label_custom']
    assert gateway.cipher.decrypt('owner', stored['credentials_ciphertext'])['secret'] == 'new-test-secret'


def test_default_names_use_bot_identity_after_account_removal(gateway, wecom):
    first = credentials_connect(gateway, 'first')
    second = credentials_connect(gateway, 'second')
    third = credentials_connect(gateway, 'third')
    assert [row['label'] for row in (first, second, third)] == [
        '企业微信机器人', '企业微信机器人', '企业微信机器人',
    ]
    assert gateway.store.delete_account('owner', second['id'])

    fourth = credentials_connect(gateway, 'fourth')

    assert fourth['label'] == '企业微信机器人'
    identities = [row['identity']['bot_id'] for row in wecom.list_accounts('owner')['items']]
    assert set(identities) == {'first', 'third', 'fourth'}
    assert credentials_connect(gateway, 'first')['label'] == first['label']


def test_qr_bot_names_use_stable_identity_and_refresh_real_name_on_reauthorization(gateway, wecom, monkeypatch):
    first = qr_connect(gateway, wecom, monkeypatch, 'first', '产品日报助手')
    second = qr_connect(gateway, wecom, monkeypatch, 'second', '产品日报助手')
    assert first['label'] == '产品日报助手'
    assert second['label'] == '产品日报助手'
    assert first['identity'] == {'bot_id': 'first', 'bot_name': '产品日报助手'}
    assert second['identity'] == {'bot_id': 'second', 'bot_name': '产品日报助手'}

    restored = qr_connect(gateway, wecom, monkeypatch, 'second', '企业微信中的新名称', second['id'])

    assert restored['id'] == second['id']
    assert restored['label'] == '企业微信中的新名称'
    assert restored['identity'] == {'bot_id': 'second', 'bot_name': '企业微信中的新名称'}
    assert gateway.store.get_account('owner', second['id'])['label'] == second['label']
    assert len(gateway.store.list_accounts('owner', 'wecom')) == 2


def test_bot_names_are_bounded_without_connection_ordinals(gateway, wecom, monkeypatch):
    name = '简' * 140
    first = qr_connect(gateway, wecom, monkeypatch, 'first', name)
    second = qr_connect(gateway, wecom, monkeypatch, 'second', name)
    third = qr_connect(gateway, wecom, monkeypatch, 'third', name)
    assert first['label'] == name[:128]
    assert second['label'] == name[:128]
    assert third['label'] == name[:128]
    assert len(third['label']) == 128


def test_concurrent_callbacks_with_same_name_keep_distinct_bot_identities(gateway):
    # Simulate two durable callbacks created before the current active-session
    # guard; each callback must remain tied to its own immutable robot identity.
    sessions = []
    with gateway.store._connect() as connection:
        for _ in range(2):
            session_id = f'cs_{uuid.uuid4().hex}'
            connection.execute('''
                INSERT INTO channel_connection_sessions(
                    id, owner_user_id, provider, status, message, expires_at
                ) VALUES(%s, 'owner', 'wecom', 'confirming', '正在连接', %s)
            ''', (session_id, dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=5)))
            sessions.append(session_id)

    def complete(session_id):
        return gateway.store.save_connected_account(
            session_id=session_id, qr_version=1, expected_revision=1, owner_user_id='owner', provider='wecom',
            external_id_hash=hashlib.sha256(session_id.encode()).hexdigest(), label='产品日报助手',
            credentials_ciphertext='test-only', conflict_message='身份已绑定', connected_message='已连接',
        )

    with ThreadPoolExecutor(max_workers=2) as executor:
        accounts = list(executor.map(complete, sessions))

    assert {row['label'] for row in accounts} == {'产品日报助手'}
    assert len({row['id'] for row in accounts}) == 2
    assert len({row['external_id_hash'] for row in accounts}) == 2


@pytest.mark.parametrize('view', ['list', 'detail', 'default_recipient', 'resume'])
def test_legacy_identity_backfills_allowlist_without_exposing_credentials(gateway, account, wecom, view):
    original = account('wecom', identity='legacy-bot-id', bot_name='企业微信中的名字',
                       private_key='synthetic-private-key')
    secret = gateway.cipher.decrypt('owner', original['credentials_ciphertext'])['secret']
    with gateway.store._connect() as connection:
        connection.execute('UPDATE channel_accounts SET label = %s WHERE id = %s',
                           ('企业微信机器人 7', original['id']))
    path = f'{PREFIX}/channel-accounts/{original["id"]}'
    if view == 'list':
        response = gateway.client.get(f'{PREFIX}/channel-accounts', params={'provider': 'wecom'})
        public = response.json()['items'][0]
    elif view == 'detail':
        response = gateway.client.get(path, params={'include_references': 'false'})
        public = response.json()
    elif view == 'default_recipient':
        response = gateway.client.put(path + '/default-recipient', json={'recipient_id': ''})
        public = response.json()
    else:
        assert gateway.store.disconnect_account('owner', original['id'], retain_credentials=True)
        response = gateway.client.post(path + ':resume')
        public = response.json()

    assert response.status_code == 200, response.text
    assert public['identity'] == {'bot_id': 'legacy-bot-id', 'bot_name': '企业微信中的名字'}
    assert public['label'] == '企业微信中的名字'
    assert public['label_custom'] is False
    assert secret not in response.text
    assert 'synthetic-private-key' not in response.text
    assert original['credentials_ciphertext'] not in response.text
    stored = gateway.store.get_account('owner', original['id'])
    assert json.loads(stored['identity_metadata']) == public['identity']
    assert stored['label'] == '企业微信机器人 7'


@pytest.mark.parametrize('custom', [False, True])
def test_legacy_default_labels_preserve_explicit_custom_names(gateway, account, wecom, custom):
    original = account('wecom', identity='legacy-bot')
    with gateway.store._connect() as connection:
        connection.execute('UPDATE channel_accounts SET label = %s, label_custom = %s WHERE id = %s',
                           ('企业微信机器人 7', custom, original['id']))
    public = wecom.list_accounts('owner')['items'][0]
    assert public['label'] == ('企业微信机器人 7' if custom else '企业微信机器人')
    assert public['identity'] == {'bot_id': 'legacy-bot', 'bot_name': ''}


def test_custom_wecom_name_and_identity_survive_qr_reauthorization(gateway, wecom, monkeypatch):
    original = qr_connect(gateway, wecom, monkeypatch, 'same-bot', '企业微信中的名字')
    renamed = gateway.client.patch(f'{PREFIX}/channel-accounts/{original["id"]}', json={'label': '  每日简报  '})
    assert renamed.status_code == 200, renamed.text
    assert renamed.json()['label'] == '每日简报'
    assert renamed.json()['label_custom'] is True
    restored = qr_connect(gateway, wecom, monkeypatch, 'same-bot', '企业微信中的新名字', original['id'])
    assert restored['id'] == original['id']
    assert restored['label'] == '每日简报'
    assert restored['identity'] == {'bot_id': 'same-bot', 'bot_name': '企业微信中的新名字'}
    assert restored['label_custom'] is True


@pytest.mark.parametrize('operation', ['list', 'detail', 'rename'])
def test_wecom_identity_and_renaming_are_owner_scoped(gateway, account, wecom, operation):
    original = account('wecom', identity='private-bot')
    path = f'{PREFIX}/channel-accounts/{original["id"]}'
    headers = {'X-User-Id': 'other'}
    if operation == 'list':
        response = gateway.client.get(f'{PREFIX}/channel-accounts', params={'provider': 'wecom'}, headers=headers)
        assert response.status_code == 200
        assert response.json()['items'] == []
    else:
        response = (gateway.client.get(path, params={'include_references': 'false'}, headers=headers)
                    if operation == 'detail' else gateway.client.patch(path, json={'label': '冒用名字'}, headers=headers))
        assert response.status_code == 404
    assert 'private-bot' not in response.text
    assert gateway.store.get_account('owner', original['id'])['label'] == original['label']
    assert gateway.store.get_account('owner', original['id'])['identity_metadata'] == '{}'


def test_public_identity_filters_secret_keys_even_when_stored_metadata_has_them(gateway, account, wecom):
    original = account('wecom', identity='public-bot')
    with gateway.store._connect() as connection:
        connection.execute('UPDATE channel_accounts SET identity_metadata = %s WHERE id = %s',
                           (json.dumps({'bot_id': 'public-bot', 'bot_name': '', 'secret': 'forbidden-secret'}),
                            original['id']))
    response = gateway.client.get(f'{PREFIX}/channel-accounts', params={'provider': 'wecom'})
    assert response.status_code == 200
    assert response.json()['items'][0]['identity'] == {'bot_id': 'public-bot', 'bot_name': ''}
    assert 'forbidden-secret' not in response.text


def test_wecom_identity_backfill_rejects_stale_revision_and_wrong_provider(gateway, account):
    original = account('wecom', identity='public-bot')
    metadata = {'bot_id': 'public-bot', 'bot_name': '机器人名称'}
    assert gateway.store.update_account_identity(
        original['id'], metadata, original['credential_revision'] - 1, provider='wecom',
    ) is None
    assert gateway.store.update_account_identity(
        original['id'], metadata, original['credential_revision'],
    ) is None
    assert gateway.store.get_account('owner', original['id'])['identity_metadata'] == '{}'
    updated = gateway.store.update_account_identity(
        original['id'], metadata, original['credential_revision'], provider='wecom',
    )
    assert json.loads(updated['identity_metadata']) == metadata


@pytest.mark.parametrize('label', ['', '  ', 'x' * 81, 'name\nother', 123, None])
def test_wecom_rename_rejects_invalid_labels(gateway, account, wecom, label):
    original = account('wecom')
    response = gateway.client.patch(f'{PREFIX}/channel-accounts/{original["id"]}', json={'label': label})
    assert response.status_code == 422
    assert gateway.store.get_account('owner', original['id'])['label'] == original['label']
