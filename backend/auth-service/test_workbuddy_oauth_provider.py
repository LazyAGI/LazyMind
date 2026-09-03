import os
import unittest
from unittest.mock import patch
from urllib import parse


os.environ.setdefault('LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'test-secret-key')

from services.providers.workbuddy_oauth_provider import WorkBuddyOAuthProvider  # noqa: E402


class WorkBuddyOAuthProviderTest(unittest.TestCase):
    def setUp(self) -> None:
        self.provider = WorkBuddyOAuthProvider()

    def test_authorize_url_uses_official_flow_and_required_local_assistant_scopes(self) -> None:
        url = self.provider.build_authorize_url(
            client_id='app-id',
            redirect_uri='https://lazymind.example/oauth/workbuddy/callback',
            scope='',
            state='csrf-state',
        )
        parsed = parse.urlparse(url)
        query = parse.parse_qs(parsed.query)

        self.assertEqual(parsed.geturl().split('?')[0], 'https://www.workbuddy.cn/openapi/v2/authorize')
        self.assertEqual(query['client_id'], ['app-id'])
        self.assertEqual(query['redirect_uri'], ['https://lazymind.example/oauth/workbuddy/callback'])
        self.assertEqual(query['response_type'], ['code'])
        self.assertEqual(query['state'], ['csrf-state'])
        self.assertEqual(
            set(query['scope'][0].split()),
            {'user.localassistant.readable', 'user.localassistant.invokable'},
        )

    def test_token_exchange_and_refresh_keep_server_side_secret(self) -> None:
        with patch(
            'services.providers.workbuddy_oauth_provider._post_form',
            side_effect=[
                {
                    'access_token': 'access-1',
                    'refresh_token': 'refresh-1',
                    'expires_in': 3600,
                    'token_type': 'Bearer',
                },
                {'access_token': 'access-2', 'expires_in': 3600},
            ],
        ) as post:
            exchanged = self.provider.exchange_code(
                client_id='app-id', client_secret='app-secret', code='auth-code',
                redirect_uri='https://lazymind.example/oauth/workbuddy/callback',
            )
            refreshed = self.provider.refresh_access_token(
                client_id='app-id', client_secret='app-secret', refresh_token='refresh-1',
            )

        self.assertEqual(exchanged.access_token, 'access-1')
        self.assertEqual(exchanged.refresh_token, 'refresh-1')
        self.assertEqual(refreshed.access_token, 'access-2')
        self.assertEqual(refreshed.refresh_token, 'refresh-1')
        self.assertEqual(post.call_args_list[0].args[0]['client_secret'], 'app-secret')
        self.assertEqual(post.call_args_list[1].args[0]['client_secret'], 'app-secret')

    def test_deployment_credentials_are_read_from_server_environment(self) -> None:
        with patch.dict(os.environ, {
            'LAZYMIND_WORKBUDDY_OAUTH_CLIENT_ID': 'configured-id',
            'LAZYMIND_WORKBUDDY_OAUTH_CLIENT_SECRET': 'configured-secret',
        }, clear=False):
            self.assertEqual(
                self.provider.configured_app_credentials(),
                ('configured-id', 'configured-secret'),
            )


if __name__ == '__main__':
    unittest.main()
