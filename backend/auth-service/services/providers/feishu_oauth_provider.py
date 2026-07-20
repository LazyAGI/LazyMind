import json
from datetime import datetime, timedelta, timezone
from urllib import parse, request
from urllib.error import HTTPError, URLError

from services.cloud_oauth_provider import (
    CloudAccountProfile,
    CloudOAuthProvider,
    CloudProviderError,
    CloudTokenPayload,
)


_FEISHU_OAUTH_AUTHORIZE_URL = 'https://accounts.feishu.cn/open-apis/authen/v1/authorize'
_FEISHU_USER_TOKEN_URL = 'https://open.feishu.cn/open-apis/authen/v2/oauth/token'
_FEISHU_TENANT_TOKEN_URL = 'https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal'
_FEISHU_USER_INFO_URL = 'https://open.feishu.cn/open-apis/authen/v1/user_info'

_DEFAULT_SCOPE = (
    'offline_access '
    'drive:drive drive:drive:readonly drive:drive.metadata:readonly '
    'wiki:wiki wiki:wiki:readonly wiki:node:retrieve docx:document'
)

_REFRESH_BUFFER_SECONDS = 300
_REAUTH_REQUIRED_CODES = {'20037', '20064', '20073'}
_REAUTH_REQUIRED_MESSAGES = (
    'invalid_grant',
    'refresh token has expired',
    'refresh_token has expired',
    'refresh token expired',
    'invalid refresh token',
    'refresh token is invalid',
    'refresh token has been revoked',
    'refresh token revoked',
    'refresh token has been used',
)


def _provider_response_error(operation: str, data: dict, *, refresh: bool = False) -> CloudProviderError:
    code = str(data.get('code') or '').strip()
    detail = data.get('error_description') or data.get('msg') or data
    detail_text = str(detail)
    normalized_detail = detail_text.lower()
    requires_reauth = refresh and (
        code in _REAUTH_REQUIRED_CODES
        or any(marker in normalized_detail for marker in _REAUTH_REQUIRED_MESSAGES)
    )
    code_suffix = f' [{code}]' if code else ''
    return CloudProviderError(
        f'feishu {operation} failed{code_suffix}: {detail_text}',
        provider_code=code,
        requires_reauth=requires_reauth,
    )


def _post_json(url: str, payload: dict, timeout_seconds: int = 30) -> dict:
    body = json.dumps(payload, ensure_ascii=False).encode('utf-8')
    req = request.Request(
        url=url,
        method='POST',
        data=body,
        headers={'Content-Type': 'application/json; charset=utf-8'},
    )
    try:
        with request.urlopen(req, timeout=timeout_seconds) as resp:
            resp_body = resp.read().decode('utf-8')
            return json.loads(resp_body) if resp_body else {}
    except HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='ignore')
        raise CloudProviderError(
            f'provider http error {exc.code}: {detail}',
            provider_code=str(exc.code),
            retryable=exc.code == 408 or exc.code == 429 or exc.code >= 500,
        ) from exc
    except (URLError, TimeoutError) as exc:
        raise CloudProviderError(f'provider network error: {exc}', retryable=True) from exc


def _get_json(url: str, *, access_token: str, timeout_seconds: int = 30) -> dict:
    req = request.Request(
        url=url,
        method='GET',
        headers={'Authorization': f'Bearer {access_token}'},
    )
    try:
        with request.urlopen(req, timeout=timeout_seconds) as resp:
            resp_body = resp.read().decode('utf-8')
            return json.loads(resp_body) if resp_body else {}
    except HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='ignore')
        raise CloudProviderError(
            f'provider http error {exc.code}: {detail}',
            provider_code=str(exc.code),
            retryable=exc.code == 408 or exc.code == 429 or exc.code >= 500,
        ) from exc
    except (URLError, TimeoutError) as exc:
        raise CloudProviderError(f'provider network error: {exc}', retryable=True) from exc


