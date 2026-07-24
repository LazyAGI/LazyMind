import uuid

from fastapi import APIRouter, Depends, Query

from core.deps import current_user, require_internal_service_token
from core.errors import ErrorCodes, raise_error
from core.rbac import permission_required
from models import User
from schemas.user import (
    CreateUserBody,
    CreateUserResponse,
    DisableUserBody,
    OkResponse,
    ResetPasswordBody,
    UserDetailResponse,
    UserListResponse,
    UserRoleBatchBody,
    UserRoleBody,
)
from schemas.group import UserGroupListResponse

from services import group_service, user_service


router = APIRouter(prefix='/user', tags=['user'])


@router.post('', response_model=CreateUserResponse)
@permission_required('user.admin')
def create_user(body: CreateUserBody, _: User = Depends(current_user)):  # noqa: B008
    role_id = None
    if body.role_id:
        try:
            role_id = uuid.UUID(body.role_id)
        except (ValueError, TypeError):
            raise_error(ErrorCodes.ROLE_NOT_FOUND)

    return user_service.create_user(
        username=body.username,
        password=body.password,
        role_id=role_id,
        email=body.email,
        tenant_id=body.tenant_id or '',
        disabled=body.disabled,
    )


@router.get('', response_model=UserListResponse)
@permission_required('user.read')
def list_users(
    _: User = Depends(current_user),  # noqa: B008
    page: int = Query(1, ge=1),  # noqa: B008
    page_size: int = Query(20, ge=1, le=200),  # noqa: B008
    search: str | None = None,
    tenant_id: str | None = None,
    active_only: bool = False,
):
    items, total = user_service.list_users(
        page=page,
        page_size=page_size,
        search=search,
        tenant_id=tenant_id,
        active_only=active_only,
    )
    return {'users': items, 'total': total, 'page': page, 'page_size': page_size}


def _parse_user_id(user_id: str) -> uuid.UUID:
    try:
        return uuid.UUID(user_id)
    except (ValueError, TypeError):
        raise_error(ErrorCodes.USER_NOT_FOUND)


def _parse_user_ids(user_ids: list[str]) -> list[uuid.UUID]:
    result: list[uuid.UUID] = []
    for value in user_ids:
        try:
            result.append(uuid.UUID(value))
        except (ValueError, TypeError):
            raise_error(ErrorCodes.USER_NOT_FOUND, extra_msg=value)
    return result


@router.patch('/role', response_model=OkResponse)
@permission_required('user.admin')
def set_user_roles_batch(body: UserRoleBatchBody, _: User = Depends(current_user)):  # noqa: B008
    uids = _parse_user_ids(body.user_ids or [])
    try:
        rid = uuid.UUID(body.role_id)
    except (ValueError, TypeError):
        raise_error(ErrorCodes.ROLE_NOT_FOUND)
    user_service.set_user_roles_batch(uids, rid)
    return {'ok': True}


@router.get('/{user_id}/groups/internal', response_model=UserGroupListResponse)
def list_user_groups_internal(
    user_id: str,
    _internal: None = Depends(require_internal_service_token),  # noqa: B008
):
    uid = _parse_user_id(user_id)
    return {'groups': group_service.list_user_groups(uid)}


@router.get('/{user_id}', response_model=UserDetailResponse)
@permission_required('user.read')
def get_user(user_id: str, _: User = Depends(current_user)):  # noqa: B008
    uid = _parse_user_id(user_id)
    return user_service.get_user(uid)


@router.patch('/{user_id}', response_model=OkResponse)
@permission_required('user.admin')
def set_user_role(
    user_id: str,
    body: UserRoleBody,
    _: User = Depends(current_user),  # noqa: B008
):
    uid = _parse_user_id(user_id)
    try:
        rid = uuid.UUID(body.role_id)
    except (ValueError, TypeError):
        raise_error(ErrorCodes.ROLE_NOT_FOUND)
    user_service.set_user_role(uid, rid)
    return {'ok': True}


@router.patch('/{user_id}/disable', response_model=OkResponse)
@permission_required('user.admin')
def disable_user(
    user_id: str,
    body: DisableUserBody,
    _: User = Depends(current_user),  # noqa: B008
):
    user_service.disable_user(_parse_user_id(user_id), body.disabled)
    return {'ok': True}


@router.patch('/{user_id}/reset_password', response_model=OkResponse)
@permission_required('user.admin')
def reset_password(
    user_id: str,
    body: ResetPasswordBody,
    _: User = Depends(current_user),  # noqa: B008
):
    user_service.reset_password(_parse_user_id(user_id), body.new_password or '')
    return {'ok': True}
