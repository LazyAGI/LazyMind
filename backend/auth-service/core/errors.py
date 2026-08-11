from dataclasses import dataclass
from typing import Any, Tuple, Type


ErrorTuple = Tuple[int, int, str]


class ErrorCodes:
    INTERNAL_ERROR: ErrorTuple = (500, 1000000, 'Internal server error')
    INVALID_REQUEST: ErrorTuple = (400, 1000001, 'Invalid request parameters')
    RESOURCE_NOT_FOUND: ErrorTuple = (404, 1000002, 'Resource not found')
    METHOD_NOT_ALLOWED: ErrorTuple = (405, 1000003, 'Method not allowed')
    HTTP_ERROR: ErrorTuple = (500, 1000004, 'HTTP request failed')

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
    EMAIL_TOO_LONG: ErrorTuple = (400, 1000210, 'Email must not exceed 30 characters')

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

    STATE_BACKEND_AUTH_FAILED: ErrorTuple = (500, 1000601, 'State backend authentication failed')
    STATE_BACKEND_UNAVAILABLE: ErrorTuple = (500, 1000602, 'State backend is unavailable')

    CLOUD_PROVIDER_UNSUPPORTED: ErrorTuple = (400, 1000701, 'cloud provider is not supported')
    CLOUD_CONNECTION_NOT_FOUND: ErrorTuple = (404, 1000702, 'cloud auth connection not found')
    CLOUD_OAUTH_STATE_INVALID: ErrorTuple = (400, 1000703, 'oauth state is invalid or expired')
    CLOUD_OAUTH_CODE_REQUIRED: ErrorTuple = (400, 1000704, 'oauth code is required')
    CLOUD_AUTH_MODE_INVALID: ErrorTuple = (400, 1000705, 'auth_mode is invalid')
    CLOUD_CREDENTIAL_INVALID: ErrorTuple = (400, 1000706, 'cloud credential is invalid')
    CLOUD_TOKEN_UNAVAILABLE: ErrorTuple = (502, 1000707, 'cloud access token is unavailable')
    CLOUD_CRYPTO_UNAVAILABLE: ErrorTuple = (500, 1000708, 'cloud oauth encryption key is not configured')

    CLOUD_CLIENT_CREDENTIALS_REQUIRED: ErrorTuple = (400, 1000801, 'client_id and client_secret are required')
    CLOUD_APP_CREDENTIAL_NOT_CONFIGURED: ErrorTuple = (400, 1000802, 'cloud app credential is not configured')
    CLOUD_APP_CREDENTIAL_INCOMPLETE: ErrorTuple = (400, 1000803, 'cloud app credential is incomplete')
    CLOUD_REAUTHORIZE_CREDENTIAL_INCOMPLETE: ErrorTuple = (
        400,
        1000804,
        'reauthorize connection credential is incomplete',
    )
    CLOUD_REAUTHORIZE_ACCOUNT_ID_REQUIRED: ErrorTuple = (
        400,
        1000805,
        'reauthorize target provider_account_id is required',
    )
    CLOUD_CLIENT_ID_REQUIRED: ErrorTuple = (400, 1000806, 'client_id is required')
    CLOUD_CLIENT_SECRET_REQUIRED: ErrorTuple = (400, 1000807, 'client_secret is required')
    CLOUD_CLIENT_SECRET_REQUIRED_ON_CLIENT_ID_CHANGE: ErrorTuple = (
        400,
        1000808,
        'client_secret is required when client_id changes',
    )
    CLOUD_REDIRECT_URI_REQUIRED: ErrorTuple = (400, 1000809, 'redirect_uri is required')
    CLOUD_REDIRECT_URI_REQUIRED_FOR_OAUTH_USER: ErrorTuple = (400, 1000810, 'redirect_uri is required for oauth_user')
    CLOUD_REAUTHORIZED_ACCOUNT_MISMATCH: ErrorTuple = (
        409,
        1000811,
        'reauthorized account does not match target connection',
    )
    CLOUD_REAUTHORIZED_TENANT_MISMATCH: ErrorTuple = (
        409,
        1000812,
        'reauthorized tenant does not match target connection',
    )
    CLOUD_REAUTHORIZE_ACCOUNT_CHANGED: ErrorTuple = (409, 1000813, 'reauthorize target account changed')
    CLOUD_CLIENT_ID_ALREADY_EXISTS: ErrorTuple = (409, 1000814, 'This account has already been added')
    CLOUD_OAUTH_AUTHORIZE_MODE_REQUIRED: ErrorTuple = (
        400,
        1000815,
        'oauth_user connections must use oauth/authorize-url',
    )
    CLOUD_AUTHORIZE_URL_OAUTH_USER_ONLY: ErrorTuple = (400, 1000816, 'authorize-url only supports oauth_user')
    CLOUD_CALLBACK_OAUTH_USER_ONLY: ErrorTuple = (400, 1000817, 'callback only supports oauth_user')
    CLOUD_REAUTHORIZE_TARGET_OAUTH_USER_ONLY: ErrorTuple = (400, 1000818, 'reauthorize target must be oauth_user')
    CLOUD_REFRESH_TOKEN_REQUIRED: ErrorTuple = (400, 1000819, 'refresh_token is required for cloud token refresh')
    CLOUD_PROVIDER_ACCESS_TOKEN_EMPTY: ErrorTuple = (502, 1000820, 'cloud provider returned an empty access token')
    CLOUD_CREDENTIAL_ENCRYPT_FAILED: ErrorTuple = (500, 1000821, 'cloud credential encryption failed')
    CLOUD_CREDENTIAL_DECRYPT_FAILED: ErrorTuple = (500, 1000822, 'cloud credential decryption failed')
    CLOUD_PROVIDER_REFRESH_TOKEN_EMPTY: ErrorTuple = (502, 1000823, 'cloud provider returned an empty refresh token')
    GOOGLE_DRIVE_OAUTH_USER_ONLY: ErrorTuple = (400, 1000824, 'Google Drive only supports oauth_user connections')

    JWT_SECRET_REQUIRED: ErrorTuple = (500, 1000901, 'JWT signing secret is not configured')
    CLOUD_PROVIDER_HTTP_ERROR: ErrorTuple = (502, 1000902, 'cloud provider returned an HTTP error')
    CLOUD_PROVIDER_NETWORK_ERROR: ErrorTuple = (502, 1000903, 'cloud provider network request failed')
    CLOUD_PROVIDER_INVALID_JSON: ErrorTuple = (502, 1000904, 'cloud provider returned invalid JSON')
    GOOGLE_DRIVE_CODE_EXCHANGE_FAILED: ErrorTuple = (502, 1000905, 'Google Drive authorization code exchange failed')
    GOOGLE_DRIVE_TOKEN_REFRESH_FAILED: ErrorTuple = (502, 1000906, 'Google Drive token refresh failed')
    NOTION_CODE_EXCHANGE_FAILED: ErrorTuple = (502, 1000907, 'Notion authorization code exchange failed')
    NOTION_TOKEN_REFRESH_FAILED: ErrorTuple = (502, 1000908, 'Notion token refresh failed')
    FEISHU_TENANT_TOKEN_FAILED: ErrorTuple = (502, 1000909, 'Feishu tenant token request failed')
    FEISHU_USER_INFO_FAILED: ErrorTuple = (502, 1000910, 'Feishu user information request failed')
    CLOUD_CIPHERTEXT_INVALID: ErrorTuple = (500, 1000911, 'cloud credential ciphertext is invalid')


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