def _safe_expires_at(seconds: int | None) -> datetime | None:
    if not seconds or seconds <= 0:
        return None
    return datetime.now(timezone.utc) + timedelta(seconds=max(0, seconds - _REFRESH_BUFFER_SECONDS))


class FeishuOAuthProvider(CloudOAuthProvider):
    def provider_name(self) -> str:
        return 'feishu'

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
            'scope': scope or self.default_scope(),
            'response_type': 'code',
            'state': state,
        })
        return f'{_FEISHU_OAUTH_AUTHORIZE_URL}?{query}'

    def exchange_code(
        self,
        *,
        client_id: str,
        client_secret: str,
        code: str,
        redirect_uri: str,
    ) -> CloudTokenPayload:
        payload = {
            'grant_type': 'authorization_code',
            'client_id': client_id,
            'client_secret': client_secret,
            'code': code,
            'redirect_uri': redirect_uri,
        }
        data = _post_json(_FEISHU_USER_TOKEN_URL, payload)
        if data.get('code', 0) != 0:
            raise _provider_response_error('code exchange', data)
        return CloudTokenPayload(
            access_token=(data.get('access_token') or '').strip(),
            expires_at=_safe_expires_at(int(data.get('expires_in') or 0)),
            refresh_token=(data.get('refresh_token') or '').strip(),
            token_type='Bearer',
        )

    def refresh_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
        refresh_token: str,
    ) -> CloudTokenPayload:
        payload = {
            'grant_type': 'refresh_token',
            'client_id': client_id,
            'client_secret': client_secret,
            'refresh_token': refresh_token,
        }
        data = _post_json(_FEISHU_USER_TOKEN_URL, payload)
        if data.get('code', 0) != 0:
            raise _provider_response_error('token refresh', data, refresh=True)
        return CloudTokenPayload(
            access_token=(data.get('access_token') or '').strip(),
            expires_at=_safe_expires_at(int(data.get('expires_in') or 0)),
            refresh_token=(data.get('refresh_token') or refresh_token or '').strip(),
            token_type='Bearer',
        )

    def acquire_tenant_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
    ) -> CloudTokenPayload:
        payload = {'app_id': client_id, 'app_secret': client_secret}
        data = _post_json(_FEISHU_TENANT_TOKEN_URL, payload)
        if data.get('code') != 0:
            raise RuntimeError(f"feishu tenant token failed: {data.get('msg') or data}")
        return CloudTokenPayload(
            access_token=(data.get('tenant_access_token') or '').strip(),
            expires_at=_safe_expires_at(int(data.get('expire') or 0)),
            refresh_token=None,
            token_type='Bearer',
        )

    def fetch_account_profile(self, *, access_token: str) -> CloudAccountProfile:
        data = _get_json(_FEISHU_USER_INFO_URL, access_token=access_token)
        if data.get('code', 0) != 0:
            raise RuntimeError(f"feishu user info failed: {data.get('msg') or data}")
        user = data.get('data') or data
        open_id = (user.get('open_id') or '').strip()
        union_id = (user.get('union_id') or '').strip()
        user_id = (user.get('user_id') or '').strip()
        tenant_key = (user.get('tenant_key') or '').strip()
        display_name = (
            user.get('name')
            or user.get('en_name')
            or user.get('email')
            or open_id
            or union_id
            or user_id
            or ''
        )
        return CloudAccountProfile(
            provider_account_id=open_id or union_id or user_id,
            display_name=(display_name or '').strip(),
            provider_tenant_key=tenant_key,
            meta={
                'open_id': open_id,
                'union_id': union_id,
                'user_id': user_id,
                'tenant_key': tenant_key,
                'name': user.get('name') or '',
                'en_name': user.get('en_name') or '',
                'email': user.get('email') or '',
                'avatar_url': user.get('avatar_url') or user.get('avatar_thumb') or '',
            },
        )
