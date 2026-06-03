from datetime import datetime
from typing import Any

from pydantic import BaseModel, Field


class CloudOAuthAuthorizeURLBody(BaseModel):
    tenant_id: str = ''
    owner_user_id: str | None = None
    auth_mode: str = 'oauth_user'
    client_id: str
    client_secret: str
    redirect_uri: str | None = None
    scope: str | None = None
    state: str | None = None
    provider_options: dict[str, Any] | None = None


class CloudOAuthAuthorizeURLResponse(BaseModel):
    connection_id: str
    tenant_id: str
    owner_user_id: str = ''
    provider: str
    auth_mode: str
    scope: str = ''
    authorize_url: str
    state: str


class CloudOAuthCallbackBody(BaseModel):
    tenant_id: str = ''
    owner_user_id: str | None = None
    connection_id: str
    code: str
    state: str | None = None
    redirect_uri: str | None = None


class CloudOAuthCallbackResponse(BaseModel):
    connection_id: str
    tenant_id: str
    owner_user_id: str = ''
    provider: str
    auth_mode: str = 'oauth_user'
    provider_account_id: str = ''
    display_name: str = ''
    provider_tenant_key: str = ''
    provider_account_meta: dict[str, Any] | None = None
    scope: str = ''
    status: str
    expires_at: datetime | None = None
    refresh_token_bound: bool = False


class CloudConnectionResponse(BaseModel):
    connection_id: str
    tenant_id: str
    owner_user_id: str = ''
    provider: str
    auth_mode: str
    app_id: str = ''
    provider_account_id: str = ''
    display_name: str = ''
    provider_tenant_key: str = ''
    provider_account_meta: dict[str, Any] | None = None
    scope: str = ''
    last_used_at: datetime | None = None
    status: str
    last_error: str = ''
    created_at: datetime
    updated_at: datetime | None = None


class CloudConnectionTokenResponse(BaseModel):
    connection_id: str
    provider: str
    auth_mode: str = ''
    access_token: str
    token_type: str = 'Bearer'
    expires_at: datetime | None = None
    status: str = Field(default='ACTIVE')


class CloudConnectionVerifyResponse(BaseModel):
    connection_id: str
    tenant_id: str
    owner_user_id: str = ''
    provider: str
    status: str = Field(default='ACTIVE')


class CloudConnectionListResponse(BaseModel):
    items: list[CloudConnectionResponse]


class CloudConnectionCreateBody(BaseModel):
    tenant_id: str = ''
    owner_user_id: str | None = None
    auth_mode: str = 'tenant'
    client_id: str
    client_secret: str
    provider_options: dict[str, Any] | None = None


class CloudConnectionCreateResponse(BaseModel):
    connection_id: str
    tenant_id: str
    owner_user_id: str = ''
    provider: str
    auth_mode: str
    scope: str = ''
    status: str = 'ACTIVE'
