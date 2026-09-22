"""Connection defaults use real stores; only the Feishu network boundary is replaced."""
import datetime as dt

import pytest

from channel_gateway.common.errors import GatewayError
from channel_gateway.feishu.groups import FeishuGroups


@pytest.fixture
def groups(monkeypatch):
    rows = [{'recipient_id': 'oc_daily', 'label': '产品日报群', 'kind': 'group', 'available': True},
            {'recipient_id': 'oc_research', 'label': '竞品研究群', 'kind': 'group', 'available': True}]
    monkeypatch.setattr(FeishuGroups, 'list', lambda self, cursor='', limit=100: {
        'items': rows[1:2] if cursor else rows[:1], 'next_cursor': '' if cursor else 'next'})
    monkeypatch.setattr(FeishuGroups, 'get',
                        lambda self, recipient: next(r for r in rows if r['recipient_id'] == recipient))
    return rows


def endpoint(row):
    return f'/api/channel-gateway/v1/channel-accounts/{row["id"]}'


@pytest.mark.parametrize('provider', ['wechat', 'wecom'])
def test_disconnect_cancels_pending_reconnect_for_same_account(gateway, account, provider):
    row = account(provider)
    session, created = gateway.store.reserve_session(
        session_id=f'{provider}-reconnect', owner_user_id='owner', provider=provider,
        idempotency_key=None, expires_at=dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=5),
        requested_account_id=row['id'],
    )
    assert created and session['status'] == 'preparing'
    response = gateway.client.delete(endpoint(row))
    assert response.status_code == 204, response.text
    retained = gateway.store.get_account('owner', row['id'])
    assert retained['credentials_ciphertext'] == row['credentials_ciphertext']
    assert gateway.store.get_session('owner', session['id'])['status'] == 'canceled'


def test_discovery_pagination_default_clear_and_restart(gateway, account, groups):
    row = account('feishu')
    url = endpoint(row)
    assert gateway.store.get_account('owner', row['id'])['default_recipient_id'] == ''
    first = gateway.client.get(url + '/notification-groups')
    assert first.status_code == 200
    assert first.json() == {'items': groups[:1], 'next_cursor': 'next'}
    assert gateway.client.get(url + '/notification-groups?cursor=next').json()['items'] == groups[1:]
    response = gateway.client.put(url + '/default-recipient', json={'recipient_id': 'oc_daily'})
    assert response.status_code == 200
    assert response.json()['default_recipient_id'] == 'oc_daily'
    assert 'credentials_ciphertext' not in response.json()
    gateway.store.initialize()
    gateway.store.initialize()
    assert gateway.store.get_account('owner', row['id'])['default_recipient_id'] == 'oc_daily'
    assert gateway.store.notification_targets('owner', row['id'])['items'] == groups
    assert gateway.client.put(url + '/default-recipient', json={'recipient_id': ''}).status_code == 200
    assert gateway.store.get_account('owner', row['id'])['default_recipient_id'] == ''


def test_owner_and_account_boundaries_checked_before_network(gateway, account, groups, monkeypatch):
    row, other = account('feishu'), account('feishu')
    gateway.client.get(endpoint(row) + '/notification-groups')

    def forbidden(*args):
        pytest.fail('Unauthorized network call')
    monkeypatch.setattr(FeishuGroups, 'list', forbidden)
    monkeypatch.setattr(FeishuGroups, 'get', forbidden)
    assert gateway.client.get(endpoint(row) + '/notification-groups', headers={'X-User-Id': 'other'}).status_code == 404
    assert gateway.client.put(endpoint(row) + '/default-recipient', headers={'X-User-Id': 'other'},
                              json={'recipient_id': 'oc_daily'}).status_code == 404
    response = gateway.client.put(endpoint(other) + '/default-recipient', json={'recipient_id': 'oc_daily'})
    assert response.status_code == 422


def test_removed_group_rejected_and_previous_default_preserved(gateway, account, groups, monkeypatch):
    row = account('feishu')
    url = endpoint(row)
    gateway.client.get(url + '/notification-groups')
    assert gateway.client.put(url + '/default-recipient', json={'recipient_id': 'oc_daily'}).status_code == 200

    def removed(*args):
        raise GatewayError(422, 'NOTIFICATION_TARGET_UNAVAILABLE', '机器人已退群')
    monkeypatch.setattr(FeishuGroups, 'get', removed)
    assert gateway.client.put(url + '/default-recipient', json={'recipient_id': 'oc_daily'}).status_code == 422
    with pytest.raises(GatewayError):
        gateway.components.notifications._validate_target('owner', row['id'], 'oc_daily', 'feishu')
    assert gateway.store.get_account('owner', row['id'])['default_recipient_id'] == 'oc_daily'


