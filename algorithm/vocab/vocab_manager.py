"""VocabManager: Multi-user vocabulary manager wrapping QueryEnhACProcessor with hot-reload support.

Each user (user_id) maintains an independent QueryEnhACProcessor instance.
Vocabulary data is queried from the backend-managed PostgreSQL core.public.words table
by user_id.

Usage:
    # Backend notifies the algorithm service to hot-reload a user's vocabulary
    get_vocab_manager('user_001').reload()

    # Enhance a query with the vocabulary before retrieval (used in pipeline)
    enhanced = get_vocab_manager('user_001')('user query text')

Environment variables:
    LAZYMIND_CORE_DATABASE_URL / LAZYMIND_ACL_DB_DSN  core database connection
    LAZYMIND_DATABASE_URL                             fallback connection
"""
from __future__ import annotations

import threading
from typing import Callable, List, Optional, Union

from lazyllm import LOG, AutoModel, ModuleBase
from lazyllm.tools.rag.query_enh_ac import QueryEnhACProcessor

from .db import fetch_vocab_for_user_id


class VocabManager(ModuleBase):
    """Single-user vocabulary manager: bound to one user_id, loads vocabulary from DB, supports hot-reload.

    Args:
        user_id: User identifier.
        data_source: Optional custom data source (callable or list);
                     mainly for testing; omit to load from the database.
    """

    def __init__(self, user_id: str = '', *, data_source: Optional[Callable] = None) -> None:
        super().__init__()
        self._user_id = user_id
        self._lock = threading.RLock()
        actual_source = data_source if data_source is not None else self._load_from_db
        self._proc = QueryEnhACProcessor(
            data_source=actual_source,
            discriminator=AutoModel(model='llm'),
        )
        LOG.info(f'[VocabManager] initialized for user_id={user_id!r}, vocab_size={self.vocab_size}')

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _load_from_db(self) -> List[dict]:
        """Load vocabulary rows for the current user from core.public.words;
        field format matches QueryEnhACProcessor."""
        return fetch_vocab_for_user_id(self._user_id)

    def _enhance_query(self, query: Union[str, List]) -> Union[str, List]:
        try:
            enhanced_query = self._proc(query)
        except Exception as exc:
            LOG.error(
                f'[VocabManager] user_id={self._user_id} '
                f'query_before={query} enhance_failed error={exc}'
            )
            return query

        if query != enhanced_query:
            LOG.info(
                f'[VocabManager] user_id={self._user_id} '
                f'query_before={query} query_after={enhanced_query}'
            )
        return enhanced_query

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def reload(self) -> int:
        """Hot-reload: re-query vocabulary from the database and rebuild the AC automaton.

        Returns:
            Total number of words in the updated vocabulary.
        """
        with self._lock:
            self._proc.update_data_source(self._load_from_db)
            count = len(self._proc.word_to_cluster)
            LOG.info(f'[VocabManager] reloaded for user_id={self._user_id!r}, vocab_size={count}')
            return count

    def forward(self, query: Union[str, List]) -> Union[str, List]:
        """Enhance the query using the vocabulary and return;
        returns as-is when vocabulary is empty, no match survives filtering, or enhancement fails."""
        with self._lock:
            return self._enhance_query(query)

    @property
    def vocab_size(self) -> int:
        """Number of words currently loaded."""
        with self._lock:
            return len(self._proc.word_to_cluster)

    @property
    def user_id(self) -> str:
        return self._user_id


# ---------------------------------------------------------------------------
# Multi-user registry (replaces the original module-level singleton)
# ---------------------------------------------------------------------------

_registry: dict = {}
_registry_lock = threading.Lock()


def get_vocab_manager(user_id: str = '') -> VocabManager:
    """Return the VocabManager for the given user_id (lazy init, one instance per user_id).

    Args:
        user_id: User identifier.
                 Pass an empty string to get the default manager with no user filter (vocabulary is usually empty).
    """
    if user_id not in _registry:
        with _registry_lock:
            if user_id not in _registry:
                _registry[user_id] = VocabManager(user_id)
    return _registry[user_id]


def clear_registry() -> None:
    """Clear the registry (for testing only, to ensure isolation between test cases)."""
    with _registry_lock:
        _registry.clear()
