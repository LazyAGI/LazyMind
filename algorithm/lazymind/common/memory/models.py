from __future__ import annotations

import hashlib
import json
import unicodedata

from enum import Enum
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


class EpisodeType(str, Enum):
    DECISION = 'decision'
    PROGRESS = 'progress'
    RESULT = 'result'
    BLOCKER = 'blocker'
    EVENT = 'event'


class EpisodeSource(BaseModel):
    model_config = ConfigDict(extra='forbid')

    kind: Literal['chat_explicit', 'memory_review']
    task_id: str
    conversation_id: str
    message_ids: list[str] = Field(default_factory=list)

    @field_validator('task_id', 'conversation_id')
    @classmethod
    def _required_context(cls, value: str) -> str:
        normalized = str(value).strip()
        if not normalized:
            raise ValueError('must not be blank')
        return normalized

    @field_validator('message_ids')
    @classmethod
    def _stable_message_ids(cls, values: list[str]) -> list[str]:
        return sorted({str(value).strip() for value in values if str(value).strip()})


class EpisodeCreateInput(BaseModel):
    """Internal create contract; agent-visible fields are supplied by MemoryTools."""

    model_config = ConfigDict(extra='forbid')

    occurred_at_ms: int
    thread_key: str
    episode_type: EpisodeType
    summary: str
    source: EpisodeSource

    @field_validator('thread_key', 'summary')
    @classmethod
    def _not_blank(cls, value: str) -> str:
        normalized = str(value).strip()
        if not normalized:
            raise ValueError('must not be blank')
        return normalized

    @field_validator('occurred_at_ms')
    @classmethod
    def _valid_timestamp(cls, value: int) -> int:
        if isinstance(value, bool) or value <= 0:
            raise ValueError('must be a positive Unix timestamp in milliseconds')
        return value

    @model_validator(mode='after')
    def _thread_tracks_source_conversation(self) -> 'EpisodeCreateInput':
        if self.thread_key != self.source.conversation_id:
            raise ValueError('thread_key must equal source.conversation_id')
        return self


class EpisodeRecord(EpisodeCreateInput):
    id: str
    schema_version: int = 1
    recorded_at_ms: int
    user_id: str
    hit_count: int = 0


class EpisodeCreateResult(BaseModel):
    status: Literal['created', 'idempotent']
    id: str
    idempotency_key: str


class EpisodeSearchResult(BaseModel):
    episode: EpisodeRecord
    bm25_score: float
    score: float
    rendered: str


def normalize_episode_summary(summary: str) -> str:
    """Return the canonical text used only for Episode identity."""

    normalized = unicodedata.normalize('NFKC', str(summary))
    return ' '.join(normalized.split()).casefold()


def build_episode_idempotency_key(
    *,
    user_id: str,
    conversation_id: str,
    task_id: str,
    episode_type: EpisodeType | str,
    summary: str,
) -> str:
    type_value = episode_type.value if isinstance(episode_type, EpisodeType) else str(episode_type)
    identity = {
        'user_id': str(user_id).strip(),
        'conversation_id': str(conversation_id).strip(),
        'task_id': str(task_id).strip(),
        'episode_type': type_value.strip().casefold(),
        'summary': normalize_episode_summary(summary),
    }
    raw = json.dumps(identity, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    return 'ep_' + hashlib.sha256(raw.encode('utf-8')).hexdigest()[:32]