def test_discovery_cannot_cache_after_credential_change(gateway, account, groups, monkeypatch):
    row = account('feishu')

    def changed(*args):
        with gateway.store._connect() as connection:
            connection.execute('UPDATE channel_accounts SET credential_revision=credential_revision+1 WHERE id=%s',
                               (row['id'],))
        return {'items': groups, 'next_cursor': ''}
    monkeypatch.setattr(FeishuGroups, 'list', changed)
    assert gateway.client.get(endpoint(row) + '/notification-groups').status_code == 409
    assert gateway.store.notification_targets('owner', row['id'])['items'] == []


@pytest.mark.parametrize('provider', ['wechat', 'wecom', 'feishu'])
def test_default_reuses_existing_targets_without_rewriting_them(gateway, account, incoming, provider):
    row = account(provider)
    incoming(row, recipient='one@im.wechat', context='synthetic-context')
    incoming(row, recipient='two', context='synthetic-context')
    before = gateway.store.notification_targets('owner', row['id'])
    for recipient in ['one@im.wechat', 'two', '']:
        response = gateway.client.put(endpoint(row) + '/default-recipient', json={'recipient_id': recipient})
        assert response.status_code == 200
    assert gateway.store.notification_targets('owner', row['id']) == before


def test_sdk_failure_is_redacted():
    def failed(request):
        raise RuntimeError('synthetic-secret-not-for-client')
    with pytest.raises(GatewayError) as error:
        FeishuGroups._data(failed, None)
    assert 'synthetic-secret' not in str(error.value)


def test_upgrade_preserves_existing_recipient_and_does_not_invent_default(gateway, account, incoming):
    row = account('feishu')
    incoming(row, recipient='oc_existing')
    with gateway.store._connect() as connection:
        connection.execute('ALTER TABLE channel_accounts DROP COLUMN default_recipient_id')
        connection.execute('ALTER TABLE channel_notification_targets DROP COLUMN label')
        connection.execute('ALTER TABLE channel_notification_targets DROP COLUMN kind')
    gateway.store.initialize()
    gateway.store.initialize()
    assert gateway.store.get_account('owner', row['id'])['default_recipient_id'] == ''
    assert gateway.store.notification_targets('owner', row['id'])['items'] == [
        {'recipient_id': 'oc_existing', 'label': 'oc_existing', 'kind': 'conversation', 'available': True}]


@pytest.mark.parametrize('payload', [{'recipient_id': '群名'}, {'recipient_id': 'oc_x', 'owner': 'other'},
                                     {'recipient_id': 1}, {'recipient_id': 'a' * 257}])
def test_default_rejects_invalid_input(gateway, account, payload):
    assert gateway.client.put(endpoint(account('feishu')) + '/default-recipient', json=payload).status_code == 422


def test_membership_uses_only_the_existing_read_group_list_permission():
    from types import SimpleNamespace as N
    calls = []

    def list_groups(request):
        calls.append(request)
        return N(success=lambda: True, data=N(items=[N(chat_id='oc_group', name='日报群')], has_more=False))

    service = object.__new__(FeishuGroups)
    # No members/get capability: a bot may list its groups without either API.
    service._client = N(im=N(v1=N(chat=N(list=list_groups))))
    assert service.get('oc_group')['label'] == '日报群'
    assert len(calls) == 1
    with pytest.raises(GatewayError) as error:
        service.get('oc_removed')
    assert error.value.code == 'NOTIFICATION_TARGET_UNAVAILABLE'


@pytest.mark.parametrize('repeat_cursor', [False, True])
def test_membership_follows_pages_and_fails_closed_on_cursor_loop(repeat_cursor):
    from types import SimpleNamespace as N
    calls = []

    def list_groups(request):
        calls.append(request)
        first = len(calls) == 1 or repeat_cursor
        return N(success=lambda: True, data=N(
            items=[] if first else [N(chat_id='oc_later', name='后页群')],
            has_more=first, page_token='next' if first else ''))

    service = object.__new__(FeishuGroups)
    service._client = N(im=N(v1=N(chat=N(list=list_groups))))
    if repeat_cursor:
        with pytest.raises(GatewayError) as error:
            service.get('oc_later')
        assert error.value.code == 'FEISHU_GROUPS_UNAVAILABLE'
    else:
        assert service.get('oc_later')['label'] == '后页群'
    assert len(calls) == 2
