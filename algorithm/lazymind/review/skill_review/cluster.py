from __future__ import annotations

import time
from collections import defaultdict
from concurrent.futures import as_completed
from pathlib import Path
from typing import Any

import numpy as np
from lazyllm import LOG, ThreadPoolExecutor

from lazymind.review.skill_review.config import (
    DEFAULT_EMBEDDING_MAX_CHARS,
    DEFAULT_EMBEDDING_RETRIES,
    DEFAULT_STAGE_WORKERS,
    STAGE_CLUSTER,
    STAGE_FILES,
)
from lazymind.review.skill_review.json_call import call_json
from lazymind.review.skill_review.prompt import cluster_prompt
from lazymind.review.skill_review.schemas import SkillDraft, TaskCluster
from lazymind.review.skill_review.reports import finish_stage_report, stage_error, start_stage, write_json_file

MIN_VALID_EMBEDDING_RATIO = 0.8
DEFAULT_LLM_CLUSTER_THRESHOLD = 20
UMAP_RANDOM_STATE = 42

_CLUSTER_SCHEMA = {
    'title': 'skill_draft_cluster_response',
    'type': 'object',
    'properties': {
        'clusters': {
            'type': 'array',
            'items': {
                'type': 'object',
                'properties': {
                    'task_scope': {'type': 'string'},
                    'draft_indexes': {
                        'type': 'array',
                        'items': {'type': 'integer'},
                    },
                },
                'required': ['task_scope', 'draft_indexes'],
            },
        },
    },
    'required': ['clusters'],
}


