from __future__ import annotations

import hashlib
import json
import unicodedata

from enum import Enum
from typing import Literal

from pydantic import AliasChoices, BaseModel, ConfigDict, Field, field_validator


class EpisodeType(str, Enum):
    DECISION = 'decision'
    PROGRESS = 'progress'
    RESULT = 'result'
    BLOCKER = 'blocker'
    EVENT = 'event'


class EpisodeSource(BaseModel):
    model_config = ConfigDict(extra='forbid')

    kind: Literal['chat_explicit', 'memory_review']
    conversation_id: str

    @field_validator('conversation_id')
    @classmethod
    def _required_context(cls, value: str) -> str:
        normalized = str(value).strip()
        if not normalized:
            raise ValueError('must not be blank')
        return normalized


class EpisodeCreateInput(BaseModel):
    """Internal create contract; agent-visible fields are supplied by MemoryTools."""

    model_config = ConfigDict(extra='forbid', populate_by_name=True)

    occurred_at_ms: int
    episode_type: EpisodeType = Field(
        validation_alias=AliasChoices('episode_type', 'type'),
        serialization_alias='type',
    )
    summary: str
    source: EpisodeSource

    @field_validator('summary')
    @classmethod
    def _valid_summary(cls, value: str) -> str:
        normalized = str(value).strip()
        if not normalized:
            raise ValueError('must not be blank')
        if len(normalized) > 200:
            raise ValueError('must be at most 200 characters')
        return normalized

    @field_validator('occurred_at_ms')
    @classmethod
    def _valid_timestamp(cls, value: int) -> int:
        if isinstance(value, bool) or value <= 0:
            raise ValueError('must be a positive Unix timestamp in milliseconds')
        return value


class EpisodeRecord(EpisodeCreateInput):
    id: str
    recorded_at_ms: int
    user_id: str
    hit_count: int = 0


class EpisodeCreateResult(BaseModel):
    status: Literal['created', 'idempotent']
    id: str


class EpisodeDeleteResult(BaseModel):
    status: Literal['deleted', 'not_found']
    id: str


class EpisodeSearchResult(BaseModel):
    episode: EpisodeRecord
    lexical_score: float
    score: float
    rendered: str


def normalize_episode_summary(summary: str) -> str:
    """Return the canonical text used only for Episode identity."""

    normalized = unicodedata.normalize('NFKC', str(summary))
    return ' '.join(normalized.split()).casefold()


def build_episode_retry_fingerprint(
    *,
    user_id: str,
    conversation_id: str,
    summary: str,
) -> str:
    identity = {
        'user_id': str(user_id).strip(),
        'conversation_id': str(conversation_id).strip(),
        'summary': normalize_episode_summary(summary),
    }
    raw = json.dumps(identity, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    return 'episode_retry_' + hashlib.sha256(raw.encode('utf-8')).hexdigest()[:32]
