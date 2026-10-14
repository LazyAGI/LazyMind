import unittest

import test_cloud_oauth_owner as fixtures
from services.cloud_oauth_service import CloudOAuthService
from core.errors import AppException
from schemas.cloud_oauth import FeishuCLIConnectionMirrorBody


class FeishuReauthorizationStatusTest(unittest.TestCase):
    def setUp(self):
        self.fixture = fixtures.CloudOAuthOwnerTest()
        self.fixture.setUp()
        self.addCleanup(self.fixture.tearDown)
        self.service = self.fixture.service

    def account_after_reload(self, connection_id):
        service = CloudOAuthService()
        service._providers = self.service._providers
        accounts = service.list_connections(owner_user_id='user-1', provider='feishu')['items']
        return next(account for account in accounts if account['connection_id'] == connection_id)

    def test_interrupted_oauth_remains_pending_after_reload_and_success_clears_it(self):
        original = self.fixture._authorize_oauth_connection()
        attempt = self.service.create_authorize_url(
            provider='feishu', tenant_id='', owner_user_id='user-1', auth_mode='oauth_user',
            redirect_uri='https://example.test/callback', state='fixture-reauthorization',
            reauthorize_connection_id=original,
        )
        # No successful callback: cancellation, browser back and window closing are equivalent here.
        self.assertTrue(self.account_after_reload(original)['provider_account_meta'].get('authorization_pending', False))
        self.assertEqual(self.service.get_access_token(original, user_id='user-1')['status'], 'ACTIVE')
        completed = self.service.oauth_callback(
            provider='feishu', tenant_id='', owner_user_id='user-1',
            connection_id=attempt['connection_id'], code='fixture-code', state=attempt['state'],
        )
        self.assertEqual(completed['connection_id'], original)
        self.assertFalse(self.account_after_reload(original)['provider_account_meta'].get('authorization_pending', False))

    def test_cli_pending_marker_is_persisted_and_cleared_by_successful_authorization(self):
        mirror = dict(
            auth_connection_id='fixture-cli', owner_user_id='user-1', display_name='Fixture CLI',
            provider_account_id='fixture-open', provider_tenant_key='fixture-tenant',
            provider_workspace_id='fixture-tenant', provider_account_meta={'open_id': 'fixture-open'},
            profile_ref='user-1/fixture-cli', granted_scopes=['offline_access'], credential_location='local',
            status='ACTIVE', capability_contract_version='feishu-cli/v1', capabilities=[],
        )
        self.service.upsert_feishu_cli_connection(**mirror)
        self.service.update_connection('fixture-cli', user_id='user-1', provider_account_meta={'authorization_pending': True})
        self.assertTrue(self.account_after_reload('fixture-cli')['provider_account_meta'].get('authorization_pending', False))
        self.service.upsert_feishu_cli_connection(**mirror)
        self.assertTrue(self.account_after_reload('fixture-cli')['provider_account_meta'].get('authorization_pending', False))
        mirror['provider_account_meta']['authorization_completed'] = True
        self.service.upsert_feishu_cli_connection(**mirror)
        self.assertFalse(self.account_after_reload('fixture-cli')['provider_account_meta'].get('authorization_pending', False))

    def test_internal_cli_marker_preserves_credentials_and_rejects_another_owner(self):
        mirror = dict(
            auth_connection_id='fixture-cli', owner_user_id='user-1', display_name='Fixture CLI',
            provider_account_id='fixture-open', provider_tenant_key='fixture-tenant',
            provider_workspace_id='fixture-tenant', provider_account_meta={'open_id': 'fixture-open'},
            profile_ref='user-1/fixture-cli', granted_scopes=['offline_access'], credential_location='local',
            status='ACTIVE', capability_contract_version='feishu-cli/v1', capabilities=[],
        )
        original = self.service.upsert_feishu_cli_connection(**mirror)
        marker = {**mirror, 'display_name': '', 'provider_account_id': '', 'provider_tenant_key': '',
                  'provider_account_meta': {'authorization_pending': True}, 'granted_scopes': []}
        result = self.service.upsert_feishu_cli_connection(**FeishuCLIConnectionMirrorBody(**marker).model_dump())
        for field in ('status', 'display_name', 'scope', 'profile_ref', 'provider_account_id'):
            self.assertEqual(result[field], original[field])
        self.assertTrue(self.account_after_reload('fixture-cli')['provider_account_meta'].get('authorization_pending', False))
        with self.assertRaises(AppException):
            self.service.upsert_feishu_cli_connection(**{**marker, 'owner_user_id': 'user-2', 'profile_ref': 'user-2/fixture-cli'})
        for overrides in ({'owner_user_id': ''}, {'profile_ref': ''}):
            with self.subTest(overrides=overrides), self.assertRaises(AppException):
                self.service.upsert_feishu_cli_connection(**{**marker, **overrides})


if __name__ == '__main__':
    unittest.main()
