from pydantic import BaseModel


class HealthResponse(BaseModel):
    status: str = 'ok'
    timestamp: float


class RegisterBody(BaseModel):
    username: str
    password: str
    confirm_password: str
    email: str | None = None
    tenant_id: str | None = None


class RegisterResponse(BaseModel):
    success: bool = True
    user_id: str
    tenant_id: str | None = None
    role: str


class LoginBody(BaseModel):
    username: str
    password: str


class LoginResponse(BaseModel):
    access_token: str
    refresh_token: str
    refresh_expires_at: str
    token_type: str = 'bearer'
    role: str
    expires_in: int
    tenant_id: str | None = None


class RefreshBody(BaseModel):
    refresh_token: str


class ValidateResponse(BaseModel):
    sub: str
    role: str
    tenant_id: str | None = None
    permissions: list[str]


class MeResponse(BaseModel):
    user_id: str
    username: str
    display_name: str = ''
    email: str | None = None
    remark: str | None = None
    status: str
    role: str
    permissions: list[str]
    tenant_id: str | None = None
    dynamic: bool


class UpdateMeBody(BaseModel):
    """Update current user's profile (all fields except username are optional)."""
    display_name: str | None = None
    email: str | None = None
    phone: str | None = None
    remark: str | None = None


class ChangePasswordBody(BaseModel):
    old_password: str
    new_password: str


class LogoutBody(BaseModel):
    refresh_token: str | None = None


class SuccessResponse(BaseModel):
    success: bool = True


class AuthorizeBody(BaseModel):
    method: str
    path: str


class AuthorizeResponse(BaseModel):
    allowed: bool
