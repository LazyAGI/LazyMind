import importlib

import pytest
from pydantic import ValidationError

from schemas.auth import (
    AuthorizeBody,
    AuthorizeResponse,
    ChangePasswordBody,
    HealthResponse,
    LoginBody,
    LoginResponse,
    LogoutBody,
    MeResponse,
    RefreshBody,
    RegisterBody,
    RegisterResponse,
    SuccessResponse,
    UpdateMeBody,
    ValidateResponse,
)
from schemas.group import (
    GroupAddUsersBody,
    GroupCreateResponse,
    GroupCreateBody,
    GroupDetailResponse,
    GroupItem,
    GroupListResponse,
    GroupMemberRoleBody,
    GroupMemberRoleBatchBody,
    GroupRemoveUsersBody,
    GroupUpdateBody,
    GroupUserItem,
    GroupUserListResponse,
    OkResponse as GroupOkResponse,
    UserGroupItem,
    UserGroupListResponse,
)
from schemas.role import (
    OkResponse as RoleOkResponse,
    PermissionGroupItem,
    RoleCreateBody,
    RoleCreateResponse,
    RoleItem,
    RolePermissionsBody,
    RolePermissionsResponse,
)
from schemas.user import (
    CreateUserBody,
    CreateUserResponse,
    DisableUserBody,
    ResetPasswordBody,
    UserItem,
    UserDetailResponse,
    UserListResponse,
    UserRoleBody,
    UserRoleBatchBody,
)


def test_schemas_package_importable():
    schemas_pkg = importlib.import_module('schemas')
    assert schemas_pkg is not None


def test_auth_schema_defaults_and_required_fields():
    body = RegisterBody(username='tester', password='Aa1!aaaa', confirm_password='Aa1!aaaa')
    update_body = UpdateMeBody()
    success = SuccessResponse()
    health = HealthResponse(timestamp=123.0)
    register_response = RegisterResponse(user_id='u1', role='user')
    login_body = LoginBody(username='tester', password='Aa1!aaaa')
    login_response = LoginResponse(
        access_token='access',
        refresh_token='refresh',
        refresh_expires_at='2026-01-01T00:00:00Z',
        role='user',
        expires_in=3600,
    )
    refresh_body = RefreshBody(refresh_token='refresh')
    validate = ValidateResponse(sub='u1', role='user', permissions=['user.read'])
    me = MeResponse(
        user_id='u1',
        username='tester',
        status='active',
        role='user',
        permissions=['user.read'],
        dynamic=False,
    )
    change_password = ChangePasswordBody(old_password='old', new_password='new')
    logout = LogoutBody()
    authorize_body = AuthorizeBody(method='GET', path='/api/auth')
    authorize_response = AuthorizeResponse(allowed=True)

    assert body.email is None
    assert body.tenant_id is None
    assert update_body.model_dump(exclude_none=True) == {}
    assert success.success is True
    assert health.status == 'ok'
    assert register_response.success is True
    assert login_body.username == 'tester'
    assert login_response.token_type == 'bearer'
    assert refresh_body.refresh_token == 'refresh'
    assert validate.permissions == ['user.read']
    assert me.display_name == ''
    assert change_password.old_password == 'old'
    assert logout.refresh_token is None
    assert authorize_body.path == '/api/auth'
    assert authorize_response.allowed is True

    try:
        RegisterBody(password='Aa1!aaaa', confirm_password='Aa1!aaaa')
    except ValidationError as exc:
        assert 'username' in str(exc)
    else:
        raise AssertionError('RegisterBody should require username')


def test_group_schema_payload_shapes():
    create_body = GroupCreateBody(group_name='ops')
    update_body = GroupUpdateBody(group_name='ops-2', remark='renamed')
    add_users = GroupAddUsersBody(user_ids=['u1', 'u2'], role='member')
    remove_users = GroupRemoveUsersBody(user_ids=['u1'])
    role_body = GroupMemberRoleBody(role='owner')
    role_batch = GroupMemberRoleBatchBody(user_ids=['u1'], role='owner')
    item = GroupItem(group_id='g1', group_name='ops')
    response = GroupListResponse(groups=[item], total=1, page=1, page_size=20)
    detail = GroupDetailResponse(group_id='g1', group_name='ops')
    create_response = GroupCreateResponse(group_id='g2')
    group_user_item = GroupUserItem(user_id='u1', username='alice', role='member')
    user_list = GroupUserListResponse(users=[group_user_item])
    user_group_item = UserGroupItem(user_id='u1', group_id='g1', group_name='ops')
    user_group_list = UserGroupListResponse(groups=[user_group_item])

    assert create_body.group_name == 'ops'
    assert update_body.remark == 'renamed'
    assert add_users.user_ids == ['u1', 'u2']
    assert remove_users.user_ids == ['u1']
    assert role_body.role == 'owner'
    assert role_batch.role == 'owner'
    assert response.groups[0].tenant_id is None
    assert detail.group_name == 'ops'
    assert create_response.group_id == 'g2'
    assert user_list.users[0].username == 'alice'
    assert user_group_list.groups[0].group_id == 'g1'
    assert GroupOkResponse().ok is True


