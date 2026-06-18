"""Authorization and permission-related APIs.

- POST /api/auth/authorize: for API gateways (e.g., Kong) RBAC.
  Allows or denies based on request method/path and user permissions.
"""
import json
import logging
import os
import uuid
from pathlib import Path

from fastapi import APIRouter, Request
from jose import JWTError, jwt

from core.errors import AppException, ErrorCodes, raise_error
from core.security import jwt_secret
from core.database import SessionLocal
from repositories import UserRepository
from schemas.auth import AuthorizeBody, AuthorizeResponse


router = APIRouter(prefix='/auth', tags=['authorization'])
logger = logging.getLogger('auth-service')

BUILTIN_ADMIN_ROLE = 'system-admin'
API_PERMISSIONS_MAP: dict[tuple[str, str], list[str]] = {}


def _normalize_path(path: str) -> str:
    return path.rstrip('/') or '/'


def _path_matches_pattern(path: str, pattern: str) -> bool:
    path_segs = [s for s in path.split('/') if s]
    pattern_segs = [s for s in pattern.split('/') if s]
    if len(path_segs) != len(pattern_segs):
        return False
    for pseg, mseg in zip(path_segs, pattern_segs):
        if not mseg.startswith('{') or not mseg.endswith('}'):
            if pseg != mseg:
                return False
    return True


def _required_permissions_for(method: str, path: str) -> list[str] | None:
    key = (method, path)
    if key in API_PERMISSIONS_MAP:
        return API_PERMISSIONS_MAP[key]
    for (m, pattern), perms in API_PERMISSIONS_MAP.items():
        if m == method and _path_matches_pattern(path, pattern):
            return perms
    return None


def load_api_permissions() -> None:
    global API_PERMISSIONS_MAP
    path = os.environ.get('LAZYMIND_AUTH_API_PERMISSIONS_FILE')
    path = Path(path) if path else Path(__file__).resolve().parent.parent / 'api_permissions.json'
    if not path.exists():
        logger.warning('api_permissions.json not found at %s; RBAC authorize will allow all', path)
        API_PERMISSIONS_MAP = {}
        return
    try:
        data = json.loads(path.read_text(encoding='utf-8'))
        API_PERMISSIONS_MAP = {}
        for item in data:
            method = (item.get('method') or 'GET').upper()
            p = _normalize_path(item.get('path') or '/')
            API_PERMISSIONS_MAP[(method, p)] = list(item.get('permissions') or [])
        logger.info('Loaded %d API permission entries from %s', len(API_PERMISSIONS_MAP), path)
    except Exception as e:
        logger.exception('Failed to load api_permissions from %s: %s', path, e)
        API_PERMISSIONS_MAP = {}


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


def _token_from_request(request: Request) -> str:
    auth_header = request.headers.get('authorization') or ''
    token = auth_header.strip()
    if token.lower().startswith('bearer '):
        token = token[7:].strip()
    return token


def _authorize_user_payload(user) -> dict:
    return {
        'allowed': True,
        'user_id': str(user.id),
        'username': user.username,
        'tenant_id': user.tenant_id,
        'role': user.role.name,
    }


def _user_from_token(token: str, *, load_permissions: bool):
    user_id = _user_id_from_token(token)
    with SessionLocal() as db:
        user = UserRepository.get_by_id(
            db,
            user_id,
            load_role=True,
            load_permission_groups=load_permissions,
            load_groups=load_permissions,
            load_group_permission_groups=load_permissions,
        )
    if not user:
        raise_error(ErrorCodes.UNAUTHORIZED)
    if user.disabled:
        raise_error(ErrorCodes.USER_DISABLED)
    return user


@router.post('/authorize', response_model=AuthorizeResponse)
def authorize(body: AuthorizeBody, request: Request):
    """
    Authorization: called by gateway (Kong); determine allow/deny based on request method, path, and user Bearer token
      1. If no required permission is configured for the API, allow directly;
      2. Otherwise verify user role and permission groups; allow if admin or if any required permission is present;
      3. Otherwise return 403.
    """
    method = (body.method or 'GET').upper()
    path = _normalize_path(body.path or '/')
    required = _required_permissions_for(method, path)
    token = _token_from_request(request)
    if not token:
        if not required:
            return {'allowed': True}
        raise_error(ErrorCodes.UNAUTHORIZED)

    try:
        user = _user_from_token(token, load_permissions=bool(required))
    except AppException:
        if not required:
            return {'allowed': True}
        raise

    if not required:
        return _authorize_user_payload(user)
    if user.role.name == BUILTIN_ADMIN_ROLE:
        return _authorize_user_payload(user)
    from core.permissions import get_effective_permission_codes
    effective = get_effective_permission_codes(user)
    if effective & set(required):
        return _authorize_user_payload(user)
    raise_error(ErrorCodes.FORBIDDEN)