def cluster_drafts(
    drafts: list[SkillDraft],
    emb,
    *,
    llm=None,
    llm_cluster_threshold: int = DEFAULT_LLM_CLUSTER_THRESHOLD,
    max_workers: int = DEFAULT_STAGE_WORKERS,
    embedding_max_chars: int = DEFAULT_EMBEDDING_MAX_CHARS,
    embedding_retries: int = DEFAULT_EMBEDDING_RETRIES,
    artifact_dir: Path | None = None,
) -> tuple[list[TaskCluster], dict]:
    started_at = start_stage()
    input_count = len(drafts)
    if not drafts:
        clusters: list[TaskCluster] = []
        if artifact_dir is not None:
            write_json_file(artifact_dir / STAGE_FILES[STAGE_CLUSTER], clusters)
        return clusters, finish_stage_report(
            STAGE_CLUSTER,
            started_at,
            input_count=0,
            output_count=0,
            errors=[],
            status='completed',
            metadata=_cluster_report_metadata(
                draft_count=0,
                valid_embedding_count=0,
                failed_embedding_count=0,
            ),
        )
    drafts = [draft for draft in drafts if _cluster_text(draft)]
    if not drafts:
        clusters = []
        if artifact_dir is not None:
            write_json_file(artifact_dir / STAGE_FILES[STAGE_CLUSTER], clusters)
        return clusters, finish_stage_report(
            STAGE_CLUSTER,
            started_at,
            input_count=input_count,
            output_count=0,
            errors=[],
            status='completed',
            metadata=_cluster_report_metadata(
                draft_count=input_count,
                valid_embedding_count=0,
                failed_embedding_count=input_count,
            ),
        )
    if len(drafts) == 1:
        clusters = [_cluster_from_indexes(drafts, [0])]
        if artifact_dir is not None:
            write_json_file(artifact_dir / STAGE_FILES[STAGE_CLUSTER], clusters)
        return clusters, finish_stage_report(
            STAGE_CLUSTER,
            started_at,
            input_count=input_count,
            output_count=len(clusters),
            errors=[],
            metadata=_cluster_report_metadata(
                draft_count=input_count,
                valid_embedding_count=len(drafts),
                failed_embedding_count=input_count - len(drafts),
            ),
        )

    if len(drafts) <= llm_cluster_threshold:
        if llm is None:
            raise ValueError('llm is required for small-sample skill draft clustering')
        try:
            clusters = _llm_clusters(drafts, llm)
            errors: list[dict] = []
        except Exception as exc:
            errors = [stage_error(STAGE_CLUSTER, 'llm_cluster', exc)]
            LOG.error(f'cluster stage failed during LLM clustering: {exc}')
            clusters = []
        if artifact_dir is not None:
            write_json_file(artifact_dir / STAGE_FILES[STAGE_CLUSTER], clusters)
        return clusters, finish_stage_report(
            STAGE_CLUSTER,
            started_at,
            input_count=input_count,
            output_count=len(clusters),
            errors=errors,
            status='failed' if not clusters else 'completed',
            metadata=_cluster_report_metadata(
                draft_count=input_count,
                valid_embedding_count=0,
                failed_embedding_count=input_count - len(drafts),
                method='llm',
                llm_cluster_threshold=llm_cluster_threshold,
            ),
        )

    texts = [_cluster_text(draft) for draft in drafts]
    raw_embeddings, embedded_drafts, errors = _embed_drafts(
        drafts,
        texts,
        emb,
        max_workers=max_workers,
        max_chars=embedding_max_chars,
        retries=embedding_retries,
    )
    embeddings, valid_drafts, dimension_errors = _validate_embeddings(raw_embeddings, embedded_drafts)
    errors.extend(dimension_errors)
    metadata = _cluster_report_metadata(
        draft_count=input_count,
        valid_embedding_count=len(valid_drafts),
        failed_embedding_count=input_count - len(valid_drafts),
    )
    if metadata['valid_embedding_ratio'] < MIN_VALID_EMBEDDING_RATIO:
        exc = RuntimeError(
            'valid embedding ratio is below threshold: '
            f"{metadata['valid_embedding_count']}/{metadata['draft_count']} "
            f"({metadata['valid_embedding_ratio']:.2%}) < {MIN_VALID_EMBEDDING_RATIO:.2%}"
        )
        errors.append(stage_error(STAGE_CLUSTER, 'embedding_quality', exc))
        LOG.error(f'cluster stage failed: {exc}')
        clusters = []
        if artifact_dir is not None:
            write_json_file(artifact_dir / STAGE_FILES[STAGE_CLUSTER], clusters)
        return clusters, finish_stage_report(
            STAGE_CLUSTER,
            started_at,
            input_count=len(valid_drafts),
            output_count=0,
            errors=errors,
            status='failed',
            metadata=metadata,
        )
    if not valid_drafts:
        LOG.warning('Failed to embed all skill drafts; no clusters can be built')
        clusters = []
        if artifact_dir is not None:
            write_json_file(artifact_dir / STAGE_FILES[STAGE_CLUSTER], clusters)
        return clusters, finish_stage_report(
            STAGE_CLUSTER,
            started_at,
            input_count=len(drafts),
            output_count=0,
            errors=errors,
            status='failed',
            metadata=metadata,
        )
    if len(valid_drafts) == 1:
        clusters = [_cluster_from_indexes(valid_drafts, [0])]
        if artifact_dir is not None:
            write_json_file(artifact_dir / STAGE_FILES[STAGE_CLUSTER], clusters)
        return clusters, finish_stage_report(
            STAGE_CLUSTER,
            started_at,
            input_count=len(drafts),
            output_count=len(clusters),
            errors=errors,
            metadata=metadata,
        )

    try:
        labels, cluster_metadata = _embedding_cluster_labels(np.array(embeddings))
        metadata.update(cluster_metadata)
        clusters = _clusters_from_labels(valid_drafts, labels)
    except Exception as exc:
        errors.append(stage_error(STAGE_CLUSTER, 'embedding_clustering', exc))
        LOG.error(f'cluster stage failed during embedding clustering: {exc}')
        clusters = []
    if artifact_dir is not None:
        write_json_file(artifact_dir / STAGE_FILES[STAGE_CLUSTER], clusters)
    return clusters, finish_stage_report(
        STAGE_CLUSTER,
        started_at,
        input_count=len(valid_drafts),
        output_count=len(clusters),
        errors=errors,
        status='failed' if not clusters else 'completed',
        metadata=metadata,
    )


def _embed_drafts(
    drafts: list[SkillDraft],
    texts: list[str],
    emb,
    *,
    max_workers: int,
    max_chars: int,
    retries: int,
) -> tuple[list, list[SkillDraft], list[dict]]:
    results = [None] * len(drafts)
    errors: list[dict] = []
    with ThreadPoolExecutor(max_workers=max(1, max_workers)) as executor:
        futures = {
            executor.submit(_embed_text_with_retry, emb, text[:max_chars], retries): (index, draft)
            for index, (draft, text) in enumerate(zip(drafts, texts))
        }
        for fut in as_completed(futures):
            index, draft = futures[fut]
            try:
                results[index] = fut.result()
            except Exception as exc:
                LOG.warning(f'failed to embed skill draft {draft.session_id}: {exc}')
                errors.append(stage_error('cluster.embedding', draft.session_id, exc))

    valid_embeddings = []
    valid_drafts = []
    for draft, embedding in zip(drafts, results):
        if embedding is None:
            continue
        valid_drafts.append(draft)
        valid_embeddings.append(embedding)
    return valid_embeddings, valid_drafts, errors


