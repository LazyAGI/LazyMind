from __future__ import annotations

import json
from collections import defaultdict
from typing import Any

import numpy as np
from lazyllm import LOG

from chat.components.skill_review.schemas import SkillDraft, TaskCluster


def cluster_drafts(drafts: list[SkillDraft], emb) -> list[TaskCluster]:
    if not drafts:
        return []
    if len(drafts) == 1:
        return [_cluster_from_indexes(drafts, [0])]

    texts = [_cluster_text(draft) for draft in drafts]
    try:
        embeddings = np.array([emb(text) for text in texts])
        labels = _hdbscan_labels(embeddings)
        return _clusters_from_labels(drafts, labels)
    except Exception as exc:
        LOG.warning(f'Failed to cluster skill drafts with embeddings; using one cluster: {exc}')
        return [_cluster_from_indexes(drafts, list(range(len(drafts))))]


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
    return clusters or [_cluster_from_indexes(drafts, list(range(len(drafts))))]


def _cluster_from_indexes(drafts: list[SkillDraft], indexes: list[int]) -> TaskCluster:
    selected = [drafts[index] for index in indexes]
    # print('cluster_from_indexes start=' + '=' * 100)
    # for d in selected:
    #     print(d.contextual_description.applicable_scenario)
    #     print(d.contextual_description.task_goal)
    #     print(d.contextual_description.key_result)
    #     print(d.contextual_description.execution_summary)
    #     print('-' * 100)
    # print('cluster_from_indexes end=' + '=' * 100)
    scope = _cluster_scope(selected)
    return TaskCluster(task_scope=scope, drafts=selected)


def _cluster_scope(drafts: list[SkillDraft]) -> str:
    for draft in drafts:
        description = draft.contextual_description
        scope = description.applicable_scenario or description.task_goal
        if scope:
            return scope
    return 'General task'
