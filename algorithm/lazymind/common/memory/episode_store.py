from __future__ import annotations

import math
import time

from datetime import datetime, timezone
from functools import lru_cache
from typing import Any, Callable, Optional

import requests

from lazymind.config import config as _cfg

from .models import (
    EpisodeCreateInput,
    EpisodeCreateResult,
    EpisodeDeleteResult,
    EpisodeRecord,
    EpisodeSearchResult,
    EpisodeType,
)
from .ranking import episode_query_coverage, informative_query_terms, tokenize_episode_text


EPISODE_TOKENIZER_VERSION = 'jieba-v1'
_EPISODE_INTERNAL_PATH = '/internal/memory/episodes'


class EpisodeReadError(RuntimeError):
    code: str
    retryable: bool

    @classmethod
    def from_exception(cls, exc: Exception) -> EpisodeReadError:
        retryable = (
            isinstance(
                exc,
                (
                    ConnectionError,
                    TimeoutError,
                    requests.exceptions.ConnectionError,
                    requests.exceptions.Timeout,
                ),
            )
            or getattr(exc, 'retryable', False) is True
        )
        error = cls('Failed to load existing Episodes.')
        error.retryable = retryable
        error.code = 'storage_unavailable' if retryable else 'storage_read_failed'
        return error


class _EpisodeHTTPError(RuntimeError):
    def __init__(self, status_code: int, message: str):
        super().__init__(status_code, message)
        self.status_code = status_code
        self.message = message
        self.retryable = status_code == 408 or status_code >= 500

    def __str__(self) -> str:
        return self.message


def _default_transport(method: str, url: str, **kwargs: Any) -> requests.Response:
    with requests.sessions.Session() as session:
        session.trust_env = False
        return session.request(method, url, **kwargs)