def _embed_text_with_retry(emb, text: str, retries: int):
    attempts = max(1, retries)
    last_exc: Exception | None = None
    for attempt in range(attempts):
        try:
            value = emb(text)
            if value is None:
                raise ValueError('embedding model returned empty response')
            return value
        except Exception as exc:
            last_exc = exc
            if attempt + 1 < attempts:
                time.sleep(min(2 ** attempt, 4))
    raise RuntimeError(f'embedding failed after {attempts} attempts: {last_exc}') from last_exc


def _validate_embeddings(
    embeddings: list,
    drafts: list[SkillDraft],
) -> tuple[list[list[float]], list[SkillDraft], list[dict]]:
    valid_embeddings: list[list[float]] = []
    valid_drafts: list[SkillDraft] = []
    errors: list[dict] = []
    expected_dim: int | None = None
    for draft, embedding in zip(drafts, embeddings):
        try:
            vector = np.squeeze(np.asarray(embedding, dtype=float))
            if vector.ndim != 1:
                raise ValueError(f'embedding vector must be one-dimensional, got shape {vector.shape}')
            if vector.size == 0:
                raise ValueError('embedding vector is empty')
            if not np.all(np.isfinite(vector)):
                raise ValueError('embedding vector contains non-finite values')
            if expected_dim is None:
                expected_dim = int(vector.size)
            elif vector.size != expected_dim:
                raise ValueError(
                    f'embedding dimension mismatch: expected {expected_dim}, got {vector.size}'
                )
        except Exception as exc:
            errors.append(stage_error('cluster.embedding_dimension', draft.session_id, exc))
            continue
        valid_embeddings.append(vector.tolist())
        valid_drafts.append(draft)
    return valid_embeddings, valid_drafts, errors


def _cluster_report_metadata(
    *,
    draft_count: int,
    valid_embedding_count: int,
    failed_embedding_count: int,
    method: str = 'embedding',
    llm_cluster_threshold: int = DEFAULT_LLM_CLUSTER_THRESHOLD,
) -> dict:
    valid_embedding_ratio = valid_embedding_count / draft_count if draft_count else 1.0
    return {
        'draft_count': draft_count,
        'valid_embedding_count': valid_embedding_count,
        'failed_embedding_count': failed_embedding_count,
        'valid_embedding_ratio': valid_embedding_ratio,
        'min_valid_embedding_ratio': MIN_VALID_EMBEDDING_RATIO,
        'method': method,
        'llm_cluster_threshold': llm_cluster_threshold,
    }


def _llm_clusters(drafts: list[SkillDraft], llm) -> list[TaskCluster]:
    LOG.info('[SkillReview] Running LLM clustering...')
    payload = call_json(llm, cluster_prompt(_cluster_prompt_items(drafts)), _CLUSTER_SCHEMA)
    raw_clusters = payload.get('clusters')
    if not isinstance(raw_clusters, list):
        raise ValueError('LLM cluster response must contain clusters list')

    clusters: list[TaskCluster] = []
    seen: set[int] = set()
    expected = set(range(len(drafts)))
    for cluster in raw_clusters:
        if not isinstance(cluster, dict):
            raise ValueError(f'cluster item must be an object: {cluster!r}')
        task_scope = str(cluster.get('task_scope') or '').strip()
        indexes = cluster.get('draft_indexes')
        if not task_scope:
            raise ValueError(f'cluster task_scope must be non-empty: {cluster!r}')
        if not isinstance(indexes, list) or not indexes:
            raise ValueError(f'cluster draft_indexes must be a non-empty list: {cluster!r}')

        normalized_indexes = []
        for raw_index in indexes:
            if not isinstance(raw_index, int):
                raise ValueError(f'cluster draft index must be integer: {raw_index!r}')
            if raw_index not in expected:
                raise ValueError(f'cluster draft index out of range: {raw_index}')
            if raw_index in seen:
                raise ValueError(f'cluster draft index appears more than once: {raw_index}')
            seen.add(raw_index)
            normalized_indexes.append(raw_index)
        selected = [drafts[index] for index in normalized_indexes]
        clusters.append(TaskCluster(task_scope=task_scope, drafts=selected))

    missing = sorted(expected - seen)
    if missing:
        raise ValueError(f'LLM cluster response omitted draft indexes: {missing}')
    return clusters


def _cluster_prompt_items(drafts: list[SkillDraft]) -> list[dict[str, Any]]:
    items = []
    for index, draft in enumerate(drafts):
        signature = draft.cluster_signature
        items.append({
            'draft_index': index,
            'intent': signature.intent,
            'procedure': signature.procedure,
            'boundaries': signature.boundaries,
        })
    return items


