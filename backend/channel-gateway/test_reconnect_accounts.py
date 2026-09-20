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
_lark = types.ModuleType('lark_oapi')
_lark_api = types.ModuleType('lark_oapi.api')
_lark_im = types.ModuleType('lark_oapi.api.im')
_lark_im_v1 = types.ModuleType('lark_oapi.api.im.v1')
_lark_im_v1.ListChatRequest = object
sys.modules.setdefault('lark_oapi', _lark)
sys.modules.setdefault('lark_oapi.api', _lark_api)
sys.modules.setdefault('lark_oapi.api.im', _lark_im)
sys.modules.setdefault('lark_oapi.api.im.v1', _lark_im_v1)

from channel_gateway.wechat.service import WeChatConnectionService, _wechat_account_label
from channel_gateway.wechat.domain import WeChatConfig, WeChatRejectedError
from channel_gateway.wechat.runtime import WeChatRuntime
from channel_gateway.wecom.service import WeComService
from channel_gateway.feishu.accounts import FeishuAccountService
from channel_gateway.feishu.domain import FeishuAppCredentials


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
        return {
            'authorized_user_id': 'stable-user',
            'token': 'token',
            'account_id': 'bot',
            'base_url': 'https://ilinkai.weixin.qq.com',
        }


class _Runtime:
    def stop_account(self, account_id):
        pass

    def restart_account(self, account_id):
        pass


class _Client:
    def notify_start(self, **kwargs):
        return None


class _RejectedClient:
    def notify_start(self, **kwargs):
        raise WeChatRejectedError()

    def get_updates(self, **kwargs):
        raise WeChatRejectedError()


class _StopAfterWait:
    def __init__(self):
        self.stopped = False

    def is_set(self):
        return self.stopped

    def wait(self, timeout):
        self.stopped = True


class _Lease:
    fence = None

    def keepalive(self):
        pass


class _RuntimeStore:
    def __init__(self):
        self.disconnected = []
        self.statuses = []

    def get_checkpoint(self, account_id):
        return {}

    def set_runtime_status(self, account_id, status, error=None, runtime_fence=None):
        self.statuses.append((account_id, status, error))

    def disconnect_account(self, owner, account_id, **kwargs):
        self.disconnected.append((owner, account_id, kwargs))
        return True


class _FeishuStore:
    def __init__(self):
        self.label = ''
        self.account = None

    def connect_referenced_account(self, **kwargs):
        self.label = kwargs['label']
        self.account = {
            'id': 'feishu-1', 'owner_user_id': kwargs['owner_user_id'],
            'provider': 'feishu', 'label': self.label,
            'status': 'provisioning', 'runtime_status': 'stopped',
            'credentials_ciphertext': kwargs['credentials_ciphertext'],
            'credential_revision': 1, 'identity_metadata': '{}',
            'updated_at': None,
        }
        return self.account

    def get_account_internal(self, account_id):
        return self.account if self.account and self.account['id'] == account_id else None

    def update_account_identity(self, account_id, metadata, credential_revision):
        self.account = {**self.account, 'identity_metadata': '{}'}
        return self.account


class _FeishuCipher:
    def __init__(self):
        self.payload = None

    def encrypt(self, owner, value):
        self.payload = value
        return 'encrypted'

    def decrypt(self, owner, value):
        return self.payload

    def needs_migration(self, value):
        return False


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
    service._wechat = _Client()
    service._on_account_connected = None

    resumed = service.resume_account('owner', 'wechat-1')

    assert resumed['status'] == 'connected'
    assert store.calls[-1] == ('resume', 'owner', 'wechat-1', 3, 'wechat')


def test_wechat_resume_requires_scanning_when_retained_token_is_rejected():
    store = _Store({'id': 'wechat-1', 'provider': 'wechat', 'status': 'disconnected',
                    'label': '微信', 'credentials_ciphertext': 'encrypted',
                    'credential_revision': 3, 'updated_at': None})
    service = object.__new__(WeChatConnectionService)
    service._store = store
    service._cipher = _Cipher()
    service._wechat = _RejectedClient()
    service._on_account_connected = None

    try:
        service.resume_account('owner', 'wechat-1')
    except Exception as exc:
        assert getattr(exc, 'code', '') == 'WECHAT_REAUTHORIZATION_REQUIRED'
    else:
        raise AssertionError('rejected retained token must require a new QR scan')

    assert not any(call[0] == 'resume' for call in store.calls)


def test_wechat_runtime_disconnects_account_when_provider_rejects_token():
    store = _RuntimeStore()
    runtime = object.__new__(WeChatRuntime)
    runtime._store = store
    runtime._client = _RejectedClient()
    runtime._shutdown = _StopAfterWait()
    runtime._config = WeChatConfig(
        'https://ilinkai.weixin.qq.com', 480, 40, 3, 1800, '/tmp', 1024,
    )

    runtime._poll(
        {'id': 'wechat-1', 'owner_user_id': 'owner'},
        {'base_url': 'https://ilinkai.weixin.qq.com', 'token': 'expired'},
        _StopAfterWait(),
        _Lease(),
    )

    assert store.disconnected == [
        ('owner', 'wechat-1', {'retain_credentials': True}),
    ]


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


def test_feishu_default_label_uses_platform_bot_name():
    store = _FeishuStore()
    service = FeishuAccountService(store=store, cipher=_FeishuCipher())
    service.connect_registered_account(
        owner_user_id='owner',
        credentials=FeishuAppCredentials(
            app_id='cli_internal', app_secret='secret',
            provider_account_id='ou_internal', provider_tenant_key='tenant',
            display_name='Alice', bot_name='LazyMind 助手',
        ),
        runtime_fence=None,
        notify_runtime=False,
    )
    assert store.label == 'LazyMind 助手'


def test_feishu_default_label_is_generic_when_authorized_name_is_missing():
    store = _FeishuStore()
    service = FeishuAccountService(store=store, cipher=_FeishuCipher())
    service.connect_registered_account(
        owner_user_id='owner',
        credentials=FeishuAppCredentials(
            app_id='cli_internal', app_secret='secret',
            provider_account_id='ou_internal', provider_tenant_key='tenant',
            display_name='Alice', bot_name='',
        ),
        runtime_fence=None,
        notify_runtime=False,
    )
    assert store.label == '飞书账号'


def test_wechat_label_uses_platform_display_name():
    assert _wechat_account_label({'nickname': 'LazyMind 助手'}) == 'LazyMind 助手'
    assert _wechat_account_label({}) == '微信机器人'
