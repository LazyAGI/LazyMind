import os
import secrets
import stat
import uuid
from pathlib import Path

from fastapi import Depends, Request
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from jose import JWTError, jwt

from core.database import SessionLocal
from core.errors import ErrorCodes, raise_error
from core.security import jwt_secret
from models import User
from repositories import UserRepository


bearer_scheme = HTTPBearer(auto_error=False)

_INTERNAL_TOKEN_HEADER = 'X-LazyMind-Internal-Token'


def _expected_internal_service_token() -> str:
    direct = (os.environ.get('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN') or '').strip()
    if direct:
        return direct
    raw_path = (
        os.environ.get('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE') or ''
    ).strip()
    path = Path(raw_path)
    if not raw_path or not path.is_absolute():
        return ''
    try:
        info = path.stat()
        docker_secret = str(path).startswith('/run/secrets/')
        if (
            not stat.S_ISREG(info.st_mode)
            or info.st_size <= 0
            or info.st_size > 4096
            or (not docker_secret and stat.S_IMODE(info.st_mode) & 0o077)
        ):
            return ''
        token = path.read_text(encoding='utf-8').strip()
    except (OSError, UnicodeError):
        return ''
    return token if 16 <= len(token) <= 4096 else ''


def require_internal_service_token(request: Request) -> None:
    """Restrict server-to-server routes; core must send matching header."""
    expected = _expected_internal_service_token()
    if not expected:
        raise_error(ErrorCodes.FORBIDDEN)
    got = (request.headers.get(_INTERNAL_TOKEN_HEADER) or '').strip()
    exp_b = expected.encode('utf-8')
    got_b = got.encode('utf-8')
    if len(got_b) != len(exp_b) or not secrets.compare_digest(got_b, exp_b):
        raise_error(ErrorCodes.UNAUTHORIZED)


def _user_id_from_token(token: str) -> uuid.UUID:
    try:
        payload = jwt.decode(token, jwt_secret(), algorithms=['HS256'])
    except JWTError:
        raise_error(ErrorCodes.UNAUTHORIZED)

    sub = payload.get('sub')
    if not sub:
        raise_error(ErrorCodes.UNAUTHORIZED)

    try:
        return uuid.UUID(sub)
    except (TypeError, ValueError):
        raise_error(ErrorCodes.UNAUTHORIZED)


def current_user_id(
    credentials: HTTPAuthorizationCredentials | None = Depends(bearer_scheme),  # noqa: B008
) -> uuid.UUID:
    if not credentials or not credentials.credentials:
        raise_error(ErrorCodes.UNAUTHORIZED)
    return _user_id_from_token(credentials.credentials)


def current_user(user_id: uuid.UUID = Depends(current_user_id)) -> User:  # noqa: B008
    with SessionLocal() as db:
        user = UserRepository.get_by_id(
            db,
            user_id,
            load_role=True,
            load_permission_groups=True,
            load_groups=True,
            load_group_permission_groups=True,
        )
    if not user:
        raise_error(ErrorCodes.UNAUTHORIZED)
    if user.disabled:
        raise_error(ErrorCodes.USER_DISABLED)
    return user


def require_admin(user: User = Depends(current_user)) -> User:  # noqa: B008
    if user.role.name != 'system-admin':
        raise_error(ErrorCodes.ADMIN_REQUIRED)
    return user
