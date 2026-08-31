import base64
import hashlib
import json
import os
import tempfile
import time
from pathlib import Path

from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.hashes import SHA256
from cryptography.hazmat.primitives.kdf.hkdf import HKDF


class JsonCipher:
    _PREFIX = 'u2:'
    _LEGACY_PREFIX = 'u1:'
    _CONTEXT = b'channel-gateway:user:v1\x00'

    def __init__(
        self,
        master_key_path: str,
        *,
        key_purpose: str = 'credential',
    ):
        purpose = key_purpose.strip()
        if not purpose:
            raise ValueError('key_purpose is required')
        self._master_key = self._load_or_create_master_key(master_key_path)
        self._key_info = (
            f'channel-gateway:{purpose}-key:v2'.encode('utf-8')
        )

    @classmethod
    def _owner_context(cls, owner_user_id: str) -> bytes:
        owner = owner_user_id.strip()
        if not owner:
            raise ValueError('owner_user_id is required')
        return cls._CONTEXT + owner.encode('utf-8')

    def encrypt(self, owner_user_id: str, payload: dict) -> str:
        context = self._owner_context(owner_user_id)
        key = self._user_key(context)
        nonce = os.urandom(12)
        body = json.dumps(payload, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
        encrypted = AESGCM(key).encrypt(nonce, body, context)
        packed = base64.urlsafe_b64encode(nonce + encrypted).decode('ascii')
        return self._PREFIX + packed

    def decrypt(self, owner_user_id: str, ciphertext: str) -> dict:
        text = (ciphertext or '').strip()
        if text.startswith(self._PREFIX):
            key = self._user_key(self._owner_context(owner_user_id))
            encoded = text[len(self._PREFIX):]
        elif text.startswith(self._LEGACY_PREFIX):
            key = hashlib.sha256(self._owner_context(owner_user_id)).digest()
            encoded = text[len(self._LEGACY_PREFIX):]
        else:
            raise ValueError('unsupported ciphertext version')
        context = self._owner_context(owner_user_id)
        raw = base64.urlsafe_b64decode(encoded + '=' * (-len(encoded) % 4))
        if len(raw) < 13:
            raise ValueError('invalid ciphertext')
        plain = AESGCM(key).decrypt(raw[:12], raw[12:], context)
        value = json.loads(plain.decode('utf-8'))
        return value if isinstance(value, dict) else {}

    @classmethod
    def needs_migration(cls, ciphertext: str) -> bool:
        return (ciphertext or '').strip().startswith(cls._LEGACY_PREFIX)

    def _user_key(self, context: bytes) -> bytes:
        return HKDF(
            algorithm=SHA256(),
            length=32,
            salt=context,
            info=self._key_info,
        ).derive(self._master_key)

    @staticmethod
    def _load_or_create_master_key(value: str) -> bytes:
        path = Path(value)
        path.parent.mkdir(parents=True, exist_ok=True)
        binary_flag = getattr(os, 'O_BINARY', 0)
        try:
            descriptor = os.open(
                path,
                os.O_WRONLY | os.O_CREAT | os.O_EXCL | binary_flag,
                0o600,
            )
        except FileExistsError:
            pass
        else:
            try:
                os.write(descriptor, os.urandom(32))
            finally:
                os.close(descriptor)
        # A concurrent gateway may observe the exclusively-created file before
        # its creator has finished the 32-byte write. This can occur while
        # recovering from an interrupted installer warmup.
        key = b''
        for attempt in range(20):
            key = path.read_bytes()
            if len(key) == 32 or attempt == 19:
                break
            time.sleep(0.05)
        recovered_key = JsonCipher._recover_windows_text_mode_key(key)
        if recovered_key is not None:
            JsonCipher._replace_master_key(path, recovered_key)
            key = recovered_key
        if len(key) != 32:
            raise RuntimeError('channel gateway credential master key is invalid')
        return key

    @staticmethod
    def _recover_windows_text_mode_key(key: bytes) -> bytes | None:
        """Undo the CRLF expansion produced by a legacy Windows text-mode write."""
        if len(key) <= 32 or b'\r\n' not in key:
            return None
        recovered = key.replace(b'\r\n', b'\n')
        return recovered if len(recovered) == 32 else None

    @staticmethod
    def _replace_master_key(path: Path, key: bytes) -> None:
        descriptor, temporary_name = tempfile.mkstemp(
            prefix=f'.{path.name}.',
            dir=path.parent,
        )
        try:
            with os.fdopen(descriptor, 'wb') as temporary_file:
                temporary_file.write(key)
                temporary_file.flush()
                os.fsync(temporary_file.fileno())
            os.replace(temporary_name, path)
        except BaseException:
            try:
                os.unlink(temporary_name)
            except FileNotFoundError:
                pass
            raise
