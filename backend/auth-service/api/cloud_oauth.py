from fastapi import APIRouter, Depends

from core.deps import current_user, require_internal_service_token
from core.rbac import permission_required
from models import User
from schemas.cloud_oauth import (
    CloudConnectionCreateBody,
    CloudConnectionDeleteResponse,
    CloudConnectionListResponse,
    CloudConnectionCreateResponse,
    CloudConnectionResponse,
    CloudConnectionTokenResponse,
    CloudConnectionUpdateBody,
    CloudConnectionVerifyResponse,
    CloudOAuthAppCredentialBody,
    CloudOAuthAppCredentialResponse,
    CloudOAuthAuthorizeURLBody,
    CloudOAuthAuthorizeURLResponse,
    CloudOAuthCallbackBody,
    CloudOAuthCallbackResponse,
)
from services.cloud_oauth_service import cloud_oauth_service


router = APIRouter(prefix='/v1/cloud', tags=['cloud-oauth'])


@router.post('/{provider}/connections', response_model=CloudConnectionCreateResponse)
@permission_required('model.write')
def create_connection(
    provider: str,
    body: CloudConnectionCreateBody,
    user: User = Depends(current_user),  # noqa: B008
):
    return cloud_oauth_service.create_connection(
        provider=provider,
        tenant_id='',
        owner_user_id=str(user.id),
        auth_mode=body.auth_mode,
        client_id=body.client_id,
        client_secret=body.client_secret,
        provider_options=body.provider_options,
    )


@router.get('/{provider}/oauth/app-credentials', response_model=CloudOAuthAppCredentialResponse)
@permission_required('model.write')
def get_oauth_app_credentials(
    provider: str,
    user: User = Depends(current_user),  # noqa: B008
):
    return cloud_oauth_service.get_app_credentials(
        provider=provider,
        owner_user_id=str(user.id),
    )


@router.put('/{provider}/oauth/app-credentials', response_model=CloudOAuthAppCredentialResponse)
@permission_required('model.write')
def save_oauth_app_credentials(
    provider: str,
    body: CloudOAuthAppCredentialBody,
    user: User = Depends(current_user),  # noqa: B008
):
    return cloud_oauth_service.save_app_credentials(
        provider=provider,
        owner_user_id=str(user.id),
        client_id=body.client_id,
        client_secret=body.client_secret,
        provider_options=body.provider_options,
    )


@router.delete('/{provider}/oauth/app-credentials', response_model=CloudOAuthAppCredentialResponse)
@permission_required('model.write')
def delete_oauth_app_credentials(
    provider: str,
    user: User = Depends(current_user),  # noqa: B008
):
    return cloud_oauth_service.delete_app_credentials(
        provider=provider,
        owner_user_id=str(user.id),
    )


@router.post('/{provider}/oauth/authorize-url', response_model=CloudOAuthAuthorizeURLResponse)
@permission_required('model.write')
def oauth_authorize_url(
    provider: str,
    body: CloudOAuthAuthorizeURLBody,
    user: User = Depends(current_user),  # noqa: B008
):
    return cloud_oauth_service.create_authorize_url(
        provider=provider,
        tenant_id='',
        owner_user_id=str(user.id),
        auth_mode=body.auth_mode,
        client_id=body.client_id,
        client_secret=body.client_secret,
        redirect_uri=body.redirect_uri or '',
        scope=body.scope,
        state=body.state,
        reauthorize_connection_id=body.reauthorize_connection_id,
        provider_options=body.provider_options,
    )


@router.post('/{provider}/oauth/callback', response_model=CloudOAuthCallbackResponse)
@permission_required('model.write')
def oauth_callback(
    provider: str,
    body: CloudOAuthCallbackBody,
    user: User = Depends(current_user),  # noqa: B008
):
    return cloud_oauth_service.oauth_callback(
        provider=provider,
        tenant_id='',
        owner_user_id=str(user.id),
        connection_id=body.connection_id,
        code=body.code,
        state=body.state,
        redirect_uri=body.redirect_uri,
    )


@router.get('/connections', response_model=CloudConnectionListResponse)
@permission_required('model.read')
def list_connections(
    provider: str | None = None,
    auth_mode: str | None = None,
    status: str | None = 'ACTIVE',
    user: User = Depends(current_user),  # noqa: B008
):
    return cloud_oauth_service.list_connections(
        owner_user_id=str(user.id),
        provider=provider,
        auth_mode=auth_mode,
        status=status,
    )


@router.get('/connections/{connection_id}', response_model=CloudConnectionResponse)
@permission_required('model.read')
def get_connection(
    connection_id: str,
    user: User = Depends(current_user),  # noqa: B008
):
    return cloud_oauth_service.get_connection(connection_id, user_id=str(user.id))


@router.delete('/connections/{connection_id}', response_model=CloudConnectionDeleteResponse)
@permission_required('model.write')
def delete_connection(
    connection_id: str,
    user: User = Depends(current_user),  # noqa: B008
):
    return cloud_oauth_service.delete_connection(connection_id, user_id=str(user.id))


@router.put('/connections/{connection_id}', response_model=CloudConnectionResponse)
@permission_required('model.write')
def update_connection(
    connection_id: str,
    body: CloudConnectionUpdateBody,
    user: User = Depends(current_user),  # noqa: B008
):
    return cloud_oauth_service.update_connection(
        connection_id,
        user_id=str(user.id),
        display_name=body.display_name,
        displayName=body.displayName,
        name=body.name,
        client_id=body.client_id,
        app_id=body.app_id,
        appId=body.appId,
        client_secret=body.client_secret,
        app_secret=body.app_secret,
        appSecret=body.appSecret,
        provider_options=body.provider_options,
        provider_account_meta=body.provider_account_meta,
        chat_enabled=body.chat_enabled,
        chatEnabled=body.chatEnabled,
    )


@router.patch('/connections/{connection_id}', response_model=CloudConnectionResponse)
@permission_required('model.write')
def patch_connection(
    connection_id: str,
    body: CloudConnectionUpdateBody,
    user: User = Depends(current_user),  # noqa: B008
):
    return update_connection(connection_id, body, user)


@router.get('/connections/internal/chat-enabled', response_model=CloudConnectionListResponse)
def list_chat_enabled_connections(
    provider: str | None = None,
    owner_user_id: str | None = None,
    _internal: None = Depends(require_internal_service_token),  # noqa: B008
):
    """Internal endpoint: list connections with chat_enabled=true for a given owner."""
    return cloud_oauth_service.list_chat_enabled_connections(
        owner_user_id=owner_user_id or '',
        provider=provider,
    )


@router.get('/connections/{connection_id}/token', response_model=CloudConnectionTokenResponse)
def get_connection_token(
    connection_id: str,
    user_id: str | None = None,
    tenant_id: str | None = None,
    _internal: None = Depends(require_internal_service_token),  # noqa: B008
):
    return cloud_oauth_service.get_access_token(connection_id, user_id=user_id, tenant_id=tenant_id)


@router.get('/connections/{connection_id}/verify', response_model=CloudConnectionVerifyResponse)
def verify_connection(
    connection_id: str,
    user_id: str | None = None,
    tenant_id: str | None = None,
    _internal: None = Depends(require_internal_service_token),  # noqa: B008
):
    return cloud_oauth_service.verify_connection(connection_id, user_id=user_id, tenant_id=tenant_id)
