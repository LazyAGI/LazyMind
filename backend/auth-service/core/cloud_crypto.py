import base64
import hashlib
import json
import os

from cryptography.hazmat.primitives.ciphers.aead import AESGCM


_ENV_KEY = 'LAZYMIND_AUTH_CLOUD_SECRET_KEY'
_LEGACY_DEFAULT_KEY = 'dev-ragscan-secret-key-change-me'


def _key_bytes(raw: str) -> bytes:
    if not raw:
        raise RuntimeError('LAZYMIND_AUTH_CLOUD_SECRET_KEY is required for cloud oauth credential encryption')

    # Preferred format: 32-byte key encoded with base64/urlsafe-base64.
    try:
        decoded = base64.urlsafe_b64decode(raw + '=' * (-len(raw) % 4))
        if len(decoded) == 32:
            return decoded
    except Exception:
        pass
    try:
        decoded = base64.b64decode(raw + '=' * (-len(raw) % 4))
        if len(decoded) == 32:
            return decoded
    except Exception:
        pass

    # Fallback for legacy plain text key material: derive a stable 32-byte key.
    return hashlib.sha256(raw.encode('utf-8')).digest()


def _resolve_secret_key() -> bytes:
    return _key_bytes((os.environ.get(_ENV_KEY) or '').strip())


def _aesgcm() -> AESGCM:
    return AESGCM(_resolve_secret_key())


def encrypt_json(payload: dict) -> str:
    body = json.dumps(payload or {}, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
    nonce = os.urandom(12)
    encrypted = _aesgcm().encrypt(nonce, body, None)
    packed = nonce + encrypted
    return base64.urlsafe_b64encode(packed).decode('ascii')


def decrypt_json(ciphertext: str) -> dict:
    raw = base64.urlsafe_b64decode((ciphertext or '').strip() + '=' * (-len((ciphertext or '').strip()) % 4))
    if len(raw) < 13:
        raise ValueError('invalid ciphertext')
    nonce, encrypted = raw[:12], raw[12:]
    try:
        plain = _aesgcm().decrypt(nonce, encrypted, None)
    except Exception as current_error:
        current_raw = (os.environ.get(_ENV_KEY) or '').strip()
        if current_raw == _LEGACY_DEFAULT_KEY:
            raise
        try:
            plain = AESGCM(_key_bytes(_LEGACY_DEFAULT_KEY)).decrypt(nonce, encrypted, None)
        except Exception:
            raise current_error
    decoded = json.loads(plain.decode('utf-8'))
    return decoded if isinstance(decoded, dict) else {}
