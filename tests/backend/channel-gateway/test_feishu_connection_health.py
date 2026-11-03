"""A failed optional bot-menu setup must not hide a healthy message socket."""
import pytest

from channel_gateway.common.domain.channel import FEISHU_BOT_MENU_UNAVAILABLE


PREFIX = '/api/channel-gateway/v1/channel-accounts'
MENU_PERMISSION_ERROR = ('Feishu bot menu configuration failed code=99991672 '
                         'msg=Access denied: application:application:patch')


@pytest.mark.parametrize('error', [
    MENU_PERMISSION_ERROR,
    f'{FEISHU_BOT_MENU_UNAVAILABLE}: {MENU_PERMISSION_ERROR}',
    'Feishu bot menu configuration request failed',
    'Feishu bot menu publish request failed',
    'Feishu bot menu publish failed code=999 msg=unavailable',
])
def test_menu_setup_failure_keeps_the_message_connection_available(gateway, account, error):
    row = account('feishu')
    gateway.store.set_runtime_status(row['id'], 'degraded', error)
    response = gateway.client.get(PREFIX, params={'provider': 'feishu'})
    assert response.status_code == 200
    item = response.json()['items'][0]
    assert item['status'] == 'connected'
    assert item['runtime_status'] == 'degraded'
    assert item['capabilities']['connection_ready'] is True
    assert item['capabilities']['bot_menu_ready'] is False
    assert item['capabilities']['bot_menu_error'] == (
        'permission_required' if 'code=99991672' in error else 'unavailable')
    detail = gateway.client.get(f'{PREFIX}/{row["id"]}', params={'include_references': 'false'})
    assert detail.status_code == 200
    assert detail.json()['capabilities'] == item['capabilities']


@pytest.mark.parametrize('runtime_status,error', [
    ('starting', ''),
    ('degraded', '飞书长连接正在重连'),
    ('failed', 'Feishu channel stopped unexpectedly'),
    ('stopped', MENU_PERMISSION_ERROR),
])
def test_unavailable_message_socket_is_not_treated_as_an_optional_menu_failure(
        gateway, account, runtime_status, error):
    row = account('feishu')
    gateway.store.set_runtime_status(row['id'], runtime_status, error)
    item = gateway.client.get(PREFIX, params={'provider': 'feishu'}).json()['items'][0]
    assert item['capabilities']['connection_ready'] is False
    assert item['capabilities']['bot_menu_ready'] is False


def test_paused_account_does_not_become_available_with_an_old_menu_warning(gateway, account):
    row = account('feishu')
    gateway.store.disconnect_account('owner', row['id'], retain_credentials=True)
    gateway.store.set_runtime_status(row['id'], 'degraded', MENU_PERMISSION_ERROR)
    item = gateway.client.get(PREFIX, params={'provider': 'feishu'}).json()['items'][0]
    assert item['status'] == 'disconnected'
    assert item['capabilities']['connection_ready'] is False


def test_successful_menu_retry_clears_the_warning(gateway, account):
    row = account('feishu')
    gateway.store.set_runtime_status(row['id'], 'degraded', MENU_PERMISSION_ERROR)
    gateway.store.set_runtime_status(row['id'], 'running')
    item = gateway.client.get(PREFIX, params={'provider': 'feishu'}).json()['items'][0]
    assert item['capabilities']['connection_ready'] is True
    assert item['capabilities']['bot_menu_ready'] is True
    assert item['capabilities']['bot_menu_error'] == ''
