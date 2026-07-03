from __future__ import annotations

from collections import Counter, defaultdict
from collections.abc import Mapping
from importlib import metadata
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

TRACE_FEATURES = (
    'node_count', 'edge_count', 'max_depth', 'branching_factor_avg', 'error_span_count', 'trace_latency_ms',
    'exclusive_latency_ms', 'retrieved_doc_count', 'retrieved_chunk_count',
)
STAGES = ('query_rewrite', 'retrieve', 'rerank', 'context_assembly', 'prompt_build', 'tool_call', 'llm_generate',
          'postprocess', 'stream')


def _version(name: str) -> str:
    try:
        return metadata.version(name).split('.', 1)[0]
    except metadata.PackageNotFoundError:
        return 'missing'


ALGORITHM_VERSION = (
    'analysis_trace_cluster.v1:agglomerative+lof;'
    'weights=categorical:0.35,route:0.25,tree:0.25,numeric:0.15;threshold=0.45;'
    f'deps=sklearn:{_version("scikit-learn")},apted:{_version("apted")},rapidfuzz:{_version("rapidfuzz")}'
)


def cluster_traces(classifications: tuple[Mapping[str, Any], ...]) -> dict[str, Any]:
    rows = sorted(
        (dict(row) for row in classifications if isinstance(row, Mapping)),
        key=lambda row: _text(row.get('case_id')),
    )
    if len(rows) != len(classifications):
        raise ValueError('analysis.trace_clusters classifications must all be mappings')
    if not rows:
        return _result(rows, [], [])
    if len(rows) < 5:
        labels = _small_labels(rows)
        matrix = np.zeros((len(rows), 1))
    else:
        matrix = _feature_matrix(rows)
        distances = _distances(rows, matrix)
        labels = _cluster_labels(distances)
    _assign_stable_ids(rows, labels)
    _assign_outliers(rows, distances if len(rows) >= 20 else None)
    groups = _groups(rows)
    for members in groups.values():
        for row in members:
            row['cluster_size'] = len(members)
            row['outlier'] = float(row.get('outlier_score') or 0.0) >= 0.8
    clusters = [
        _cluster_summary(cluster_id, members, matrix, rows)
        for cluster_id, members in sorted(groups.items())
    ]
    outliers = [
        {
            'case_id': row['case_id'],
            'cluster_id': row['cluster_id'],
            'outlier_score': row['outlier_score'],
        }
        for row in sorted(rows, key=lambda r: _text(r.get('case_id')))
        if row.get('outlier')
    ]
    return _result(rows, sorted(clusters, key=lambda c: c['cluster_id']), outliers)


def _result(
    rows: list[dict[str, Any]],
    clusters: list[dict[str, Any]],
    outliers: list[dict[str, Any]],
) -> dict[str, Any]:
    return {
        'id': 'analysis.trace_clusters',
        'total': len(rows),
        'algorithm_version': ALGORITHM_VERSION,
        'clusters': clusters,
        'outliers': outliers,
        'rows': [
            {
                'case_id': row.get('case_id', ''),
                'cluster_id': row.get('cluster_id', ''),
                'cluster_size': row.get('cluster_size', 0),
                'outlier': bool(row.get('outlier')),
                'outlier_score': row.get('outlier_score', 0.0),
                'issue_type': row.get('issue_type', ''),
                'affected_block': row.get('affected_block', ''),
                'failure_mode': row.get('failure_mode', ''),
                'route_signature': _trace(row).get('route_signature', ''),
            }
            for row in rows
        ],
    }


def _small_labels(rows: list[dict[str, Any]]) -> list[int]:
    labels: list[int] = []
    seen: dict[tuple[str, str, str, str], int] = {}
    for row in rows:
        key = (_text(row.get('issue_type')), _text(row.get('affected_block')), _text(row.get('failure_mode')),
               _text(_trace(row).get('route_signature')))
        seen.setdefault(key, len(seen))
        labels.append(seen[key])
    return labels


