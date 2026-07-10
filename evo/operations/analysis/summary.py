from __future__ import annotations

from collections import Counter
from collections.abc import Mapping
from typing import Any

from .classify import classify_case
from .cluster import cluster_traces
from .repair_groups import build_repair_group_queue
from .trace_summary import build_trace_summary


def build_analysis_detail(
    classifications: tuple[Mapping[str, Any], ...],
    clusters: Mapping[str, Any],
) -> dict[str, Any]:
    rows = sorted(
        (dict(row) for row in classifications if isinstance(row, Mapping)),
        key=lambda row: _text(row.get('case_id')),
    )
    if len(rows) != len(classifications):
        raise ValueError('analysis.summary classifications must all be mappings')
    cluster_items = [
        row for row in clusters.get('rows', ())
        if isinstance(row, Mapping) and row.get('case_id')
    ]
    cluster_ids = [_text(row.get('case_id')) for row in cluster_items]
    row_ids = [_text(row.get('case_id')) for row in rows]
    if len(set(cluster_ids)) != len(cluster_ids) or set(cluster_ids) != set(row_ids):
        raise ValueError('analysis.summary cluster rows must cover every classification')
    if int(clusters.get('total') or 0) != len(rows):
        raise ValueError('analysis.summary cluster total must match classifications')
    cluster_rows = {_text(row.get('case_id')): row for row in cluster_items}
    for row in rows:
        cluster = cluster_rows[_text(row.get('case_id'))]
        row['cluster_id'] = _text(cluster.get('cluster_id'))
        row['outlier_score'] = float(cluster.get('outlier_score') or 0.0)
    actionable = [case_brief(row) for row in rows if row.get('actionable')]
    pending = [case_brief(row) for row in rows if row.get('pending_analysis')]
    runtime = [case_brief(row) for row in rows if row.get('issue_category') == 'runtime_infra']
    contract = [case_brief(row) for row in rows if row.get('issue_category') == 'contract']
    return {
        'id': 'analysis.summary',
        'case_ids': [_text(row.get('case_id')) for row in rows],
        'total': len(rows),
        'issue_category_counts': dict(Counter(_text(row.get('issue_category')) for row in rows)),
        'issue_type_counts': dict(Counter(_text(row.get('issue_type')) for row in rows)),
        'affected_block_counts': dict(Counter(_text(row.get('affected_block')) for row in rows)),
        'failure_mode_counts': dict(Counter(_text(row.get('failure_mode')) for row in rows)),
        'trace_quality': trace_quality(rows),
        'actionable_cases': actionable,
        'pending_cases': pending,
        'runtime_infra_cases': runtime,
        'contract_cases': contract,
        'top_failure_patterns': top_failure_patterns(rows, clusters),
        'repair_group_queue': build_repair_group_queue(rows),
        'clusters': list(clusters.get('clusters') or []),
        'rows': rows,
        'checks': {
            'ready': True,
            'errors': [],
            'case_count_matches': len(rows) == int(clusters.get('total') or 0),
        },
    }


def build_analysis_summary(
    run_id: str,
    classifications: tuple[Mapping[str, Any], ...],
    clusters: Mapping[str, Any],
) -> dict[str, Any]:
    _validate_clusters(classifications, clusters)
    return build_analysis_detail(classifications, clusters) | {'run_id': str(run_id)}


def build_analysis_from_answers(
    cases: Mapping[str, Mapping[str, Any]],
    answers: Mapping[str, Mapping[str, Any]],
    judges: Mapping[str, Mapping[str, Any]],
) -> dict[str, Any]:
    classifications = []
    for case_id, case in cases.items():
        trace = build_trace_summary(case, answers[case_id])
        classifications.append(classify_case(case, answers[case_id], judges[case_id], trace))
    clusters = cluster_traces(tuple(classifications))
    return build_analysis_detail(tuple(classifications), clusters)