def _cluster_text(draft: SkillDraft) -> str:
    signature = draft.cluster_signature
    parts = [
        signature.intent.strip(),
        '\n'.join(step.strip() for step in signature.procedure if step.strip()),
        signature.boundaries.strip(),
    ]
    return '\n'.join(part for part in parts if part)


def _embedding_cluster_labels(embeddings: np.ndarray) -> tuple[list[int], dict]:
    LOG.info('[SkillReview] Running embedding clustering...')
    reduced_embeddings, reduction_metadata = _umap_reduce_embeddings(embeddings)
    hdbscan_labels, hdbscan_metadata = _hdbscan_labels(reduced_embeddings)
    hdbscan_stats = _label_stats(hdbscan_labels)
    hdbscan_stats.update(hdbscan_metadata)
    return hdbscan_labels, {
        'embedding_clusterer': 'umap_hdbscan',
        'embedding_cluster_stats': hdbscan_stats,
        'embedding_reduction': reduction_metadata,
    }


def _umap_reduce_embeddings(embeddings: np.ndarray) -> tuple[np.ndarray, dict]:
    try:
        from umap import UMAP
    except ImportError as exc:
        raise ImportError('umap-learn is required for embedding clustering') from exc

    sample_count = len(embeddings)
    n_neighbors = min(sample_count - 1, min(20, max(10, int(round(sample_count ** 0.5 * 1.5)))))
    n_components = min(15, sample_count - 1)
    reducer = UMAP(
        n_neighbors=n_neighbors,
        n_components=n_components,
        min_dist=0.0,
        metric='cosine',
        random_state=UMAP_RANDOM_STATE,
    )
    reduced = reducer.fit_transform(embeddings)
    return np.asarray(reduced, dtype=float), {
        'method': 'umap',
        'input_dimension': int(np.asarray(embeddings).shape[1]),
        'output_dimension': int(n_components),
        'n_neighbors': int(n_neighbors),
        'min_dist': 0.0,
        'metric': 'cosine',
        'random_state': UMAP_RANDOM_STATE,
    }


def _hdbscan_labels(embeddings: np.ndarray) -> tuple[list[int], dict]:
    min_cluster_size = max(2, min(10, int(round(len(embeddings) ** 0.5)) - 1))
    min_samples = 1
    try:
        import hdbscan

        clusterer = hdbscan.HDBSCAN(
            min_cluster_size=min_cluster_size,
            min_samples=min_samples,
            metric='euclidean',
        )
    except ImportError:
        from sklearn.cluster import HDBSCAN

        clusterer = HDBSCAN(
            min_cluster_size=min_cluster_size,
            min_samples=min_samples,
            metric='euclidean',
        )
    labels = clusterer.fit_predict(embeddings)
    return [int(label) for label in labels], {
        'min_cluster_size': int(min_cluster_size),
        'min_samples': int(min_samples),
        'metric': 'euclidean',
    }


def _label_stats(labels: list[int]) -> dict:
    grouped: dict[int, int] = defaultdict(int)
    noise_count = 0
    for label in labels:
        if label == -1:
            noise_count += 1
        else:
            grouped[label] += 1
    singleton_count = noise_count + sum(1 for size in grouped.values() if size == 1)
    cluster_count = noise_count + len(grouped)
    total_count = len(labels)
    return {
        'total_count': total_count,
        'cluster_count': cluster_count,
        'singleton_count': singleton_count,
        'noise_count': noise_count,
        'singleton_ratio': singleton_count / total_count if total_count else 0.0,
        'cluster_ratio': cluster_count / total_count if total_count else 0.0,
    }


def _clusters_from_labels(drafts: list[SkillDraft], labels: list[int]) -> list[TaskCluster]:
    grouped: dict[int, list[int]] = defaultdict(list)
    noise_indexes: list[int] = []
    for index, label in enumerate(labels):
        if label == -1:
            noise_indexes.append(index)
        else:
            grouped[label].append(index)

    clusters = [
        _cluster_from_indexes(drafts, indexes)
        for _, indexes in sorted(grouped.items(), key=lambda item: item[0])
        if indexes
    ]
    clusters.extend(_cluster_from_indexes(drafts, [index]) for index in noise_indexes)
    return clusters


def _cluster_from_indexes(drafts: list[SkillDraft], indexes: list[int]) -> TaskCluster:
    selected = [drafts[index] for index in indexes]
    scope = _cluster_scope(selected)
    return TaskCluster(task_scope=scope, drafts=selected)


def _cluster_scope(drafts: list[SkillDraft]) -> str:
    for draft in drafts:
        signature = draft.cluster_signature
        scope = signature.intent or signature.boundaries
        if scope:
            return scope
    raise ValueError('cannot derive task scope from empty cluster signatures')
