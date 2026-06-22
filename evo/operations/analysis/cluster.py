from __future__ import annotations

from collections import Counter, defaultdict
from collections.abc import Mapping
from typing import Any

import numpy as np
from apted import APTED
from apted.helpers import Tree
from rapidfuzz.distance import Levenshtein
from sklearn.cluster import AgglomerativeClustering
from sklearn.feature_extraction import DictVectorizer
from sklearn.metrics import pairwise_distances
from sklearn.neighbors import LocalOutlierFactor
from sklearn.preprocessing import StandardScaler

from evo.operations.common import as_list, text


def trace_clusters(classifications: Mapping[str, Any]) -> dict[str, Any]:
    rows = [dict(item) for _, item in sorted(classifications.items()) if isinstance(item, Mapping)]
    if not rows:
        return {'id': 'analysis.trace_clusters', 'total': 0, 'clusters': [], 'outliers': []}
    vectors = _feature_rows(rows)
    matrix = DictVectorizer(sparse=False).fit_transform(vectors)
    scaled = StandardScaler().fit_transform(matrix) if len(rows) > 1 else matrix
    labels = _cluster_labels(rows, scaled)
    outlier_scores = _outlier_scores(scaled)
    for row, label, score in zip(rows, labels, outlier_scores, strict=False):
        row['cluster_id'] = f'trace_cluster_{int(label):02d}'
        row['outlier_score'] = round(float(score), 4)
    clusters = [_cluster_summary(cluster_id, members, scaled, rows) for cluster_id, members in _groups(rows).items()]
    return {
        'id': 'analysis.trace_clusters',
        'total': len(rows),
        'clusters': sorted(clusters, key=lambda item: (-int(item['size']), item['cluster_id'])),
        'outliers': [
            {'case_id': row['case_id'], 'cluster_id': row['cluster_id'], 'outlier_score': row['outlier_score']}
            for row in sorted(rows, key=lambda item: float(item.get('outlier_score') or 0.0), reverse=True)[:5]
            if float(row.get('outlier_score') or 0.0) >= 0.8
        ],
        'rows': [
            {
                'case_id': row['case_id'],
                'cluster_id': row['cluster_id'],
                'outlier_score': row['outlier_score'],
                'route_signature': _trace(row).get('route_signature', ''),
                'fine_category': row.get('fine_category', ''),
            }
            for row in rows
        ],
    }


def tree_edit_distance(left: Mapping[str, Any], right: Mapping[str, Any]) -> float:
    left_text = text(left.get('tree_text')) or '{unknown}'
    right_text = text(right.get('tree_text')) or '{unknown}'
    left_tree = _apted_tree(left_text)
    right_tree = _apted_tree(right_text)
    return float(APTED(left_tree, right_tree).compute_edit_distance())


def _feature_rows(rows: list[dict[str, Any]]) -> list[dict[str, float | str]]:
    return [_features(row) for row in rows]


def _features(row: Mapping[str, Any]) -> dict[str, float | str]:
    trace = _trace(row)
    judge = row.get('judge') if isinstance(row.get('judge'), Mapping) else {}
    features: dict[str, float | str] = {
        'question_type': text(row.get('question_type') or _case(row).get('question_type')),
        'fine_category': text(row.get('fine_category')),
        'route_signature': text(trace.get('route_signature')),
        'bottleneck_stage': text(trace.get('bottleneck_stage')),
        'trace_available': 1.0 if trace.get('trace_available') else 0.0,
        'answer_score': _number(judge.get('answer_score')),
        'retrieval_score': _number(judge.get('retrieval_score')),
        'chunk_recall': _number(judge.get('chunk_recall')),
        'doc_recall': _number(judge.get('doc_recall')),
        'error_stages': float(len(as_list(trace.get('error_stages')))),
    }
    trace_features = trace.get('features') if isinstance(trace.get('features'), Mapping) else {}
    for key, value in trace_features.items():
        if isinstance(value, (int, float)):
            features[f'trace.{key}'] = float(value)
    return features


