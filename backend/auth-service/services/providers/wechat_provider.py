import json
from datetime import datetime, timedelta, timezone
from urllib import request
from urllib.error import HTTPError, URLError

from services.cloud_oauth_provider import (
    CloudAccountProfile,
    CloudOAuthProvider,
    CloudProviderError,
    CloudTokenPayload,
)


_STABLE_TOKEN_URL = 'https://api.weixin.qq.com/cgi-bin/stable_token'
_RETRYABLE_ERROR_CODES = {'-1', '45009'}


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
            response_body = resp.read().decode('utf-8')
    except HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='ignore')
        raise CloudProviderError(
            f'provider http error {exc.code}: {detail}',
            provider_code=str(exc.code),
            retryable=exc.code == 408 or exc.code == 429 or exc.code >= 500,
        ) from exc
    except (URLError, TimeoutError) as exc:
        raise CloudProviderError(f'provider network error: {exc}', retryable=True) from exc

    try:
        parsed = json.loads(response_body) if response_body else {}
    except json.JSONDecodeError as exc:
        raise CloudProviderError('provider returned invalid json: WeChat stable token response') from exc
    if not isinstance(parsed, dict):
        raise CloudProviderError('provider returned invalid json: WeChat stable token response')
    return parsed


class WeChatProvider(CloudOAuthProvider):
    def provider_name(self) -> str:
        return 'wechat'

    def acquire_tenant_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
    ) -> CloudTokenPayload:
        data = _post_json(
            _STABLE_TOKEN_URL,
            {
                'grant_type': 'client_credential',
                'appid': client_id,
                'secret': client_secret,
                'force_refresh': False,
            },
        )
        code = str(data.get('errcode') or '').strip()
        if code and code != '0':
            detail = str(data.get('errmsg') or data)
            raise CloudProviderError(
                f'wechat stable token failed [{code}]: {detail}',
                provider_code=code,
                retryable=code in _RETRYABLE_ERROR_CODES,
            )

        access_token = (data.get('access_token') or '').strip()
        if not access_token:
            raise CloudProviderError('wechat stable token failed: empty access_token')
        expires_in = int(data.get('expires_in') or 0)
        expires_at = (
            datetime.now(timezone.utc) + timedelta(seconds=expires_in)
            if expires_in > 0
            else None
        )
        return CloudTokenPayload(
            access_token=access_token,
            expires_at=expires_at,
            refresh_token=None,
            token_type='Bearer',
        )

    def build_authorize_url(
        self,
        *,
        client_id: str,
        redirect_uri: str,
        scope: str,
        state: str,
    ) -> str:
        raise NotImplementedError

    def exchange_code(
        self,
        *,
        client_id: str,
        client_secret: str,
        code: str,
        redirect_uri: str,
    ) -> CloudTokenPayload:
        raise NotImplementedError

    def refresh_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
        refresh_token: str,
    ) -> CloudTokenPayload:
        raise NotImplementedError

    def fetch_account_profile(self, *, access_token: str) -> CloudAccountProfile:
        return CloudAccountProfile()
