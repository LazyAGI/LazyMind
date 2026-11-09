import importlib
import json
import os
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from concurrent.futures import ThreadPoolExecutor
from unittest.mock import patch

from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker

from core.cloud_crypto import decrypt_json, encrypt_json
from core.errors import AppException
from models import Base, CloudAuthConnection
from services.cloud_oauth_provider import CloudAccountProfile, CloudProviderError, CloudTokenPayload
from services.cloud_oauth_service import CloudOAuthService


cloud_oauth = importlib.import_module('services.cloud_oauth_service')
FAKE_TOKEN = 'fixture-github-cli-token-not-a-credential'
FAKE_OLD_TOKEN = 'fixture-old-oauth-token-not-a-credential'


class GitHubProvider:
    def __init__(self):
        self.account_id = '10001'
        self.failure = None
        self.profile_calls = []
        self.refresh_calls = []

    def provider_name(self):
        return 'github'

    def default_scope(self):
        return 'repo'

    def build_authorize_url(self, **kwargs):
        return 'https://example.test/authorize'

    def fetch_account_profile(self, *, access_token):
        self.profile_calls.append(access_token)
        if self.failure:
            raise self.failure
        return CloudAccountProfile(
            provider_account_id=self.account_id,
            display_name='Fixture User',
            meta={'id': self.account_id, 'login': 'fixture-user'},
        )

    def verify_cli_access_token(self, *, access_token):
        return self.fetch_account_profile(access_token=access_token), 'repo'

    def exchange_code(self, **kwargs):
        return CloudTokenPayload(access_token=FAKE_OLD_TOKEN)

    def refresh_access_token(self, **kwargs):
        self.refresh_calls.append(kwargs)
        raise AssertionError('CLI credentials must not use the BYO OAuth refresh path')


class GitHubConnectionFixture:
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='github-cli-connection-')
        self.addCleanup(self.temp.cleanup)
        self.env = patch.dict(os.environ, {'LAZYMIND_AUTH_CLOUD_SECRET_KEY': 'fixture-cli-encryption-key'})
        self.env.start()
        self.addCleanup(self.env.stop)
        self.engine = create_engine(
            f'sqlite:///{self.temp.name}/connections.db',
            connect_args={'check_same_thread': False},
        )
        self.addCleanup(self.engine.dispose)
        Base.metadata.create_all(self.engine)
        self.sessions = sessionmaker(bind=self.engine, expire_on_commit=False)
        self.session_patch = patch.object(cloud_oauth, 'SessionLocal', self.sessions)
        self.session_patch.start()
        self.addCleanup(self.session_patch.stop)
        self.service = CloudOAuthService()
        self.provider = GitHubProvider()
        self.service._providers['github'] = self.provider

    def seed(self, connection_id, *, source='github_cli', owner='owner-1', status='ACTIVE', account='10001'):
        credential = {
            'client_id': 'fixture-client' if source == 'manual_oauth' else '',
            'client_secret': 'fixture-secret' if source == 'manual_oauth' else '',
            'provider_options': {'chat_enabled': True},
        }
        state = {'access_token': FAKE_OLD_TOKEN, 'access_expires_at': '', 'refresh_token': ''}
        if source == 'github_cli':
            state['token_source'] = source
        with self.sessions() as db:
            db.add(CloudAuthConnection(
                connection_id=connection_id, owner_user_id=owner, tenant_id='',
                provider='github', auth_mode='oauth_user', client_id=credential['client_id'] or None,
                connection_method='legacy_byo', credential_location='local',
                credential_ciphertext=encrypt_json(credential), auth_state_ciphertext=encrypt_json(state),
                provider_account_id=account, display_name='Existing fixture', scope='repo', status=status,
            ))
            db.commit()

    def row(self, connection_id):
        with self.sessions() as db:
            return db.get(CloudAuthConnection, connection_id)

    def save(self, **kwargs):
        return self.service.save_github_cli_token(
            owner_user_id=kwargs.pop('owner_user_id', 'owner-1'), access_token=FAKE_TOKEN,
            **kwargs,
        )