def _validate_clusters(classifications: tuple[Mapping[str, Any], ...], clusters: Mapping[str, Any]) -> None:
    rows = [row for row in classifications if isinstance(row, Mapping)]
    if len(rows) != len(classifications):
        raise ValueError('analysis.summary classifications must all be mappings')
    cluster_items = [row for row in clusters.get('rows', ()) if isinstance(row, Mapping) and row.get('case_id')]
    cluster_ids = [_text(row.get('case_id')) for row in cluster_items]
    row_ids = [_text(row.get('case_id')) for row in rows]
    if len(set(cluster_ids)) != len(cluster_ids) or set(cluster_ids) != set(row_ids):
        raise ValueError('analysis.summary cluster rows must cover every classification')
    if int(clusters.get('total') or 0) != len(rows):
        raise ValueError('analysis.summary cluster total must match classifications')


def case_brief(row: Mapping[str, Any]) -> dict[str, Any]:
    return {
        'case_id': _text(row.get('case_id')),
        'issue_type': _text(row.get('issue_type')),
        'affected_block': _text(row.get('affected_block')),
        'failure_mode': _text(row.get('failure_mode')),
        'confidence': _text(row.get('confidence')),
        'reason': _text(row.get('root_cause_reason')),
        'cluster_id': _text(row.get('cluster_id')),
        'outlier_score': float(row.get('outlier_score') or 0.0),
    }


def trace_quality(rows: list[Mapping[str, Any]]) -> dict[str, Any]:
    traces = [_mapping(row.get('trace_summary')) for row in rows]
    features = [_mapping(trace.get('features')) for trace in traces]
    total = len(rows)
    unavailable = [_text(row.get('case_id')) for row in rows
                   if _mapping(row.get('trace_summary')).get('route_signature') == 'trace_unavailable'
                   or _mapping(row.get('trace_summary')).get('trace_status') == 'unavailable']
    return {
        'total': total,
        'complete': total - len(unavailable),
        'trace_unavailable': unavailable,
        'stage_unknown': [_text(row.get('case_id')) for row in rows
                          if _mapping(row.get('trace_summary')).get('unknown_stage_count')],
        'metrics_missing': [_text(row.get('case_id')) for row in rows
                            if row.get('issue_type') == 'trace_metrics_missing'],
        'error_stage_present': [_text(row.get('case_id')) for row in rows
                                if _mapping(row.get('trace_summary')).get('error_stages')],
        'avg_node_count': _avg(item.get('node_count') for item in features),
        'avg_trace_latency_ms': _avg(item.get('trace_latency_ms') for item in features),
    }


def top_failure_patterns(rows: list[Mapping[str, Any]], clusters: Mapping[str, Any]) -> list[dict[str, Any]]:
    cluster_items = [item for item in clusters.get('clusters', ()) if isinstance(item, Mapping)]
    if cluster_items:
        patterns = [
            {
                'pattern': '/'.join(_text(item.get(key)) for key in (
                    'dominant_affected_block', 'dominant_failure_mode',
                )),
                'cluster_id': _text(item.get('cluster_id')),
                'case_count': int(item.get('size') or 0),
                'representative_case_id': _text(item.get('representative_case_id')),
            }
            for item in cluster_items
            if _text(item.get('dominant_issue_type')) != 'correct'
        ]
        return patterns[:10]
    counts = Counter(
        (_text(row.get('affected_block')), _text(row.get('failure_mode')))
        for row in rows
        if row.get('issue_type') != 'correct'
    )
    return [
        {'pattern': f'{block}/{mode}', 'cluster_id': '', 'case_count': count, 'representative_case_id': ''}
        for (block, mode), count in counts.most_common(10)
    ]


def _mapping(value: object) -> Mapping[str, Any]:
    return value if isinstance(value, Mapping) else {}


def _avg(values: Any) -> float:
    rows = []
    for value in values:
        try:
            rows.append(float(value or 0.0))
        except (TypeError, ValueError):
            pass
    return round(sum(rows) / len(rows), 4) if rows else 0.0


def _text(value: Any) -> str:
    return str(value or '').strip()
