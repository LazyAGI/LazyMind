"""Provider account regressions that do not require a real external SDK connection."""
import hashlib
import threading
import types
from unittest.mock import patch

import httpx
import pytest

from channel_gateway.common.errors import GatewayError
from channel_gateway.feishu.accounts import FeishuAccountService
from channel_gateway.feishu.domain import FeishuAppCredentials
from channel_gateway.wechat.client import WeChatClient
from channel_gateway.wechat.domain import WeChatConfig, WeChatRejectedError
from channel_gateway.wechat.runtime import WeChatRuntime, _AccountWorker
from channel_gateway.wechat.service import WeChatConnectionService, _wechat_account_label


class AccountStore:
    def __init__(self, account):
        self.account = account
        self.calls = []

    def get_account(self, owner, account_id):
        return self.account if owner == 'owner' and account_id == self.account['id'] else None

    def resume_account(self, owner, account_id, credential_revision, provider):
        self.calls.append(('resume', owner, account_id, credential_revision, provider))
        return {**self.account, 'status': 'connected', 'credential_revision': credential_revision}


class CredentialCipher:
    def decrypt(self, owner, value):
        if isinstance(value, dict):
            return value
        return {
            'authorized_user_id': 'stable-user',
            'token': 'token',
            'account_id': 'bot',
            'base_url': 'https://ilinkai.weixin.qq.com',
        }


class AcceptedClient:
    def notify_start(self, **kwargs):
        return None


class RejectedClient:
    def notify_start(self, **kwargs):
        raise WeChatRejectedError()

    def get_updates(self, **kwargs):
        raise WeChatRejectedError()


class StopAfterWait:
    def __init__(self):
        self.stopped = False

    def is_set(self):
        return self.stopped

    def wait(self, timeout):
        self.stopped = True


class Lease:
    fence = 7

    def keepalive(self):
        pass

    def close(self):
        pass


class RuntimeStore:
    def __init__(self):
        self.disconnected = []
        self.statuses = []

    def get_checkpoint(self, account_id):
        return {}

    def acquire_runtime_lease(self, account_id):
        return Lease()

    def set_runtime_status(self, account_id, status, error=None, runtime_fence=None):
        self.statuses.append((account_id, status, error))

    def disconnect_account(self, owner, account_id, **kwargs):
        self.disconnected.append((owner, account_id, kwargs))
        return True


class FeishuStore:
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
            'credential_revision': 1, 'identity_metadata': '{}', 'updated_at': None,
        }
        return self.account

    def get_account_internal(self, account_id):
        return self.account if self.account and self.account['id'] == account_id else None

    def update_account_identity(self, account_id, metadata, credential_revision):
        self.account = {**self.account, 'identity_metadata': '{}'}
        return self.account


class FeishuCipher:
    def __init__(self):
        self.payload = None

    def encrypt(self, owner, value):
        self.payload = value
        return 'encrypted'

    def decrypt(self, owner, value):
        return self.payload

    def needs_migration(self, value):
        return False


def disconnected_wechat_service(client):
    store = AccountStore({
        'id': 'wechat-1', 'provider': 'wechat', 'status': 'disconnected',
        'label': '微信', 'credentials_ciphertext': 'encrypted',
        'credential_revision': 3, 'updated_at': None,
    })
    service = object.__new__(WeChatConnectionService)
    service._store = store
    service._cipher = CredentialCipher()
    service._wechat = client
    service._on_account_connected = None
    return service, store


def test_wechat_resume_uses_retained_credentials_without_scanning():
    service, store = disconnected_wechat_service(AcceptedClient())

    resumed = service.resume_account('owner', 'wechat-1')

    assert resumed['status'] == 'connected'
    assert store.calls == [('resume', 'owner', 'wechat-1', 3, 'wechat')]


def test_wechat_resume_requires_scanning_when_retained_token_is_rejected():
    service, store = disconnected_wechat_service(RejectedClient())

    with pytest.raises(GatewayError, match='微信会话已失效') as error:
        service.resume_account('owner', 'wechat-1')

    assert error.value.code == 'WECHAT_REAUTHORIZATION_REQUIRED'
    assert store.calls == []


