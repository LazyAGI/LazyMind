"""Token persistence: save / load / clear credentials."""

import json
import os
import tempfile
import time
from typing import Any, Dict, Optional

from cli.config import CREDENTIALS_DIR, CREDENTIALS_FILE


def _ensure_dir() -> None:
    CREDENTIALS_DIR.mkdir(parents=True, exist_ok=True)
    # Credentials are per-user secrets; tighten dir permissions so
    # other users on multi-tenant hosts can't enumerate our tokens.
    try:
        CREDENTIALS_DIR.chmod(0o700)
    except OSError:
        pass


def save(data: Dict[str, Any]) -> None:
    """Persist login tokens to disk."""
    _ensure_dir()
    # Copy so we don't mutate the caller's dict with our bookkeeping field.
    to_write = {**data, 'saved_at': time.time()}
    payload = json.dumps(to_write, indent=2, ensure_ascii=False) + '\n'
    fd, temporary = tempfile.mkstemp(
        prefix=f'{CREDENTIALS_FILE.name}.', suffix='.tmp',
        dir=str(CREDENTIALS_DIR),
    )
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, 'w', encoding='utf-8') as handle:
            fd = -1
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, CREDENTIALS_FILE)
        CREDENTIALS_FILE.chmod(0o600)
    finally:
        if fd >= 0:
            os.close(fd)
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def load() -> Optional[Dict[str, Any]]:
    """Return stored credentials or *None* if not logged in."""
    if not CREDENTIALS_FILE.exists():
        return None
    try:
        data = json.loads(CREDENTIALS_FILE.read_text(encoding='utf-8'))
    except (json.JSONDecodeError, OSError):
        return None
    if not isinstance(data, dict) or 'access_token' not in data:
        return None
    return data


def clear() -> None:
    """Remove stored credentials."""
    CREDENTIALS_FILE.unlink(missing_ok=True)


def access_token() -> Optional[str]:
    """Return the current access token, or *None*."""
    creds = load()
    if creds is None:
        return None
    return creds.get('access_token')


def refresh_token() -> Optional[str]:
    """Return the current refresh token, or *None*."""
    creds = load()
    if creds is None:
        return None
    return creds.get('refresh_token')


def server_url() -> Optional[str]:
    """Return the server URL from stored credentials, or *None*."""
    creds = load()
    if creds is None:
        return None
    return creds.get('server_url')


def is_token_expired() -> bool:
    """Heuristic check: is the access token likely expired?"""
    creds = load()
    if creds is None:
        return True
    saved_at = creds.get('saved_at', 0)
    expires_in = creds.get('expires_in', 0)
    if not saved_at or not expires_in:
        return False  # can't tell, assume valid
    # consider expired 60s before actual expiry
    return time.time() > saved_at + expires_in - 60
