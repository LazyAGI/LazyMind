import base64
import json
from datetime import datetime, timedelta, timezone
from urllib import parse, request
from urllib.error import HTTPError, URLError

from services.cloud_oauth_provider import CloudOAuthProvider, CloudTokenPayload, CloudAccountProfile


_NOTION_OAUTH_AUTHORIZE_URL = 'https://api.notion.com/v1/oauth/authorize'
_NOTION_TOKEN_URL = 'https://api.notion.com/v1/oauth/token'
_REFRESH_BUFFER_SECONDS = 300


def _safe_expires_at(seconds: int | None) -> datetime | None:
    if not seconds or seconds <= 0:
        return None
    return datetime.now(timezone.utc) + timedelta(seconds=max(0, seconds - _REFRESH_BUFFER_SECONDS))


def _post_oauth_token(client_id: str, client_secret: str, payload: dict, timeout_seconds: int = 30) -> dict:
    body = json.dumps(payload, ensure_ascii=False).encode('utf-8')
    basic = base64.b64encode(f'{client_id}:{client_secret}'.encode('utf-8')).decode('ascii')
    req = request.Request(
        url=_NOTION_TOKEN_URL,
        method='POST',
        data=body,
        headers={
            'Accept': 'application/json',
            'Authorization': f'Basic {basic}',
            'Content-Type': 'application/json',
        },
    )
    try:
        with request.urlopen(req, timeout=timeout_seconds) as resp:
            resp_body = resp.read().decode('utf-8')
            return json.loads(resp_body) if resp_body else {}
    except HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='ignore')
        raise RuntimeError(f'provider http error {exc.code}: {detail}') from exc
    except URLError as exc:
        raise RuntimeError(f'provider network error: {exc}') from exc


class NotionOAuthProvider(CloudOAuthProvider):
    def provider_name(self) -> str:
        return 'notion'

    def default_scope(self) -> str:
        return ''

    def build_authorize_url(
        self,
        *,
        client_id: str,
        redirect_uri: str,
        scope: str,
        state: str,
    ) -> str:
        query = parse.urlencode({
            'client_id': client_id,
            'redirect_uri': redirect_uri,
            'response_type': 'code',
            'owner': 'user',
            'state': state,
        })
        return f'{_NOTION_OAUTH_AUTHORIZE_URL}?{query}'

    def exchange_code(
        self,
        *,
        client_id: str,
        client_secret: str,
        code: str,
        redirect_uri: str,
    ) -> CloudTokenPayload:
        data = _post_oauth_token(
            client_id,
            client_secret,
            {
                'grant_type': 'authorization_code',
                'code': code,
                'redirect_uri': redirect_uri,
            },
        )
        if data.get('error'):
            raise RuntimeError(f"notion code exchange failed: {data.get('error_description') or data.get('error')}")
        return CloudTokenPayload(
            access_token=(data.get('access_token') or '').strip(),
            expires_at=_safe_expires_at(int(data.get('expires_in') or 0)),
            refresh_token=(data.get('refresh_token') or '').strip(),
            token_type=(data.get('token_type') or 'Bearer').strip() or 'Bearer',
        )

    def refresh_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
        refresh_token: str,
    ) -> CloudTokenPayload:
        data = _post_oauth_token(
            client_id,
            client_secret,
            {
                'grant_type': 'refresh_token',
                'refresh_token': refresh_token,
            },
        )
        if data.get('error'):
            raise RuntimeError(f"notion token refresh failed: {data.get('error_description') or data.get('error')}")
        return CloudTokenPayload(
            access_token=(data.get('access_token') or '').strip(),
            expires_at=_safe_expires_at(int(data.get('expires_in') or 0)),
            refresh_token=(data.get('refresh_token') or refresh_token or '').strip(),
            token_type=(data.get('token_type') or 'Bearer').strip() or 'Bearer',
        )

    def acquire_tenant_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
    ) -> CloudTokenPayload:
        # Notion internal connections use a static installation token. Reuse the
        # existing service_account/tenant auth path by storing that token as
        # client_secret.
        return CloudTokenPayload(
            access_token=(client_secret or '').strip(),
            expires_at=None,
            refresh_token=None,
            token_type='Bearer',
        )

    def fetch_account_profile(
        self,
        *,
        access_token: str,
    ) -> CloudAccountProfile:
        """Notion does not expose a profile endpoint via the access token.
        Return an empty profile so the caller stores the connection record
        without a provider_account_id."""
        return CloudAccountProfile()
