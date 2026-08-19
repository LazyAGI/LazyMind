from __future__ import annotations

import hashlib
import math
import re
import threading
import time
from collections import Counter, OrderedDict
from concurrent.futures import ThreadPoolExecutor, TimeoutError as FutureTimeoutError
from contextvars import copy_context
from dataclasses import dataclass
from typing import Any, Iterable, Mapping, Sequence


_LATIN_TOKEN = re.compile(r'[a-z0-9]+', re.I)
_CJK_RUN = re.compile(r'[\u3400-\u4dbf\u4e00-\u9fff]+')


@dataclass(frozen=True)
class SkillDescriptor:
    skill_id: str
    name: str
    description: str
    aliases: tuple[str, ...] = ()
    tags: tuple[str, ...] = ()
    revision: str = ''

    @classmethod
    def from_mapping(cls, value: Mapping[str, Any]) -> 'SkillDescriptor':
        def strings(raw: Any) -> tuple[str, ...]:
            if isinstance(raw, str):
                items = raw.split(',')
            elif isinstance(raw, (list, tuple, set)):
                items = raw
            else:
                items = ()
            return tuple(dict.fromkeys(str(item).strip() for item in items if str(item).strip()))

        skill_id = str(value.get('id') or value.get('key') or '').strip()
        return cls(
            skill_id=skill_id,
            name=str(value.get('name') or skill_id).strip(),
            description=str(value.get('description') or '').strip(),
            aliases=strings(value.get('aliases')),
            tags=strings(value.get('tags')),
            revision=str(value.get('revision') or '').strip(),
        )

    @property
    def search_text(self) -> str:
        return '\n'.join(filter(None, [
            self.skill_id,
            self.name,
            self.description,
            ' '.join(self.aliases),
            ' '.join(self.tags),
        ]))

    @property
    def fingerprint(self) -> str:
        payload = '\0'.join([
            self.skill_id,
            self.name,
            self.description,
            *self.aliases,
            *self.tags,
            self.revision,
        ])
        return hashlib.sha256(payload.encode('utf-8')).hexdigest()


@dataclass(frozen=True)
class SkillRetrievalHit:
    descriptor: SkillDescriptor
    score: float
    channels: tuple[str, ...]

    def to_dict(self) -> dict[str, Any]:
        return {
            'id': self.descriptor.skill_id,
            'name': self.descriptor.name,
            'description': self.descriptor.description,
            'aliases': list(self.descriptor.aliases),
            'tags': list(self.descriptor.tags),
            'score': round(float(self.score), 8),
            'channels': list(self.channels),
        }


@dataclass(frozen=True)
class SkillRetrievalResult:
    hits: tuple[SkillRetrievalHit, ...]
    strategy: str
    catalog_size: int
    latency_ms: int
    embedding_error: str = ''

    @property
    def skill_ids(self) -> list[str]:
        return [hit.descriptor.skill_id for hit in self.hits]

    def to_trace_dict(self) -> dict[str, Any]:
        return {
            'strategy': self.strategy,
            'catalog_size': self.catalog_size,
            'latency_ms': self.latency_ms,
            'embedding_error': self.embedding_error,
            'hits': [hit.to_dict() for hit in self.hits],
        }