def test_wechat_runtime_disconnects_account_when_provider_rejects_token():
    store = RuntimeStore()
    runtime = object.__new__(WeChatRuntime)
    runtime._store = store
    runtime._client = RejectedClient()
    runtime._shutdown = StopAfterWait()
    runtime._config = WeChatConfig(
        'https://ilinkai.weixin.qq.com', 480, 40, 3, 1800, '/tmp', 1024,
    )

    runtime._poll(
        {'id': 'wechat-1', 'owner_user_id': 'owner', 'credential_revision': 3},
        {'base_url': 'https://ilinkai.weixin.qq.com', 'token': 'expired'},
        StopAfterWait(),
        Lease(),
    )

    assert store.disconnected == [
        ('owner', 'wechat-1', {'retain_credentials': True, 'expected_revision': 3, 'runtime_fence': 7}),
    ]


def test_wechat_get_updates_maps_provider_rejection_to_expired_credentials():
    response = httpx.Response(200, json={'ret': -1, 'errcode': 40001, 'errmsg': 'invalid token'})

    with patch('channel_gateway.wechat.client.httpx.post', return_value=response):
        with pytest.raises(WeChatRejectedError) as error:
            WeChatClient('https://ilinkai.weixin.qq.com', 40).get_updates(
                base_url='https://ilinkai.weixin.qq.com', token='expired', cursor='', timeout_ms=5000,
            )

    assert error.value.retryable is False


def test_wechat_runtime_disconnects_when_start_probe_rejects_token():
    store = RuntimeStore()
    runtime = object.__new__(WeChatRuntime)
    runtime._store = store
    runtime._credentials = types.SimpleNamespace(load_runtime_account=lambda account_id: {
        'id': account_id, 'owner_user_id': 'owner', 'status': 'connected', 'credential_revision': 3,
        'credentials': {'base_url': 'https://ilinkai.weixin.qq.com', 'token': 'expired'},
    })
    runtime._client = RejectedClient()
    runtime._shutdown = StopAfterWait()
    runtime._lock = threading.Lock()
    runtime._workers = {}

    runtime._run_account(_AccountWorker('wechat-1', 1, StopAfterWait()))

    assert store.disconnected == [
        ('owner', 'wechat-1', {'retain_credentials': True, 'expected_revision': 3, 'runtime_fence': 7}),
    ]


def test_wechat_reconnect_identity_uses_existing_stable_user_identity():
    account = {
        'id': 'wechat-1', 'provider': 'wechat',
        'external_id_hash': hashlib.sha256(b'old-bot').hexdigest(),
        'owner_user_id': 'owner',
        'credentials_ciphertext': {'account_id': 'old-bot', 'authorized_user_id': 'stable-user'},
    }
    service = object.__new__(WeChatConnectionService)
    service._store = AccountStore(account)
    service._cipher = CredentialCipher()

    assert service._reconnect_identity_hash(
        {'requested_account_id': 'wechat-1', 'owner_user_id': 'owner'},
        'new-bot',
        'stable-user',
    ) == account['external_id_hash']


@pytest.mark.parametrize('bot_name, expected', [('LazyMind 助手', 'LazyMind 助手'), ('', '飞书账号')])
def test_feishu_default_label_uses_bot_name_or_generic_fallback(bot_name, expected):
    store = FeishuStore()
    service = FeishuAccountService(store=store, cipher=FeishuCipher())

    service.connect_registered_account(
        owner_user_id='owner',
        credentials=FeishuAppCredentials(
            app_id='cli_internal', app_secret='secret',
            provider_account_id='ou_internal', provider_tenant_key='tenant',
            display_name='Alice', bot_name=bot_name,
        ),
        runtime_fence=None,
        notify_runtime=False,
    )

    assert store.label == expected


@pytest.mark.parametrize('profile, expected', [
    ({'nickname': 'LazyMind 助手'}, 'LazyMind 助手'),
    ({}, '微信机器人'),
])
def test_wechat_label_uses_platform_display_name_or_generic_fallback(profile, expected):
    assert _wechat_account_label(profile) == expected
