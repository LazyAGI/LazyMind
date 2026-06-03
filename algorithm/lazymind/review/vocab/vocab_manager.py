"""VocabManager: Multi-user vocabulary manager wrapping QueryEnhACProcessor with hot-reload support.

The manager itself is algorithm-only. Service-side registry / DB loading lives
under ``lazymind.review.service``.
"""
from __future__ import annotations

import threading
from typing import Callable, List, Optional, Union

from lazyllm import LOG, AutoModel, ModuleBase
from lazyllm.tools.rag.query_enh_ac import QueryEnhACProcessor


class VocabManager(ModuleBase):
    """Single-user vocabulary manager: bound to one user_id, loads vocabulary from DB, supports hot-reload.

    Args:
        user_id: User identifier.
        data_source: Optional custom data source (callable or list);
                     mainly for testing or service-layer injection.
    """

    def __init__(self, user_id: str = '', *, data_source: Optional[Callable] = None) -> None:
        super().__init__()
        self._user_id = user_id
        self._lock = threading.RLock()
        actual_source = data_source if data_source is not None else []
        self._data_source = actual_source
        self._proc = QueryEnhACProcessor(
            data_source=actual_source,
            discriminator=AutoModel(model='llm'),
        )
        LOG.info(f'[VocabManager] initialized for user_id={user_id!r}, vocab_size={self.vocab_size}')

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _load_vocab(self) -> List[dict]:
        if callable(self._data_source):
            return list(self._data_source())
        return list(self._data_source)

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
        """Hot-reload from the configured data source and rebuild the AC automaton.

        Returns:
            Total number of words in the updated vocabulary.
        """
        with self._lock:
            self._proc.update_data_source(self._load_vocab)
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
