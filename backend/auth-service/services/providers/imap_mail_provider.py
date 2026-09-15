import imaplib
import re
import smtplib

from services.cloud_oauth_provider import CloudAccountProfile, CloudOAuthProvider, CloudTokenPayload
from services.mail_providers import IMAP_ENDPOINTS, resolve_imap_endpoint


_EMAIL_RE = re.compile(r'^[^@\s]+@[^@\s]+\.[^@\s]+$')


class IMAPMailProvider(CloudOAuthProvider):
    def __init__(self, provider_name: str) -> None:
        key = (provider_name or '').strip().lower()
        if key not in IMAP_ENDPOINTS:
            raise ValueError(f'unsupported imap mail provider: {provider_name}')
        self._name = key

    def provider_name(self) -> str:
        return self._name

    def default_scope(self) -> str:
        return 'imap smtp'

    def build_authorize_url(self, *, client_id: str, redirect_uri: str, scope: str, state: str) -> str:
        raise RuntimeError(f'{self._name} uses an authorization code, not OAuth')

    def exchange_code(self, *, client_id: str, client_secret: str, code: str, redirect_uri: str) -> CloudTokenPayload:
        raise RuntimeError(f'{self._name} uses an authorization code, not OAuth')

    def refresh_access_token(self, *, client_id: str, client_secret: str, refresh_token: str) -> CloudTokenPayload:
        return self.acquire_tenant_access_token(client_id=client_id, client_secret=client_secret)

    def acquire_tenant_access_token(self, *, client_id: str, client_secret: str) -> CloudTokenPayload:
        email = (client_id or '').strip()
        secret = (client_secret or '').strip()
        if not email or not _EMAIL_RE.match(email):
            raise RuntimeError('a valid mailbox address is required')
        if not secret:
            raise RuntimeError('mailbox authorization code is required')
        endpoint = resolve_imap_endpoint(self._name, email)
        self._verify_imap(email, secret, endpoint)
        self._verify_smtp(email, secret, endpoint)
        return CloudTokenPayload(access_token=secret, token_type='IMAP')

    def fetch_account_profile(self, *, access_token: str) -> CloudAccountProfile:
        return CloudAccountProfile()

    def account_profile_from_email(self, email: str) -> CloudAccountProfile:
        address = (email or '').strip()
        endpoint = resolve_imap_endpoint(self._name, address) if '@' in address else {}
        return CloudAccountProfile(
            provider_account_id=address,
            display_name=address,
            meta={
                'email': address,
                'imap_host': endpoint.get('imap_host') or '',
                'smtp_host': endpoint.get('smtp_host') or '',
                'permissions': ['read', 'send'],
            },
        )

    def _verify_imap(self, email: str, secret: str, endpoint: dict[str, object]) -> None:
        client = imaplib.IMAP4_SSL(str(endpoint['imap_host']), int(endpoint['imap_port']))
        try:
            if endpoint.get('imap_id'):
                try:
                    client.xatom('ID', '("name" "LazyMind" "version" "1.0")')
                except Exception:
                    pass
            status, _ = client.login(email, secret)
            if status != 'OK':
                raise RuntimeError('mailbox IMAP login failed')
        except imaplib.IMAP4.error as orig:
            raise RuntimeError(f'mailbox IMAP login failed: {orig}') from orig
        finally:
            try:
                client.logout()
            except Exception:
                pass

    def _verify_smtp(self, email: str, secret: str, endpoint: dict[str, object]) -> None:
        try:
            with smtplib.SMTP_SSL(str(endpoint['smtp_host']), int(endpoint['smtp_port']), timeout=20) as smtp:
                smtp.login(email, secret)
        except smtplib.SMTPException as orig:
            raise RuntimeError(f'mailbox SMTP login failed: {orig}') from orig