def _cluster_labels(rows: list[dict[str, Any]], scaled: np.ndarray) -> np.ndarray:
    if len(rows) == 1:
        return np.asarray([0], dtype=int)
    distances = _combined_distances(rows, scaled)
    cluster_count = 1 if len(rows) == 2 else min(max(2, len(rows) // 3), len(rows) - 1, 8)
    model = AgglomerativeClustering(n_clusters=cluster_count, metric='precomputed', linkage='average')
    return model.fit_predict(distances)


def _combined_distances(rows: list[dict[str, Any]], scaled: np.ndarray) -> np.ndarray:
    numeric = pairwise_distances(scaled, metric='cosine')
    numeric = np.nan_to_num(numeric)
    sequence = np.zeros_like(numeric)
    tree = np.zeros_like(numeric)
    for i, left in enumerate(rows):
        for j in range(i + 1, len(rows)):
            right = rows[j]
            sequence_distance = _sequence_distance(_trace(left), _trace(right))
            tree_distance = _normalized_tree_distance(_trace(left), _trace(right))
            sequence[i, j] = sequence[j, i] = sequence_distance
            tree[i, j] = tree[j, i] = tree_distance
    return np.nan_to_num((0.55 * numeric) + (0.25 * sequence) + (0.20 * tree))


def _sequence_distance(left: Mapping[str, Any], right: Mapping[str, Any]) -> float:
    left_sig = text(left.get('route_signature'))
    right_sig = text(right.get('route_signature'))
    return float(Levenshtein.normalized_distance(left_sig, right_sig))


def _normalized_tree_distance(left: Mapping[str, Any], right: Mapping[str, Any]) -> float:
    raw = tree_edit_distance(left, right)
    left_size = max(1, text(left.get('tree_text')).count('{'))
    right_size = max(1, text(right.get('tree_text')).count('{'))
    return raw / max(left_size, right_size)


def _outlier_scores(scaled: np.ndarray) -> list[float]:
    if len(scaled) < 5:
        return [0.0 for _ in range(len(scaled))]
    neighbors = min(20, max(2, len(scaled) - 1))
    lof = LocalOutlierFactor(n_neighbors=neighbors)
    lof.fit_predict(scaled)
    raw = -lof.negative_outlier_factor_
    low, high = float(np.min(raw)), float(np.max(raw))
    if high <= low:
        return [0.0 for _ in raw]
    return [float((value - low) / (high - low)) for value in raw]


def _groups(rows: list[dict[str, Any]]) -> dict[str, list[dict[str, Any]]]:
    grouped: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        grouped[text(row.get('cluster_id'))].append(row)
    return dict(grouped)


def _cluster_summary(
    cluster_id: str,
    members: list[dict[str, Any]],
    scaled: np.ndarray,
    all_rows: list[dict[str, Any]],
) -> dict[str, Any]:
    indices = [all_rows.index(row) for row in members]
    sub = scaled[indices]
    center = np.mean(sub, axis=0)
    representative_index = indices[int(np.argmin(pairwise_distances(sub, center.reshape(1, -1)).ravel()))]
    representative = all_rows[representative_index]
    fine_counts = Counter(text(row.get('fine_category')) for row in members)
    route_counts = Counter(text(_trace(row).get('route_signature')) for row in members)
    return {
        'cluster_id': cluster_id,
        'size': len(members),
        'representative_case_id': representative.get('case_id', ''),
        'fine_category_counts': dict(fine_counts),
        'dominant_fine_category': fine_counts.most_common(1)[0][0] if fine_counts else '',
        'route_signature_counts': dict(route_counts),
        'common_route_signature': route_counts.most_common(1)[0][0] if route_counts else '',
        'case_ids': [row.get('case_id', '') for row in members],
        'avg_answer_score': _avg(_number(_judge(row).get('answer_score')) for row in members),
        'avg_retrieval_score': _avg(_number(_judge(row).get('retrieval_score')) for row in members),
    }


def _trace(row: Mapping[str, Any]) -> Mapping[str, Any]:
    value = row.get('trace_summary')
    return value if isinstance(value, Mapping) else {}


def _judge(row: Mapping[str, Any]) -> Mapping[str, Any]:
    value = row.get('judge')
    return value if isinstance(value, Mapping) else {}


def _case(row: Mapping[str, Any]) -> Mapping[str, Any]:
    value = row.get('case')
    return value if isinstance(value, Mapping) else {}


def _apted_tree(value: str) -> Tree:
    try:
        return Tree.from_text(value)
    except Exception:
        return Tree.from_text('{unknown}')


def _number(value: Any) -> float:
    try:
        return float(value or 0.0)
    except (TypeError, ValueError):
        return 0.0


def _avg(values: Any) -> float:
    rows = [float(value) for value in values]
    return round(float(np.mean(rows)), 4) if rows else 0.0
