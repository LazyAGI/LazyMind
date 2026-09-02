import os
import unittest
from unittest.mock import MagicMock, patch


os.environ.setdefault('LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'test-secret-key')

from services.mail_providers import resolve_imap_endpoint  # noqa: E402
from services.providers.imap_mail_provider import IMAPMailProvider  # noqa: E402


class IMAPMailProviderTest(unittest.TestCase):
    def _login(self, provider_name: str, email: str):
        provider = IMAPMailProvider(provider_name)
        imap = MagicMock()
        imap.login.return_value = ('OK', [b'Logged in'])
        smtp = MagicMock()
        smtp.__enter__.return_value = smtp
        with patch('services.providers.imap_mail_provider.imaplib.IMAP4_SSL', return_value=imap) as imap_cls:
            with patch('services.providers.imap_mail_provider.smtplib.SMTP_SSL', return_value=smtp) as smtp_cls:
                token = provider.acquire_tenant_access_token(
                    client_id=email,
                    client_secret='auth-code',
                )
        return token, imap_cls, smtp_cls, imap, smtp

    def test_163_login_success(self) -> None:
        token, imap_cls, smtp_cls, imap, smtp = self._login('netease163', 'user@163.com')
        self.assertEqual(token.access_token, 'auth-code')
        imap_cls.assert_called_once_with('imap.163.com', 993)
        smtp_cls.assert_called_once_with('smtp.163.com', 465, timeout=20)
        imap.login.assert_called_once_with('user@163.com', 'auth-code')
        smtp.login.assert_called_once_with('user@163.com', 'auth-code')

    def test_126_uses_netease_126_host(self) -> None:
        _, imap_cls, smtp_cls, _, _ = self._login('netease163', 'user@126.com')
        imap_cls.assert_called_once_with('imap.126.com', 993)
        smtp_cls.assert_called_once_with('smtp.126.com', 465, timeout=20)

    def test_vip_163_uses_longest_domain_match(self) -> None:
        endpoint = resolve_imap_endpoint('netease163', 'user@vip.163.com')
        self.assertEqual(endpoint['imap_host'], 'imap.vip.163.com')
        self.assertTrue(endpoint['imap_id'])

    def test_invalid_email_rejected(self) -> None:
        provider = IMAPMailProvider('qqmail')
        with self.assertRaises(RuntimeError):
            provider.acquire_tenant_access_token(client_id='not-an-email', client_secret='x')

    def test_unsupported_domain_rejected(self) -> None:
        provider = IMAPMailProvider('netease163')
        with self.assertRaises(RuntimeError):
            provider.acquire_tenant_access_token(client_id='user@gmail.com', client_secret='x')

    def test_qqexmail_allows_custom_domain(self) -> None:
        _, imap_cls, smtp_cls, _, _ = self._login('qqexmail', 'alice@company.com')
        imap_cls.assert_called_once_with('imap.exmail.qq.com', 993)
        smtp_cls.assert_called_once_with('smtp.exmail.qq.com', 465, timeout=20)

    def test_netease_enterprise_host(self) -> None:
        endpoint = resolve_imap_endpoint('neteaseqiye', 'bob@corp.cn')
        self.assertEqual(endpoint['imap_host'], 'imap.qiye.163.com')

    def test_gmail_imap_workspace_domain(self) -> None:
        _, imap_cls, smtp_cls, _, _ = self._login('gmailimap', 'user@company.com')
        imap_cls.assert_called_once_with('imap.gmail.com', 993)
        smtp_cls.assert_called_once_with('smtp.gmail.com', 465, timeout=20)


if __name__ == '__main__':
    unittest.main()