def _feature_matrix(rows: list[dict[str, Any]]) -> np.ndarray:
    raw = DictVectorizer(sparse=False).fit_transform([_feature_row(row) for row in rows])
    if raw.size == 0:
        return np.zeros((len(rows), 1))
    return StandardScaler().fit_transform(raw)


def _feature_row(row: Mapping[str, Any]) -> dict[str, float | str]:
    trace, judge = _trace(row), _mapping(row.get('judge'))
    features: dict[str, float | str] = {
        'question_type': _text(row.get('question_type') or _mapping(row.get('case')).get('question_type')),
        'issue_category': _text(row.get('issue_category')),
        'issue_type': _text(row.get('issue_type')),
        'affected_block': _text(row.get('affected_block')),
        'failure_mode': _text(row.get('failure_mode')),
        'confidence': _text(row.get('confidence')),
        'bottleneck_stage': _text(trace.get('bottleneck_stage')),
        'pending_analysis': float(bool(row.get('pending_analysis'))),
        'actionable': float(bool(row.get('actionable'))),
    }
    for key in ('answer_quality_score', 'retrieval_quality_score', 'overall_score', 'context_recall',
                'context_precision', 'chunk_recall', 'chunk_precision', 'doc_recall', 'doc_precision'):
        features[key] = _number(judge.get(key))
    trace_features = trace.get('features') if isinstance(trace.get('features'), Mapping) else {}
    for key in TRACE_FEATURES:
        features[f'trace.{key}'] = _number(trace_features.get(key))
    for stage in STAGES:
        features[f'trace.stage_count.{stage}'] = _number(trace_features.get(f'stage_count.{stage}'))
        features[f'trace.latency.{stage}'] = _number(trace_features.get(f'latency.{stage}'))
    return features


def _distances(rows: list[dict[str, Any]], matrix: np.ndarray) -> np.ndarray:
    return np.nan_to_num(
        0.35 * _categorical_distances(rows)
        + 0.25 * _route_distances(rows)
        + 0.25 * _tree_distances(rows)
        + 0.15 * pairwise_distances(matrix, metric='cosine')
    )


def _cluster_labels(distances: np.ndarray) -> np.ndarray:
    model = AgglomerativeClustering(
        n_clusters=None,
        distance_threshold=0.45,
        metric='precomputed',
        linkage='average',
    )
    return model.fit_predict(distances)


def _categorical_distances(rows: list[dict[str, Any]]) -> np.ndarray:
    fields = ('question_type', 'issue_category', 'issue_type', 'affected_block', 'failure_mode', 'confidence',
              'bottleneck_stage')
    values = [
        tuple(_text(row.get(field) or _trace(row).get(field)) for field in fields)
        for row in rows
    ]
    distances = np.zeros((len(rows), len(rows)))
    for i, left in enumerate(values):
        for j in range(i + 1, len(values)):
            value = sum(a != b for a, b in zip(left, values[j], strict=True)) / len(fields)
            distances[i, j] = distances[j, i] = value
    return distances


def _route_distances(rows: list[dict[str, Any]]) -> np.ndarray:
    distances = np.zeros((len(rows), len(rows)))
    for i, left in enumerate(rows):
        for j in range(i + 1, len(rows)):
            value = Levenshtein.normalized_distance(
                _text(_trace(left).get('route_signature')),
                _text(_trace(rows[j]).get('route_signature')),
            )
            distances[i, j] = distances[j, i] = float(value)
    return distances


def _tree_distances(rows: list[dict[str, Any]]) -> np.ndarray:
    trees = [_tree(_text(_trace(row).get('tree_text')) or '{unknown}') for row in rows]
    sizes = [max(1, (_text(_trace(row).get('tree_text')) or '{unknown}').count('{')) for row in rows]
    distances = np.zeros((len(rows), len(rows)))
    for i, left in enumerate(trees):
        for j in range(i + 1, len(trees)):
            distances[i, j] = distances[j, i] = APTED(left, trees[j]).compute_edit_distance() / max(sizes[i], sizes[j])
    return distances


