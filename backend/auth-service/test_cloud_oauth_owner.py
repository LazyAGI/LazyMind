import importlib
import json
import os
import tempfile
import unittest
from datetime import datetime, timedelta, timezone


os.environ.setdefault('LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'test-secret-key')

from core import database as database_module  # noqa: E402
from models import Base, CloudAuthConnection  # noqa: E402
from services.cloud_oauth_provider import CloudAccountProfile, CloudTokenPayload  # noqa: E402
from services.cloud_oauth_service import CloudOAuthService  # noqa: E402


cloud_oauth_module = importlib.import_module('services.cloud_oauth_service')


class _Provider:
    def __init__(self) -> None:
        self.authorize_client_ids: list[str] = []
        self.provider_account_id = 'feishu-open-1'
        self.display_name = 'Feishu User 1'
        self.provider_tenant_key = 'tenant-key-1'
        self.refresh_error: Exception | None = None

    def provider_name(self) -> str:
        return 'feishu'

    def build_authorize_url(self, *, client_id: str, redirect_uri: str, scope: str, state: str) -> str:
        self.authorize_client_ids.append(client_id)
        return f'https://example.test/oauth?state={state}'

    def exchange_code(self, *, client_id: str, client_secret: str, code: str, redirect_uri: str) -> CloudTokenPayload:
        return CloudTokenPayload(
            access_token='oauth-token',
            refresh_token='refresh-token',
            expires_at=datetime.now(timezone.utc) + timedelta(hours=1),
        )

    def refresh_access_token(self, *, client_id: str, client_secret: str, refresh_token: str) -> CloudTokenPayload:
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
        self.service._providers = {'feishu': self.provider}

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

    def test_oauth_callback_reuses_existing_provider_account_connection(self) -> None:
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

    def test_oauth_callback_does_not_reuse_deleted_provider_account_connection(self) -> None:
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

        self.assertNotEqual(second_callback['connection_id'], first_callback['connection_id'])
        active = self.service.list_connections(
            owner_user_id='user-1',
            provider='feishu',
            auth_mode='oauth_user',
            status='ACTIVE',
        )
        self.assertEqual(len(active['items']), 1)
        self.assertEqual(active['items'][0]['connection_id'], second_callback['connection_id'])

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


if __name__ == '__main__':
    unittest.main()
