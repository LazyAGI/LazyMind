import re
import uuid

from passlib.context import CryptContext
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from core.errors import AuthError, ErrorCodes, raise_error
from core.rate_limit import login_rate_limiter
from models import User
from repositories import UserRepository


pwd_context = CryptContext(schemes=['pbkdf2_sha256'], deprecated='auto')

USERNAME_PATTERN = re.compile(r'^[a-zA-Z0-9][a-zA-Z0-9._@#-]*[a-zA-Z0-9]$')
PASSWORD_MIN_LEN = 8
PASSWORD_MAX_LEN = 32
EMAIL_MAX_LEN = 30


class AuthService:
    """Authentication: validation, password hashing, registration, login."""

    def validate_username(self, username: str) -> bool:
        """Validate username: at least 2 chars, alphanumeric start/end, only letters, digits, . _ @ # - in between."""
        if not username or len(username) < 2:
            return False
        return USERNAME_PATTERN.match(username) is not None

    def validate_password(self, password: str) -> bool:
        """Validate password strength: 8~32 chars, at least one upper, lower, digit, special."""
        if not password or len(password) < PASSWORD_MIN_LEN or len(password) > PASSWORD_MAX_LEN:
            return False
        has_upper = bool(re.search(r'[A-Z]', password))
        has_lower = bool(re.search(r'[a-z]', password))
        has_digit = bool(re.search(r'\d', password))
        has_special = bool(re.search(r'[!@#$%^&*()_+\-=\[\]{}|;:\'",.<>/?`~]', password))
        return has_upper and has_lower and has_digit and has_special

    def hash_password(self, password: str) -> str:
        return pwd_context.hash(password)

    def verify_password(self, password: str, password_hash: str) -> bool:
        return pwd_context.verify(password, password_hash)

    def register_user(
        self,
        *,
        db: Session,
        username: str,
        password: str,
        role_id: uuid.UUID,
        email: str | None = None,
        tenant_id: str | None = None,
    ) -> User:
        username = (username or '').strip()
        if not username:
            raise_error(ErrorCodes.USERNAME_REQUIRED, exc_cls=AuthError)
        if not self.validate_username(username):
            raise_error(
                ErrorCodes.INVALID_USERNAME,
                extra_msg=(
                    'Username must be at least 2 characters, start/end with '
                    'alphanumeric characters, and only contain letters, '
                    'numbers, . _ @ # - in the middle'
                ),
                exc_cls=AuthError,
            )
        if email is not None:
            email = email.strip()
            if not email:
                email = None
            elif len(email) > EMAIL_MAX_LEN:
                raise_error(ErrorCodes.EMAIL_TOO_LONG, exc_cls=AuthError)
        if not password:
            raise_error(ErrorCodes.PASSWORD_REQUIRED, exc_cls=AuthError)
        if not self.validate_password(password):
            raise_error(
                ErrorCodes.INVALID_PASSWORD,
                extra_msg=(
                    'Password must be 8-32 characters and include at least one '
                    'uppercase letter, one lowercase letter, one digit, and '
                    'one special character'
                ),
                exc_cls=AuthError,
            )
        try:
            return UserRepository.create(
                db,
                username=username,
                password_hash=self.hash_password(password),
                role_id=role_id,
                tenant_id=(tenant_id or ''),
                email=email,
                display_name=username,
                disabled=False,
            )
        except IntegrityError:
            db.rollback()
            raise_error(ErrorCodes.USER_ALREADY_EXISTS, exc_cls=AuthError)

    def authenticate_user(self, *, db: Session, username: str, password: str) -> User:
        """Authenticate user with login failure rate limit (3 failures/min per account)."""
        user = UserRepository.get_by_username(db, username)
        if not user:
            raise_error(ErrorCodes.INVALID_CREDENTIALS, exc_cls=AuthError)
        if login_rate_limiter.is_limited(user.id):
            raise_error(ErrorCodes.LOGIN_LOCKED, exc_cls=AuthError)
        if not self.verify_password(password, user.password_hash):
            login_rate_limiter.record_failure(user.id)
            raise_error(ErrorCodes.INVALID_CREDENTIALS, exc_cls=AuthError)
        if user.disabled:
            raise_error(ErrorCodes.USER_DISABLED, exc_cls=AuthError)
        return user


auth_service = AuthService()
