import json
import os
import tempfile
import unittest
from datetime import datetime, timedelta, timezone


os.environ.setdefault('LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'test-secret-key')

from core import database as database_module  # noqa: E402
from models import Base  # noqa: E402
from services.cloud_oauth_provider import CloudAccountProfile, CloudTokenPayload  # noqa: E402
import services.cloud_oauth_service as cloud_oauth_module  # noqa: E402
from services.cloud_oauth_service import CloudOAuthService  # noqa: E402


class _Provider:
    def provider_name(self) -> str:
        return 'feishu'

    def build_authorize_url(self, *, client_id: str, redirect_uri: str, scope: str, state: str) -> str:
        return f'https://example.test/oauth?state={state}'

    def exchange_code(self, *, client_id: str, client_secret: str, code: str, redirect_uri: str) -> CloudTokenPayload:
        return CloudTokenPayload(
            access_token='oauth-token',
            refresh_token='refresh-token',
            expires_at=datetime.now(timezone.utc) + timedelta(hours=1),
        )

    def refresh_access_token(self, *, client_id: str, client_secret: str, refresh_token: str) -> CloudTokenPayload:
        return CloudTokenPayload(access_token='refreshed-token', refresh_token=refresh_token)

    def acquire_tenant_access_token(self, *, client_id: str, client_secret: str) -> CloudTokenPayload:
        return CloudTokenPayload(
            access_token='tenant-token',
            expires_at=datetime.now(timezone.utc) + timedelta(hours=1),
        )

    def fetch_account_profile(self, *, access_token: str) -> CloudAccountProfile:
        return CloudAccountProfile(
            provider_account_id='feishu-open-1',
            display_name='Feishu User 1',
            provider_tenant_key='tenant-key-1',
            meta={'open_id': 'feishu-open-1', 'name': 'Feishu User 1'},
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
        self.service._providers = {'feishu': _Provider()}

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
        self.assertEqual(item['provider_account_id'], 'feishu-open-1')

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


if __name__ == '__main__':
    unittest.main()
