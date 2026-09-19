import hashlib
import sys
import types

sys.modules.setdefault(
    'aibot',
    types.SimpleNamespace(
        WSClient=object,
        WSClientOptions=object,
    ),
)

from channel_gateway.wechat.service import WeChatConnectionService
from channel_gateway.wechat.domain import WeChatConfig
from channel_gateway.wecom.service import WeComService


class _Store:
    def __init__(self, account):
        self.account = account
        self.calls = []

    def get_account(self, owner, account_id):
        return self.account if owner == 'owner' and account_id == self.account['id'] else None

    def disconnect_account(self, owner, account_id, **kwargs):
        self.calls.append((owner, account_id, kwargs))
        return True

    def resume_account(self, owner, account_id, credential_revision, provider):
        self.calls.append(('resume', owner, account_id, credential_revision, provider))
        return {**self.account, 'status': 'connected', 'credential_revision': credential_revision}


class _Cipher:
    def decrypt(self, owner, value):
        if isinstance(value, dict):
            return value
        return {'authorized_user_id': 'stable-user', 'token': 'token', 'account_id': 'bot'}


class _Runtime:
    def stop_account(self, account_id):
        pass

    def restart_account(self, account_id):
        pass


class _Client:
    pass


def test_wechat_disconnect_retains_credentials_for_reconnect():
    store = _Store({'id': 'wechat-1', 'provider': 'wechat'})
    service = WeChatConnectionService(
        config=WeChatConfig('https://ilinkai.weixin.qq.com', 480, 40, 3, 1800, '/tmp', 1024),
        store=store, cipher=_Cipher(), client=_Client()
    )

    service.disconnect_account('owner', 'wechat-1')

    assert store.calls == [('owner', 'wechat-1', {'retain_credentials': True})]


def test_wecom_disconnect_retains_credentials_for_reconnect():
    store = _Store({'id': 'wecom-1', 'provider': 'wecom'})
    service = WeComService(store, _Cipher(), _Runtime())

    service.disconnect_account('owner', 'wecom-1')

    assert store.calls == [('owner', 'wecom-1', {'retain_credentials': True})]


def test_wechat_resume_uses_retained_credentials_without_scanning():
    store = _Store({'id': 'wechat-1', 'provider': 'wechat', 'status': 'disconnected',
                    'label': '微信', 'credentials_ciphertext': 'encrypted',
                    'credential_revision': 3, 'updated_at': None})
    service = object.__new__(WeChatConnectionService)
    service._store = store
    service._cipher = _Cipher()
    service._on_account_connected = None

    resumed = service.resume_account('owner', 'wechat-1')

    assert resumed['status'] == 'connected'
    assert store.calls[-1] == ('resume', 'owner', 'wechat-1', 3, 'wechat')


def test_wechat_reconnect_identity_uses_existing_stable_user_identity():
    account = {
        'id': 'wechat-1',
        'provider': 'wechat',
        'external_id_hash': hashlib.sha256(b'old-bot').hexdigest(),
        'owner_user_id': 'owner',
        'credentials_ciphertext': {
            'account_id': 'old-bot',
            'authorized_user_id': 'stable-user',
        },
    }
    service = object.__new__(WeChatConnectionService)
    service._store = _Store(account)
    service._cipher = _Cipher()

    assert service._reconnect_identity_hash(
        {'requested_account_id': 'wechat-1', 'owner_user_id': 'owner'},
        'new-bot', 'stable-user'
    ) == account['external_id_hash']
