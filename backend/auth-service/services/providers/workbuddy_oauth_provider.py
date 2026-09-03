import json
import os
from datetime import datetime, timedelta, timezone
from urllib import parse, request
from urllib.error import HTTPError, URLError

from services.cloud_oauth_provider import CloudAccountProfile, CloudOAuthProvider, CloudTokenPayload


_DEFAULT_BASE_URL = 'https://www.workbuddy.cn/openapi/v2'
_DEFAULT_SCOPE = 'user.localassistant.readable user.localassistant.invokable'
_REFRESH_BUFFER_SECONDS = 300


def _base_url() -> str:
    return (os.getenv('LAZYMIND_WORKBUDDY_OPENAPI_URL') or _DEFAULT_BASE_URL).strip().rstrip('/')


def _safe_expires_at(seconds: int | None) -> datetime | None:
    if not seconds or seconds <= 0:
        return None
    return datetime.now(timezone.utc) + timedelta(seconds=max(0, seconds - _REFRESH_BUFFER_SECONDS))


def _post_form(payload: dict, timeout_seconds: int = 30) -> dict:
    body = parse.urlencode(payload).encode('utf-8')
    req = request.Request(
        url=f'{_base_url()}/token',
        method='POST',
        data=body,
        headers={
            'Accept': 'application/json',
            'Content-Type': 'application/x-www-form-urlencoded',
        },
    )
    try:
        with request.urlopen(req, timeout=timeout_seconds) as response:
            response_body = response.read().decode('utf-8')
            return json.loads(response_body) if response_body else {}
    except HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='ignore')
        raise RuntimeError(f'provider http error {exc.code}: {detail}') from exc
    except URLError as exc:
        raise RuntimeError(f'provider network error: {exc}') from exc
    except json.JSONDecodeError as exc:
        raise RuntimeError(f'provider returned invalid json: {exc}') from exc


class WorkBuddyOAuthProvider(CloudOAuthProvider):
    def provider_name(self) -> str:
        return 'workbuddy'

    def default_scope(self) -> str:
        return _DEFAULT_SCOPE

    def configured_app_credentials(self) -> tuple[str, str]:
        return (
            (os.getenv('LAZYMIND_WORKBUDDY_OAUTH_CLIENT_ID') or '').strip(),
            (os.getenv('LAZYMIND_WORKBUDDY_OAUTH_CLIENT_SECRET') or '').strip(),
        )

    def build_authorize_url(
        self,
        *,
        client_id: str,
        redirect_uri: str,
        scope: str,
        state: str,
    ) -> str:
        query = parse.urlencode({
            'response_type': 'code',
            'client_id': client_id,
            'redirect_uri': redirect_uri,
            'scope': scope or self.default_scope(),
            'state': state,
        })
        return f'{_base_url()}/authorize?{query}'

    def exchange_code(
        self,
        *,
        client_id: str,
        client_secret: str,
        code: str,
        redirect_uri: str,
    ) -> CloudTokenPayload:
        return self._token_payload(_post_form({
            'grant_type': 'authorization_code',
            'code': code,
            'client_id': client_id,
            'client_secret': client_secret,
            'redirect_uri': redirect_uri,
        }))

    def refresh_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
        refresh_token: str,
    ) -> CloudTokenPayload:
        token = self._token_payload(_post_form({
            'grant_type': 'refresh_token',
            'refresh_token': refresh_token,
            'client_id': client_id,
            'client_secret': client_secret,
        }))
        token.refresh_token = token.refresh_token or refresh_token
        return token

    def acquire_tenant_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
    ) -> CloudTokenPayload:
        raise RuntimeError('WorkBuddy only supports oauth_user connections in LazyMind')

    def fetch_account_profile(self, *, access_token: str) -> CloudAccountProfile:
        # Local Assistant access does not require the optional profile scope.
        return CloudAccountProfile(display_name='WorkBuddy')

    @staticmethod
    def _token_payload(data: dict) -> CloudTokenPayload:
        if data.get('error'):
            raise RuntimeError(
                f"workbuddy token request failed: {data.get('error_description') or data.get('error')}"
            )
        return CloudTokenPayload(
            access_token=(data.get('access_token') or '').strip(),
            expires_at=_safe_expires_at(int(data.get('expires_in') or 0)),
            refresh_token=(data.get('refresh_token') or '').strip(),
            token_type=(data.get('token_type') or 'Bearer').strip() or 'Bearer',
        )
