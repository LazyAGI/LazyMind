from __future__ import annotations

import hashlib
import json
import math
import re
import time
import unicodedata

from datetime import datetime, timezone
from enum import Enum
from functools import lru_cache
from typing import Any, Literal, Optional

import jieba
from pydantic import BaseModel, ConfigDict, Field, field_validator

from lazyllm.tools.rag.store import SegmentStore, SegmentStoreConflictError

from lazymind.config import config as _cfg


EPISODE_COLLECTION = 'lazymind_memory_episode_v1'
_ASCII_OR_CJK = re.compile(r'[\w\u3400-\u9fff]+', re.UNICODE)


class EpisodeType(str, Enum):
    DECISION = 'decision'
    PROGRESS = 'progress'
    RESULT = 'result'
    BLOCKER = 'blocker'
    EVENT = 'event'


class EpisodeSource(BaseModel):
    model_config = ConfigDict(extra='forbid')
    kind: Literal['chat_explicit', 'memory_review']
    task_id: Optional[str] = None
    conversation_id: Optional[str] = None
    message_ids: list[str] = Field(default_factory=list)


class EpisodeCreateInput(BaseModel):
    model_config = ConfigDict(extra='forbid')
    id: Optional[str] = None
    occurred_at_ms: int
    thread_key: str
    episode_type: EpisodeType
    summary: str
    source: EpisodeSource

    @field_validator('thread_key', 'summary')
    @classmethod
    def _not_blank(cls, value: str) -> str:
        value = value.strip()
        if not value:
            raise ValueError('must not be blank')
        return value

    @field_validator('occurred_at_ms')
    @classmethod
    def _valid_timestamp(cls, value: int) -> int:
        if value <= 0:
            raise ValueError('must be a positive Unix timestamp in milliseconds')
        return value


class EpisodeRecord(EpisodeCreateInput):
    id: str
    schema_version: int = 1
    recorded_at_ms: int
    user_id: str
    hit_count: int = 0


class EpisodeCreateItemResult(BaseModel):
    status: Literal['created', 'idempotent', 'rejected']
    id: Optional[str] = None
    reason: Optional[str] = None


class EpisodeSearchResult(BaseModel):
    episode: EpisodeRecord
    bm25_score: float
    score: float
    rendered: str


def tokenize_episode_text(text: str) -> str:
    normalized = unicodedata.normalize('NFKC', str(text)).lower()
    pieces = []
    for block in _ASCII_OR_CJK.findall(normalized):
        if any('\u3400' <= char <= '\u9fff' for char in block):
            pieces.extend(token.strip() for token in jieba.cut_for_search(block) if token.strip())
        else:
            pieces.append(block)
    return ' '.join(pieces)


def _canonical_create(user_id: str, item: EpisodeCreateInput) -> dict[str, Any]:
    payload = item.model_dump(mode='json', exclude={'id'})
    payload['user_id'] = user_id
    payload['source']['message_ids'] = sorted(set(payload['source'].get('message_ids') or []))
    return payload