def test_role_schema_payload_shapes():
    create_body = RoleCreateBody(name='auditor')
    permissions_body = RolePermissionsBody(permission_groups=['user.read'])
    permission_item = PermissionGroupItem(
        id='pg1',
        code='user.read',
        description='Read user',
        module='user',
        action='read',
    )
    role_item = RoleItem(id='r1', name='auditor', built_in=False)
    create_response = RoleCreateResponse(id='r1', name='auditor', built_in=False)
    response = RolePermissionsResponse(role_id='r1', permission_groups=['user.read'])

    assert create_body.name == 'auditor'
    assert permissions_body.permission_groups == ['user.read']
    assert permission_item.code == 'user.read'
    assert role_item.name == 'auditor'
    assert create_response.id == 'r1'
    assert response.permission_groups == ['user.read']
    assert RoleOkResponse().ok is True


def test_user_schema_defaults_and_collections():
    create_body = CreateUserBody(username='alice', password='Aa1!aaaa')
    create_response = CreateUserResponse(user_id='u1', username='alice', role_id='r1', role_name='user')
    role_body = UserRoleBody(role_id='r1')
    batch_body = UserRoleBatchBody(user_ids=['u1', 'u2'], role_id='r1')
    reset_password = ResetPasswordBody(new_password='Bb2@bbbb')
    disable_body = DisableUserBody()
    item = UserItem(
        user_id='u1',
        username='alice',
        status='active',
        role_id='r1',
        role_name='user',
    )
    detail = UserDetailResponse(
        user_id='u1',
        username='alice',
        status='active',
        role_id='r1',
        role_name='user',
    )
    listing = UserListResponse(users=[], total=0, page=1, page_size=20)

    assert create_body.tenant_id == ''
    assert create_body.disabled is False
    assert create_response.username == 'alice'
    assert role_body.role_id == 'r1'
    assert batch_body.user_ids == ['u1', 'u2']
    assert reset_password.new_password == 'Bb2@bbbb'
    assert disable_body.disabled is True
    assert item.is_bootstrap_admin is False
    assert detail.is_bootstrap_admin is False
    assert listing.users == []


@pytest.mark.parametrize(
    'schema_cls,payload,missing_field',
    [
        (LoginBody, {'password': 'Aa1!aaaa'}, 'username'),
        (RefreshBody, {}, 'refresh_token'),
        (AuthorizeBody, {'method': 'GET'}, 'path'),
        (GroupCreateBody, {}, 'group_name'),
        (GroupAddUsersBody, {}, 'user_ids'),
        (RoleCreateBody, {}, 'name'),
        (RolePermissionsBody, {}, 'permission_groups'),
        (CreateUserBody, {'username': 'alice'}, 'password'),
        (UserRoleBatchBody, {'role_id': 'r1'}, 'user_ids'),
    ],
)
def test_schema_required_fields_validation(schema_cls, payload, missing_field):
    with pytest.raises(ValidationError) as exc_info:
        schema_cls(**payload)

    assert missing_field in str(exc_info.value)


@pytest.mark.parametrize(
    'schema_cls,payload,error_field',
    [
        (ValidateResponse, {'sub': 'u1', 'role': 'user', 'permissions': 'user.read'}, 'permissions'),
        (GroupAddUsersBody, {'user_ids': 'u1'}, 'user_ids'),
        (GroupMemberRoleBatchBody, {'user_ids': ['u1'], 'role': 1}, 'role'),
        (RolePermissionsResponse, {'role_id': 'r1', 'permission_groups': 'user.read'}, 'permission_groups'),
        (UserListResponse, {'users': [], 'total': 'abc', 'page': 1, 'page_size': 20}, 'total'),
    ],
)
def test_schema_type_validation(schema_cls, payload, error_field):
    with pytest.raises(ValidationError) as exc_info:
        schema_cls(**payload)

    assert error_field in str(exc_info.value)
