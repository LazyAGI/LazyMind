from __future__ import annotations

import math
import time

from datetime import datetime, timezone
from functools import lru_cache
from typing import Callable, Optional

from lazyllm.tools.rag.store import SegmentStore, SegmentStoreConflictError

from lazymind.config import config as _cfg

from .models import (
    EpisodeCreateInput,
    EpisodeCreateResult,
    EpisodeRecord,
    EpisodeSearchResult,
    build_episode_idempotency_key,
)
from .ranking import episode_query_coverage, informative_query_terms, tokenize_episode_text


EPISODE_COLLECTION = 'lazymind_memory_episode_v1'


class EpisodeConflictError(RuntimeError):
    pass


def _store_config() -> dict:
    store_type = _cfg['segment_store_type']
    path = _cfg['segment_store_uri_or_path']
    if store_type == 'SQLiteStore':
        return {'type': 'SQLiteStore', 'kwargs': {'db_path': path}}
    if store_type == 'opensearch':
        return {'type': 'opensearch', 'kwargs': {
            'uris': path,
            'client_kwargs': {
                'http_compress': True,
                'use_ssl': True,
                'verify_certs': False,
                'user': _cfg['segment_store_user'],
                'password': _cfg['segment_store_password'],
            },
        }}
    raise ValueError(f'Unsupported segment store type: {store_type!r}')


class EpisodeStore:
    def __init__(
        self,
        segment_store: Optional[SegmentStore] = None,
        collection: str = EPISODE_COLLECTION,
        *,
        clock_ms: Callable[[], int] | None = None,
    ):
        self._store = segment_store or SegmentStore(_store_config())
        self.collection = collection
        self._clock_ms = clock_ms or (lambda: int(time.time() * 1000))

    @staticmethod
    def _segment(record: EpisodeRecord) -> dict:
        metadata = record.model_dump(mode='json')
        metadata.pop('id', None)
        return {
            'id': record.id,
            'content': tokenize_episode_text(record.summary),
            'metadata': metadata,
        }

    @staticmethod
    def _record(segment: dict) -> EpisodeRecord:
        metadata = dict(segment['metadata'])
        metadata['id'] = segment['id']
        return EpisodeRecord.model_validate(metadata)

    @staticmethod
    def _identity_for_record(record: EpisodeRecord) -> str:
        return build_episode_idempotency_key(
            user_id=record.user_id,
            conversation_id=record.source.conversation_id,
            task_id=record.source.task_id,
            episode_type=record.episode_type,
            summary=record.summary,
        )

    def create(self, user_id: str, item: EpisodeCreateInput) -> EpisodeCreateResult:
        normalized_user_id = str(user_id).strip()
        if not normalized_user_id:
            raise ValueError('user_id is required')
        create_input = EpisodeCreateInput.model_validate(item)
        episode_id = build_episode_idempotency_key(
            user_id=normalized_user_id,
            conversation_id=create_input.source.conversation_id,
            task_id=create_input.source.task_id,
            episode_type=create_input.episode_type,
            summary=create_input.summary,
        )
        record = EpisodeRecord(
            **create_input.model_dump(),
            id=episode_id,
            recorded_at_ms=self._clock_ms(),
            user_id=normalized_user_id,
        )
        try:
            created = self._store.create(self.collection, [self._segment(record)])
            if not created:
                raise RuntimeError('SegmentStore did not confirm Episode creation.')
            status = 'created'
        except SegmentStoreConflictError as exc:
            existing = self._store.get(
                self.collection,
                {'id': episode_id, 'user_id': normalized_user_id},
            )
            if not existing:
                raise EpisodeConflictError(
                    'Episode id already exists outside the current tenant identity.'
                ) from exc
            existing_record = self._record(existing[0])
            if self._identity_for_record(existing_record) != episode_id:
                raise EpisodeConflictError(
                    'Episode id already exists with different idempotent content.'
                ) from exc
            status = 'idempotent'
        return EpisodeCreateResult(
            status=status,
            id=episode_id,
            idempotency_key=episode_id,
        )

    def search(
        self,
        user_id: str,
        query: str,
        *,
        now_ms: Optional[int] = None,
    ) -> list[EpisodeSearchResult]:
        normalized_user_id = str(user_id).strip()
        query_terms = informative_query_terms(query)
        if not normalized_user_id or not query_terms:
            return []
        prepared = ' '.join(query_terms)
        candidates = self._store.search(
            self.collection,
            prepared,
            topk=_cfg['episode_candidate_topk'],
            filters={'user_id': normalized_user_id},
            query_fields=['content'],
            match_mode='any',
        )
        current_ms = now_ms or self._clock_ms()
        ranked: list[EpisodeSearchResult] = []
        for candidate in candidates:
            bm25_score = float(candidate.get('score', 0) or 0)
            if bm25_score <= 0:
                continue
            record = self._record(candidate)
            coverage = episode_query_coverage(query, record.summary)
            if coverage is None:
                continue
            age_days = max(0.0, (current_ms - record.occurred_at_ms) / 86_400_000)
            half_life_days = max(float(_cfg['episode_half_life_days']), 0.000001)
            recency = 2 ** (-age_days / half_life_days)
            saturation = max(int(_cfg['episode_hit_saturation']), 1)
            usage = min(math.log1p(record.hit_count) / math.log1p(saturation), 1.0)
            final_score = (
                float(_cfg['episode_relevance_weight']) * coverage
                + float(_cfg['episode_recency_weight']) * recency
                + float(_cfg['episode_hit_weight']) * usage
            )
            ranked.append(EpisodeSearchResult(
                episode=record,
                bm25_score=bm25_score,
                score=final_score,
                rendered=self.render(record),
            ))
        ranked.sort(key=lambda value: (
            -value.score,
            -value.bm25_score,
            -value.episode.occurred_at_ms,
            value.episode.id,
        ))
        return ranked

    @staticmethod
    def render(record: EpisodeRecord) -> str:
        occurred = datetime.fromtimestamp(
            record.occurred_at_ms / 1000,
            tz=timezone.utc,
        ).isoformat()
        return (
            f'- occurred_at: {occurred}\n'
            f'  type: {record.episode_type.value}\n'
            f'  summary: {record.summary}'
        )

    def increment_hits(self, user_id: str, episode_ids: list[str]) -> dict[str, bool]:
        result: dict[str, bool] = {}
        for episode_id in dict.fromkeys(episode_ids):
            result[episode_id] = self._store.patch(
                self.collection,
                {'id': episode_id, 'user_id': user_id},
                inc_fields={'hit_count': 1},
            ) == 1
        return result

    def reset_episode(self, user_id: str) -> bool:
        if not str(user_id).strip():
            raise ValueError('user_id is required')
        return self._store.delete(self.collection, {'user_id': str(user_id).strip()})


@lru_cache(maxsize=1)
def get_episode_store() -> EpisodeStore:
    return EpisodeStore()
