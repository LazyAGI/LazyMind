from __future__ import annotations

import threading

from .vocab_db import fetch_vocab_for_user_id
from .vocab_manager import VocabManager

_registry: dict[str, VocabManager] = {}
_registry_lock = threading.Lock()


def get_vocab_manager(user_id: str = '') -> VocabManager:
    if user_id not in _registry:
        with _registry_lock:
            if user_id not in _registry:
                _registry[user_id] = VocabManager(
                    user_id=user_id,
                    data_source=lambda: fetch_vocab_for_user_id(user_id),
                )
    manager = _registry[user_id]
    manager.reload()
    return manager


def clear_vocab_registry() -> None:
    with _registry_lock:
        _registry.clear()
