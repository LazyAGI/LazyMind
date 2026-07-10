from __future__ import annotations

import json
import math
from collections import Counter, defaultdict
from collections.abc import Mapping
from hashlib import sha1
from importlib.resources import files
from typing import Any

SCORE_KEYS = (
    'answer_correctness',
    'answer_relevance',
    'completeness',
    'groundedness',
    'format_compliance',
    'answer_quality_score',
    'retrieval_quality_score',
    'overall_score',
    'chunk_recall',
    'doc_recall',
)
REPAIRABLE_CATEGORIES = {'retrieval', 'generation', 'execution', 'tracing'}
FUNCTION_BLOCKS: Mapping[str, Mapping[str, Any]] = json.loads(
    files(__package__).joinpath('repair_block_registry.json').read_text(encoding='utf-8')
)


def build_repair_group_queue(rows: list[Mapping[str, Any]]) -> list[dict[str, Any]]:
    groups: dict[tuple[str, str, str, str, str], list[Mapping[str, Any]]] = defaultdict(list)
    for row in rows:
        if _is_repairable(row):
            groups[_group_key(row)].append(row)
    queue = [_group(block, mode, issue, cluster, route, items)
             for (block, mode, issue, cluster, route), items in groups.items()]
    return sorted(queue, key=lambda item: (
        -item['answer_impact_score'],
        -item['badcase_count'],
        -item['severity_score'],
        -item['confidence_score'],
        item['function_block_id'],
        item['failure_mode'],
        item['trace_signature'],
    ))


def _group_key(row: Mapping[str, Any]) -> tuple[str, str, str, str, str]:
    trace = row.get('trace_summary') if isinstance(row.get('trace_summary'), Mapping) else {}
    return (
        _text(row.get('affected_block')),
        _text(row.get('failure_mode')),
        _text(row.get('issue_type')),
        _text(row.get('cluster_id')),
        _text(trace.get('route_signature')),
    )


def _group(block: str, mode: str, issue: str, cluster: str, route: str,
           rows: list[Mapping[str, Any]]) -> dict[str, Any]:
    registry = FUNCTION_BLOCKS.get(block, {})
    case_ids = [_text(row.get('case_id')) for row in rows if _text(row.get('case_id'))]
    gid = _stable_id({
        'block': block,
        'mode': mode,
        'issue': issue,
        'cluster': cluster,
        'route': route,
        'case_ids': sorted(case_ids),
    })[:12]
    return {
        'group_id': f'grp_{gid}',
        'function_block_id': block,
        'affected_block': block,
        'failure_mode': mode,
        'issue_type': issue,
        'issue_category': _text(rows[0].get('issue_category')) if rows else '',
        'trace_cluster_id': cluster,
        'trace_signature': route,
        'badcase_count': len(rows),
        'case_ids': case_ids,
        'representative_case_id': case_ids[0] if case_ids else '',
        'answer_impact_score': round(max((_answer_gap(row) for row in rows), default=0.0), 4),
        'confidence_score': round(sum(_confidence(row) for row in rows) / len(rows), 4),
        'severity_score': round(sum(_severity(row) for row in rows) / len(rows), 4),
        'candidate_files': list(registry.get('entrypoints') or ()),
        'adjacent_blocks': list(registry.get('adjacent_blocks') or ()),
        'primary_metrics': list(registry.get('primary_metrics') or _metric_focus(rows)),
        'guard_metrics': list(registry.get('guard_metrics') or ('answer_correctness',)),
        'invariants': list(registry.get('invariants') or ()),
        'evidence': [_case_evidence(row) for row in rows],
    }


def _is_repairable(row: Mapping[str, Any]) -> bool:
    if row.get('actionable') is not True:
        return False
    return (
        _text(row.get('issue_category')) in REPAIRABLE_CATEGORIES
        and _text(row.get('affected_block')) not in {'', 'undetermined', 'runtime_infra', 'eval_contract'}
        and _text(row.get('failure_mode')) not in {'', 'correct', 'insufficient_evidence'}
        and not bool(row.get('pending_analysis'))
    )


