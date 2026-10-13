import hashlib
import math
import os
import secrets
from collections import Counter
from datetime import datetime, timedelta, timezone
from typing import Any

from jose import jwt


_MIN_JWT_SECRET_BYTES = 32
_MIN_JWT_SECRET_ENTROPY_BITS = 128.0


def jwt_secret() -> str:
    s = os.environ.get('LAZYMIND_JWT_SECRET')
    if not s:
        raise RuntimeError('LAZYMIND_JWT_SECRET is required')
    return s


def _estimated_entropy_bits(secret: str) -> float:
    raw = secret.encode('utf-8')
    if not raw:
        return 0.0

    length = len(raw)
    counts = Counter(raw)
    entropy_per_byte = -sum(
        (count / length) * math.log2(count / length)
        for count in counts.values()
    )
    return entropy_per_byte * length


def validate_jwt_secret() -> str:
    secret = jwt_secret()
    secret_bytes = secret.encode('utf-8')
    if len(secret_bytes) < _MIN_JWT_SECRET_BYTES:
        raise RuntimeError('LAZYMIND_JWT_SECRET must be at least 32 bytes')
    if _estimated_entropy_bits(secret) < _MIN_JWT_SECRET_ENTROPY_BITS:
        raise RuntimeError('LAZYMIND_JWT_SECRET has insufficient entropy')
    return secret


def _env_int(key: str, default: int) -> int:
    try:
        return int(os.environ.get(key, str(default)))
    except ValueError:
        return default


def jwt_ttl_minutes() -> int:
    return _env_int('LAZYMIND_JWT_TTL_MINUTES', 60)


def jwt_ttl_seconds() -> int:
    return jwt_ttl_minutes() * 60


def refresh_token_ttl_days() -> int:
    return _env_int('LAZYMIND_JWT_REFRESH_TTL_DAYS', 7)


def refresh_token_ttl_seconds() -> int:
    return refresh_token_ttl_days() * 86400


def create_access_token(
    *,
    subject: str,
    role: str,
    tenant_id: str | None = None,
    username: str | None = None,
    jti: str | None = None,
) -> str:
    now = datetime.now(timezone.utc)
    exp = now + timedelta(minutes=jwt_ttl_minutes())
    payload: dict[str, Any] = {
        # Keep 'sub' for existing token verification logic in deps.py/authorization.py
        'sub': subject,
        'tenant_id': tenant_id,
        'tenant_code': 'default',
        'user_id': subject,
        'username': username,
        'user_type': role,
        # Existing fields (still useful for downstream authorization)
        'role': role,
        'iat': int(now.timestamp()),
        'exp': int(exp.timestamp()),
    }
    if jti:
        payload['jti'] = jti
    return jwt.encode(payload, jwt_secret(), algorithm='HS256')


def generate_refresh_token() -> str:
    return secrets.token_urlsafe(32)


def generate_jti() -> str:
    return secrets.token_hex(16)


def hash_refresh_token(token: str) -> str:
    return hashlib.sha256(token.encode('utf-8')).hexdigest()


def refresh_token_expires_at() -> datetime:
    return datetime.now(timezone.utc) + timedelta(days=refresh_token_ttl_days())