class EpisodeStore:
    def __init__(
        self,
        *,
        transport: Callable[..., Any] | None = None,
        base_url: str | None = None,
        internal_token: str | None = None,
        timeout: float | None = None,
        clock_ms: Callable[[], int] | None = None,
    ):
        self._base_url = (
            str(base_url).strip()
            if base_url is not None
            else str(_cfg['core_api_url'] or '').strip()
        ).rstrip('/')
        if not self._base_url:
            raise ValueError('LAZYMIND_CORE_API_URL is required for Episode storage')
        self._internal_token = (
            str(internal_token).strip()
            if internal_token is not None
            else str(_cfg['core_internal_token'] or '').strip()
        )
        if not self._internal_token:
            raise ValueError(
                'LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN is required for Episode storage'
            )
        self._timeout = (
            float(timeout)
            if timeout is not None
            else float(_cfg['core_api_timeout'])
        )
        self._transport = transport or _default_transport
        self._clock_ms = clock_ms or (lambda: int(time.time() * 1000))

    def _request(self, method: str, path: str, **kwargs: Any) -> dict[str, Any]:
        from lazymind.common.maintenance import identity_headers, check_response

        maintenance_headers = identity_headers()
        headers = dict(kwargs.pop('headers', {}) or {})
        headers['X-LazyMind-Internal-Token'] = self._internal_token
        headers.update(maintenance_headers)
        response = self._transport(
            method,
            f'{self._base_url}{path}',
            headers=headers,
            timeout=self._timeout,
            **kwargs,
        )
        check_response(response)
        status_code = int(getattr(response, 'status_code', 0) or 0)
        try:
            payload = response.json()
        except (TypeError, ValueError) as exc:
            if not 200 <= status_code < 300:
                raise _EpisodeHTTPError(
                    status_code,
                    f'Episode Core request failed with HTTP {status_code}',
                ) from exc
            raise RuntimeError('Episode Core returned invalid JSON') from exc
        message = str(payload.get('message') or '').strip() if isinstance(payload, dict) else ''
        code = payload.get('code') if isinstance(payload, dict) else None
        if not 200 <= status_code < 300 or code != 0:
            raise _EpisodeHTTPError(
                status_code,
                message or f'Episode Core request failed with HTTP {status_code}',
            )
        data = payload.get('data')
        if not isinstance(data, dict):
            raise RuntimeError('Episode Core returned invalid data')
        return data

    @staticmethod
    def _record(payload: dict[str, Any]) -> EpisodeRecord:
        return EpisodeRecord.model_validate({
            'id': payload.get('id'),
            'user_id': payload.get('user_id'),
            'occurred_at_ms': payload.get('occurred_at_ms'),
            'recorded_at_ms': payload.get('recorded_at_ms'),
            'episode_type': payload.get('episode_type'),
            'summary': payload.get('summary'),
            'source': {
                'kind': payload.get('source_kind'),
                'conversation_id': payload.get('conversation_id'),
            },
            'hit_count': payload.get('hit_count', 0),
        })

    def create(self, user_id: str, item: EpisodeCreateInput) -> EpisodeCreateResult:
        normalized_user_id = str(user_id).strip()
        if not normalized_user_id:
            raise ValueError('user_id is required')
        create_input = EpisodeCreateInput.model_validate(item)
        data = self._request('POST', _EPISODE_INTERNAL_PATH, json={
            'user_id': normalized_user_id,
            'conversation_id': create_input.source.conversation_id,
            'source_kind': create_input.source.kind,
            'episode_type': create_input.episode_type.value,
            'summary': create_input.summary,
            'search_text': tokenize_episode_text(create_input.summary),
            'tokenizer_version': EPISODE_TOKENIZER_VERSION,
            'occurred_at_ms': create_input.occurred_at_ms,
        })
        status = data.get('status')
        episode_id = str(data.get('id') or '').strip()
        if status not in {'created', 'idempotent'} or not episode_id:
            raise RuntimeError('Episode Core returned an invalid create result')
        return EpisodeCreateResult(
            status=status,
            id=episode_id,
        )

    def delete(self, user_id: str, episode_id: str) -> EpisodeDeleteResult:
        normalized_user_id = str(user_id).strip()
        normalized_episode_id = str(episode_id).strip()
        if not normalized_user_id:
            raise ValueError('user_id is required')
        if not normalized_episode_id:
            raise ValueError('episode_id is required')
        try:
            data = self._request(
                'DELETE',
                f'{_EPISODE_INTERNAL_PATH}/{normalized_episode_id}',
                params={'user_id': normalized_user_id},
            )
            result = EpisodeDeleteResult.model_validate(data)
            if result.id != normalized_episode_id:
                raise ValueError('Episode Core returned an unexpected delete id')
        except EpisodeReadError:
            raise
        except Exception as exc:
            raise EpisodeReadError.from_exception(exc) from exc
        return result

    def list_by_conversation(
        self,
        user_id: str,
        conversation_id: str,
    ) -> list[EpisodeRecord]:
        normalized_user_id = str(user_id).strip()
        normalized_conversation_id = str(conversation_id).strip()
        if not normalized_user_id:
            raise ValueError('user_id is required')
        if not normalized_conversation_id:
            raise ValueError('conversation_id is required')
        try:
            data = self._request('GET', _EPISODE_INTERNAL_PATH, params={
                'user_id': normalized_user_id,
                'conversation_id': normalized_conversation_id,
            })
            items = data.get('items')
            if not isinstance(items, list):
                raise RuntimeError('Episode Core returned an invalid list result')
            records = [self._record(item) for item in items]
            if any(record.user_id != normalized_user_id for record in records):
                raise ValueError('conversation Episode belongs to another user')
            if any(
                record.source.conversation_id != normalized_conversation_id
                for record in records
            ):
                raise ValueError('Episode belongs to another conversation')
        except EpisodeReadError:
            raise
        except Exception as exc:
            raise EpisodeReadError.from_exception(exc) from exc
        records.sort(key=lambda record: (
            record.recorded_at_ms,
            record.id,
        ))
        return records

    def list_recent(
        self,
        user_id: str,
        episode_type: EpisodeType,
        limit: int,
    ) -> list[EpisodeRecord]:
        normalized_user_id = str(user_id).strip()
        if not normalized_user_id:
            raise ValueError('user_id is required')
        normalized_type = EpisodeType(episode_type)
        if isinstance(limit, bool) or limit < 1 or limit > 100:
            raise ValueError('limit must be between 1 and 100')
        try:
            data = self._request(
                'POST',
                f'{_EPISODE_INTERNAL_PATH}:listRecent',
                json={
                    'user_id': normalized_user_id,
                    'episode_type': normalized_type.value,
                    'limit': limit,
                },
            )
            items = data.get('items')
            if not isinstance(items, list):
                raise RuntimeError('Episode Core returned an invalid recent list result')
            if len(items) > limit:
                raise ValueError('Episode Core returned too many recent records')
            records = [self._record(item) for item in items]
            if any(record.user_id != normalized_user_id for record in records):
                raise ValueError('recent Episode belongs to another user')
            if any(record.episode_type != normalized_type for record in records):
                raise ValueError('recent Episode has an unexpected type')
        except EpisodeReadError:
            raise
        except Exception as exc:
            raise EpisodeReadError.from_exception(exc) from exc
        return records

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
        try:
            data = self._request(
                'POST',
                f'{_EPISODE_INTERNAL_PATH}:searchCandidates',
                json={
                    'user_id': normalized_user_id,
                    'terms': query_terms,
                    'limit': int(_cfg['episode_candidate_topk']),
                },
            )
            candidates = data.get('items')
            if not isinstance(candidates, list):
                raise RuntimeError('Episode Core returned an invalid search result')
        except EpisodeReadError:
            raise
        except Exception as exc:
            raise EpisodeReadError.from_exception(exc) from exc
        current_ms = now_ms if now_ms is not None else self._clock_ms()
        ranked: list[EpisodeSearchResult] = []
        for candidate in candidates:
            try:
                if not isinstance(candidate, dict):
                    raise ValueError('candidate must be an object')
                episode = candidate.get('episode')
                if not isinstance(episode, dict):
                    raise ValueError('candidate episode must be an object')
                record = self._record(episode)
                if record.user_id != normalized_user_id:
                    raise ValueError('candidate belongs to another user')
                lexical_score = float(candidate.get('lexical_score'))
                if not math.isfinite(lexical_score):
                    raise ValueError('candidate lexical_score must be finite')
            except Exception as exc:
                raise EpisodeReadError.from_exception(exc) from exc
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
                lexical_score=lexical_score,
                score=final_score,
                rendered=self.render(record),
            ))
        ranked.sort(key=lambda value: (
            -value.score,
            -value.lexical_score,
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
        normalized_user_id = str(user_id).strip()
        if not normalized_user_id:
            raise ValueError('user_id is required')
        normalized_ids = list(dict.fromkeys(
            episode_id
            for raw_episode_id in episode_ids
            if (episode_id := str(raw_episode_id).strip())
        ))
        if not normalized_ids:
            return {}
        data = self._request(
            'POST',
            f'{_EPISODE_INTERNAL_PATH}:recordHits',
            json={
                'user_id': normalized_user_id,
                'episode_ids': normalized_ids,
            },
        )
        raw_results = data.get('results')
        if not isinstance(raw_results, dict):
            raise RuntimeError('Episode Core returned an invalid hit result')
        return {
            episode_id: raw_results.get(episode_id) is True
            for episode_id in normalized_ids
        }


@lru_cache(maxsize=1)
def get_episode_store() -> EpisodeStore:
    return EpisodeStore()