def _case_evidence(row: Mapping[str, Any]) -> dict[str, Any]:
    case = row.get('case') if isinstance(row.get('case'), Mapping) else {}
    answer = row.get('rag_answer') if isinstance(row.get('rag_answer'), Mapping) else {}
    judge = row.get('judge') if isinstance(row.get('judge'), Mapping) else {}
    trace = row.get('trace_summary') if isinstance(row.get('trace_summary'), Mapping) else {}
    return {
        'case_id': _text(row.get('case_id')),
        'question_type': _text(case.get('question_type')),
        'question': _clip(case.get('question'), 280),
        'reference_answer': _clip(case.get('answer'), 420),
        'actual_answer': _clip(answer.get('answer'), 420),
        'judge_reason': _clip(judge.get('reason'), 320),
        'metrics': {key: judge.get(key) for key in SCORE_KEYS if key in judge},
        'reference_doc_ids': _list(case.get('reference_doc_ids'))[:8],
        'reference_chunk_ids': _list(case.get('reference_chunk_ids'))[:8],
        'actual_doc_ids': _list(answer.get('doc_ids'))[:8],
        'actual_chunk_ids': _list(answer.get('chunk_ids'))[:8],
        'tool_errors': _list(answer.get('tool_errors'))[:3],
        'trace': {
            'trace_id': _text(trace.get('trace_id') or answer.get('trace_id')),
            'cluster_id': _text(row.get('cluster_id')),
            'route_signature': _text(trace.get('route_signature')),
            'diagnostic_stage_sequence': _list(trace.get('diagnostic_stage_sequence'))[:12],
            'error_stages': _list(trace.get('error_stages'))[:3],
            'final_context_doc_ids': _list(trace.get('final_context_doc_ids'))[:8],
            'final_context_chunk_ids': _list(trace.get('final_context_chunk_ids'))[:8],
        },
    }


def _metric_focus(rows: list[Mapping[str, Any]]) -> list[str]:
    totals = Counter()
    for row in rows:
        judge = row.get('judge') if isinstance(row.get('judge'), Mapping) else {}
        for key in SCORE_KEYS:
            if _score(judge.get(key), 1.0) < 0.7:
                totals[key] += 1
    return [key for key, _ in totals.most_common(3)] or ['answer_correctness']


def _severity(row: Mapping[str, Any]) -> float:
    category = _text(row.get('issue_category'))
    judge = row.get('judge') if isinstance(row.get('judge'), Mapping) else {}
    answer_gap = _answer_gap(row)
    retrieval_gap = 1.0 - _score(judge.get('retrieval_quality_score'), 1.0)
    weight = {'execution': 1.0, 'retrieval': 0.9, 'generation': 0.8, 'tracing': 0.4}.get(category, 0.2)
    retrieval_signal = retrieval_gap if category == 'retrieval' else 0.0
    return round((0.55 * answer_gap) + (0.25 * retrieval_signal) + (0.10 * weight) + (0.10 * _confidence(row)), 4)


def _answer_gap(row: Mapping[str, Any]) -> float:
    judge = row.get('judge') if isinstance(row.get('judge'), Mapping) else {}
    return max(
        1.0 - _score(judge.get('answer_correctness'), 0.0),
        1.0 - _score(judge.get('answer_quality_score'), 0.0),
        1.0 - _score(judge.get('overall_score'), 0.0),
    )


def _confidence(row: Mapping[str, Any]) -> float:
    return {'high': 1.0, 'medium': 0.65, 'low': 0.25}.get(_text(row.get('confidence')), 0.0)


def _score(value: Any, default: float) -> float:
    try:
        number = float(value)
    except (TypeError, ValueError):
        return default
    return number if math.isfinite(number) else default


def _list(value: Any) -> list[Any]:
    if isinstance(value, list):
        return value
    if isinstance(value, tuple):
        return list(value)
    if value in (None, ''):
        return []
    return [value]


def _clip(value: Any, limit: int) -> str:
    text = _text(value)
    return text if len(text) <= limit else text[:limit - 3] + '...'


def _text(value: Any) -> str:
    return str(value or '').strip()


def _stable_id(value: Mapping[str, Any]) -> str:
    return sha1(json.dumps(value, ensure_ascii=False, sort_keys=True).encode()).hexdigest()