class SkillRetriever:
    _embedding_cache: OrderedDict[tuple[str, str], tuple[float, ...]] = OrderedDict()
    _embedding_cache_lock = threading.Lock()
    _embedding_executor = ThreadPoolExecutor(max_workers=4)
    _embedding_cache_limit = 4096

    def __init__(
        self,
        *,
        embedder: Any = None,
        cache_namespace: str = 'default',
        small_catalog_threshold: int = 20,
        embedding_timeout_seconds: float = 3.0,
        rrf_constant: int = 60,
    ) -> None:
        self._embedder = embedder
        self._cache_namespace = str(cache_namespace or 'default')
        self._small_catalog_threshold = max(0, int(small_catalog_threshold))
        self._embedding_timeout_seconds = max(0.1, float(embedding_timeout_seconds))
        self._rrf_constant = max(1, int(rrf_constant))

    def retrieve(
        self,
        query_context: str,
        descriptors: Iterable[SkillDescriptor | Mapping[str, Any]],
        limit: int = 8,
    ) -> SkillRetrievalResult:
        started = time.monotonic()
        catalog = self._normalize_descriptors(descriptors)
        if not catalog:
            return self._result((), 'empty', 0, started)
        if len(catalog) <= self._small_catalog_threshold:
            hits = tuple(SkillRetrievalHit(item, 1.0, ('all',)) for item in catalog)
            return self._result(hits, 'all', len(catalog), started)

        limit = max(1, min(int(limit), len(catalog)))
        pool_size = min(len(catalog), max(limit * 2, 12))
        lexical = self._lexical_ranking(str(query_context or ''), catalog)[:pool_size]
        dense, embedding_error = self._dense_ranking(str(query_context or ''), catalog)
        if dense:
            lexical_matches = [item for item in lexical if item[1] > 0]
            hits = self._fuse_rankings(
                catalog,
                lexical_matches,
                dense[:pool_size],
                limit,
            )
            strategy = 'hybrid'
        else:
            hits = tuple(
                SkillRetrievalHit(catalog[index], score, ('lexical',))
                for index, score in lexical
                if score > 0
            )
            hits = hits[:limit]
            strategy = 'lexical'
        return self._result(hits, strategy, len(catalog), started, embedding_error)

    @staticmethod
    def _normalize_descriptors(
        descriptors: Iterable[SkillDescriptor | Mapping[str, Any]],
    ) -> list[SkillDescriptor]:
        result, seen = [], set()
        for value in descriptors:
            descriptor = value if isinstance(value, SkillDescriptor) else SkillDescriptor.from_mapping(value)
            if not descriptor.skill_id or descriptor.skill_id in seen:
                continue
            seen.add(descriptor.skill_id)
            result.append(descriptor)
        return result

    def _result(
        self,
        hits: Sequence[SkillRetrievalHit],
        strategy: str,
        catalog_size: int,
        started: float,
        embedding_error: str = '',
    ) -> SkillRetrievalResult:
        return SkillRetrievalResult(
            hits=tuple(hits),
            strategy=strategy,
            catalog_size=catalog_size,
            latency_ms=max(0, int((time.monotonic() - started) * 1000)),
            embedding_error=str(embedding_error or '')[:240],
        )

    @classmethod
    def _tokens(cls, text: str) -> list[str]:
        lowered = str(text or '').lower()
        tokens = _LATIN_TOKEN.findall(lowered)
        for run in _CJK_RUN.findall(lowered):
            tokens.extend(run)
            tokens.extend(run[index:index + 2] for index in range(len(run) - 1))
        return tokens

    def _lexical_ranking(
        self,
        query: str,
        catalog: Sequence[SkillDescriptor],
    ) -> list[tuple[int, float]]:
        query_terms = self._tokens(query)
        documents = [self._tokens(item.search_text) for item in catalog]
        if not query_terms:
            return [(index, 0.0) for index in range(len(catalog))]
        document_frequency = Counter(
            term for document in documents for term in set(document)
        )
        average_length = sum(map(len, documents)) / max(1, len(documents))
        query_frequency = Counter(query_terms)
        normalized_query = re.sub(r'\s+', '', query.lower())
        ranking = []
        for index, (descriptor, document) in enumerate(zip(catalog, documents)):
            term_frequency = Counter(document)
            score = 0.0
            for term, query_count in query_frequency.items():
                frequency = term_frequency.get(term, 0)
                if not frequency:
                    continue
                inverse_frequency = math.log(
                    1 + (len(documents) - document_frequency[term] + 0.5)
                    / (document_frequency[term] + 0.5)
                )
                length_factor = 1 - 0.75 + 0.75 * len(document) / max(1.0, average_length)
                score += query_count * inverse_frequency * (
                    frequency * 2.2 / (frequency + 1.2 * length_factor)
                )
            exact_labels = [descriptor.name, *descriptor.aliases]
            if any(
                (normalized := re.sub(r'\s+', '', label.lower()))
                and (normalized in normalized_query or normalized_query in normalized)
                for label in exact_labels
            ):
                score += 4.0
            exact_latin_terms = {
                term for term in query_frequency
                if len(term) >= 2 and term.isascii() and term in term_frequency
            }
            score += 5.0 * len(exact_latin_terms)
            ranking.append((index, score))
        return sorted(ranking, key=lambda item: (-item[1], item[0]))

    def _dense_ranking(
        self,
        query: str,
        catalog: Sequence[SkillDescriptor],
    ) -> tuple[list[tuple[int, float]], str]:
        if self._embedder is None or not query.strip():
            return [], 'embedding model unavailable' if self._embedder is None else ''
        cached: dict[int, tuple[float, ...]] = {}
        missing_indices = []
        with self._embedding_cache_lock:
            for index, descriptor in enumerate(catalog):
                key = (self._cache_namespace, descriptor.fingerprint)
                vector = self._embedding_cache.get(key)
                if vector is None:
                    missing_indices.append(index)
                else:
                    self._embedding_cache.move_to_end(key)
                    cached[index] = vector
        texts = [query, *(catalog[index].search_text for index in missing_indices)]
        try:
            vectors = self._embed_batch(texts)
            query_vector = vectors[0]
            if len(vectors) != len(texts):
                raise ValueError('embedding model returned an unexpected batch size')
            with self._embedding_cache_lock:
                for index, vector in zip(missing_indices, vectors[1:]):
                    cached[index] = vector
                    descriptor = catalog[index]
                    key = (self._cache_namespace, descriptor.fingerprint)
                    self._embedding_cache[key] = vector
                    self._embedding_cache.move_to_end(key)
                while len(self._embedding_cache) > self._embedding_cache_limit:
                    self._embedding_cache.popitem(last=False)
            dimensions = {len(query_vector), *(len(vector) for vector in cached.values())}
            if len(dimensions) != 1:
                raise ValueError('embedding dimensions do not match')
        except Exception as exc:
            return [], f'{type(exc).__name__}: {exc}'
        ranking = [
            (index, self._cosine(query_vector, cached[index]))
            for index in range(len(catalog))
        ]
        return sorted(ranking, key=lambda item: (-item[1], item[0])), ''

    def _embed_batch(self, texts: list[str]) -> list[tuple[float, ...]]:
        context = copy_context()
        future = self._embedding_executor.submit(context.run, self._embedder, texts)
        try:
            raw = future.result(timeout=self._embedding_timeout_seconds)
        except FutureTimeoutError as exc:
            future.cancel()
            raise TimeoutError(
                f'embedding exceeded {self._embedding_timeout_seconds:.1f}s'
            ) from exc
        if not isinstance(raw, (list, tuple)) or len(raw) != len(texts):
            raise ValueError('embedding model must return one vector per input')
        vectors = []
        for value in raw:
            if not isinstance(value, (list, tuple)) or not value:
                raise ValueError('embedding vector is empty or invalid')
            vector = tuple(float(item) for item in value)
            if not all(math.isfinite(item) for item in vector):
                raise ValueError('embedding vector contains non-finite values')
            vectors.append(vector)
        return vectors

    @staticmethod
    def _cosine(left: Sequence[float], right: Sequence[float]) -> float:
        left_norm = math.sqrt(sum(value * value for value in left))
        right_norm = math.sqrt(sum(value * value for value in right))
        if not left_norm or not right_norm:
            return 0.0
        return sum(a * b for a, b in zip(left, right)) / (left_norm * right_norm)

    def _fuse_rankings(
        self,
        catalog: Sequence[SkillDescriptor],
        lexical: Sequence[tuple[int, float]],
        dense: Sequence[tuple[int, float]],
        limit: int,
    ) -> tuple[SkillRetrievalHit, ...]:
        scores: dict[int, float] = {}
        channels: dict[int, list[str]] = {}
        for channel, ranking in (('lexical', lexical), ('dense', dense)):
            for rank, (index, _) in enumerate(ranking, start=1):
                scores[index] = scores.get(index, 0.0) + 1.0 / (self._rrf_constant + rank)
                channels.setdefault(index, []).append(channel)
        ranked = sorted(scores, key=lambda index: (-scores[index], index))
        selected = ranked[:limit]
        if limit >= 2:
            channel_leaders = [ranking[0][0] for ranking in (dense, lexical) if ranking]
            for leader in channel_leaders:
                if leader in selected:
                    continue
                replace_at = next(
                    (
                        index for index in range(len(selected) - 1, -1, -1)
                        if selected[index] not in channel_leaders
                    ),
                    len(selected) - 1,
                )
                selected[replace_at] = leader
        return tuple(
            SkillRetrievalHit(catalog[index], scores[index], tuple(channels[index]))
            for index in selected
        )
