from __future__ import annotations

from collections import Counter
from collections.abc import Mapping
from typing import Any

from evo.operations.common import text


def analysis_summary(classifications: Mapping[str, Any], clusters: Mapping[str, Any]) -> dict[str, Any]:
    rows = [dict(item) for _, item in sorted(classifications.items()) if isinstance(item, Mapping)]
    cluster_rows = {
        text(item.get('case_id')): item
        for item in clusters.get('rows', [])
        if isinstance(item, Mapping)
    } if isinstance(clusters, Mapping) else {}
    for row in rows:
        cluster = cluster_rows.get(text(row.get('case_id')), {})
        row['cluster_id'] = text(cluster.get('cluster_id'))
        row['outlier_score'] = cluster.get('outlier_score', 0.0)
    trace_available = sum(1 for row in rows if _trace(row).get('trace_available'))
    fine_counts = Counter(text(row.get('fine_category')) for row in rows)
    return {
        'id': 'analysis.summary',
        'case_ids': [text(row.get('case_id')) for row in rows],
        'total': len(rows),
        'category_counts': dict(Counter(text(row.get('coarse_category')) for row in rows)),
        'fine_category_counts': dict(fine_counts),
        'trace_coverage': {
            'available': trace_available,
            'missing': len(rows) - trace_available,
            'total': len(rows),
        },
        'repairable_cases': [
            {
                'case_id': row['case_id'],
                'category': row['coarse_category'],
                'fine_category': row['fine_category'],
                'reason': row.get('root_cause_reason') or row.get('reason', ''),
                'diagnosis_features': list(row.get('diagnosis_features') or []),
                'confidence': row.get('confidence', ''),
                'cluster_id': row.get('cluster_id', ''),
                'recommended_action': row.get('recommended_action', ''),
            }
            for row in rows
            if row.get('repairable') and not row.get('pending_analysis')
        ],
        'infra_failures': [row['case_id'] for row in rows if row.get('coarse_category') == 'infra_failure'],
        'top_failure_patterns': _top_failure_patterns(rows, clusters),
        'clusters': list(clusters.get('clusters') or []) if isinstance(clusters, Mapping) else [],
        'llm_analysis_queue': [
            {
                'case_id': row['case_id'],
                'fine_category': row.get('fine_category', ''),
                'reason': row.get('llm_analysis_reason', ''),
                'cluster_id': row.get('cluster_id', ''),
            }
            for row in rows
            if row.get('llm_analysis_required')
        ],
        'rows': rows,
    }


def _top_failure_patterns(rows: list[dict[str, Any]], clusters: Mapping[str, Any]) -> list[dict[str, Any]]:
    cluster_items = clusters.get('clusters') if isinstance(clusters, Mapping) else []
    if cluster_items:
        return [
            {
                'pattern': text(item.get('dominant_fine_category')),
                'cluster_id': text(item.get('cluster_id')),
                'case_count': int(item.get('size') or 0),
                'representative_case_id': text(item.get('representative_case_id')),
            }
            for item in cluster_items
            if isinstance(item, Mapping) and text(item.get('dominant_fine_category')) != 'correct'
        ][:5]
    counts = Counter(text(row.get('fine_category')) for row in rows if text(row.get('fine_category')) != 'correct')
    return [
        {'pattern': pattern, 'case_count': count, 'cluster_id': '', 'representative_case_id': ''}
        for pattern, count in counts.most_common(5)
    ]


def _trace(row: Mapping[str, Any]) -> Mapping[str, Any]:
    value = row.get('trace_summary')
    return value if isinstance(value, Mapping) else {}
