from __future__ import annotations

import json
import math
from collections import Counter, defaultdict
from collections.abc import Mapping
from hashlib import sha1
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
FUNCTION_BLOCKS: Mapping[str, Mapping[str, Any]] = {
    'request_intake_routing': {
        'entrypoints': ('lazymind/chat/api/chat_routes.py', 'lazymind/chat/service/chat_service.py'),
        'adjacent_blocks': ('query_rewrite', 'tool_orchestration', 'tracing_observability'),
        'primary_metrics': ('overall_score', 'answer_relevance'),
        'guard_metrics': ('answer_correctness',),
    },
    'query_rewrite': {
        'entrypoints': ('lazymind/chat/service/chat_service.py', 'lazymind/chat/engine/prompts/guidance.py'),
        'adjacent_blocks': ('retrieval', 'prompt_build'),
        'primary_metrics': ('answer_relevance', 'retrieval_quality_score'),
        'guard_metrics': ('answer_correctness', 'chunk_recall'),
    },
    'retrieval': {
        'entrypoints': (
            'lazymind/chat/engine/tools/kb.py',
            'lazymind/chat/engine/tools/algo/search_kb.py',
            'lazymind/chat/engine/tools/algo/kb_adaptive_topk.py',
            'lazymind/chat/engine/tools/infra/kb_opensearch_client.py',
        ),
        'adjacent_blocks': ('context_assembly', 'rerank', 'tracing_observability'),
        'primary_metrics': ('chunk_recall', 'doc_recall', 'retrieval_quality_score'),
        'guard_metrics': ('answer_correctness', 'chunk_precision'),
    },
    'rerank': {
        'entrypoints': ('lazymind/chat/engine/tools/kb.py', 'lazymind/chat/engine/tools/algo/kb_adaptive_topk.py'),
        'adjacent_blocks': ('retrieval', 'context_assembly'),
        'primary_metrics': ('chunk_precision', 'doc_precision', 'retrieval_quality_score'),
        'guard_metrics': ('chunk_recall', 'answer_correctness'),
    },
    'context_assembly': {
        'entrypoints': (
            'lazymind/chat/engine/tools/kb.py',
            'lazymind/chat/engine/tools/algo/kb_context_expansion.py',
            'lazymind/chat/service/utils/citations.py',
        ),
        'adjacent_blocks': ('retrieval', 'rerank', 'llm_generation', 'prompt_build'),
        'primary_metrics': ('chunk_recall', 'groundedness', 'answer_correctness'),
        'guard_metrics': ('chunk_precision', 'answer_relevance'),
    },
    'prompt_build': {
        'entrypoints': (
            'lazymind/chat/engine/prompts/system_prompt.py',
            'lazymind/chat/engine/prompts/guidance.py',
            'lazymind/chat/service/chat_service.py',
        ),
        'adjacent_blocks': ('context_assembly', 'llm_generation'),
        'primary_metrics': ('answer_relevance', 'groundedness', 'format_compliance'),
        'guard_metrics': ('answer_correctness', 'chunk_recall'),
    },
    'tool_orchestration': {
        'entrypoints': (
            'lazymind/chat/engine/agent_core.py',
            'lazymind/chat/service/component/tool_registry.py',
            'lazymind/chat/engine/tools/infra/tool_runtime.py',
            'lazymind/chat/service/chat_service.py',
        ),
        'adjacent_blocks': ('retrieval', 'prompt_build', 'llm_generation'),
        'primary_metrics': ('overall_score', 'answer_correctness'),
        'guard_metrics': ('format_compliance', 'answer_relevance'),
    },
    'llm_generation': {
        'entrypoints': (
            'lazymind/chat/engine/agent_core.py',
            'lazymind/chat/engine/prompts/system_prompt.py',
            'lazymind/chat/engine/prompts/guidance.py',
            'lazymind/chat/service/chat_service.py',
        ),
        'adjacent_blocks': ('context_assembly', 'prompt_build', 'postprocess_serialization'),
        'primary_metrics': ('answer_correctness', 'groundedness', 'answer_relevance'),
        'guard_metrics': ('format_compliance', 'chunk_recall'),
    },
    'postprocess_serialization': {
        'entrypoints': (
            'lazymind/chat/api/chat_routes.py',
            'lazymind/chat/service/chat_service.py',
            'lazymind/chat/service/component/event_translator.py',
            'lazymind/chat/service/utils/streaming.py',
        ),
        'adjacent_blocks': ('llm_generation', 'tracing_observability'),
        'primary_metrics': ('format_compliance', 'answer_quality_score'),
        'guard_metrics': ('answer_correctness', 'groundedness'),
    },
    'tracing_observability': {
        'entrypoints': (
            'lazymind/chat/service/utils/trace_archive.py',
            'lazymind/chat/service/component/event_translator.py',
            'lazymind/chat/service/chat_service.py',
        ),
        'adjacent_blocks': ('retrieval', 'context_assembly', 'postprocess_serialization'),
        'primary_metrics': ('overall_score',),
        'guard_metrics': ('answer_correctness',),
    },
}


def build_repair_group_queue(rows: list[Mapping[str, Any]]) -> list[dict[str, Any]]:
    groups: dict[tuple[str, str, str, str, str], list[Mapping[str, Any]]] = defaultdict(list)
    for row in rows:
        if _is_repairable(row):
            groups[_group_key(row)].append(row)
    queue = [_group(block, mode, issue, cluster, route, items)
             for (block, mode, issue, cluster, route), items in groups.items()]
    return sorted(queue, key=lambda item: (
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
        'confidence_counts': dict(Counter(_text(row.get('confidence')) for row in rows)),
        'confidence_score': round(sum(_confidence(row) for row in rows) / len(rows), 4),
        'severity_score': round(sum(_severity(row) for row in rows) / len(rows), 4),
        'candidate_files': list(registry.get('entrypoints') or ()),
        'adjacent_blocks': list(registry.get('adjacent_blocks') or ()),
        'primary_metrics': list(registry.get('primary_metrics') or _metric_focus(rows)),
        'guard_metrics': list(registry.get('guard_metrics') or ('answer_correctness',)),
        'risk': _risk(block, bool(registry), rows),
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
    score = 1.0 - _score(judge.get('answer_correctness'), 0.0)
    weight = {'execution': 1.0, 'retrieval': 0.9, 'generation': 0.8, 'tracing': 0.4}.get(category, 0.2)
    return round((0.6 * weight) + (0.25 * score) + (0.15 * _confidence(row)), 4)


def _confidence(row: Mapping[str, Any]) -> float:
    return {'high': 1.0, 'medium': 0.65, 'low': 0.25}.get(_text(row.get('confidence')), 0.0)


def _risk(block: str, known_block: bool, rows: list[Mapping[str, Any]]) -> str:
    if not known_block:
        return 'high_no_registry_entry'
    if block in {'llm_generation', 'postprocess_serialization', 'prompt_build'}:
        return 'medium_user_visible_behavior'
    if any(_text(row.get('confidence')) == 'low' for row in rows):
        return 'medium_mixed_confidence'
    return 'low_scoped_patch'


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
