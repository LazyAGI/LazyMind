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

_GITHUB_AUTHORIZE_URL = 'https://github.com/login/oauth/authorize'
_GITHUB_TOKEN_URL = 'https://github.com/login/oauth/access_token'
_GITHUB_USER_URL = 'https://api.github.com/user'
_DEFAULT_SCOPE = 'repo'
_REFRESH_BUFFER_SECONDS = 300


def _safe_expires_at(seconds: int | None) -> datetime | None:
    if not seconds or seconds <= 0:
        return None
    return datetime.now(timezone.utc) + timedelta(
        seconds=max(0, seconds - _REFRESH_BUFFER_SECONDS),
    )


def _request_json(
    url: str,
    *,
    method: str,
    payload: dict | None = None,
    access_token: str = '',
    timeout_seconds: int = 30,
) -> dict:
    body = (
        parse.urlencode(payload).encode('utf-8')
        if payload is not None else None
    )
    headers = {
        'Accept': 'application/json',
        'User-Agent': 'LazyMind-GitHub-OAuth',
    }
    if body is not None:
        headers['Content-Type'] = 'application/x-www-form-urlencoded'
    if access_token:
        headers['Authorization'] = f'Bearer {access_token}'
        headers['X-GitHub-Api-Version'] = '2022-11-28'
    req = request.Request(url=url, method=method, data=body, headers=headers)
    try:
        with request.urlopen(req, timeout=timeout_seconds) as resp:
            response_body = resp.read().decode('utf-8')
            return json.loads(response_body) if response_body else {}
    except HTTPError as exc:
        detail = exc.read().decode('utf-8', errors='ignore')
        raise CloudProviderError(
            f'github provider http error {exc.code}: {detail}',
            provider_code=str(exc.code),
            retryable=exc.code == 408 or exc.code == 429 or exc.code >= 500,
            requires_reauth=exc.code == 401,
        ) from exc
    except (URLError, TimeoutError) as exc:
        raise CloudProviderError(
            f'github provider network error: {exc}', retryable=True,
        ) from exc
    except json.JSONDecodeError as exc:
        raise CloudProviderError('github provider returned invalid json') from exc


def _token_payload(data: dict, *, refresh_token: str = '') -> CloudTokenPayload:
    error = str(data.get('error') or '').strip()
    if error:
        detail = str(data.get('error_description') or error)
        raise CloudProviderError(
            f'github token exchange failed: {detail}',
            provider_code=error,
            requires_reauth=error in {'bad_verification_code', 'incorrect_client_credentials'},
        )
    return CloudTokenPayload(
        access_token=str(data.get('access_token') or '').strip(),
        expires_at=_safe_expires_at(int(data.get('expires_in') or 0)),
        refresh_token=str(data.get('refresh_token') or refresh_token or '').strip() or None,
        token_type=str(data.get('token_type') or 'Bearer').strip() or 'Bearer',
    )


class GitHubOAuthProvider(CloudOAuthProvider):
    def provider_name(self) -> str:
        return 'github'

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
            'state': state,
            'allow_signup': 'true',
        })
        return f'{_GITHUB_AUTHORIZE_URL}?{query}'

    def exchange_code(
        self,
        *,
        client_id: str,
        client_secret: str,
        code: str,
        redirect_uri: str,
    ) -> CloudTokenPayload:
        return _token_payload(_request_json(
            _GITHUB_TOKEN_URL,
            method='POST',
            payload={
                'client_id': client_id,
                'client_secret': client_secret,
                'code': code,
                'redirect_uri': redirect_uri,
            },
        ))

    def refresh_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
        refresh_token: str,
    ) -> CloudTokenPayload:
        # GitHub OAuth Apps normally issue non-expiring tokens. This path also
        # supports GitHub's optional expiring user-token configuration.
        return _token_payload(_request_json(
            _GITHUB_TOKEN_URL,
            method='POST',
            payload={
                'client_id': client_id,
                'client_secret': client_secret,
                'grant_type': 'refresh_token',
                'refresh_token': refresh_token,
            },
        ), refresh_token=refresh_token)

    def acquire_tenant_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
    ) -> CloudTokenPayload:
        raise RuntimeError('GitHub only supports oauth_user connections in LazyMind')

    def fetch_account_profile(self, *, access_token: str) -> CloudAccountProfile:
        user = _request_json(
            _GITHUB_USER_URL,
            method='GET',
            access_token=access_token,
        )
        account_id = str(user.get('id') or '').strip()
        login = str(user.get('login') or '').strip()
        return CloudAccountProfile(
            provider_account_id=account_id or login,
            display_name=str(user.get('name') or login or account_id).strip(),
            meta={
                'id': account_id,
                'login': login,
                'name': str(user.get('name') or ''),
                'avatar_url': str(user.get('avatar_url') or ''),
                'html_url': str(user.get('html_url') or ''),
            },
        )


__all__ = ['GitHubOAuthProvider']
