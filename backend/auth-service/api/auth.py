import logging
import os
import re
import time
from datetime import datetime, timezone
from pathlib import Path

from alembic import command
from alembic.config import Config
from fastapi import APIRouter, Depends

from api.authorization import load_api_permissions
from bootstrap import bootstrap
from core.database import SessionLocal
from core.deps import _user_id_from_token, bearer_scheme, current_user
from core.errors import ErrorCodes, raise_error
from core.refresh_token_store import (
    delete_refresh_token,
    get_user_id_by_token,
    set_refresh_token,
)
from core.security import (
    create_access_token,
    generate_jti,
    generate_refresh_token,
    hash_refresh_token,
    jwt_ttl_seconds,
    refresh_token_expires_at,
)
from models import User
from repositories import RoleRepository, UserRepository
from schemas.auth import (
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
from services.auth_service import auth_service


router = APIRouter(prefix='/auth', tags=['auth'])
logger = logging.getLogger('uvicorn.error')

PHONE_PATTERN = re.compile(r'^\+?[0-9]{6,20}$')
MODEL_CONFIG_PATH_ENV = 'LAZYMIND_MODEL_CONFIG_PATH'


def _normalize_and_validate_phone(phone: str | None) -> str | None:
    if phone is None:
        return None
    normalized = phone.strip()
    # Empty string means clearing phone number.
    if normalized == '':
        return ''
    if not PHONE_PATTERN.match(normalized):
        raise_error(
            ErrorCodes.INVALID_PHONE_FORMAT,
            extra_msg='Phone must contain 6-20 digits and may start with +',
        )
    return normalized


def _is_dynamic_model_config() -> bool:
    return (os.getenv(MODEL_CONFIG_PATH_ENV) or '').strip() == 'dynamic'


def _default_role_id(session):
    role = RoleRepository.get_by_name(session, 'user')
    if not role:
        raise_error(ErrorCodes.DEFAULT_ROLE_NOT_FOUND)
    return role.id


def _run_alembic_upgrade() -> None:
    auth_service_root = Path(__file__).resolve().parent.parent
    alembic_ini = auth_service_root / 'alembic.ini'
    if not alembic_ini.exists():
        logger.warning('alembic.ini not found at %s; skipping migrations', alembic_ini)
        return
    config = Config(str(alembic_ini))
    config.set_main_option('script_location', str(auth_service_root / 'alembic'))

    try:
        command.upgrade(config, 'head')
        logger.info('Alembic upgrade head completed')
    except Exception as exc:
        logger.exception('Alembic upgrade failed: %s', exc)
        raise


@router.on_event('startup')
def on_startup():
    _run_alembic_upgrade()
    with SessionLocal() as db:
        bootstrap(db)
    load_api_permissions()


@router.get('/health', response_model=HealthResponse)
def health():
    return {'status': 'ok', 'timestamp': time.time()}


@router.post('/register', response_model=RegisterResponse)
def register(body: RegisterBody):
    username = (body.username or '').strip()
    password = (body.password or '').strip() if body.password else ''
    confirm = (body.confirm_password or '').strip() if body.confirm_password else ''
    if password != confirm:
        raise_error(ErrorCodes.PASSWORD_CONFIRM_MISMATCH)
    with SessionLocal() as db:
        role_id = _default_role_id(db)
        user = auth_service.register_user(
            db=db,
            username=username,
            password=password,
            role_id=role_id,
            email=body.email,
            tenant_id=body.tenant_id,
        )
        role = RoleRepository.get_by_id(db, user.role_id)
        role_name = role.name if role else 'user'

    return {
        'success': True,
        'user_id': str(user.id),
        'tenant_id': user.tenant_id,
        'role': role_name,
    }


@router.post('/login', response_model=LoginResponse)
def login(body: LoginBody):
    username = (body.username or '').strip()
    password = body.password or ''

    logger.info('[auth-service] login enter username=%r', username)
    if not username:
        raise_error(ErrorCodes.USERNAME_REQUIRED)
    if not password:
        raise_error(ErrorCodes.PASSWORD_REQUIRED)
    try:
        with SessionLocal() as db:
            user = auth_service.authenticate_user(db=db, username=username, password=password)
            user = UserRepository.get_by_id(db, user.id, load_role=True)
            if not user:
                raise_error(ErrorCodes.UNAUTHORIZED)

            user_id = user.id
            role_name = user.role.name
            access_token = create_access_token(
                subject=str(user_id),
                role=role_name,
                tenant_id=user.tenant_id or None,
                username=user.username,
                jti=generate_jti(),
            )
            refresh_token = generate_refresh_token()
            set_refresh_token(hash_refresh_token(refresh_token), user_id)
    except Exception as exc:
        logger.exception('[auth-service] login exception username=%r: %s', username, exc)
        raise

    return {
        'access_token': access_token,
        'refresh_token': refresh_token,
        'refresh_expires_at': refresh_token_expires_at().isoformat(),
        'token_type': 'bearer',
        'role': role_name,
        'expires_in': jwt_ttl_seconds(),
        'tenant_id': user.tenant_id,
    }


@router.post('/refresh', response_model=LoginResponse)
def refresh(body: RefreshBody):
    if not body.refresh_token or not body.refresh_token.strip():
        raise_error(ErrorCodes.REFRESH_TOKEN_REQUIRED)

    token_hash = hash_refresh_token(body.refresh_token.strip())
    user_id = get_user_id_by_token(token_hash)
    if user_id is None:
        raise_error(ErrorCodes.REFRESH_TOKEN_INVALID)
    delete_refresh_token(token_hash)
    with SessionLocal() as db:
        user = UserRepository.get_by_id(db, user_id, load_role=True)
        if not user:
            raise_error(ErrorCodes.UNAUTHORIZED)
        if user.disabled:
            raise_error(ErrorCodes.USER_DISABLED)
        role_name = user.role.name
        tenant_id = user.tenant_id
        username = user.username

    new_refresh_token = generate_refresh_token()
    set_refresh_token(hash_refresh_token(new_refresh_token), user_id)

    return {
        'access_token': create_access_token(
            subject=str(user_id),
            role=role_name,
            tenant_id=tenant_id or None,
            username=username,
            jti=generate_jti(),
        ),
        'refresh_token': new_refresh_token,
        'refresh_expires_at': refresh_token_expires_at().isoformat(),
        'token_type': 'bearer',
        'expires_in': jwt_ttl_seconds(),
        'role': role_name,
        'tenant_id': tenant_id,
    }


@router.post('/validate', response_model=ValidateResponse)
def validate(credentials=Depends(bearer_scheme)):  # noqa: B008
    if not credentials or credentials.credentials is None:
        raise_error(ErrorCodes.UNAUTHORIZED)

    user_id = _user_id_from_token(credentials.credentials)
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

    from core.permissions import get_effective_permission_codes

    return {
        'sub': str(user.id),
        'role': user.role.name,
        'tenant_id': user.tenant_id,
        'permissions': list(get_effective_permission_codes(user)),
    }


@router.get('/me', response_model=MeResponse)
def me(user: User = Depends(current_user)):  # noqa: B008
    from core.permissions import get_effective_permission_codes

    return {
        'user_id': str(user.id),
        'username': user.username,
        'display_name': user.display_name or user.username,
        'email': user.email,
        'remark': user.remark or '',
        'status': 'inactive' if user.disabled else 'active',
        'role': user.role.name,
        'permissions': list(get_effective_permission_codes(user)),
        'tenant_id': user.tenant_id,
        'dynamic': _is_dynamic_model_config(),
    }


@router.patch('/me', response_model=SuccessResponse)
def update_me(
    body: UpdateMeBody,
    user: User = Depends(current_user),  # noqa: B008
):
    """Update current user's profile (all fields except username)."""
    phone = _normalize_and_validate_phone(body.phone)
    with SessionLocal() as db:
        updated = UserRepository.update_profile(
            db,
            user.id,
            display_name=body.display_name,
            email=body.email,
            phone=phone,
            remark=body.remark,
        )
        if not updated:
            raise_error(ErrorCodes.USER_NOT_FOUND)
    return {'success': True}


@router.post('/change_password', response_model=SuccessResponse)
def change_password(
    body: ChangePasswordBody,
    user: User = Depends(current_user),  # noqa: B008
):
    if not auth_service.verify_password(body.old_password, user.password_hash):
        raise_error(ErrorCodes.OLD_PASSWORD_INVALID)

    new_password = (body.new_password or '').strip()
    if not new_password:
        raise_error(ErrorCodes.NEW_PASSWORD_REQUIRED)
    if body.old_password == new_password:
        raise_error(ErrorCodes.NEW_PASSWORD_SAME_AS_OLD)
    if not auth_service.validate_password(new_password):
        raise_error(ErrorCodes.INVALID_PASSWORD)
    with SessionLocal() as db:
        row = UserRepository.get_by_id(db, user.id)
        if not row:
            raise_error(ErrorCodes.USER_NOT_FOUND)
        row.password_hash = auth_service.hash_password(new_password)
        row.updated_pwd_time = datetime.now(timezone.utc)
        db.commit()
    return {'success': True}


@router.post('/logout', response_model=SuccessResponse)
def logout(
    body: LogoutBody,
    user: User = Depends(current_user),  # noqa: B008
):
    if not body.refresh_token:
        return {'success': True}

    token_hash = hash_refresh_token(body.refresh_token.strip())
    token_user_id = get_user_id_by_token(token_hash)
    if token_user_id is None or token_user_id != user.id:
        raise_error(ErrorCodes.REFRESH_TOKEN_INVALID)

    delete_refresh_token(token_hash)
    return {'success': True}