def _assign_stable_ids(rows: list[dict[str, Any]], labels: list[int] | np.ndarray) -> None:
    grouped: dict[int, list[dict[str, Any]]] = defaultdict(list)
    for row, label in zip(rows, labels, strict=True):
        grouped[int(label)].append(row)
    ordered = sorted(grouped.values(), key=lambda members: (_fingerprint(members), _text(members[0].get('case_id'))))
    for index, members in enumerate(ordered, 1):
        for row in members:
            row['cluster_id'] = f'cluster_{index:04d}'


def _assign_outliers(rows: list[dict[str, Any]], distances: np.ndarray | None) -> None:
    if distances is None or len(rows) < 20:
        for row in rows:
            row['outlier_score'] = 0.0
        return
    lof = LocalOutlierFactor(n_neighbors=min(20, len(rows) - 1), metric='precomputed', contamination='auto')
    lof.fit_predict(distances)
    raw = -lof.negative_outlier_factor_
    low, high = float(np.min(raw)), float(np.max(raw))
    scores = [0.0 if high <= low else float((value - low) / (high - low)) for value in raw]
    for row, score in zip(rows, scores, strict=True):
        row['outlier_score'] = round(score, 4)


def _cluster_summary(cluster_id: str, members: list[dict[str, Any]], matrix: np.ndarray,
                     rows: list[dict[str, Any]]) -> dict[str, Any]:
    indices = [rows.index(row) for row in members]
    rep = members[0] if len(members) == 1 else rows[indices[int(np.argmin(
        pairwise_distances(matrix[indices], np.mean(matrix[indices], axis=0).reshape(1, -1)).ravel()
    ))]]
    issues = Counter(_text(row.get('issue_type')) for row in members)
    blocks = Counter(_text(row.get('affected_block')) for row in members)
    modes = Counter(_text(row.get('failure_mode')) for row in members)
    routes = Counter(_text(_trace(row).get('route_signature')) for row in members)
    return {
        'cluster_id': cluster_id,
        'size': len(members),
        'case_ids': [_text(row.get('case_id')) for row in members],
        'representative_case_id': _text(rep.get('case_id')),
        'dominant_issue_type': issues.most_common(1)[0][0],
        'dominant_affected_block': blocks.most_common(1)[0][0],
        'dominant_failure_mode': modes.most_common(1)[0][0],
        'common_route_signature': routes.most_common(1)[0][0] if routes else '',
        'issue_type_counts': dict(issues),
        'affected_block_counts': dict(blocks),
        'failure_mode_counts': dict(modes),
        'avg_overall_score': _avg(_mapping(row.get('judge')).get('overall_score') for row in members),
        'avg_retrieval_quality_score': _avg(
            _mapping(row.get('judge')).get('retrieval_quality_score')
            for row in members
        ),
        'avg_answer_quality_score': _avg(_mapping(row.get('judge')).get('answer_quality_score') for row in members),
    }


def _groups(rows: list[dict[str, Any]]) -> dict[str, list[dict[str, Any]]]:
    grouped: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        grouped[_text(row.get('cluster_id'))].append(row)
    return dict(grouped)


def _fingerprint(rows: list[Mapping[str, Any]]) -> tuple[str, str, str, str]:
    first = rows[0]
    return (_text(first.get('issue_type')), _text(first.get('affected_block')), _text(first.get('failure_mode')),
            _text(_trace(first).get('route_signature')))


def _tree(value: str) -> Tree:
    try:
        return Tree.from_text(value)
    except Exception:
        return Tree.from_text('{unknown}')


def _trace(row: Mapping[str, Any]) -> Mapping[str, Any]:
    return _mapping(row.get('trace_summary'))


def _mapping(value: object) -> Mapping[str, Any]:
    return value if isinstance(value, Mapping) else {}


def _avg(values: Any) -> float:
    rows = [_number(value) for value in values]
    return round(float(np.mean(rows)), 4) if rows else 0.0


def _number(value: Any) -> float:
    try:
        return round(float(value or 0.0), 4)
    except (TypeError, ValueError):
        return 0.0


def _text(value: Any) -> str:
    return str(value or '').strip()
