from dataclasses import dataclass
from typing import Any, Tuple, Type


ErrorTuple = Tuple[int, int, str]


class ErrorCodes:
    INVALID_USERNAME: ErrorTuple = (400, 1000101, 'Invalid username format')
    USER_ALREADY_EXISTS: ErrorTuple = (400, 1000102, 'User already exists')
    INVALID_PASSWORD: ErrorTuple = (400, 1000103, 'Invalid password format')
    LOGIN_LOCKED: ErrorTuple = (400, 1000104, 'Login is locked, please try again later')
    INVALID_CREDENTIALS: ErrorTuple = (400, 1000105, 'Invalid username or password')
    USER_DISABLED: ErrorTuple = (400, 1000106, 'User is disabled')

    USERNAME_REQUIRED: ErrorTuple = (400, 1000201, 'Username is required')
    PASSWORD_REQUIRED: ErrorTuple = (400, 1000202, 'Password is required')
    REFRESH_TOKEN_REQUIRED: ErrorTuple = (401, 1000203, 'refresh_token is required')
    PASSWORD_CONFIRM_MISMATCH: ErrorTuple = (400, 1000204, 'Password confirmation does not match')
    OLD_PASSWORD_INVALID: ErrorTuple = (400, 1000205, 'Old password is incorrect')
    NEW_PASSWORD_REQUIRED: ErrorTuple = (400, 1000206, 'New password is required')
    REFRESH_TOKEN_INVALID: ErrorTuple = (401, 1000207, 'refresh_token is invalid or expired')
    NEW_PASSWORD_SAME_AS_OLD: ErrorTuple = (400, 1000208, 'New password must be different from old password')
    INVALID_PHONE_FORMAT: ErrorTuple = (400, 1000209, 'Invalid phone format')

    UNAUTHORIZED: ErrorTuple = (401, 1000301, 'Unauthorized')
    FORBIDDEN: ErrorTuple = (403, 1000302, 'Forbidden')
    ADMIN_REQUIRED: ErrorTuple = (403, 1000303, 'Admin permission is required')

    USER_NOT_FOUND: ErrorTuple = (404, 1000401, 'User not found')
    GROUP_NOT_FOUND: ErrorTuple = (404, 1000402, 'Group not found')
    ROLE_NOT_FOUND: ErrorTuple = (404, 1000403, 'Role not found')
    GROUP_NAME_REQUIRED: ErrorTuple = (400, 1000404, 'Group name is required')
    GROUP_NAME_EMPTY: ErrorTuple = (400, 1000405, 'Group name cannot be empty')
    ROLE_REQUIRED: ErrorTuple = (400, 1000406, 'Role is required')
    MEMBERSHIP_NOT_FOUND: ErrorTuple = (404, 1000407, 'Membership not found')
    ROLE_NAME_REQUIRED: ErrorTuple = (400, 1000408, 'Role name is required')
    ROLE_NAME_EXISTS: ErrorTuple = (400, 1000409, 'Role name already exists')
    GROUP_NAME_EXISTS: ErrorTuple = (400, 1000413, 'Group name already exists')
    CANNOT_DELETE_BUILTIN_ROLE: ErrorTuple = (400, 1000410, 'Built-in role cannot be deleted')
    CANNOT_CHANGE_ADMIN_PERMS: ErrorTuple = (400, 1000411, 'System-admin role permissions cannot be changed')
    BOOTSTRAP_ADMIN_ROLE_CHANGE_FORBIDDEN: ErrorTuple = (403, 1000412, 'Bootstrap admin role cannot be changed')

    DEFAULT_ROLE_NOT_FOUND: ErrorTuple = (500, 1000501, "Default role 'user' does not exist")

    REDIS_AUTH_FAILED: ErrorTuple = (500, 1000601, 'Redis authentication failed')
    REDIS_UNAVAILABLE: ErrorTuple = (500, 1000602, 'Redis is unavailable')

    CLOUD_PROVIDER_UNSUPPORTED: ErrorTuple = (400, 1000701, 'cloud provider is not supported')
    CLOUD_CONNECTION_NOT_FOUND: ErrorTuple = (404, 1000702, 'cloud auth connection not found')
    CLOUD_OAUTH_STATE_INVALID: ErrorTuple = (400, 1000703, 'oauth state is invalid or expired')
    CLOUD_OAUTH_CODE_REQUIRED: ErrorTuple = (400, 1000704, 'oauth code is required')
    CLOUD_AUTH_MODE_INVALID: ErrorTuple = (400, 1000705, 'auth_mode is invalid')
    CLOUD_CREDENTIAL_INVALID: ErrorTuple = (400, 1000706, 'cloud credential is invalid')
    CLOUD_TOKEN_UNAVAILABLE: ErrorTuple = (502, 1000707, 'cloud access token is unavailable')
    CLOUD_CRYPTO_UNAVAILABLE: ErrorTuple = (500, 1000708, 'cloud oauth encryption key is not configured')


@dataclass
class AppException(Exception):
    http_code: int
    code: int
    message: str
    extra: str | None = None

    def __str__(self) -> str:
        return self.message


class AuthError(AppException):
    pass


def raise_error(
    err: ErrorTuple,
    extra_msg: str | None = None,
    *,
    exc_cls: Type[AppException] = AppException,
) -> None:
    http_code, code, message = err
    raise exc_cls(http_code=http_code, code=code, message=message, extra=extra_msg)


def error_payload_from_exception(exc: AppException) -> dict[str, Any]:
    return {
        'code': exc.code,
        'message': exc.message,
        'ex_mesage': exc.extra or '',
    }
