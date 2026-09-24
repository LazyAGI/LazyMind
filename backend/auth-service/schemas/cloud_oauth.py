from datetime import datetime
from typing import Any

from pydantic import BaseModel, Field


class CloudOAuthAuthorizeURLBody(BaseModel):
    tenant_id: str = ''
    owner_user_id: str | None = None
    auth_mode: str = 'oauth_user'
    client_id: str | None = None
    client_secret: str | None = None
    redirect_uri: str | None = None
    scope: str | None = None
    state: str | None = None
    reauthorize_connection_id: str | None = None
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


class CloudOAuthAppCredentialBody(BaseModel):
    client_id: str
    client_secret: str | None = None
    provider_options: dict[str, Any] | None = None


class CloudOAuthAppCredentialResponse(BaseModel):
    provider: str
    app_id: str = ''
    secret_configured: bool = False
    status: str = ''
    created_at: datetime | None = None
    updated_at: datetime | None = None


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
    connection_method: str = 'legacy_byo'
    credential_location: str = 'local'
    profile_ref: str = ''
    cloud_connection_id: str = ''
    cloud_owner_user_id: str = ''
    app_id: str = ''
    provider_account_id: str = ''
    display_name: str = ''
    provider_tenant_key: str = ''
    provider_workspace_id: str = ''
    capability_contract_version: str = ''
    provider_account_meta: dict[str, Any] | None = None
    provider_options: dict[str, Any] | None = None
    scope: str = ''
    last_used_at: datetime | None = None
    status: str
    can_use_chat: bool = Field(
        description='Whether the connection is active and enabled for chat.',
        json_schema_extra={'readOnly': True},
    )
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


class ManagedConnectionMirrorBody(BaseModel):
    auth_connection_id: str
    owner_user_id: str
    cloud_owner_user_id: str
    provider: str
    display_name: str = ''
    provider_tenant_key: str = ''
    provider_workspace_id: str = ''
    provider_account_meta: dict[str, Any] = Field(default_factory=dict)
    status: str
    capability_contract_version: str
    capabilities: list[dict[str, Any]] = Field(default_factory=list)


class FeishuCLIConnectionMirrorBody(BaseModel):
    auth_connection_id: str
    owner_user_id: str
    display_name: str = ''
    provider_account_id: str
    provider_tenant_key: str
    provider_workspace_id: str = ''
    provider_account_meta: dict[str, Any] = Field(default_factory=dict)
    profile_ref: str
    granted_scopes: list[str] = Field(default_factory=list)
    credential_location: str = 'local'
    status: str
    capability_contract_version: str = 'feishu-cli/v1'
    capabilities: list[dict[str, Any]] = Field(default_factory=list)


class CloudConnectionVerifyResponse(BaseModel):
    connection_id: str
    tenant_id: str
    owner_user_id: str = ''
    provider: str
    status: str = Field(default='ACTIVE')


class CloudConnectionStatusItem(BaseModel):
    connection_id: str
    tenant_id: str = ''
    owner_user_id: str = ''
    provider: str = ''
    auth_mode: str = ''
    provider_account_id: str = ''
    display_name: str = ''
    provider_tenant_key: str = ''
    status: str = ''
    last_error: str = ''
    last_used_at: datetime | None = None
    updated_at: datetime | None = None


class CloudConnectionStatusBatchBody(BaseModel):
    connection_ids: list[str] = Field(default_factory=list, max_length=500)


class CloudConnectionStatusBatchResponse(BaseModel):
    items: list[CloudConnectionStatusItem]


class CloudConnectionListResponse(BaseModel):
    items: list[CloudConnectionResponse]


class CloudConnectionDeleteResponse(BaseModel):
    connection_id: str
    status: str = Field(default='REVOKED')
    deleted: bool = True


class CloudConnectionUpdateBody(BaseModel):
    display_name: str | None = None
    displayName: str | None = None
    name: str | None = None
    client_id: str | None = None
    app_id: str | None = None
    appId: str | None = None
    client_secret: str | None = None
    app_secret: str | None = None
    appSecret: str | None = None
    provider_options: dict[str, Any] | None = None
    provider_account_meta: dict[str, Any] | None = None
    chat_enabled: bool | None = None
    chatEnabled: bool | None = None


class CloudConnectionCreateBody(BaseModel):
    tenant_id: str = ''
    owner_user_id: str | None = None
    auth_mode: str = 'tenant'
    client_id: str
    client_secret: str
    display_name: str = ''
    provider_options: dict[str, Any] | None = None


class CloudConnectionCreateResponse(BaseModel):
    connection_id: str
    tenant_id: str
    owner_user_id: str = ''
    provider: str
    auth_mode: str
    scope: str = ''
    status: str = 'ACTIVE'
