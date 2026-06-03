from pydantic import BaseModel


class CreateUserBody(BaseModel):
    username: str
    password: str
    role_id: str | None = None  # Default: user role
    email: str | None = None
    tenant_id: str = ''
    disabled: bool = False


class CreateUserResponse(BaseModel):
    user_id: str
    username: str
    role_id: str
    role_name: str


class UserRoleBody(BaseModel):
    role_id: str  # UUID string


class UserRoleBatchBody(BaseModel):
    """Assign system roles directly to users (independent of groups); user_ids supports one or multiple values"""
    user_ids: list[str]  # Array of user UUID strings
    role_id: str  # Role UUID string


class ResetPasswordBody(BaseModel):
    new_password: str


class DisableUserBody(BaseModel):
    disabled: bool = True


class UserItem(BaseModel):
    """User list item"""
    user_id: str
    username: str
    display_name: str = ''
    email: str | None = None
    phone: str | None = None
    status: str  # 'active' | 'inactive'(derived from disabled)
    tenant_id: str | None = None
    role_id: str  # UUID string
    role_name: str
    is_bootstrap_admin: bool = False


class UserListResponse(BaseModel):
    """User list"""
    users: list[UserItem]
    total: int
    page: int
    page_size: int


class UserDetailResponse(BaseModel):
    """User details"""
    user_id: str
    username: str
    display_name: str = ''
    email: str | None = None
    phone: str | None = None
    remark: str | None = None
    status: str  # 'active' | 'inactive'
    tenant_id: str | None = None
    role_id: str
    role_name: str
    is_bootstrap_admin: bool = False


class OkResponse(BaseModel):
    """Generic ok response"""
    ok: bool = True
