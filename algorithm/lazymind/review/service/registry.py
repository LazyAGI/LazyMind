from __future__ import annotations

import threading

from lazymind.review.vocab import VocabManager
from lazymind.review.service.db import fetch_vocab_for_user_id

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
    return _registry[user_id]


def clear_registry() -> None:
    with _registry_lock:
        _registry.clear()
