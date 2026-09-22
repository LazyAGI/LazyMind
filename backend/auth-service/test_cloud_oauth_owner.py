import importlib
import json
import os
import tempfile
import unittest
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from unittest.mock import patch


os.environ.setdefault('LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'test-secret-key')

from core import database as database_module  # noqa: E402
from core.errors import AppException  # noqa: E402
from models import Base, CloudAuthConnection  # noqa: E402
from services.cloud_oauth_provider import CloudAccountProfile, CloudProviderError, CloudTokenPayload  # noqa: E402
from services.cloud_oauth_service import CloudOAuthService  # noqa: E402
from services.providers.wechat_provider import WeChatProvider  # noqa: E402


cloud_oauth_module = importlib.import_module('services.cloud_oauth_service')


class _Provider:
    def __init__(self) -> None:
        self.authorize_client_ids: list[str] = []
        self.provider_account_id = 'feishu-open-1'
        self.display_name = 'Feishu User 1'
        self.provider_tenant_key = 'tenant-key-1'
        self.refresh_error: Exception | None = None
        self.refresh_calls = 0
        self.exchange_refresh_token = 'refresh-token'

    def provider_name(self) -> str:
        return 'feishu'

    def build_authorize_url(self, *, client_id: str, redirect_uri: str, scope: str, state: str) -> str:
        self.authorize_client_ids.append(client_id)
        return f'https://example.test/oauth?state={state}'

    def exchange_code(self, *, client_id: str, client_secret: str, code: str, redirect_uri: str) -> CloudTokenPayload:
        return CloudTokenPayload(
            access_token='oauth-token',
            refresh_token=self.exchange_refresh_token,
            expires_at=datetime.now(timezone.utc) + timedelta(hours=1),
        )

    def refresh_access_token(self, *, client_id: str, client_secret: str, refresh_token: str) -> CloudTokenPayload:
        self.refresh_calls += 1
        if self.refresh_error is not None:
            raise self.refresh_error
        return CloudTokenPayload(access_token='refreshed-token', refresh_token=refresh_token)

    def acquire_tenant_access_token(self, *, client_id: str, client_secret: str) -> CloudTokenPayload:
        return CloudTokenPayload(
            access_token='tenant-token',
            expires_at=datetime.now(timezone.utc) + timedelta(hours=1),
        )

    def fetch_account_profile(self, *, access_token: str) -> CloudAccountProfile:
        return CloudAccountProfile(
            provider_account_id=self.provider_account_id,
            display_name=self.display_name,
            provider_tenant_key=self.provider_tenant_key,
            meta={'open_id': self.provider_account_id, 'name': self.display_name},
        )


class CloudOAuthOwnerTest(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.NamedTemporaryFile(suffix='.db', delete=False)
        self._tmp.close()
        engine = database_module.create_engine(
            f'sqlite:///{self._tmp.name}',
            connect_args={'check_same_thread': False},
        )
        self._old_session = cloud_oauth_module.SessionLocal
        self._old_encrypt = cloud_oauth_module.encrypt_json
        self._old_decrypt = cloud_oauth_module.decrypt_json
        self._old_engine = database_module.engine
        self._old_session_global = database_module.SessionLocal
        cloud_oauth_module.SessionLocal = database_module.sessionmaker(
            bind=engine,
            autoflush=False,
            autocommit=False,
        )
        database_module.engine = engine
        database_module.SessionLocal = cloud_oauth_module.SessionLocal
        cloud_oauth_module.encrypt_json = lambda payload: json.dumps(payload)
        cloud_oauth_module.decrypt_json = lambda payload: json.loads(payload)
        Base.metadata.create_all(engine)
        self.service = CloudOAuthService()
        self.provider = _Provider()
        self.wechat_provider = WeChatProvider()
        self.service._providers = {
            'feishu': self.provider,
            'wechat': self.wechat_provider,
        }

    def tearDown(self) -> None:
        cloud_oauth_module.SessionLocal = self._old_session
        cloud_oauth_module.encrypt_json = self._old_encrypt
        cloud_oauth_module.decrypt_json = self._old_decrypt
        database_module.engine = self._old_engine
        database_module.SessionLocal = self._old_session_global
        try:
            os.unlink(self._tmp.name)
        except OSError:
            pass

    def _authorize_oauth_connection(self) -> str:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )
        return callback['connection_id']

    def _expire_access_token(self, connection_id: str) -> None:
        self.service._cache_delete(connection_id)
        with cloud_oauth_module.SessionLocal() as db:
            row = db.query(CloudAuthConnection).filter_by(connection_id=connection_id).first()
            state = self.service._decrypt_payload(row.auth_state_ciphertext, field_name='auth_state')
            state.update({
                'access_token': '',
                'access_expires_at': '',
                'refresh_token': 'refresh-token-health',
            })
            row.auth_state_ciphertext = self.service._encrypt_payload(state, field_name='auth_state')
            db.commit()

    def test_token_and_verify_require_owner(self) -> None:
        created = self.service.create_connection(
            provider='feishu',
            tenant_id='tenant-ignored',
            owner_user_id='user-1',
            auth_mode='tenant',
            client_id='client',
            client_secret='secret',
        )

        verified = self.service.verify_connection(
            created['connection_id'],
            user_id='user-1',
            tenant_id='tenant-ignored',
        )
        self.assertEqual(verified['owner_user_id'], 'user-1')
        self.assertEqual(verified['tenant_id'], '')

        token = self.service.get_access_token(
            created['connection_id'],
            user_id='user-1',
            tenant_id='tenant-ignored',
        )
        self.assertEqual(token['access_token'], 'tenant-token')

        with self.assertRaisesRegex(Exception, 'Forbidden'):
            self.service.verify_connection(created['connection_id'], user_id='user-2', tenant_id='tenant-ignored')
        with self.assertRaisesRegex(Exception, 'Forbidden'):
            self.service.get_access_token(created['connection_id'], user_id='user-2', tenant_id='tenant-ignored')

    def test_create_connection_reuses_identity_and_updates_credentials(self) -> None:
        first = self.service.create_connection(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='tenant',
            client_id='client',
            client_secret='secret-1',
            provider_options={'chat_enabled': False},
        )
        second = self.service.create_connection(
            provider=' FEISHU ',
            tenant_id='ignored',
            owner_user_id=' user-1 ',
            auth_mode=' TENANT ',
            client_id=' client ',
            client_secret='secret-2',
            provider_options={'chat_enabled': True},
        )

        self.assertEqual(second['connection_id'], first['connection_id'])
        with cloud_oauth_module.SessionLocal() as db:
            rows = db.query(CloudAuthConnection).all()
            self.assertEqual(len(rows), 1)
            self.assertEqual(rows[0].client_id, 'client')
            credential = self.service._decrypt_payload(rows[0].credential_ciphertext, field_name='credential')
            self.assertEqual(credential['client_secret'], 'secret-2')
            self.assertEqual(credential['provider_options'], {'chat_enabled': True})

    def test_feishu_new_connections_default_to_chat_enabled(self) -> None:
        tenant = self.service.create_connection(
            provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='tenant',
            client_id='tenant-client', client_secret='fixture-secret',
        )
        oauth_id = self._authorize_oauth_connection()
        for connection_id in (tenant['connection_id'], oauth_id):
            detail = self.service.get_connection(connection_id, user_id='user-1')
            self.assertTrue(detail['provider_options']['chat_enabled'])
        enabled = self.service.list_chat_enabled_connections(provider='feishu', owner_user_id='user-1')
        self.assertEqual({item['connection_id'] for item in enabled['items']}, {tenant['connection_id'], oauth_id})

    def test_feishu_reconnect_preserves_explicit_chat_opt_out(self) -> None:
        for option in ('chat_enabled', 'chatEnabled'):
            with self.subTest(option=option):
                created = self.service.create_connection(
                    provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='tenant',
                    client_id=option, client_secret='fixture-secret', provider_options={option: False},
                )
                self.service.create_connection(
                    provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='tenant',
                    client_id=option, client_secret='new-fixture-secret',
                )
                detail = self.service.get_connection(created['connection_id'], user_id='user-1')
                self.assertFalse(detail['provider_options'][option])
        connection_id = self._authorize_oauth_connection()
        self.service.update_connection(connection_id, user_id='user-1', chat_enabled=False)
        reauth = self.service.create_authorize_url(
            provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='oauth_user',
            client_id='client', client_secret='new-fixture-secret',
            redirect_uri='https://example.test/callback', state='reauth', reauthorize_connection_id=connection_id,
        )
        self.service.oauth_callback(provider='feishu', tenant_id='', owner_user_id='user-1',
                                    connection_id=reauth['connection_id'], code='fixture-code', state='reauth')
        self.assertFalse(
            self.service.get_connection(connection_id, user_id='user-1')['provider_options']['chat_enabled'],
        )

    def test_feishu_pending_authorization_is_not_available_for_chat(self) -> None:
        kwargs = dict(provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='oauth_user',
                      client_id='fixture-pending', client_secret='fixture-secret',
                      redirect_uri='https://example.test/callback')
        created = self.service.create_authorize_url(**kwargs, state='first', provider_options={'chat_enabled': False})
        retried = self.service.create_authorize_url(**kwargs, state='second')
        self.assertEqual(created['connection_id'], retried['connection_id'])
        self.assertFalse(
            self.service.get_connection(retried['connection_id'], user_id='user-1')['provider_options']['chat_enabled'],
        )
        self.assertEqual(
            self.service.list_chat_enabled_connections(provider='feishu', owner_user_id='user-1')['items'], [],
        )

    def test_chat_availability_keeps_preference_separate_from_status(self) -> None:
        created = self.service.create_connection(
            provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='tenant',
            client_id='availability-fixture', client_secret='fixture-secret',
        )
        connection_id = created['connection_id']
        for status in ('ACTIVE', 'EXPIRED', 'ERROR', 'PENDING', 'REVOKED', 'UNKNOWN'):
            for preference in (True, False):
                with self.subTest(status=status, preference=preference):
                    with cloud_oauth_module.SessionLocal() as db:
                        row = db.query(CloudAuthConnection).filter_by(connection_id=connection_id).one()
                        credential = json.loads(row.credential_ciphertext)
                        credential['provider_options'] = {'chat_enabled': preference}
                        row.credential_ciphertext = json.dumps(credential)
                        row.status = status
                        db.commit()
                    detail = self.service.get_connection(connection_id, user_id='user-1')
                    expected = status == 'ACTIVE' and preference
                    self.assertEqual(detail['provider_options']['chat_enabled'], preference)
                    self.assertEqual(detail['can_use_chat'], expected)
                    enabled = self.service.list_chat_enabled_connections(
                        provider='feishu', owner_user_id='user-1',
                    )['items']
                    self.assertEqual([item['connection_id'] for item in enabled], [connection_id] if expected else [])
                    self.assertTrue(all(item['can_use_chat'] for item in enabled))
                    if status != 'REVOKED':
                        listed = self.service.list_connections(provider='feishu', owner_user_id='user-1')['items']
                        self.assertEqual(listed[0]['can_use_chat'], expected)

    def test_chat_availability_normalizes_legacy_flags_without_trusting_cached_metadata(self) -> None:
        created = self.service.create_connection(
            provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='tenant',
            client_id='legacy-flags-fixture', client_secret='fixture-secret',
        )
        for options, expected in (({}, False), ({'chatEnabled': True}, True),
                                  ({'chat_enabled': False, 'chatEnabled': True}, False),
                                  ({'chat_enabled': 'false'}, False)):
            with self.subTest(options=options):
                with cloud_oauth_module.SessionLocal() as db:
                    row = db.query(CloudAuthConnection).filter_by(connection_id=created['connection_id']).one()
                    credential = json.loads(row.credential_ciphertext)
                    credential['provider_options'] = options
                    row.credential_ciphertext = json.dumps(credential)
                    row.provider_account_meta = json.dumps({'chat_enabled': True})
                    db.commit()
                detail = self.service.get_connection(created['connection_id'], user_id='user-1')
                self.assertEqual(detail['provider_options']['chat_enabled'], expected)
                self.assertEqual(detail['provider_options']['chatEnabled'], expected)
                self.assertEqual(detail['can_use_chat'], expected)
                enabled = self.service.list_chat_enabled_connections(provider='feishu', owner_user_id='user-1')['items']
                self.assertEqual(len(enabled), int(expected))
        updated = self.service.update_connection(created['connection_id'], user_id='user-1', chat_enabled=True)
        self.assertTrue(updated['can_use_chat'])
        updated = self.service.update_connection(created['connection_id'], user_id='user-1', chat_enabled=False)
        self.assertFalse(updated['can_use_chat'])

    def test_reconnect_does_not_enable_a_legacy_connection_without_a_preference(self) -> None:
        kwargs = dict(provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='tenant',
                      client_id='historical-fixture', client_secret='fixture-secret')
        created = self.service.create_connection(**kwargs)
        with cloud_oauth_module.SessionLocal() as db:
            row = db.query(CloudAuthConnection).filter_by(connection_id=created['connection_id']).one()
            credential = json.loads(row.credential_ciphertext)
            credential.pop('provider_options', None)
            row.credential_ciphertext = json.dumps(credential)
            db.commit()
        self.service.create_connection(**kwargs)
        detail = self.service.get_connection(created['connection_id'], user_id='user-1')
        self.assertFalse(detail['provider_options']['chat_enabled'])
        self.assertFalse(detail['can_use_chat'])

    def test_chat_availability_is_a_read_only_response_field_and_requires_owner(self) -> None:
        from schemas.cloud_oauth import CloudConnectionResponse, CloudConnectionUpdateBody

        created = self.service.create_connection(
            provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='tenant',
            client_id='schema-fixture', client_secret='fixture-secret',
        )
        detail = self.service.get_connection(created['connection_id'], user_id='user-1')
        self.assertTrue(CloudConnectionResponse.model_validate(detail).can_use_chat)
        self.assertTrue(CloudConnectionResponse.model_json_schema()['properties']['can_use_chat']['readOnly'])
        body = CloudConnectionUpdateBody.model_validate({'chat_enabled': False, 'can_use_chat': True})
        self.assertNotIn('can_use_chat', body.model_dump())
        with self.assertRaises(AppException):
            self.service.list_chat_enabled_connections(provider='feishu', owner_user_id='')
        self.assertEqual(
            self.service.list_chat_enabled_connections(provider='feishu', owner_user_id='user-2')['items'], [],
        )

    def test_feishu_reference_connections_default_enabled_and_preserve_opt_out(self) -> None:
        for method in ('managed', 'cli'):
            with self.subTest(method=method):
                kwargs = dict(
                    auth_connection_id=f'fixture-{method}', owner_user_id='user-1', display_name='Fixture',
                    provider_tenant_key='fixture-tenant', provider_workspace_id='fixture-tenant',
                    provider_account_meta={'open_id': 'fixture-account'}, status='ACTIVE',
                    capability_contract_version='fixture/v1', capabilities=[],
                )
                if method == 'managed':
                    upsert = self.service.upsert_managed_connection
                    kwargs.update(provider='feishu', cloud_owner_user_id='fixture-cloud-owner')
                else:
                    upsert = self.service.upsert_feishu_cli_connection
                    kwargs.update(provider_account_id='fixture-account', profile_ref='user-1/fixture-cli',
                                  granted_scopes=['docx:document'], credential_location='local')
                connection = upsert(**kwargs)
                self.assertTrue(connection['provider_options']['chat_enabled'])
                self.assertTrue(connection['can_use_chat'])
                enabled = self.service.list_chat_enabled_connections(provider='feishu', owner_user_id='user-1')
                self.assertIn(connection['connection_id'], [item['connection_id'] for item in enabled['items']])
                self.assertEqual(
                    self.service.list_chat_enabled_connections(provider='feishu', owner_user_id='user-2')['items'], [],
                )
                self.assertTrue(upsert(**kwargs)['provider_options']['chat_enabled'])
                expired = upsert(**{**kwargs, 'status': 'EXPIRED'})
                self.assertTrue(expired['provider_options']['chat_enabled'])
                self.assertFalse(expired['can_use_chat'])
                self.assertEqual(
                    self.service.list_chat_enabled_connections(provider='feishu', owner_user_id='user-1')['items'], [],
                )
                upsert(**kwargs)
                self.service.update_connection(connection['connection_id'], user_id='user-1', chat_enabled=False)
                reauthorized = upsert(**kwargs)
                self.assertFalse(reauthorized['provider_options']['chat_enabled'])
                self.assertFalse(reauthorized['can_use_chat'])
                self.assertEqual(
                    self.service.list_chat_enabled_connections(provider='feishu', owner_user_id='user-1')['items'], [],
                )

    def test_wechat_connection_lifecycle(self) -> None:
        created = self.service.create_connection(
            provider='wechat',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='service_account',
            client_id='wx-app-id',
            client_secret='wx-app-secret',
            display_name='公众号测试账号',
            provider_options={'chat_enabled': True},
        )
        detail = self.service.get_connection(created['connection_id'], user_id='user-1')
        self.assertEqual((detail['status'], detail['display_name']), ('PENDING', '公众号测试账号'))
        self.assertFalse(detail['provider_options']['chat_enabled'])
        with self.assertRaises(AppException) as raised:
            self.service.update_connection(
                created['connection_id'], user_id='user-1', chat_enabled=True,
            )
        self.assertEqual(raised.exception.code, 1000832)

        with patch(
            'services.providers.wechat_provider._post_json',
            return_value={'access_token': 'wechat-stable-token', 'expires_in': 7200},
        ) as request_token:
            refreshed = self.service.refresh_connection_token(
                created['connection_id'], user_id='user-1',
            )
            token = self.service.get_access_token(created['connection_id'], user_id='user-1')
        self.assertEqual((refreshed['status'], token['access_token']), ('ACTIVE', 'wechat-stable-token'))
        self.assertEqual(request_token.call_count, 1)

        self.service.update_connection(
            created['connection_id'], user_id='user-1', chat_enabled=True,
        )
        updated = self.service.update_connection(
            created['connection_id'], user_id='user-1', client_secret='wx-new-secret',
        )
        self.assertEqual(updated['status'], 'PENDING')
        self.assertFalse(updated['provider_options']['chat_enabled'])

        with patch(
            'services.providers.wechat_provider._post_json',
            return_value={
                'errcode': 40164,
                'errmsg': 'invalid ip 203.0.113.8 ipv6 ::ffff:203.0.113.8',
            },
        ), self.assertRaises(AppException):
            self.service.refresh_connection_token(created['connection_id'], user_id='user-1')
        failed = self.service.get_connection(created['connection_id'], user_id='user-1')
        self.assertEqual(failed['status'], 'ERROR')
        self.assertIn('40164', failed['last_error'])
        self.assertIn('203.0.113.8', failed['last_error'])

    def test_list_feishu_cli_connection_does_not_decrypt_reference_marker(self) -> None:
        cli = self.service.upsert_feishu_cli_connection(
            auth_connection_id='conn_cli_fixture',
            owner_user_id='user-1',
            display_name='CLI User',
            provider_account_id='ou_cli_fixture',
            provider_tenant_key='tenant-cli',
            provider_workspace_id='tenant-cli',
            provider_account_meta={'open_id': 'ou_cli_fixture'},
            profile_ref='user-1/conn_cli_fixture',
            granted_scopes=['drive:drive:readonly'],
            credential_location='local',
            status='ACTIVE',
            capability_contract_version='feishu-cli/v1',
            capabilities=[],
        )

        listed = self.service.list_connections(owner_user_id='user-1', provider='feishu')

        self.assertEqual(len(listed['items']), 1)
        self.assertEqual(listed['items'][0]['connection_id'], cli['connection_id'])
        self.assertEqual(listed['items'][0]['profile_ref'], 'user-1/conn_cli_fixture')

    def test_delete_feishu_cli_connection_revokes_without_deleting_profile_reference(self) -> None:
        cli = self.service.upsert_feishu_cli_connection(
            auth_connection_id='conn_cli_delete_fixture',
            owner_user_id='user-1',
            display_name='CLI User',
            provider_account_id='ou_cli_delete_fixture',
            provider_tenant_key='tenant-cli',
            provider_workspace_id='tenant-cli',
            provider_account_meta={'open_id': 'ou_cli_delete_fixture'},
            profile_ref='user-1/conn_cli_delete_fixture',
            granted_scopes=['drive:drive:readonly'],
            credential_location='local',
            status='ACTIVE',
            capability_contract_version='feishu-cli/v1',
            capabilities=[],
        )

        with self.assertRaisesRegex(Exception, 'Forbidden'):
            self.service.delete_connection(cli['connection_id'], user_id='user-2')

        deleted = self.service.delete_connection(cli['connection_id'], user_id='user-1')

        self.assertEqual(deleted['status'], 'REVOKED')
        with cloud_oauth_module.SessionLocal() as db:
            row = db.query(CloudAuthConnection).filter_by(connection_id=cli['connection_id']).one()
            self.assertEqual(row.status, 'REVOKED')
            self.assertEqual(row.profile_ref, 'user-1/conn_cli_delete_fixture')
            self.assertEqual(row.credential_ciphertext, 'cli-profile-reference-v1')
            self.assertEqual(row.auth_state_ciphertext, 'cli-profile-reference-v1')

    def test_create_connection_identity_is_scoped_by_owner_and_auth_mode(self) -> None:
        first = self.service.create_connection(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='tenant',
            client_id='client',
            client_secret='secret',
        )
        other_owner = self.service.create_connection(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-2',
            auth_mode='tenant',
            client_id='client',
            client_secret='secret',
        )
        other_mode = self.service.create_connection(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='service_account',
            client_id='client',
            client_secret='secret',
        )

        self.assertEqual(len({first['connection_id'], other_owner['connection_id'], other_mode['connection_id']}), 3)

    def test_create_connection_reuses_legacy_row_without_client_identity(self) -> None:
        first = self.service.create_connection(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='tenant',
            client_id='client',
            client_secret='secret-1',
        )
        with cloud_oauth_module.SessionLocal() as db:
            row = db.query(CloudAuthConnection).filter_by(connection_id=first['connection_id']).one()
            row.client_id = None
            db.commit()

        second = self.service.create_connection(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='tenant',
            client_id='client',
            client_secret='secret-2',
        )

        self.assertEqual(second['connection_id'], first['connection_id'])
        with cloud_oauth_module.SessionLocal() as db:
            rows = db.query(CloudAuthConnection).all()
            self.assertEqual(len(rows), 1)
            self.assertEqual(rows[0].client_id, 'client')

    def test_create_connection_reactivates_same_revoked_identity(self) -> None:
        first = self.service.create_connection(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='tenant',
            client_id='client',
            client_secret='secret-1',
        )
        self.service.delete_connection(first['connection_id'], user_id='user-1')

        second = self.service.create_connection(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='tenant',
            client_id='client',
            client_secret='secret-2',
        )

        self.assertEqual(second['connection_id'], first['connection_id'])
        with cloud_oauth_module.SessionLocal() as db:
            row = db.query(CloudAuthConnection).one()
            self.assertEqual(row.status, 'ACTIVE')
            credential = self.service._decrypt_payload(row.credential_ciphertext, field_name='credential')
            self.assertEqual(credential['client_secret'], 'secret-2')

    def test_concurrent_create_connection_keeps_single_identity(self) -> None:
        def create(index: int) -> str:
            result = self.service.create_connection(
                provider='feishu',
                tenant_id='',
                owner_user_id='user-1',
                auth_mode='tenant',
                client_id='client',
                client_secret=f'secret-{index}',
            )
            return result['connection_id']

        with ThreadPoolExecutor(max_workers=6) as executor:
            connection_ids = list(executor.map(create, range(6)))

        self.assertEqual(len(set(connection_ids)), 1)
        with cloud_oauth_module.SessionLocal() as db:
            self.assertEqual(db.query(CloudAuthConnection).count(), 1)

    def test_oauth_callback_records_profile_and_lists_owner_connections(self) -> None:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='tenant-ignored',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            scope='drive:read',
            state='state-1',
        )
        self.assertEqual(created['tenant_id'], '')
        self.assertEqual(created['owner_user_id'], 'user-1')

        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='tenant-ignored',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )

        self.assertEqual(callback['connection_id'], created['connection_id'])
        self.assertEqual(callback['tenant_id'], '')
        self.assertEqual(callback['provider_account_id'], 'feishu-open-1')
        self.assertEqual(callback['display_name'], 'Feishu User 1')
        self.assertEqual(callback['scope'], 'drive:read')

        listed = self.service.list_connections(
            owner_user_id='user-1',
            provider='feishu',
            auth_mode='oauth_user',
            status='ACTIVE',
        )
        self.assertEqual(len(listed['items']), 1)
        item = listed['items'][0]
        self.assertEqual(item['connection_id'], created['connection_id'])
        self.assertEqual(item['app_id'], 'client')
        self.assertEqual(item['provider_account_id'], 'feishu-open-1')

        detail = self.service.get_connection(created['connection_id'], user_id='user-1')
        self.assertEqual(detail['app_id'], 'client')

        other = self.service.list_connections(owner_user_id='user-2', provider='feishu')
        self.assertEqual(other['items'], [])

    def test_authorize_url_rejects_duplicate_active_app(self) -> None:
        first = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        first_callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=first['connection_id'],
            code='code-1',
            state='state-1',
        )

        with self.assertRaises(AppException) as raised:
            self.service.create_authorize_url(
                provider='feishu',
                tenant_id='',
                owner_user_id='user-1',
                auth_mode='oauth_user',
                client_id='client',
                client_secret='secret',
                redirect_uri='https://example.test/callback',
                state='state-2',
            )

        self.assertEqual(raised.exception.code, 1000814)
        detail = self.service.get_connection(first_callback['connection_id'], user_id='user-1')
        self.assertEqual(detail['status'], 'ACTIVE')

    def test_get_access_token_recovers_when_concurrent_refresh_already_succeeded(self) -> None:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )
        connection_id = callback['connection_id']
        self.service._cache_delete(connection_id)

        with cloud_oauth_module.SessionLocal() as db:
            row = db.query(CloudAuthConnection).filter_by(connection_id=connection_id).first()
            state = self.service._decrypt_payload(row.auth_state_ciphertext, field_name='auth_state')
            state.update({
                'access_token': 'token-from-other-refresh',
                'access_expires_at': (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat(),
                'refresh_token': 'refresh-token-2',
            })
            row.auth_state_ciphertext = self.service._encrypt_payload(state, field_name='auth_state')
            row.status = 'ERROR'
            row.last_error = 'provider http error 400: invalid_grant'
            db.commit()

        self.provider.refresh_error = RuntimeError('provider http error 400: invalid_grant')
        token = self.service.get_access_token(connection_id, user_id='user-1')

        self.assertEqual(token['access_token'], 'token-from-other-refresh')
        detail = self.service.get_connection(connection_id, user_id='user-1')
        self.assertEqual(detail['status'], 'ACTIVE')
        self.assertEqual(detail['last_error'], '')

    def test_batch_connection_status_reads_without_refreshing_token(self) -> None:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )
        connection_id = callback['connection_id']
        self.provider.refresh_error = RuntimeError('refresh should not run')

        status = self.service.batch_connection_status([connection_id, connection_id], user_id='user-1')

        self.assertEqual(len(status['items']), 1)
        self.assertEqual(status['items'][0]['connection_id'], connection_id)
        self.assertEqual(status['items'][0]['status'], 'ACTIVE')

    def test_health_check_refreshes_error_connection_to_active(self) -> None:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )
        connection_id = callback['connection_id']
        self.service._cache_delete(connection_id)
        with cloud_oauth_module.SessionLocal() as db:
            row = db.query(CloudAuthConnection).filter_by(connection_id=connection_id).first()
            state = self.service._decrypt_payload(row.auth_state_ciphertext, field_name='auth_state')
            state.update({
                'access_token': '',
                'access_expires_at': '',
                'refresh_token': 'refresh-token-health',
            })
            row.auth_state_ciphertext = self.service._encrypt_payload(state, field_name='auth_state')
            row.status = 'ERROR'
            row.last_error = 'previous failure'
            db.commit()

        result = self.service.run_health_check_once(provider='feishu', batch_size=10)

        self.assertEqual(result['checked'], 1)
        self.assertEqual(result['active'], 1)
        detail = self.service.get_connection(connection_id, user_id='user-1')
        self.assertEqual(detail['status'], 'ACTIVE')
        self.assertEqual(detail['last_error'], '')

    def test_health_check_keeps_active_connection_on_retryable_network_error(self) -> None:
        connection_id = self._authorize_oauth_connection()
        self._expire_access_token(connection_id)
        self.provider.refresh_error = CloudProviderError('provider network error', retryable=True)

        result = self.service.run_health_check_once(provider='feishu', batch_size=10)

        self.assertEqual(result['active'], 1)
        self.assertEqual(result['retryable_errors'], 1)
        detail = self.service.get_connection(connection_id, user_id='user-1')
        self.assertEqual(detail['status'], 'ACTIVE')
        self.assertEqual(detail['last_error'], 'provider network error')

        self.provider.refresh_error = None
        recovered = self.service.run_health_check_once(provider='feishu', batch_size=10)

        self.assertEqual(recovered['active'], 1)
        detail = self.service.get_connection(connection_id, user_id='user-1')
        self.assertEqual(detail['status'], 'ACTIVE')
        self.assertEqual(detail['last_error'], '')

    def test_health_check_marks_expired_refresh_token_and_lists_connection(self) -> None:
        connection_id = self._authorize_oauth_connection()
        self._expire_access_token(connection_id)
        self.provider.refresh_error = CloudProviderError(
            'feishu token refresh failed [20037]: refresh token has expired',
            provider_code='20037',
            requires_reauth=True,
        )

        result = self.service.run_health_check_once(provider='feishu', batch_size=10)

        self.assertEqual(result['expired'], 1)
        detail = self.service.get_connection(connection_id, user_id='user-1')
        self.assertEqual(detail['status'], 'EXPIRED')
        listed = self.service.list_connections(
            owner_user_id='user-1',
            provider='feishu',
            status='ACTIVE,ERROR,EXPIRED',
        )
        self.assertEqual([item['connection_id'] for item in listed['items']], [connection_id])

    def test_oauth_callback_rejects_missing_refresh_token(self) -> None:
        self.provider.exchange_refresh_token = ''
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )

        with self.assertRaisesRegex(Exception, 'cloud access token is unavailable'):
            self.service.oauth_callback(
                provider='feishu',
                tenant_id='',
                owner_user_id='user-1',
                connection_id=created['connection_id'],
                code='code-1',
                state='state-1',
            )

        detail = self.service.get_connection(created['connection_id'], user_id='user-1')
        self.assertEqual(detail['status'], 'ERROR')
        self.assertEqual(detail['last_error'], 'provider returned empty refresh_token')

    def test_health_check_skips_revoked_connection(self) -> None:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )
        connection_id = callback['connection_id']
        self.service.delete_connection(connection_id, user_id='user-1')

        result = self.service.run_health_check_once(provider='feishu', batch_size=10)

        self.assertEqual(result['candidate_count'], 0)
        revoked = self.service.list_connections(
            owner_user_id='user-1',
            provider='feishu',
            auth_mode='oauth_user',
            status='REVOKED',
        )
        self.assertEqual(len(revoked['items']), 1)

    def test_delete_connection_revokes_owner_connection(self) -> None:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )
        connection_id = callback['connection_id']

        deleted = self.service.delete_connection(connection_id, user_id='user-1')

        self.assertTrue(deleted['deleted'])
        self.assertEqual(deleted['connection_id'], connection_id)
        self.assertEqual(deleted['status'], 'REVOKED')

        active = self.service.list_connections(
            owner_user_id='user-1',
            provider='feishu',
            auth_mode='oauth_user',
            status='ACTIVE',
        )
        self.assertEqual(active['items'], [])

        revoked = self.service.list_connections(
            owner_user_id='user-1',
            provider='feishu',
            auth_mode='oauth_user',
            status='REVOKED',
        )
        self.assertEqual(len(revoked['items']), 1)
        self.assertEqual(revoked['items'][0]['connection_id'], connection_id)
        self.assertEqual(revoked['items'][0]['app_id'], '')

        with self.assertRaisesRegex(Exception, 'cloud auth connection not found'):
            self.service.verify_connection(connection_id, user_id='user-1')
        with self.assertRaisesRegex(Exception, 'cloud auth connection not found'):
            self.service.get_access_token(connection_id, user_id='user-1')

    def test_delete_recovery_clears_invalid_ciphertext_and_allows_new_authorization(self) -> None:
        self._assert_unreadable_connection_can_be_replaced('invalid-ciphertext')

    def test_delete_recovery_clears_different_key_ciphertext_and_allows_new_authorization(self) -> None:
        self._assert_unreadable_connection_can_be_replaced('different-key')

    def _assert_unreadable_connection_can_be_replaced(self, damage: str) -> None:
        cloud_oauth_module.encrypt_json = self._old_encrypt
        cloud_oauth_module.decrypt_json = self._old_decrypt
        connection_id = self._authorize_oauth_connection()
        with cloud_oauth_module.SessionLocal() as db:
            row = db.get(CloudAuthConnection, connection_id)
            if damage == 'different-key':
                with patch.dict(os.environ, {'LAZYMIND_AUTH_CLOUD_SECRET_KEY': 'fixture-other-key'}):
                    row.credential_ciphertext = self._old_encrypt({'client_id': 'client'})
            else:
                row.credential_ciphertext = 'invalid-ciphertext'
            row.status = 'ERROR'
            db.commit()

        with self.assertRaises(AppException) as denied:
            self.service.delete_connection(connection_id, user_id='user-2')
        self.assertEqual(denied.exception.code, 1000302)

        deleted = self.service.delete_connection(connection_id, user_id='user-1')

        self.assertTrue(deleted['deleted'])
        self.assertIsNone(self.service._cache_get(connection_id))
        with cloud_oauth_module.SessionLocal() as db:
            row = db.get(CloudAuthConnection, connection_id)
            self.assertEqual(row.status, 'REVOKED')
            self.assertEqual(self._old_decrypt(row.credential_ciphertext)['client_secret'], '')
            self.assertEqual(self._old_decrypt(row.auth_state_ciphertext)['access_token'], '')
            self.assertEqual(self._old_decrypt(row.auth_state_ciphertext)['refresh_token'], '')
        self.assertEqual(self.service.list_connections(owner_user_id='user-1')['items'], [])
        restored_id = self._authorize_oauth_connection()
        self.assertEqual(self.service.get_connection(restored_id, user_id='user-1')['status'], 'ACTIVE')
        self.service.delete_connection(restored_id, user_id='user-1')

    def _create_pending_for_deletion(self, *, client_id='other-client', owner='user-1', target=None):
        return self.service.create_authorize_url(
            provider='feishu', tenant_id='', owner_user_id=owner,
            auth_mode='oauth_user', client_id=client_id, client_secret='fixture-secret',
            redirect_uri='https://example.test/callback',
            reauthorize_connection_id=target,
        )['connection_id']

    def test_delete_recovery_skips_unreadable_pending_and_preserves_other_apps_and_owners(self) -> None:
        connection_id = self._authorize_oauth_connection()
        matching_id = self._create_pending_for_deletion(client_id='client', target=connection_id)
        other_app_id = self._create_pending_for_deletion()
        other_owner_id = self._create_pending_for_deletion(client_id='client', owner='user-2')
        broken_id = self._create_pending_for_deletion(client_id='broken-app')
        with cloud_oauth_module.SessionLocal() as db:
            db.get(CloudAuthConnection, broken_id).credential_ciphertext = 'invalid-ciphertext'
            db.commit()

        deleted = self.service.delete_connection(connection_id, user_id='user-1')

        self.assertTrue(deleted['deleted'])
        with cloud_oauth_module.SessionLocal() as db:
            for target in (connection_id, matching_id):
                self.assertEqual(db.get(CloudAuthConnection, target).status, 'REVOKED')
            for target in (other_app_id, other_owner_id, broken_id):
                self.assertEqual(db.get(CloudAuthConnection, target).status, 'PENDING')

    def test_delete_recovery_does_not_commit_target_before_cleanup_query(self) -> None:
        connection_id = self._authorize_oauth_connection()
        with patch.object(
            cloud_oauth_module.CloudAuthConnectionRepository, 'list_for_owner',
            side_effect=RuntimeError('fixture database query failure'),
        ):
            with self.assertRaisesRegex(RuntimeError, 'fixture database query failure'):
                self.service.delete_connection(connection_id, user_id='user-1')

        self.assertEqual(self.service.get_connection(connection_id, user_id='user-1')['status'], 'ACTIVE')
        self.assertIsNotNone(self.service._cache_get(connection_id))

    def test_delete_recovery_does_not_guess_app_when_target_is_unreadable(self) -> None:
        connection_id = self._authorize_oauth_connection()
        pending_id = self._create_pending_for_deletion()
        with cloud_oauth_module.SessionLocal() as db:
            row = db.get(CloudAuthConnection, connection_id)
            row.credential_ciphertext = 'invalid-ciphertext'
            row.status = 'ERROR'
            db.commit()

        self.service.delete_connection(connection_id, user_id='user-1')

        with cloud_oauth_module.SessionLocal() as db:
            self.assertEqual(db.get(CloudAuthConnection, connection_id).status, 'REVOKED')
            self.assertEqual(db.get(CloudAuthConnection, pending_id).status, 'PENDING')

    def test_delete_recovery_rolls_back_target_if_pending_update_fails(self) -> None:
        connection_id = self._authorize_oauth_connection()
        pending_id = self._create_pending_for_deletion(client_id='client', target=connection_id)
        with cloud_oauth_module.SessionLocal() as db:
            db.connection().exec_driver_sql("""
                CREATE TRIGGER reject_pending_cleanup BEFORE UPDATE ON cloud_auth_connections
                WHEN NEW.last_error = 'parent connection deleted by owner'
                BEGIN SELECT RAISE(ABORT, 'fixture pending update failure'); END
            """)
            db.commit()

        with self.assertRaisesRegex(Exception, 'fixture pending update failure'):
            self.service.delete_connection(connection_id, user_id='user-1')

        with cloud_oauth_module.SessionLocal() as db:
            self.assertEqual(db.get(CloudAuthConnection, connection_id).status, 'ACTIVE')
            self.assertEqual(db.get(CloudAuthConnection, pending_id).status, 'PENDING')
        self.assertIsNotNone(self.service._cache_get(connection_id))

    def test_delete_recovery_propagates_encryption_failure_without_revoking(self) -> None:
        connection_id = self._authorize_oauth_connection()
        with patch.object(cloud_oauth_module, 'encrypt_json', side_effect=RuntimeError('fixture key unavailable')):
            with self.assertRaises(AppException) as failure:
                self.service.delete_connection(connection_id, user_id='user-1')
        self.assertEqual(failure.exception.code, 1000708)
        self.assertEqual(self.service.get_connection(connection_id, user_id='user-1')['status'], 'ACTIVE')

    def test_delete_recovery_broken_pending_does_not_block_listing_or_new_authorization(self) -> None:
        cloud_oauth_module.encrypt_json = self._old_encrypt
        cloud_oauth_module.decrypt_json = self._old_decrypt
        connection_id = self._authorize_oauth_connection()
        broken_id = self._create_pending_for_deletion()
        with cloud_oauth_module.SessionLocal() as db:
            target = db.get(CloudAuthConnection, connection_id)
            target.credential_ciphertext = 'invalid-target-ciphertext'
            target.status = 'ERROR'
            db.get(CloudAuthConnection, broken_id).credential_ciphertext = 'invalid-pending-ciphertext'
            db.commit()

        self.service.delete_connection(connection_id, user_id='user-1')

        items = self.service.list_connections(owner_user_id='user-1')['items']
        self.assertEqual([item['connection_id'] for item in items], [broken_id])
        self.assertEqual(items[0]['status'], 'ERROR')
        self.assertEqual(items[0]['last_error'], 'cloud credential decryption failed')
        self.assertEqual(self.service.get_connection(broken_id, user_id='user-1')['status'], 'ERROR')
        restored_id = self._authorize_oauth_connection()
        self.assertEqual(self.service.get_connection(restored_id, user_id='user-1')['status'], 'ACTIVE')
        with cloud_oauth_module.SessionLocal() as db:
            self.assertEqual(db.get(CloudAuthConnection, broken_id).status, 'PENDING')
        self.service.delete_connection(broken_id, user_id='user-1')
        items = self.service.list_connections(owner_user_id='user-1')['items']
        self.assertEqual([item['connection_id'] for item in items], [restored_id])

    def test_delete_recovery_broken_active_and_pending_remain_visible_without_merging(self) -> None:
        cloud_oauth_module.encrypt_json = self._old_encrypt
        cloud_oauth_module.decrypt_json = self._old_decrypt
        active_id = self._authorize_oauth_connection()
        pending_id = self._create_pending_for_deletion()
        with cloud_oauth_module.SessionLocal() as db:
            for connection_id in (active_id, pending_id):
                db.get(CloudAuthConnection, connection_id).credential_ciphertext = 'invalid-ciphertext'
            db.commit()
        self.service._cache_delete(active_id)

        items = self.service.list_connections(owner_user_id='user-1')['items']
        self.assertEqual({item['connection_id'] for item in items}, {active_id, pending_id})
        self.assertTrue(all(item['status'] == 'ERROR' for item in items))
        self.assertTrue(all(item['can_use_chat'] is False for item in items))
        self.assertEqual(self.service.list_connections(owner_user_id='user-2')['items'], [])
        with self.assertRaises(AppException):
            self.service.get_access_token(active_id, user_id='user-1')

        # The same provider user on a new app must not overwrite an unreadable account.
        created = self.service.create_authorize_url(
            provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='oauth_user',
            client_id='new-fixture-app', client_secret='fixture-secret',
            redirect_uri='https://example.test/callback', state='fixture-recovery-state',
        )
        callback = self.service.oauth_callback(
            provider='feishu', tenant_id='', owner_user_id='user-1',
            connection_id=created['connection_id'], code='fixture-code', state='fixture-recovery-state',
        )
        self.assertEqual(callback['connection_id'], created['connection_id'])
        with cloud_oauth_module.SessionLocal() as db:
            self.assertEqual(db.get(CloudAuthConnection, active_id).credential_ciphertext, 'invalid-ciphertext')
            self.assertEqual(db.get(CloudAuthConnection, pending_id).credential_ciphertext, 'invalid-ciphertext')

    def test_delete_recovery_management_still_reports_unavailable_crypto(self) -> None:
        cloud_oauth_module.encrypt_json = self._old_encrypt
        cloud_oauth_module.decrypt_json = self._old_decrypt
        connection_id = self._authorize_oauth_connection()
        with patch.dict(os.environ, {'LAZYMIND_AUTH_CLOUD_SECRET_KEY': ''}):
            for operation in (
                lambda: self.service.list_connections(owner_user_id='user-1'),
                lambda: self.service.get_connection(connection_id, user_id='user-1'),
                lambda: self.service.delete_connection(connection_id, user_id='user-1'),
            ):
                with self.assertRaises(AppException) as failure:
                    operation()
                self.assertEqual(failure.exception.code, 1000708)

    def test_delete_connection_requires_owner(self) -> None:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )

        with self.assertRaisesRegex(Exception, 'Forbidden'):
            self.service.delete_connection(callback['connection_id'], user_id='user-2')

        detail = self.service.get_connection(callback['connection_id'], user_id='user-1')
        self.assertEqual(detail['status'], 'ACTIVE')

    def test_update_connection_updates_owner_fields(self) -> None:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )

        updated = self.service.update_connection(
            callback['connection_id'],
            user_id='user-1',
            name=' Docs App ',
            appId='client-new',
            appSecret='secret-new',
            provider_options={'space': 'drive'},
            provider_account_meta={'tenant_name': 'Tenant One'},
            chatEnabled=True,
        )

        self.assertEqual(updated['display_name'], 'Docs App')
        self.assertEqual(updated['app_id'], 'client-new')
        self.assertEqual(updated['provider_account_meta']['name'], 'Docs App')
        self.assertEqual(updated['provider_account_meta']['display_name'], 'Docs App')
        self.assertEqual(updated['provider_account_meta']['client_id'], 'client-new')
        self.assertEqual(updated['provider_account_meta']['app_id'], 'client-new')
        self.assertEqual(updated['provider_account_meta']['tenant_name'], 'Tenant One')
        self.assertTrue(updated['provider_account_meta']['chatEnabled'])
        self.assertEqual(updated['provider_options']['space'], 'drive')
        self.assertTrue(updated['provider_options']['chat_enabled'])

        detail = self.service.get_connection(callback['connection_id'], user_id='user-1')
        self.assertEqual(detail['display_name'], 'Docs App')
        self.assertEqual(detail['app_id'], 'client-new')
        self.assertTrue(detail['provider_options']['chatEnabled'])

    def test_update_connection_requires_owner(self) -> None:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )

        with self.assertRaisesRegex(Exception, 'Forbidden'):
            self.service.update_connection(callback['connection_id'], user_id='user-2', name='Other')

        detail = self.service.get_connection(callback['connection_id'], user_id='user-1')
        self.assertEqual(detail['display_name'], 'Feishu User 1')

    def test_update_connection_rejects_deleted_connection(self) -> None:
        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )
        self.service.delete_connection(callback['connection_id'], user_id='user-1')

        with self.assertRaisesRegex(Exception, 'cloud auth connection not found'):
            self.service.update_connection(callback['connection_id'], user_id='user-1', name='Deleted')

    def test_oauth_callback_restores_deleted_provider_account_connection(self) -> None:
        first = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        first_callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=first['connection_id'],
            code='code-1',
            state='state-1',
        )
        self.service.delete_connection(first_callback['connection_id'], user_id='user-1')

        revoked = self.service.list_connections(
            owner_user_id='user-1',
            provider='feishu',
            auth_mode='oauth_user',
            status='REVOKED',
        )
        self.assertEqual(len(revoked['items']), 1)
        self.assertEqual(revoked['items'][0]['connection_id'], first_callback['connection_id'])

        second = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-2',
        )
        second_callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=second['connection_id'],
            code='code-2',
            state='state-2',
        )

        self.assertEqual(second_callback['connection_id'], first_callback['connection_id'])
        active = self.service.list_connections(
            owner_user_id='user-1',
            provider='feishu',
            auth_mode='oauth_user',
            status='ACTIVE',
        )
        self.assertEqual(len(active['items']), 1)
        self.assertEqual(active['items'][0]['connection_id'], first_callback['connection_id'])
        revoked = self.service.list_connections(
            owner_user_id='user-1',
            provider='feishu',
            auth_mode='oauth_user',
            status='REVOKED',
        )
        self.assertEqual(len(revoked['items']), 1)
        self.assertEqual(revoked['items'][0]['connection_id'], second['connection_id'])

    def test_oauth_callback_refreshes_created_at_when_restoring_deleted_account(self) -> None:
        first_connection_id = self._authorize_oauth_connection()
        with cloud_oauth_module.SessionLocal() as db:
            row = db.query(CloudAuthConnection).filter_by(
                connection_id=first_connection_id
            ).one()
            row.created_at = datetime(2020, 1, 1, tzinfo=timezone.utc)
            db.commit()

        self.service.delete_connection(first_connection_id, user_id='user-1')
        second = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-2',
        )
        with cloud_oauth_module.SessionLocal() as db:
            new_created_at = db.query(CloudAuthConnection).filter_by(
                connection_id=second['connection_id']
            ).one().created_at

        restored = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=second['connection_id'],
            code='code-2',
            state='state-2',
        )

        self.assertEqual(restored['connection_id'], first_connection_id)
        with cloud_oauth_module.SessionLocal() as db:
            row = db.query(CloudAuthConnection).filter_by(
                connection_id=first_connection_id
            ).one()
            self.assertEqual(row.status, 'ACTIVE')
            self.assertEqual(row.created_at, new_created_at)

    def test_reauthorize_connection_locks_existing_provider_account(self) -> None:
        first = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        first_callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=first['connection_id'],
            code='code-1',
            state='state-1',
        )

        reauth = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            redirect_uri='https://example.test/callback',
            state='state-2',
            reauthorize_connection_id=first_callback['connection_id'],
        )
        self.assertEqual(self.provider.authorize_client_ids[-1], 'client')
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=reauth['connection_id'],
            code='code-2',
            state='state-2',
        )

        self.assertEqual(callback['connection_id'], first_callback['connection_id'])
        self.assertEqual(callback['provider_account_id'], 'feishu-open-1')

    def test_reauthorize_connection_rejects_different_provider_account(self) -> None:
        first = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        first_callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=first['connection_id'],
            code='code-1',
            state='state-1',
        )

        reauth = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-2',
            reauthorize_connection_id=first_callback['connection_id'],
        )
        self.provider.provider_account_id = 'feishu-open-2'
        self.provider.display_name = 'Feishu User 2'

        with self.assertRaisesRegex(Exception, 'cloud credential is invalid'):
            self.service.oauth_callback(
                provider='feishu',
                tenant_id='',
                owner_user_id='user-1',
                connection_id=reauth['connection_id'],
                code='code-2',
                state='state-2',
            )

        detail = self.service.get_connection(first_callback['connection_id'], user_id='user-1')
        self.assertEqual(detail['provider_account_id'], 'feishu-open-1')

    def test_reauthorize_connection_requires_target_owner(self) -> None:
        first = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            client_id='client',
            client_secret='secret',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        first_callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=first['connection_id'],
            code='code-1',
            state='state-1',
        )

        with self.assertRaisesRegex(Exception, 'Forbidden'):
            self.service.create_authorize_url(
                provider='feishu',
                tenant_id='',
                owner_user_id='user-2',
                auth_mode='oauth_user',
                client_id='client',
                client_secret='secret',
                redirect_uri='https://example.test/callback',
                state='state-2',
                reauthorize_connection_id=first_callback['connection_id'],
            )

    def test_app_credentials_are_saved_and_reused_for_authorize_url(self) -> None:
        saved = self.service.save_app_credentials(
            provider='feishu',
            owner_user_id='user-1',
            client_id='client',
            client_secret='secret',
        )
        self.assertEqual(saved['provider'], 'feishu')
        self.assertEqual(saved['app_id'], 'client')
        self.assertTrue(saved['secret_configured'])

        loaded = self.service.get_app_credentials(provider='feishu', owner_user_id='user-1')
        self.assertEqual(loaded['app_id'], 'client')
        self.assertTrue(loaded['secret_configured'])
        self.assertNotIn('client_secret', loaded)

        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='tenant-ignored',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        self.assertEqual(created['owner_user_id'], 'user-1')
        self.assertEqual(self.provider.authorize_client_ids[-1], 'client')

    def test_app_credentials_keep_secret_when_updating_same_client_id(self) -> None:
        self.service.save_app_credentials(
            provider='feishu',
            owner_user_id='user-1',
            client_id='client',
            client_secret='secret',
        )
        updated = self.service.save_app_credentials(
            provider='feishu',
            owner_user_id='user-1',
            client_id='client',
        )
        self.assertEqual(updated['app_id'], 'client')
        self.assertTrue(updated['secret_configured'])

        created = self.service.create_authorize_url(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='oauth_user',
            redirect_uri='https://example.test/callback',
            state='state-1',
        )
        callback = self.service.oauth_callback(
            provider='feishu',
            tenant_id='',
            owner_user_id='user-1',
            connection_id=created['connection_id'],
            code='code-1',
            state='state-1',
        )
        self.assertEqual(callback['status'], 'ACTIVE')

    def test_app_credentials_are_owner_scoped_and_hidden_from_connection_list(self) -> None:
        self.service.save_app_credentials(
            provider='feishu',
            owner_user_id='user-1',
            client_id='client',
            client_secret='secret',
        )

        other = self.service.get_app_credentials(provider='feishu', owner_user_id='user-2')
        self.assertEqual(other['app_id'], '')
        self.assertFalse(other['secret_configured'])

        listed = self.service.list_connections(owner_user_id='user-1', provider='feishu')
        self.assertEqual(listed['items'], [])

        with self.assertRaisesRegex(Exception, 'cloud credential is invalid'):
            self.service.create_authorize_url(
                provider='feishu',
                tenant_id='',
                owner_user_id='user-2',
                auth_mode='oauth_user',
                redirect_uri='https://example.test/callback',
            )

    def test_app_credentials_reset_prevents_reuse(self) -> None:
        self.service.save_app_credentials(
            provider='feishu',
            owner_user_id='user-1',
            client_id='client',
            client_secret='secret',
        )
        reset = self.service.delete_app_credentials(provider='feishu', owner_user_id='user-1')
        self.assertFalse(reset['secret_configured'])

        loaded = self.service.get_app_credentials(provider='feishu', owner_user_id='user-1')
        self.assertEqual(loaded['app_id'], '')
        self.assertFalse(loaded['secret_configured'])

        with self.assertRaisesRegex(Exception, 'cloud credential is invalid'):
            self.service.create_authorize_url(
                provider='feishu',
                tenant_id='',
                owner_user_id='user-1',
                auth_mode='oauth_user',
                redirect_uri='https://example.test/callback',
            )

    def test_failed_imap_readd_does_not_corrupt_existing_connection(self) -> None:
        class _ImapProvider:
            def __init__(self) -> None:
                self.fail = False
                self.secrets: list[str] = []

            def provider_name(self) -> str:
                return 'qqmail'

            def acquire_tenant_access_token(self, *, client_id: str, client_secret: str) -> CloudTokenPayload:
                self.secrets.append(client_secret)
                if self.fail:
                    raise RuntimeError('bad auth code')
                return CloudTokenPayload(access_token=client_secret)

            def account_profile_from_email(self, email: str) -> CloudAccountProfile:
                return CloudAccountProfile(provider_account_id=email, display_name=email)

        provider = _ImapProvider()
        self.service._providers['qqmail'] = provider
        first = self.service.create_connection(
            provider='qqmail',
            tenant_id='',
            owner_user_id='user-1',
            auth_mode='service_account',
            client_id='user@qq.com',
            client_secret='good-code',
        )
        provider.fail = True
        with self.assertRaises(AppException):
            self.service.create_connection(
                provider='qqmail',
                tenant_id='',
                owner_user_id='user-1',
                auth_mode='service_account',
                client_id='user@qq.com',
                client_secret='bad-code',
            )
        with cloud_oauth_module.SessionLocal() as db:
            row = db.query(CloudAuthConnection).filter_by(connection_id=first['connection_id']).first()
            creds = self.service._decrypt_payload(row.credential_ciphertext, field_name='credential')
            self.assertEqual(row.status, 'ACTIVE')
            self.assertEqual(creds['client_secret'], 'good-code')


if __name__ == '__main__':
    unittest.main()