class GitHubCLIConnectionTest(GitHubConnectionFixture, unittest.TestCase):

    def test_verified_token_is_encrypted_and_survives_new_service_instance(self):
        result = self.save()
        connection_id = result['connection_id']
        row = self.row(connection_id)
        self.assertEqual(self.provider.profile_calls, [FAKE_TOKEN])
        self.assertEqual(row.status, 'ACTIVE')
        self.assertEqual(row.provider_account_id, '10001')
        self.assertEqual(row.connection_method, 'legacy_byo')
        self.assertEqual(row.credential_location, 'local')
        self.assertNotIn(FAKE_TOKEN, row.auth_state_ciphertext)
        self.assertEqual(decrypt_json(row.auth_state_ciphertext)['token_source'], 'github_cli')
        credential = decrypt_json(row.credential_ciphertext)
        self.assertFalse(credential.get('client_id'))
        self.assertFalse(credential.get('client_secret'))
        self.assertNotIn(FAKE_TOKEN, json.dumps(result, default=str))
        restarted = CloudOAuthService()
        restarted._providers['github'] = self.provider
        restored = restarted.get_access_token(connection_id, user_id='owner-1')
        self.assertEqual(restored['access_token'], FAKE_TOKEN)
        self.assertEqual(restored['connection_id'], connection_id)

    def test_profile_failure_or_missing_stable_id_never_creates_connection(self):
        cases = [CloudProviderError('fixture upstream failure', requires_reauth=True), None]
        for failure in cases:
            with self.subTest(failure=bool(failure)):
                self.provider.failure = failure
                self.provider.account_id = ''
                with self.assertRaises(AppException):
                    self.save()
                with self.sessions() as db:
                    self.assertEqual(db.query(CloudAuthConnection).count(), 0)

    def test_cli_import_never_overwrites_same_account_manual_oauth(self):
        self.seed('conn-manual', source='manual_oauth')
        previous = self.row('conn-manual')
        result = self.save()
        self.assertNotEqual(result['connection_id'], 'conn-manual')
        current = self.row('conn-manual')
        self.assertEqual(current.auth_state_ciphertext, previous.auth_state_ciphertext)
        self.assertEqual(current.credential_ciphertext, previous.credential_ciphertext)
        self.assertEqual(current.status, 'ACTIVE')

    def test_manual_callback_never_merges_into_cli_connection(self):
        self.seed('conn-cli')
        self.seed('conn-pending', source='manual_oauth', status='PENDING', account='')
        with self.sessions() as db:
            row = db.get(CloudAuthConnection, 'conn-pending')
            row.auth_state_ciphertext = encrypt_json({
                'oauth_state': 'fixture-state',
                'oauth_state_expires_at': (datetime.now(timezone.utc) + timedelta(minutes=5)).isoformat(),
            })
            row.credential_ciphertext = encrypt_json({
                'client_id': 'fixture-client', 'client_secret': 'fixture-secret',
                'redirect_uri': 'https://example.test/callback',
            })
            db.commit()
        previous = self.row('conn-cli')
        result = self.service.oauth_callback(
            provider='github', tenant_id='', owner_user_id='owner-1', connection_id='conn-pending',
            code='fixture-code', state='fixture-state', redirect_uri='https://example.test/callback',
        )
        self.assertNotEqual(result['connection_id'], 'conn-cli')
        self.assertEqual(self.row('conn-cli').auth_state_ciphertext, previous.auth_state_ciphertext)

    def test_manual_authorization_cannot_explicitly_reauthorize_cli_connection(self):
        self.seed('conn-cli')
        previous = self.row('conn-cli')
        with self.assertRaises(AppException):
            self.service.create_authorize_url(
                provider='github', tenant_id='', owner_user_id='owner-1', auth_mode='oauth_user',
                client_id='fixture-client', client_secret='fixture-secret',
                redirect_uri='https://example.test/callback', reauthorize_connection_id='conn-cli',
            )
        self.assertEqual(self.row('conn-cli').auth_state_ciphertext, previous.auth_state_ciphertext)

    def manual_authorization(self, target=''):
        pending = self.service.create_authorize_url(
            provider='github', tenant_id='', owner_user_id='owner-1', auth_mode='oauth_user',
            client_id='fixture-client', client_secret='fixture-secret', state='fixture-state',
            redirect_uri='https://example.test/callback', reauthorize_connection_id=target,
        )
        return self.service.oauth_callback(
            provider='github', tenant_id='', owner_user_id='owner-1',
            connection_id=pending['connection_id'], code='fixture-code', state='fixture-state',
            redirect_uri='https://example.test/callback',
        )

    def test_manual_callback_does_not_repurpose_a_disconnected_cli_identity(self):
        self.seed('conn-cli')
        self.service.delete_connection('conn-cli', user_id='owner-1')
        result = self.manual_authorization()
        self.assertNotEqual(result['connection_id'], 'conn-cli')
        self.assertEqual(self.row('conn-cli').status, 'REVOKED')
        self.assertEqual(decrypt_json(self.row('conn-cli').auth_state_ciphertext)['token_source'], 'github_cli')

    def test_existing_manual_oauth_reauthorization_still_keeps_its_connection_id(self):
        self.seed('conn-manual', source='manual_oauth')
        result = self.manual_authorization('conn-manual')
        self.assertEqual(result['connection_id'], 'conn-manual')
        self.assertEqual(self.row('conn-manual').status, 'ACTIVE')
        self.assertEqual(decrypt_json(self.row('conn-manual').credential_ciphertext)['client_id'], 'fixture-client')

    def test_explicit_reconnect_preserves_id_and_chat_preference(self):
        self.seed('conn-cli')
        result = self.save(reauthorize_connection_id='conn-cli')
        self.assertEqual(result['connection_id'], 'conn-cli')
        row = self.row('conn-cli')
        self.assertTrue(decrypt_json(row.credential_ciphertext)['provider_options']['chat_enabled'])
        self.assertEqual(decrypt_json(row.auth_state_ciphertext)['access_token'], FAKE_TOKEN)

    def test_import_finds_existing_cli_even_when_manual_connection_is_newer(self):
        self.seed('conn-cli')
        self.seed('conn-manual', source='manual_oauth')
        with self.sessions() as db:
            db.get(CloudAuthConnection, 'conn-manual').updated_at = datetime.now(timezone.utc)
            db.commit()
        result = self.save()
        self.assertEqual(result['connection_id'], 'conn-cli')
        with self.sessions() as db:
            self.assertEqual(db.query(CloudAuthConnection).filter_by(status='ACTIVE').count(), 2)

    def test_same_identity_concurrent_saves_create_one_real_sqlite_connection(self):
        with ThreadPoolExecutor(max_workers=2) as workers:
            results = list(workers.map(lambda _: self.save(), range(2)))
        self.assertEqual(len({result['connection_id'] for result in results}), 1)
        with self.sessions() as db:
            rows = db.query(CloudAuthConnection).filter_by(status='ACTIVE', owner_user_id='owner-1').all()
            self.assertEqual(len(rows), 1)
            self.assertEqual(decrypt_json(rows[0].auth_state_ciphertext)['access_token'], FAKE_TOKEN)

    def test_reconnect_rejects_different_owner_source_or_github_identity(self):
        cases = [
            {'owner': 'owner-2'}, {'source': 'manual_oauth'}, {'account': '10002'},
        ]
        for index, kwargs in enumerate(cases):
            connection_id = f'conn-protected-{index}'
            self.seed(connection_id, **kwargs)
            previous = self.row(connection_id)
            with self.subTest(case=kwargs), self.assertRaises(AppException):
                self.save(reauthorize_connection_id=connection_id)
            self.assertEqual(self.row(connection_id).auth_state_ciphertext, previous.auth_state_ciphertext)

    def test_import_requires_nonempty_owner(self):
        with self.assertRaises(AppException):
            self.save(owner_user_id='')
        with self.sessions() as db:
            self.assertEqual(db.query(CloudAuthConnection).count(), 0)

    def test_cli_connection_rejects_manual_client_credential_updates(self):
        self.seed('conn-cli')
        previous = self.row('conn-cli')
        for update in [{'client_id': 'different-client'}, {'client_secret': 'different-secret'}]:
            with self.subTest(update=update), self.assertRaises(AppException):
                self.service.update_connection('conn-cli', user_id='owner-1', **update)
        self.assertEqual(self.row('conn-cli').credential_ciphertext, previous.credential_ciphertext)

    def test_known_expired_cli_token_is_not_resurrected_from_storage_or_cache(self):
        for status in ['EXPIRED', 'ERROR']:
            connection_id = f'conn-{status.lower()}'
            self.seed(connection_id, status=status)
            self.service._cache_set(connection_id, 'github', CloudTokenPayload(access_token=FAKE_OLD_TOKEN))
            with self.subTest(status=status), self.assertRaises(AppException):
                self.service.get_access_token(connection_id, user_id='owner-1')
            self.assertEqual(self.row(connection_id).status, status)
        self.assertEqual(self.provider.refresh_calls, [])

    def test_health_check_does_not_reactivate_invalid_cli_credentials(self):
        self.seed('conn-invalid', status='EXPIRED')
        self.service.check_connection_health('conn-invalid')
        self.assertNotEqual(self.row('conn-invalid').status, 'ACTIVE')
        self.assertEqual(self.provider.refresh_calls, [])

    def test_storage_failure_does_not_cache_new_token_or_replace_old_connection(self):
        self.seed('conn-cli')
        previous = self.row('conn-cli')
        with patch.object(self.sessions.class_, 'commit', side_effect=RuntimeError('fixture storage failure')):
            with self.assertRaises(AppException):
                self.save(reauthorize_connection_id='conn-cli')
        self.assertEqual(self.row('conn-cli').auth_state_ciphertext, previous.auth_state_ciphertext)
        token = self.service.get_access_token('conn-cli', user_id='owner-1')
        self.assertEqual(token['access_token'], FAKE_OLD_TOKEN)

    def test_new_token_is_not_visible_from_cache_before_commit(self):
        self.seed('conn-cli')
        observed = []
        original_commit = self.sessions.class_.commit

        def commit(db):
            cached = self.service._cache_get('conn-cli')
            observed.append(cached.access_token if cached else None)
            with self.sessions() as separate_reader:
                row = separate_reader.get(CloudAuthConnection, 'conn-cli')
                self.assertEqual(decrypt_json(row.auth_state_ciphertext)['access_token'], FAKE_OLD_TOKEN)
            return original_commit(db)
        with patch.object(self.sessions.class_, 'commit', commit):
            self.save(reauthorize_connection_id='conn-cli')
        self.assertTrue(observed)
        self.assertNotIn(FAKE_TOKEN, observed)

    def test_encryption_failure_leaves_no_new_connection_or_token_cache(self):
        self.seed('conn-cli')
        previous = self.row('conn-cli')
        for target in ['', 'conn-cli']:
            with self.subTest(target=target):
                with patch.object(cloud_oauth, 'encrypt_json', side_effect=RuntimeError('fixture encryption failure')):
                    with self.assertRaises(AppException):
                        self.save(reauthorize_connection_id=target)
                with self.sessions() as db:
                    self.assertEqual(db.query(CloudAuthConnection).count(), 1)
                self.assertEqual(self.row('conn-cli').auth_state_ciphertext, previous.auth_state_ciphertext)
                cached = self.service._cache_get('conn-cli')
                self.assertNotEqual(cached.access_token if cached else None, FAKE_TOKEN)

    def test_first_connection_encryption_failure_leaves_empty_database_and_cache(self):
        with self.sessions() as db:
            self.assertEqual(db.query(CloudAuthConnection).count(), 0)
        self.assertEqual(self.service._token_cache, {})
        with patch.object(cloud_oauth, 'encrypt_json', side_effect=RuntimeError('fixture encryption failure')), \
                patch.object(self.service, '_cache_set', wraps=self.service._cache_set) as cache_set:
            with self.assertRaises(AppException):
                self.save()
        with self.sessions() as db:
            self.assertEqual(db.query(CloudAuthConnection).count(), 0)
        cache_set.assert_not_called()
        self.assertEqual(self.service._token_cache, {})

    def test_disconnect_clears_only_selected_connection_and_its_cached_token(self):
        self.seed('conn-cli')
        self.seed('conn-manual', source='manual_oauth')
        manual = self.row('conn-manual')
        self.service.get_access_token('conn-cli', user_id='owner-1')
        self.service.delete_connection('conn-cli', user_id='owner-1')
        self.assertEqual(self.row('conn-cli').status, 'REVOKED')
        self.assertFalse(decrypt_json(self.row('conn-cli').auth_state_ciphertext).get('access_token'))
        self.assertEqual(self.row('conn-manual').auth_state_ciphertext, manual.auth_state_ciphertext)
        with self.assertRaises(AppException):
            self.service.get_access_token('conn-cli', user_id='owner-1')


if __name__ == '__main__':
    unittest.main()
