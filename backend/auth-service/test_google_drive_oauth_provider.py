import os
import unittest
from unittest.mock import patch


os.environ.setdefault('LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'test-secret-key')

from services.providers.google_drive_oauth_provider import GoogleDriveOAuthProvider  # noqa: E402


class GoogleDriveOAuthProviderTest(unittest.TestCase):
    def setUp(self) -> None:
        self.provider = GoogleDriveOAuthProvider()

    def test_refresh_uses_rotated_refresh_token_when_returned(self) -> None:
        with patch(
            'services.providers.google_drive_oauth_provider._post_form',
            return_value={
                'access_token': 'new-access-token',
                'refresh_token': 'rotated-refresh-token',
                'expires_in': 3600,
            },
        ):
            token = self.provider.refresh_access_token(
                client_id='client-id',
                client_secret='client-secret',
                refresh_token='old-refresh-token',
            )

        self.assertEqual(token.refresh_token, 'rotated-refresh-token')

    def test_refresh_keeps_existing_refresh_token_when_omitted(self) -> None:
        with patch(
            'services.providers.google_drive_oauth_provider._post_form',
            return_value={'access_token': 'new-access-token', 'expires_in': 3600},
        ):
            token = self.provider.refresh_access_token(
                client_id='client-id',
                client_secret='client-secret',
                refresh_token='old-refresh-token',
            )

        self.assertEqual(token.refresh_token, 'old-refresh-token')


if __name__ == '__main__':
    unittest.main()
