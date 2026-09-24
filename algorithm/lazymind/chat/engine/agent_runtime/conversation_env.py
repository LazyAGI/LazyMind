from __future__ import annotations

import threading
from typing import MutableMapping
from weakref import WeakValueDictionary

from .env_policy import validate_env_name


class ConversationEnvLease:
    """Identity of an active conversation generation, invalidated by cleanup."""


class StaleConversationEnvError(ValueError):
    pass


class ConversationEnvStore:
    """Ephemeral, process-local conversation credentials; never persisted to DB."""

    def __init__(self, backing: MutableMapping[str, dict[str, str]] | None = None) -> None:
        self._backing = backing if backing is not None else {}
        self._lock = threading.RLock()
        self._leases: WeakValueDictionary[str, ConversationEnvLease] = WeakValueDictionary()

    def snapshot(self, conversation_id: str) -> tuple[dict[str, str], ConversationEnvLease]:
        key = str(conversation_id or '').strip()
        with self._lock:
            lease = self._leases.get(key)
            if lease is None:
                lease = ConversationEnvLease()
                self._leases[key] = lease
            return dict(self._backing.get(key) or {}), lease

    def get_many(self, conversation_id: str, names: list[str] | None = None) -> dict[str, str]:
        key = str(conversation_id or '').strip()
        if not key:
            return {}
        with self._lock:
            scoped = dict(self._backing.get(key) or {})
        if names is None:
            return scoped
        requested = {validate_env_name(name) for name in names}
        return {name: value for name, value in scoped.items() if name in requested}

    def set(self, conversation_id: str, name: str, value: str, *, lease: ConversationEnvLease | None = None) -> None:
        key = str(conversation_id or '').strip()
        if not key:
            raise ValueError('conversation id is required to store session env')
        env_name = validate_env_name(name)
        if not isinstance(value, str) or not value.strip() or '\0' in value or value.strip() == '<redacted>':
            raise ValueError('env value must not be empty, masked, or contain NUL')
        with self._lock:
            if lease is not None and self._leases.get(key) is not lease:
                raise StaleConversationEnvError('conversation environment was cleared; start a new turn')
            scoped = self._backing.setdefault(key, {})
            scoped[env_name] = value

    def clear(self, conversation_id: str) -> bool:
        key = str(conversation_id or '').strip()
        if not key:
            return False
        with self._lock:
            self._leases.pop(key, None)
            return self._backing.pop(key, None) is not None

    def check_lease(self, conversation_id: str, lease: ConversationEnvLease) -> None:
        with self._lock:
            if not conversation_id or self._leases.get(conversation_id) is not lease:
                raise StaleConversationEnvError('conversation environment was cleared; start a new turn')

    def remove(self, conversation_id: str, name: str, *, lease: ConversationEnvLease) -> bool:
        key = str(conversation_id or '').strip()
        env_name = validate_env_name(name)
        with self._lock:
            if not key or self._leases.get(key) is not lease:
                raise StaleConversationEnvError('conversation environment was cleared; start a new turn')
            scoped = self._backing.get(key, {})
            existed = env_name in scoped
            scoped.pop(env_name, None)
            if not scoped:
                self._backing.pop(key, None)
            return existed
