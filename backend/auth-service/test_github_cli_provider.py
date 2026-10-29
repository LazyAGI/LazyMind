import io
import json
import unittest
from urllib.error import HTTPError, URLError
from unittest.mock import patch

from services.cloud_oauth_provider import CloudProviderError
from services.providers.github_oauth_provider import GitHubOAuthProvider


TOKEN = 'fixture-cli-verification-token-not-a-credential'


class Response:
    def __init__(self, payload, scopes='repo, read:user'):
        self.body = json.dumps(payload).encode()
        self.headers = {'X-OAuth-Scopes': scopes} if scopes is not None else {}

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False

    def read(self):
        return self.body


class GitHubCLIProviderTest(unittest.TestCase):
    def setUp(self):
        self.provider = GitHubOAuthProvider()

    def test_cli_verification_uses_user_endpoint_and_real_response_scope(self):
        with patch('services.providers.github_oauth_provider.request.urlopen', return_value=Response({
            'id': 10001, 'login': 'fixture-user', 'name': 'Fixture User',
        })) as request:
            profile, scope = self.provider.verify_cli_access_token(access_token=TOKEN)
        self.assertEqual(profile.provider_account_id, '10001')
        self.assertEqual(set(scope.replace(',', ' ').split()), {'repo', 'read:user'})
        req = request.call_args.args[0]
        self.assertEqual(req.full_url, 'https://api.github.com/user')
        self.assertEqual(req.get_method(), 'GET')
        self.assertEqual(req.get_header('Authorization'), f'Bearer {TOKEN}')

    def test_login_name_without_stable_uid_does_not_pass(self):
        with patch('services.providers.github_oauth_provider.request.urlopen', return_value=Response({
            'login': 'fixture-user',
        })), self.assertRaises(CloudProviderError):
            self.provider.verify_cli_access_token(access_token=TOKEN)

    def test_insufficient_scope_is_distinct_from_unverifiable_scope(self):
        for scopes, code in [('read:user', 'GITHUB_SCOPE_MISSING'), (None, 'GITHUB_SCOPE_UNVERIFIED')]:
            with self.subTest(scopes=scopes):
                with patch('services.providers.github_oauth_provider.request.urlopen',
                           return_value=Response({'id': 10001, 'login': 'fixture-user'}, scopes)), \
                        self.assertRaises(CloudProviderError) as error:
                    self.provider.verify_cli_access_token(access_token=TOKEN)
                self.assertEqual(error.exception.provider_code, code)

    def test_upstream_error_is_classified_without_exposing_response_or_token(self):
        for status, retryable, requires_reauth in [
            (401, False, True), (403, False, False), (429, True, False), (503, True, False),
        ]:
            error = HTTPError('https://api.github.com/user', status, 'fixture', {}, io.BytesIO(TOKEN.encode()))
            with self.subTest(status=status):
                with patch('services.providers.github_oauth_provider.request.urlopen', side_effect=error), \
                        self.assertRaises(CloudProviderError) as caught:
                    self.provider.verify_cli_access_token(access_token=TOKEN)
                self.assertEqual(caught.exception.retryable, retryable)
                self.assertEqual(caught.exception.requires_reauth, requires_reauth)
                self.assertNotIn(TOKEN, str(caught.exception))

    def test_network_failure_is_retryable_and_does_not_require_new_authorization(self):
        with patch('services.providers.github_oauth_provider.request.urlopen', side_effect=URLError(TOKEN)), \
                self.assertRaises(CloudProviderError) as error:
            self.provider.verify_cli_access_token(access_token=TOKEN)
        self.assertTrue(error.exception.retryable)
        self.assertFalse(error.exception.requires_reauth)
        self.assertNotIn(TOKEN, str(error.exception))


if __name__ == '__main__':
    unittest.main()
