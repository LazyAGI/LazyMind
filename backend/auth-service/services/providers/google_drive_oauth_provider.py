import json
from datetime import datetime, timedelta, timezone
from urllib import parse, request
from urllib.error import HTTPError, URLError

from services.cloud_oauth_provider import CloudAccountProfile, CloudOAuthProvider, CloudTokenPayload


_GOOGLE_AUTHORIZE_URL = 'https://accounts.google.com/o/oauth2/v2/auth'
_GOOGLE_TOKEN_URL = 'https://oauth2.googleapis.com/token'
_GOOGLE_DRIVE_ABOUT_URL = 'https://www.googleapis.com/drive/v3/about?fields=user'
_DEFAULT_SCOPE = 'https://www.googleapis.com/auth/drive.readonly'
_REFRESH_BUFFER_SECONDS = 300


def _safe_expires_at(seconds: int | None) -> datetime | None:
    if not seconds or seconds <= 0:
        return None
    return datetime.now(timezone.utc) + timedelta(seconds=max(0, seconds - _REFRESH_BUFFER_SECONDS))


def _post_form(payload: dict, timeout_seconds: int = 30) -> dict:
    body = parse.urlencode(payload).encode('utf-8')
    req = request.Request(
        url=_GOOGLE_TOKEN_URL,
        method='POST',
        data=body,
        headers={'Content-Type': 'application/x-www-form-urlencoded'},
    )
    try:
        with request.urlopen(req, timeout=timeout_seconds) as resp:
            response_body = resp.read().decode('utf-8')
            return json.loads(response_body) if response_body else {}
    except HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='ignore')
        raise RuntimeError(f'provider http error {exc.code}: {detail}') from exc
    except URLError as exc:
        raise RuntimeError(f'provider network error: {exc}') from exc
    except json.JSONDecodeError as exc:
        raise RuntimeError(f'provider returned invalid json: {exc}') from exc


def _get_json(url: str, access_token: str, timeout_seconds: int = 30) -> dict:
    req = request.Request(
        url=url,
        method='GET',
        headers={'Authorization': f'Bearer {access_token}'},
    )
    try:
        with request.urlopen(req, timeout=timeout_seconds) as resp:
            response_body = resp.read().decode('utf-8')
            return json.loads(response_body) if response_body else {}
    except HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='ignore')
        raise RuntimeError(f'provider http error {exc.code}: {detail}') from exc
    except URLError as exc:
        raise RuntimeError(f'provider network error: {exc}') from exc
    except json.JSONDecodeError as exc:
        raise RuntimeError(f'provider returned invalid json: {exc}') from exc


class GoogleDriveOAuthProvider(CloudOAuthProvider):
    def provider_name(self) -> str:
        return 'googledrive'

    def default_scope(self) -> str:
        return _DEFAULT_SCOPE

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
            'scope': scope or self.default_scope(),
            'access_type': 'offline',
            'include_granted_scopes': 'true',
            'prompt': 'consent',
            'state': state,
        })
        return f'{_GOOGLE_AUTHORIZE_URL}?{query}'

    def exchange_code(
        self,
        *,
        client_id: str,
        client_secret: str,
        code: str,
        redirect_uri: str,
    ) -> CloudTokenPayload:
        data = _post_form({
            'client_id': client_id,
            'client_secret': client_secret,
            'code': code,
            'redirect_uri': redirect_uri,
            'grant_type': 'authorization_code',
        })
        if data.get('error'):
            raise RuntimeError(
                f"google drive code exchange failed: {data.get('error_description') or data.get('error')}"
            )
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
        data = _post_form({
            'client_id': client_id,
            'client_secret': client_secret,
            'refresh_token': refresh_token,
            'grant_type': 'refresh_token',
        })
        if data.get('error'):
            raise RuntimeError(
                f"google drive token refresh failed: {data.get('error_description') or data.get('error')}"
            )
        return CloudTokenPayload(
            access_token=(data.get('access_token') or '').strip(),
            expires_at=_safe_expires_at(int(data.get('expires_in') or 0)),
            refresh_token=(data.get('refresh_token') or '').strip() or refresh_token,
            token_type=(data.get('token_type') or 'Bearer').strip() or 'Bearer',
        )

    def acquire_tenant_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
    ) -> CloudTokenPayload:
        raise RuntimeError('Google Drive only supports oauth_user connections in LazyMind')

    def fetch_account_profile(self, *, access_token: str) -> CloudAccountProfile:
        data = _get_json(_GOOGLE_DRIVE_ABOUT_URL, access_token)
        user = data.get('user') or {}
        permission_id = (user.get('permissionId') or '').strip()
        email = (user.get('emailAddress') or '').strip()
        display_name = (user.get('displayName') or email or permission_id).strip()
        return CloudAccountProfile(
            provider_account_id=permission_id or email,
            display_name=display_name,
            meta={
                'permission_id': permission_id,
                'email': email,
                'display_name': user.get('displayName') or '',
                'photo_link': user.get('photoLink') or '',
            },
        )
