from __future__ import annotations

import time
from collections import defaultdict
from concurrent.futures import as_completed
from pathlib import Path

import numpy as np
from lazyllm import LOG, ThreadPoolExecutor

from lazymind.review.skill_review.config import (
    DEFAULT_EMBEDDING_MAX_CHARS,
    DEFAULT_EMBEDDING_RETRIES,
    DEFAULT_STAGE_WORKERS,
    STAGE_CLUSTER,
    STAGE_FILES,
)
from lazymind.review.skill_review.schemas import SkillDraft, TaskCluster
from lazymind.review.skill_review.reports import finish_stage_report, stage_error, start_stage, write_json_file

MIN_VALID_EMBEDDING_RATIO = 0.8


def cluster_drafts(
    drafts: list[SkillDraft],
    emb,
    *,
    max_workers: int = DEFAULT_STAGE_WORKERS,
    embedding_max_chars: int = DEFAULT_EMBEDDING_MAX_CHARS,
    embedding_retries: int = DEFAULT_EMBEDDING_RETRIES,
    artifact_dir: Path | None = None,
) -> tuple[list[TaskCluster], dict]:
    started_at = start_stage()
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
    if len(drafts) == 1:
        clusters = [_cluster_from_indexes(drafts, [0])]
        if artifact_dir is not None:
            write_json_file(artifact_dir / STAGE_FILES[STAGE_CLUSTER], clusters)
        return clusters, finish_stage_report(
            STAGE_CLUSTER,
            started_at,
            input_count=len(drafts),
            output_count=len(clusters),
            errors=[],
            metadata=_cluster_report_metadata(
                draft_count=len(drafts),
                valid_embedding_count=len(drafts),
                failed_embedding_count=0,
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
        draft_count=len(drafts),
        valid_embedding_count=len(valid_drafts),
        failed_embedding_count=len(drafts) - len(valid_drafts),
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
        labels = _hdbscan_labels(np.array(embeddings))
        clusters = _clusters_from_labels(valid_drafts, labels)
    except Exception as exc:
        errors.append(stage_error(STAGE_CLUSTER, 'hdbscan', exc))
        LOG.error(f'cluster stage failed during HDBSCAN: {exc}')
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
) -> dict:
    valid_embedding_ratio = valid_embedding_count / draft_count if draft_count else 1.0
    return {
        'draft_count': draft_count,
        'valid_embedding_count': valid_embedding_count,
        'failed_embedding_count': failed_embedding_count,
        'valid_embedding_ratio': valid_embedding_ratio,
        'min_valid_embedding_ratio': MIN_VALID_EMBEDDING_RATIO,
    }


def _cluster_text(draft: SkillDraft) -> str:
    description = draft.contextual_description
    parts = [
        description.applicable_scenario.strip(),
        description.execution_summary.strip(),
    ]
    text = '\n'.join(part for part in parts if part)
    return text or description.task_goal or description.key_result or 'General task'


def _hdbscan_labels(embeddings: np.ndarray) -> list[int]:
    min_cluster_size = max(2, min(5, len(embeddings) // 3))
    try:
        import hdbscan

        clusterer = hdbscan.HDBSCAN(
            min_cluster_size=min_cluster_size,
            min_samples=1,
            metric='euclidean',
        )
    except ImportError:
        from sklearn.cluster import HDBSCAN

        clusterer = HDBSCAN(
            min_cluster_size=min_cluster_size,
            min_samples=1,
            metric='euclidean',
        )
    labels = clusterer.fit_predict(embeddings)
    return [int(label) for label in labels]


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
        description = draft.contextual_description
        scope = description.applicable_scenario or description.task_goal
        if scope:
            return scope
    return 'General task'
