from __future__ import annotations

import threading
import time
from dataclasses import dataclass

from .conversation_env import ConversationEnvLease, ConversationEnvStore


@dataclass
class PendingInput:
    store: ConversationEnvStore
    conversation_id: str
    lease: ConversationEnvLease
    name: str
    expires_at: float
    submitted: bool = False
    canceled: bool = False


class SessionEnvInputRegistry:
    """Short-lived worker affinity and idempotent receipts; never stores submitted values."""

    def __init__(self) -> None:
        self._pending: dict[str, PendingInput] = {}
        self._lock = threading.RLock()

    def register(self, ask_id: str, store: ConversationEnvStore, conversation_id: str,
                 lease: ConversationEnvLease, name: str) -> None:
        with self._lock:
            store.check_lease(conversation_id, lease)
            now = time.monotonic()
            self._pending = {
                key: item for key, item in self._pending.items()
                if item.expires_at > now and item.conversation_id != conversation_id
            }
            if len(self._pending) >= 4096:
                raise ValueError('too many pending environment inputs; retry later')
            self._pending[ask_id] = PendingInput(store, conversation_id, lease, name, now + 1800)

    def submit(self, ask_id: str, conversation_id: str, value: str) -> bool:
        with self._lock:
            item = self._pending.get(ask_id)
            if item is None or item.conversation_id != conversation_id or item.expires_at <= time.monotonic():
                return False
            item.store.check_lease(conversation_id, item.lease)
            if item.canceled:
                return False
            if not item.submitted:
                item.store.set(conversation_id, item.name, value, lease=item.lease)
                item.submitted = True
            return True

    def cancel(self, ask_id: str, conversation_id: str) -> str | None:
        with self._lock:
            item = self._pending.get(ask_id)
            if item is None or item.conversation_id != conversation_id or item.expires_at <= time.monotonic():
                return None
            item.store.check_lease(conversation_id, item.lease)
            # A response may have been lost after applying the value. Report the
            # committed outcome instead of claiming cancellation undid a write.
            if item.submitted:
                return 'configured'
            item.canceled = True
            return 'canceled'

    def owns(self, ask_id: str, conversation_id: str) -> bool:
        with self._lock:
            item = self._pending.get(ask_id)
            return bool(item and item.conversation_id == conversation_id and item.expires_at > time.monotonic())


session_env_inputs = SessionEnvInputRegistry()
