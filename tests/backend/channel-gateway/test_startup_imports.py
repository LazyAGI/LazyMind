"""Gateway readiness must not depend on loading the optional channel SDKs."""
import builtins
import os
from pathlib import Path
import subprocess
import sys
import textwrap

import pytest

from channel_gateway.common.errors import GatewayError
from channel_gateway.feishu import registration
from channel_gateway.feishu.groups import FeishuGroups


@pytest.fixture
def unavailable_feishu_sdk(monkeypatch):
    original_import = builtins.__import__

    def import_without_sdk(name, *args, **kwargs):
        if name.split('.')[0] == 'lark_oapi':
            raise ImportError('Feishu SDK is unavailable')
        return original_import(name, *args, **kwargs)

    monkeypatch.setattr(builtins, '__import__', import_without_sdk)


def test_optional_feishu_name_lookups_ignore_sdk_import_failure(unavailable_feishu_sdk):
    assert registration.get_bot_name('cli_test', 'secret') == ''
    assert registration._owner_name('cli_test', 'secret', 'ou_test') == ''


@pytest.mark.parametrize('operation', ['construct', 'list'])
def test_feishu_group_import_failure_uses_stable_error(unavailable_feishu_sdk, operation):
    with pytest.raises(GatewayError) as error:
        if operation == 'construct':
            FeishuGroups(None)
        else:
            object.__new__(FeishuGroups).list()
    assert error.value.http_status == 503
    assert error.value.code == 'FEISHU_GROUPS_UNAVAILABLE'


def test_gateway_starts_and_lists_accounts_without_importing_feishu_sdks(tmp_path):
    # Run in a fresh interpreter: the rest of the suite intentionally imports
    # the real SDK for provider contract tests and would hide eager imports.
    source = Path(__file__).resolve().parents[3] / 'backend/channel-gateway'
    script = textwrap.dedent('''\
        import importlib.abc
        import sys

        attempted = []

        class BlockFeishuSDKs(importlib.abc.MetaPathFinder):
            def find_spec(self, fullname, path=None, target=None):
                if fullname.split('.')[0] in {'lark_oapi', 'lark_channel'}:
                    attempted.append(fullname)
                    raise AssertionError(f'SDK imported during gateway startup: {fullname}')
                return None

        sys.meta_path.insert(0, BlockFeishuSDKs())

        from fastapi.testclient import TestClient
        from channel_gateway.app import app
        from channel_gateway.feishu.domain import FeishuAppCredentials
        from channel_gateway.feishu.receiver import LarkChannelFactory

        with TestClient(app, headers={'X-User-Id': 'owner'}) as client:
            assert client.get('/healthz').status_code == 200
            assert client.get('/readyz').json() == {'status': 'ready'}
            for provider in ('wechat', 'feishu', 'wecom'):
                response = client.get(
                    '/api/channel-gateway/v1/channel-accounts',
                    params={'provider': provider},
                )
                assert response.status_code == 200, response.text
                assert response.json() == {'items': []}

            # Receiver construction belongs to the gateway process; the SDK
            # must be imported only after the receiver child process starts.
            receiver = LarkChannelFactory().create_receiver(
                FeishuAppCredentials(
                    app_id='cli_test', app_secret='secret',
                    provider_account_id='ou_test',
                    provider_tenant_key='', display_name='Test owner',
                ),
                lambda message: None,
                lambda action: None,
                lambda menu: None,
            )
            receiver.stop()

        assert attempted == [], attempted
        assert not any(name.split('.')[0] in {'lark_oapi', 'lark_channel'}
                       for name in sys.modules)
    ''')
    result = subprocess.run(
        [sys.executable, '-c', script],
        env={
            **os.environ,
            'PYTHONPATH': str(source),
            'LAZYMIND_CHANNEL_GATEWAY_DATABASE_DSN': f'sqlite:///{tmp_path / "gateway.db"}',
            'LAZYMIND_CHANNEL_GATEWAY_CREDENTIAL_KEY_PATH': str(tmp_path / 'master.key'),
        },
        capture_output=True,
        text=True,
        timeout=20,
    )
    assert result.returncode == 0, result.stdout + result.stderr
