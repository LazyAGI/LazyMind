import importlib
import io
import json
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from unittest.mock import patch
from urllib.error import HTTPError, URLError

from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker

from core.errors import AppException
from models import Base, CloudAuthConnection
from services.cloud_oauth_service import CloudOAuthService


cloud_oauth_module = importlib.import_module('services.cloud_oauth_service')


class GoogleDriveRefreshTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.engine = create_engine(f'sqlite:///{self.directory.name}/auth.db')
        self.addCleanup(self.engine.dispose)
        Base.metadata.create_all(self.engine)
        self.session = sessionmaker(bind=self.engine)
        for name, value in (
            ('SessionLocal', self.session),
            ('encrypt_json', json.dumps),
            ('decrypt_json', json.loads),
        ):
            mock = patch.object(cloud_oauth_module, name, value)
            mock.start()
            self.addCleanup(mock.stop)
        self.service = CloudOAuthService()

    def _expired_google_access_token(self):
        with self.session() as db:
            row = CloudAuthConnection(
                connection_id='conn-google', owner_user_id='user-1',
                provider='googledrive', auth_mode='oauth_user', status='ACTIVE',
                credential_ciphertext=json.dumps({'client_id': 'client', 'client_secret': 'secret'}),
                auth_state_ciphertext=json.dumps({'refresh_token': 'old-refresh-token'}),
                provider_account_id='drive-account-1', provider_account_meta='{}',
            )
            db.add(row)
            db.commit()
        return 'conn-google'

    def test_google_invalid_grant_requires_reauthorization_on_first_and_later_requests(self) -> None:
        connection_id = self._expired_google_access_token()
        error = HTTPError(
            'https://oauth2.googleapis.com/token', 400, 'failure', {}, io.BytesIO(b'{"error":"invalid_grant"}'),
        )
        with patch('services.providers.google_drive_oauth_provider.request.urlopen', side_effect=error):
            for _ in range(2):
                with self.assertRaises(AppException) as raised:
                    self.service.get_access_token(connection_id, user_id='user-1')
                self.assertEqual((raised.exception.http_code, raised.exception.code), (409, 1000833))
        self.assertEqual(self.service.get_connection(connection_id, user_id='user-1')['status'], 'EXPIRED')
        with self.assertRaises(AppException) as raised:
            self.service.get_access_token(connection_id, user_id='user-2')
        self.assertEqual(raised.exception.http_code, 403)
        with self.assertRaises(AppException) as raised:
            self.service.get_access_token('missing-connection', user_id='user-1')
        self.assertEqual(raised.exception.code, 1000702)

    def test_google_temporary_failure_remains_active_and_can_recover(self) -> None:
        connection_id = self._expired_google_access_token()
        for error in (
            URLError('unreachable'), TimeoutError('timed out'),
            HTTPError('https://oauth2.googleapis.com/token', 429, 'limited', {}, io.BytesIO(b'')),
            HTTPError('https://oauth2.googleapis.com/token', 503, 'unavailable', {}, io.BytesIO(b'')),
        ):
            with self.subTest(error=error):
                with patch('services.providers.google_drive_oauth_provider.request.urlopen', side_effect=error):
                    with self.assertRaises(AppException) as raised:
                        self.service.get_access_token(connection_id, user_id='user-1')
                self.assertEqual(raised.exception.code, 1000707)
                self.assertEqual(self.service.get_connection(connection_id, user_id='user-1')['status'], 'ACTIVE')
        with patch('services.providers.google_drive_oauth_provider._post_form', return_value={
            'access_token': 'recovered-token', 'expires_in': 3600,
        }):
            token = self.service.get_access_token(connection_id, user_id='user-1')
        self.assertEqual(token['access_token'], 'recovered-token')
        self.assertEqual(self.service.get_connection(connection_id, user_id='user-1')['last_error'], '')

    def test_google_invalid_client_is_error_not_expired(self) -> None:
        connection_id = self._expired_google_access_token()
        error = HTTPError(
            'https://oauth2.googleapis.com/token', 400, 'failure', {}, io.BytesIO(b'{"error":"invalid_client"}'),
        )
        with patch('services.providers.google_drive_oauth_provider.request.urlopen', side_effect=error):
            with self.assertRaises(AppException) as raised:
                self.service.get_access_token(connection_id, user_id='user-1')
        self.assertEqual(raised.exception.code, 1000707)
        self.assertEqual(self.service.get_connection(connection_id, user_id='user-1')['status'], 'ERROR')

    def test_failed_google_refresh_recovers_concurrently_saved_token(self) -> None:
        connection_id = self._expired_google_access_token()

        def save_new_token_then_fail(*args, **kwargs):
            with cloud_oauth_module.SessionLocal() as db:
                row = db.query(CloudAuthConnection).filter_by(connection_id=connection_id).first()
                row.auth_state_ciphertext = self.service._encrypt_payload({
                    'access_token': 'concurrent-token',
                    'access_expires_at': (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat(),
                    'refresh_token': 'rotated-token',
                }, field_name='auth_state')
                db.commit()
            raise HTTPError(
                'https://oauth2.googleapis.com/token', 400, 'failure', {}, io.BytesIO(b'{"error":"invalid_grant"}'),
            )

        with patch(
            'services.providers.google_drive_oauth_provider.request.urlopen', side_effect=save_new_token_then_fail,
        ):
            token = self.service.get_access_token(connection_id, user_id='user-1')
        self.assertEqual(token['access_token'], 'concurrent-token')
        detail = self.service.get_connection(connection_id, user_id='user-1')
        self.assertEqual((detail['status'], detail['last_error']), ('ACTIVE', ''))

    def test_google_reauthorization_preserves_identity_and_rejects_other_accounts(self):
        connection_id = self._expired_google_access_token()
        for account, expected_code in (('wrong-account', 1000811), ('drive-account-1', None)):
            authorization = self.service.create_authorize_url(
                provider='googledrive', tenant_id='', owner_user_id='user-1', auth_mode='oauth_user',
                redirect_uri='https://example.test/callback', state='reauth-state',
                reauthorize_connection_id=connection_id,
            )
            with patch('services.providers.google_drive_oauth_provider._post_form', return_value={
                'access_token': 'new-token', 'refresh_token': 'new-refresh', 'expires_in': 3600,
            }), patch('services.providers.google_drive_oauth_provider._get_json', return_value={
                'user': {'permissionId': account, 'emailAddress': 'user@example.test'},
            }):
                kwargs = dict(provider='googledrive', tenant_id='', owner_user_id='user-1',
                              connection_id=authorization['connection_id'], code='code', state='reauth-state')
                if expected_code:
                    with self.assertRaises(AppException) as raised:
                        self.service.oauth_callback(**kwargs)
                    self.assertEqual(raised.exception.code, expected_code)
                else:
                    result = self.service.oauth_callback(**kwargs)
                    self.assertEqual((result['connection_id'], result['status']), (connection_id, 'ACTIVE'))
            detail = self.service.get_connection(connection_id, user_id='user-1')
            self.assertEqual(detail['provider_account_id'], 'drive-account-1')

        with self.assertRaises(AppException) as raised:
            self.service.create_authorize_url(
                provider='googledrive', tenant_id='', owner_user_id='user-2', auth_mode='oauth_user',
                redirect_uri='https://example.test/callback', reauthorize_connection_id=connection_id,
            )
        self.assertEqual(raised.exception.http_code, 403)
