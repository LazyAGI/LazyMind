from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Protocol


class CloudProviderError(RuntimeError):
    def __init__(
        self,
        message: str,
        provider_code: str = '',
        retryable: bool = False,
        requires_reauth: bool = False,
    ) -> None:
        super().__init__(message, provider_code, retryable, requires_reauth)
        self.provider_code = (provider_code or '').strip()
        self.retryable = retryable
        self.requires_reauth = requires_reauth

    def __str__(self) -> str:
        return str(self.args[0])


@dataclass
class CloudTokenPayload:
    access_token: str
    expires_at: datetime | None = None
    refresh_token: str | None = None
    token_type: str = 'Bearer'


@dataclass
class CloudAccountProfile:
    provider_account_id: str = ''
    display_name: str = ''
    provider_tenant_key: str = ''
    meta: dict[str, Any] = field(default_factory=dict)


class CloudOAuthProvider(Protocol):
    def provider_name(self) -> str:
        ...

    def build_authorize_url(
        self,
        *,
        client_id: str,
        redirect_uri: str,
        scope: str,
        state: str,
    ) -> str:
        ...

    def exchange_code(
        self,
        *,
        client_id: str,
        client_secret: str,
        code: str,
        redirect_uri: str,
    ) -> CloudTokenPayload:
        ...

    def refresh_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
        refresh_token: str,
    ) -> CloudTokenPayload:
        ...

    def acquire_tenant_access_token(
        self,
        *,
        client_id: str,
        client_secret: str,
    ) -> CloudTokenPayload:
        ...

    def fetch_account_profile(
        self,
        *,
        access_token: str,
    ) -> CloudAccountProfile:
        ...