def _episode_id(payload: dict[str, Any]) -> str:
    raw = json.dumps(payload, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    return 'ep_' + hashlib.sha256(raw.encode()).hexdigest()[:32]


def _store_config() -> dict[str, Any]:
    store_type = _cfg['segment_store_type']
    path = _cfg['segment_store_uri_or_path']
    if store_type == 'SQLiteStore':
        return {'type': 'SQLiteStore', 'kwargs': {'db_path': path}}
    if store_type == 'opensearch':
        return {'type': 'opensearch', 'kwargs': {
            'uris': path,
            'client_kwargs': {
                'http_compress': True, 'use_ssl': True, 'verify_certs': False,
                'user': _cfg['segment_store_user'], 'password': _cfg['segment_store_password'],
            },
        }}
    raise ValueError(f'Unsupported segment store type: {store_type!r}')


class EpisodeStore:
    def __init__(self, segment_store: Optional[SegmentStore] = None,
                 collection: str = EPISODE_COLLECTION):
        self._store = segment_store or SegmentStore(_store_config())
        self.collection = collection

    @staticmethod
    def _segment(record: EpisodeRecord) -> dict[str, Any]:
        metadata = record.model_dump(mode='json')
        metadata.pop('id', None)
        return {'id': record.id, 'content': tokenize_episode_text(record.summary), 'metadata': metadata}

    @staticmethod
    def _record(segment: dict[str, Any]) -> EpisodeRecord:
        metadata = dict(segment['metadata'])
        metadata['id'] = segment['id']
        return EpisodeRecord.model_validate(metadata)

    @staticmethod
    def _immutable(record: EpisodeRecord) -> dict[str, Any]:
        return record.model_dump(mode='json', exclude={'recorded_at_ms', 'hit_count'})

    def create_many(self, user_id: str, items: list[EpisodeCreateInput]) -> list[EpisodeCreateItemResult]:
        if not str(user_id).strip():
            return [EpisodeCreateItemResult(status='rejected', reason='user_id is required') for _ in items]
        results = []
        batch_records: dict[str, EpisodeRecord] = {}
        for item in items:
            payload = _canonical_create(user_id, item)
            episode_id = item.id or _episode_id(payload)
            record = EpisodeRecord(**payload, id=episode_id, recorded_at_ms=int(time.time() * 1000))
            if episode_id in batch_records:
                if self._immutable(batch_records[episode_id]) == self._immutable(record):
                    results.append(EpisodeCreateItemResult(status='idempotent', id=episode_id))
                else:
                    results.append(EpisodeCreateItemResult(
                        status='rejected', id=episode_id,
                        reason='episode id is duplicated in batch with different immutable content',
                    ))
                continue
            batch_records[episode_id] = record
            try:
                self._store.create(self.collection, [self._segment(record)])
                results.append(EpisodeCreateItemResult(status='created', id=episode_id))
            except SegmentStoreConflictError:
                existing = self._store.get(self.collection, {'id': episode_id, 'user_id': user_id})
                if existing and self._immutable(self._record(existing[0])) == self._immutable(record):
                    results.append(EpisodeCreateItemResult(status='idempotent', id=episode_id))
                else:
                    results.append(EpisodeCreateItemResult(
                        status='rejected', id=episode_id,
                        reason='episode id already exists with different immutable content',
                    ))
            except Exception as exc:
                results.append(EpisodeCreateItemResult(status='rejected', id=episode_id, reason=str(exc)))
        return results

    def search(self, user_id: str, query: str, *, now_ms: Optional[int] = None) -> list[EpisodeSearchResult]:
        prepared = tokenize_episode_text(query)
        if not user_id or not prepared:
            return []
        candidates = self._store.search(
            self.collection, prepared, topk=_cfg['episode_candidate_topk'], filters={'user_id': user_id},
        )
        candidates = [item for item in candidates if float(item.get('score', 0) or 0) > 0]
        if not candidates:
            return []
        maximum = max(float(item['score']) for item in candidates)
        now_ms = now_ms or int(time.time() * 1000)
        ranked = []
        for item in candidates:
            record = self._record(item)
            relevance = float(item['score']) / maximum if maximum > 0 else 0
            age_days = max(0.0, (now_ms - record.occurred_at_ms) / 86_400_000)
            recency = 2 ** (-age_days / _cfg['episode_half_life_days'])
            usage = min(math.log1p(record.hit_count) / math.log1p(_cfg['episode_hit_saturation']), 1.0)
            final = (_cfg['episode_relevance_weight'] * relevance
                     + _cfg['episode_recency_weight'] * recency
                     + _cfg['episode_hit_weight'] * usage)
            if final >= _cfg['episode_score_threshold']:
                rendered = self.render(record)
                ranked.append(EpisodeSearchResult(
                    episode=record, bm25_score=float(item['score']), score=final, rendered=rendered,
                ))
        ranked.sort(key=lambda value: (-value.score, -value.episode.occurred_at_ms, value.episode.id))
        selected, used = [], 0
        for result in ranked:
            if len(selected) >= _cfg['episode_inject_topk']:
                break
            length = len(result.rendered)
            if used + length > _cfg['episode_context_max_chars']:
                continue
            selected.append(result)
            used += length
        return selected

    @staticmethod
    def render(record: EpisodeRecord) -> str:
        occurred = datetime.fromtimestamp(record.occurred_at_ms / 1000, tz=timezone.utc).isoformat()
        return (f'- occurred_at: {occurred}\n  type: {record.episode_type.value}\n'
                f'  thread: {record.thread_key}\n  summary: {record.summary}')

    def increment_hits(self, user_id: str, episode_ids: list[str]) -> dict[str, bool]:
        result = {}
        for episode_id in dict.fromkeys(episode_ids):
            result[episode_id] = self._store.patch(
                self.collection, {'id': episode_id, 'user_id': user_id}, inc_fields={'hit_count': 1},
            ) == 1
        return result

    def reset_episode(self, user_id: str) -> bool:
        if not user_id:
            raise ValueError('user_id is required')
        return self._store.delete(self.collection, {'user_id': user_id})


@lru_cache(maxsize=1)
def get_episode_store() -> EpisodeStore:
    return EpisodeStore()
