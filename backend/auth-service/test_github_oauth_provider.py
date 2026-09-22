import json
import unittest
from unittest.mock import patch
from urllib import parse

from services.providers.github_oauth_provider import GitHubOAuthProvider


class _Response:
    def __init__(self, payload: dict):
        self._body = json.dumps(payload).encode('utf-8')

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False

    def read(self):
        return self._body


class GitHubOAuthProviderTest(unittest.TestCase):
    def setUp(self):
        self.provider = GitHubOAuthProvider()

    def test_authorize_url_uses_repo_scope_and_state(self):
        url = self.provider.build_authorize_url(
            client_id='client-id',
            redirect_uri='https://example.test/callback',
            scope='',
            state='opaque-state',
        )

        query = parse.parse_qs(parse.urlparse(url).query)
        self.assertEqual(query['client_id'], ['client-id'])
        self.assertEqual(query['scope'], ['repo'])
        self.assertEqual(query['state'], ['opaque-state'])

    @patch('services.providers.github_oauth_provider.request.urlopen')
    def test_exchange_code_returns_non_expiring_oauth_token(self, urlopen):
        urlopen.return_value = _Response({
            'access_token': 'github-token',
            'scope': 'repo',
            'token_type': 'bearer',
        })

        token = self.provider.exchange_code(
            client_id='client-id',
            client_secret='client-secret',
            code='temporary-code',
            redirect_uri='https://example.test/callback',
        )

        self.assertEqual(token.access_token, 'github-token')
        self.assertIsNone(token.expires_at)
        self.assertIsNone(token.refresh_token)

    @patch('services.providers.github_oauth_provider.request.urlopen')
    def test_fetch_account_profile_uses_stable_github_id(self, urlopen):
        urlopen.return_value = _Response({
            'id': 1234,
            'login': 'octocat',
            'name': 'The Octocat',
            'avatar_url': 'https://avatars.example/octocat',
            'html_url': 'https://github.com/octocat',
        })

        profile = self.provider.fetch_account_profile(access_token='github-token')

        self.assertEqual(profile.provider_account_id, '1234')
        self.assertEqual(profile.display_name, 'The Octocat')
        self.assertEqual(profile.meta['login'], 'octocat')


if __name__ == '__main__':
    unittest.main()
