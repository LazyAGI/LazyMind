"""User business logic: called by API layer, and this module calls repositories."""
import uuid
from datetime import datetime, timezone

from core.database import SessionLocal
from core.errors import ErrorCodes, raise_error
from repositories import RoleRepository, UserRepository
from services.auth_service import auth_service


class UserService:
    """User CRUD, role assignment, password reset."""

    def _is_bootstrap_admin(self, user) -> bool:
        if not user:
            return False
        return (getattr(user, 'source', '') or '').strip() == 'init'

    def _default_user_role_id(self, db) -> uuid.UUID:
        role = RoleRepository.get_by_name(db, 'user')
        if not role:
            raise_error(ErrorCodes.DEFAULT_ROLE_NOT_FOUND)
        return role.id

    def create_user(
        self,
        username: str,
        password: str,
        role_id: uuid.UUID | None = None,
        email: str | None = None,
        tenant_id: str = '',
        disabled: bool = False,
    ) -> dict:
        """Create user (system-admin only). Default role is user. Returns user_id, username, role_id, role_name."""
        username = (username or '').strip()
        if not username:
            raise_error(ErrorCodes.USERNAME_REQUIRED)
        password = (password or '').strip()
        if not password:
            raise_error(ErrorCodes.PASSWORD_REQUIRED)
        if not auth_service.validate_password(password):
            raise_error(ErrorCodes.INVALID_PASSWORD)
        with SessionLocal() as db:
            if UserRepository.get_by_username(db, username):
                raise_error(ErrorCodes.USER_ALREADY_EXISTS)
            rid = role_id
            if rid is None:
                rid = self._default_user_role_id(db)
            else:
                role = RoleRepository.get_by_id(db, rid)
                if not role:
                    raise_error(ErrorCodes.ROLE_NOT_FOUND)
            user = UserRepository.create(
                db,
                username=username,
                password_hash=auth_service.hash_password(password),
                role_id=rid,
                tenant_id=tenant_id or '',
                email=email,
                disabled=disabled,
                source='admin',
            )
            role = RoleRepository.get_by_id(db, user.role_id)
            role_name = role.name if role else 'user'
            return {
                'user_id': str(user.id),
                'username': user.username,
                'role_id': str(user.role_id),
                'role_name': role_name,
            }

    def list_users(
        self,
        page: int = 1,
        page_size: int = 20,
        search: str | None = None,
        tenant_id: str | None = None,
        active_only: bool = False,
    ) -> tuple[list[dict], int]:
        """Paginated user list. Returns (items, total)."""
        with SessionLocal() as db:
            users, total = UserRepository.list_paginated(
                db, page, page_size, search, tenant_id, active_only=active_only
            )
            items = [
                {
                    'user_id': str(u.id),
                    'username': u.username,
                    'display_name': u.display_name or u.username,
                    'email': u.email,
                    'phone': u.phone or None,
                    'status': 'inactive' if u.disabled else 'active',
                    'tenant_id': u.tenant_id,
                    'role_id': str(u.role_id),
                    'role_name': u.role.name,
                    'is_bootstrap_admin': self._is_bootstrap_admin(u),
                }
                for u in users
            ]
            return items, int(total)

    def get_user(self, user_id: uuid.UUID) -> dict:
        """Get user detail by id."""
        with SessionLocal() as db:
            u = UserRepository.get_by_id(db, user_id, load_role=True)
            if not u:
                raise_error(ErrorCodes.USER_NOT_FOUND)
            return {
                'user_id': str(u.id),
                'username': u.username,
                'display_name': u.display_name or u.username,
                'email': u.email,
                'phone': u.phone or None,
                'remark': u.remark or '',
                'status': 'inactive' if u.disabled else 'active',
                'tenant_id': u.tenant_id,
                'role_id': str(u.role_id),
                'role_name': u.role.name if u.role else 'user',
                'is_bootstrap_admin': self._is_bootstrap_admin(u),
            }

    def set_user_role(self, user_id: uuid.UUID, role_id: uuid.UUID) -> None:
        """Update user role. Raises if user or role not found."""
        with SessionLocal() as db:
            user = UserRepository.get_by_id(db, user_id, load_role=True)
            if not user:
                raise_error(ErrorCodes.USER_NOT_FOUND)
            if self._is_bootstrap_admin(user):
                raise_error(ErrorCodes.BOOTSTRAP_ADMIN_ROLE_CHANGE_FORBIDDEN)
            role = RoleRepository.get_by_id(db, role_id)
            if not role:
                raise_error(ErrorCodes.ROLE_NOT_FOUND)
            user.role_id = role.id
            db.commit()

    def set_user_roles_batch(
        self, user_ids: list[uuid.UUID], role_id: uuid.UUID
    ) -> None:
        """Batch assign system roles directly to specified users.

        This is independent of groups. Raise an error if any user or role
        does not exist.
        """
        if not user_ids:
            return
        with SessionLocal() as db:
            role = RoleRepository.get_by_id(db, role_id)
            if not role:
                raise_error(ErrorCodes.ROLE_NOT_FOUND)
            for uid in user_ids:
                user = UserRepository.get_by_id(db, uid, load_role=True)
                if not user:
                    raise_error(ErrorCodes.USER_NOT_FOUND, extra_msg=str(uid))
                if self._is_bootstrap_admin(user):
                    raise_error(ErrorCodes.BOOTSTRAP_ADMIN_ROLE_CHANGE_FORBIDDEN, extra_msg=str(uid))
                user.role_id = role.id
            db.commit()

    def disable_user(self, user_id: uuid.UUID, disabled: bool = True) -> None:
        """Disable or enable a user. Raises if user not found."""
        with SessionLocal() as db:
            user = UserRepository.get_by_id(db, user_id)
            if not user:
                raise_error(ErrorCodes.USER_NOT_FOUND)
            user.disabled = disabled
            db.commit()

    def reset_password(self, user_id: uuid.UUID, new_password: str) -> None:
        """Reset user password. Raises if user not found or password invalid."""
        new_password = (new_password or '').strip()
        if not new_password:
            raise_error(ErrorCodes.NEW_PASSWORD_REQUIRED)
        if not auth_service.validate_password(new_password):
            raise_error(ErrorCodes.INVALID_PASSWORD)
        with SessionLocal() as db:
            user = UserRepository.get_by_id(db, user_id)
            if not user:
                raise_error(ErrorCodes.USER_NOT_FOUND)
            user.password_hash = auth_service.hash_password(new_password)
            user.updated_pwd_time = datetime.now(timezone.utc)
            db.commit()


user_service = UserService()