_EXCEPTION_PREFIXES: tuple[tuple[str, ErrorTuple], ...] = (
    ('LAZYMIND_JWT_SECRET is required', ErrorCodes.JWT_SECRET_REQUIRED),
    ('provider returned invalid json', ErrorCodes.CLOUD_PROVIDER_INVALID_JSON),
    ('provider network error', ErrorCodes.CLOUD_PROVIDER_NETWORK_ERROR),
    ('provider http error', ErrorCodes.CLOUD_PROVIDER_HTTP_ERROR),
    ('google drive code exchange failed', ErrorCodes.GOOGLE_DRIVE_CODE_EXCHANGE_FAILED),
    ('google drive token refresh failed', ErrorCodes.GOOGLE_DRIVE_TOKEN_REFRESH_FAILED),
    ('notion code exchange failed', ErrorCodes.NOTION_CODE_EXCHANGE_FAILED),
    ('notion token refresh failed', ErrorCodes.NOTION_TOKEN_REFRESH_FAILED),
    ('feishu tenant token failed', ErrorCodes.FEISHU_TENANT_TOKEN_FAILED),
    ('feishu user info failed', ErrorCodes.FEISHU_USER_INFO_FAILED),
    ('invalid ciphertext', ErrorCodes.CLOUD_CIPHERTEXT_INVALID),
    ('Google Drive only supports oauth_user connections in LazyMind', ErrorCodes.GOOGLE_DRIVE_OAUTH_USER_ONLY),
    (
        'LAZYMIND_AUTH_CLOUD_SECRET_KEY is required for cloud oauth credential encryption',
        ErrorCodes.CLOUD_CRYPTO_UNAVAILABLE,
    ),
)


def app_exception_from_exception(exc: Exception) -> AppException:
    raw_message = str(exc).strip()
    lowered = raw_message.lower()
    for prefix, err in _EXCEPTION_PREFIXES:
        normalized_prefix = prefix.lower()
        if (
            lowered == normalized_prefix
            or lowered.startswith(normalized_prefix + ':')
            or lowered.startswith(normalized_prefix + ' ')
        ):
            http_code, code, message = err
            detail = raw_message[len(prefix):].lstrip(': ').strip()
            return AppException(http_code=http_code, code=code, message=message, extra=detail or None)
    http_code, code, message = ErrorCodes.INTERNAL_ERROR
    return AppException(http_code=http_code, code=code, message=message)
