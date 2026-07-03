from __future__ import annotations

import json
from collections.abc import Mapping
from hashlib import sha1
from typing import Any

DEFAULT_ALLOWED_ROOTS = ('lazymind/chat',)
DEFAULT_BLOCKED_ROOTS = ('tests', '.git', 'lazyllm', 'evo', 'data')
POLICY_VIEW_KEYS = (
    'allowed_roots',
    'blocked_roots',
    'target_case_budget',
    'neighbor_case_budget',
    'goodcase_guard_budget',
    'cross_block_guard_budget',
    'evidence_case_budget',
    'seed_file_budget',
)
BLOCK_INVARIANTS = {
    'retrieval': ('do not change eval policy, case data, reference answers, or document ids',
                  'preserve chat API request/response contract and trace emission'),
    'context_assembly': ('preserve retrieved source attribution',
                         'do not synthesize unseen source chunks or document ids'),
    'rerank': ('preserve recall before optimizing precision',
               'do not hide low confidence retrieval evidence'),
    'prompt_build': ('do not lower safety, grounding, or tool-use constraints',
                     'do not hard-code case questions, answers, or expected ids'),
    'llm_generation': ('do not lower safety, grounding, or tool-use constraints',
                       'do not hard-code case questions, answers, or expected ids'),
    'postprocess_serialization': ('preserve SSE protocol compatibility',
                                  'do not change evaluator schemas to mask output defects'),
    'tracing_observability': ('preserve trace ids across answer and analysis',
                              'do not fabricate trace stages or metrics'),
}


def build_repair_plan(analysis: Mapping[str, Any], policy: Mapping[str, Any]) -> dict[str, Any]:
    rows = [row for row in _items(analysis.get('rows')) if isinstance(row, Mapping)]
    allowed_roots = _roots(policy.get('allowed_roots'), DEFAULT_ALLOWED_ROOTS)
    blocked_roots = _roots(policy.get('blocked_roots'), DEFAULT_BLOCKED_ROOTS)
    budgets = {
        'target_case_budget': _bounded_int(policy.get('target_case_budget'), 8, 1, 30),
        'neighbor_case_budget': _bounded_int(policy.get('neighbor_case_budget'), 3, 0, 12),
        'goodcase_guard_budget': _bounded_int(policy.get('goodcase_guard_budget'), 2, 0, 12),
        'cross_block_guard_budget': _bounded_int(policy.get('cross_block_guard_budget'), 2, 0, 12),
        'evidence_case_budget': _bounded_int(policy.get('evidence_case_budget'), 8, 1, 30),
        'seed_file_budget': _bounded_int(policy.get('seed_file_budget'), 5, 1, 10),
    }
    policy_view = {'allowed_roots': allowed_roots, 'blocked_roots': blocked_roots}
    policy_view.update({key: budgets[key] for key in POLICY_VIEW_KEYS if key in budgets and key in policy})

    def empty_plan(code: str, blocked: bool = False) -> dict[str, Any]:
        return {
            'id': 'repair.plan',
            'status': 'blocked' if blocked else code,
            'blocked_reason': code if blocked else '',
            'repair_group_queue': [],
            'selected_group': {},
            'objective': {},
            'brief': {},
            'policy': policy_view,
            'analysis_summary': {'total': len(rows)},
            'events': [_event('group_selected', 'skipped', '', '', [], code)],
            'checks': {'ready': False, 'errors': [{'code': code}]},
        }

    if not rows:
        return empty_plan('skipped_no_analysis_rows')
    if 'repair_group_queue' not in analysis:
        return empty_plan('blocked_missing_repair_group_queue', blocked=True)

    groups = []
    for raw_group in _items(analysis.get('repair_group_queue')):
        if not isinstance(raw_group, Mapping):
            continue
        case_ids = [str(item or '').strip() for item in _items(raw_group.get('case_ids'))
                    if str(item or '').strip()]
        group_id = str(raw_group.get('group_id') or '').strip()
        if not group_id:
            group_id = 'malformed_' + sha1(json.dumps(
                dict(raw_group),
                default=str,
                ensure_ascii=False,
                sort_keys=True,
            ).encode()).hexdigest()[:12]
        block_id = str(raw_group.get('function_block_id') or '').strip()
        issue_type = str(raw_group.get('issue_type') or '').strip()
        failure_mode = str(raw_group.get('failure_mode') or '').strip()
        representative = str(raw_group.get('representative_case_id') or (case_ids[0] if case_ids else '')).strip()
        confidence = _number(raw_group.get('confidence_score'), 0.0)
        badcase_count = int(_number(raw_group.get('badcase_count'), 0.0))
        missing = [
            name for name, value in (
                ('group_id', raw_group.get('group_id')),
                ('function_block_id', block_id),
                ('case_ids', case_ids),
                ('badcase_count', badcase_count),
                ('issue_type', issue_type),
                ('failure_mode', failure_mode),
            )
            if not value
        ]
        candidate_files = []
        for item in _items(raw_group.get('candidate_files')):
            path = _path(item)
            allowed = any(path == root or path.startswith(f'{root}/') for root in allowed_roots)
            blocked = any(path == root or path.startswith(f'{root}/') for root in blocked_roots)
            if path and allowed and not blocked:
                candidate_files.append(path)

        group = {
            **dict(raw_group),
            'group_id': group_id,
            'function_block_id': block_id or 'unknown',
            'issue_type': issue_type or 'unknown',
            'failure_mode': failure_mode or 'unknown',
            'representative_case_id': representative,
            'case_ids': case_ids,
            'candidate_files': list(dict.fromkeys(candidate_files)),
            'evidence': [dict(row) for row in _items(raw_group.get('evidence'))
                         if isinstance(row, Mapping)][:budgets['evidence_case_budget']],
            'confidence_score': confidence,
            'badcase_count': badcase_count,
        }
        if missing:
            reason = f"malformed_repair_group_missing_{'_'.join(missing)}"
        elif not group['candidate_files']:
            reason = 'function block has no editable candidate files in allowed roots'
        elif confidence < 0.5:
            reason = 'selected group confidence below patch threshold'
        elif group.get('issue_category') == 'tracing' and badcase_count < 2:
            reason = 'single tracing-only badcase requires deeper analysis before patch'
        else:
            reason = ''
        group['deeper_analysis_reason'] = reason
        groups.append(group)

    if not groups:
        return empty_plan('skipped_no_repairable_group')

    selected = groups[0]
    selected_ids = set(selected.get('case_ids') or ())
    block = str(selected.get('function_block_id') or '').strip()
    adjacent = {str(item or '').strip() for item in selected.get('adjacent_blocks') or () if str(item or '').strip()}
    cluster = str(selected.get('trace_cluster_id') or '').strip()
    route = str(selected.get('trace_signature') or '').strip()

    target = list(selected.get('case_ids') or [])[:budgets['target_case_budget']]
    used = set(target)
    neighbor, good, cross = [], [], []
    for row in rows:
        case_id = str(row.get('case_id') or '').strip()
        trace = row.get('trace_summary') if isinstance(row.get('trace_summary'), Mapping) else {}
        same_trace = (
            (cluster and str(row.get('cluster_id') or '').strip() == cluster)
            or (route and str(trace.get('route_signature') or '').strip() == route)
        )
        issue = str(row.get('issue_type') or '').strip()
        affected = str(row.get('affected_block') or '').strip()
        if (
            case_id and case_id not in used and case_id not in selected_ids and issue != 'correct'
            and (affected in adjacent or same_trace)
        ):
            used.add(case_id)
            neighbor.append(case_id)
            if len(neighbor) >= budgets['neighbor_case_budget']:
                break
    for row in rows:
        case_id = str(row.get('case_id') or '').strip()
        affected = str(row.get('affected_block') or '').strip()
        if (
            case_id and case_id not in used and str(row.get('issue_type') or '').strip() == 'correct'
            and (affected in adjacent or affected in {block, 'not_applicable'})
        ):
            used.add(case_id)
            good.append(case_id)
            if len(good) >= budgets['goodcase_guard_budget']:
                break
    for group in groups:
        if group.get('group_id') == selected.get('group_id') or group.get('deeper_analysis_reason'):
            continue
        for case_id in group.get('case_ids') or ():
            if case_id and case_id not in used:
                used.add(case_id)
                cross.append(case_id)
                if len(cross) >= budgets['cross_block_guard_budget']:
                    break
        if len(cross) >= budgets['cross_block_guard_budget']:
            break

    validation_cases = list(dict.fromkeys([*target, *neighbor, *good, *cross]))
    objective = {
        'selected_group_id': selected['group_id'],
        'function_block_id': block,
        'selection_reason': 'highest badcase_count in analysis repair_group_queue',
        'target_cases': target,
        'neighbor_cases': neighbor,
        'goodcase_guard_cases': good,
        'cross_block_guard_cases': cross,
        'validation_case_ids': validation_cases,
        'group_rank': 1,
        'candidate_group_count': len(groups),
    }

    blocked_reason = selected.get('deeper_analysis_reason') or ''
    invariants = list(dict.fromkeys([
        *list(BLOCK_INVARIANTS.get(block, ())),
        'do not edit eval, analysis, dataset, tests, secrets, generated data, or vendored lazyllm',
        'do not hard-code case ids, questions, answers, document ids, chunk ids, or metric thresholds',
    ]))
    brief = {
        'function_block_id': block,
        'group_id': selected['group_id'],
        'symptom': {
            'issue_type': selected['issue_type'],
            'failure_mode': selected['failure_mode'],
            'badcase_count': selected['badcase_count'],
            'representative_case_id': selected['representative_case_id'],
        },
        'patch_intent': (
            f"make the smallest code change in {block} that reduces {selected['failure_mode']} "
            'for the selected trace-derived badcase group'
        ),
        'pre_patch_level': 0 if not blocked_reason else 2,
        'expansion_notes': [],
        'evidence_cases': list(selected.get('evidence') or []),
        'target_files': list(selected.get('candidate_files') or []),
        'seed_files': list(selected.get('candidate_files') or [])[
            :budgets['seed_file_budget']
        ],
        'allowed_roots': allowed_roots,
        'blocked_roots': blocked_roots,
        'primary_metrics': list(selected.get('primary_metrics') or ()),
        'guard_metrics': list(selected.get('guard_metrics') or ()),
        'invariants': invariants,
        'validation_focus': [
            f"target group {selected['group_id']} must improve or resolve",
            f"primary metrics: {', '.join(selected.get('primary_metrics') or [])}",
            f"guard metrics: {', '.join(selected.get('guard_metrics') or [])}",
            f"validation cases: {', '.join(validation_cases)}",
        ],
        'rollback_triggers': [
            'empty diff',
            'blocked root touched',
            'compile or smoke validation failed',
            'candidate service cannot start or route to algorithm_id',
            'candidate analysis shows target group unchanged or regressed',
            'goodcase guard regression',
            'new high-severity repair group appears',
        ],
        'needs_deeper_analysis': bool(blocked_reason),
        'deeper_analysis_reason': blocked_reason,
    }

    return {
        'id': 'repair.plan',
        'status': 'blocked' if blocked_reason else 'planned',
        'blocked_reason': blocked_reason,
        'repair_group_queue': groups,
        'selected_group': selected,
        'objective': objective,
        'brief': brief,
        'policy': policy_view,
        'analysis_summary': {
            'id': str(analysis.get('id') or '').strip(),
            'total': int(_number(analysis.get('total'), 0.0)),
            'issue_category_counts': dict(analysis.get('issue_category_counts'))
            if isinstance(analysis.get('issue_category_counts'), Mapping) else {},
            'issue_type_counts': dict(analysis.get('issue_type_counts'))
            if isinstance(analysis.get('issue_type_counts'), Mapping) else {},
            'affected_block_counts': dict(analysis.get('affected_block_counts'))
            if isinstance(analysis.get('affected_block_counts'), Mapping) else {},
            'failure_mode_counts': dict(analysis.get('failure_mode_counts'))
            if isinstance(analysis.get('failure_mode_counts'), Mapping) else {},
            'top_failure_patterns': _items(analysis.get('top_failure_patterns'))[:10],
            'trace_quality': dict(analysis.get('trace_quality'))
            if isinstance(analysis.get('trace_quality'), Mapping) else {},
            'repair_group_count': len(_items(analysis.get('repair_group_queue'))),
        },
        'events': [
            _event('group_selected', 'completed', selected['group_id'], block, selected['case_ids'],
                   f"selected {block} with {selected['badcase_count']} bad case(s)"),
            _event('brief_ready', 'completed', selected['group_id'], block, selected['case_ids'],
                   'repair brief generated from analysis summary'),
        ],
        'checks': {
            'ready': not blocked_reason,
            'errors': [{'code': blocked_reason}] if blocked_reason else [],
        },
    }


def _event(phase: str, status: str, group_id: str, block: str, case_ids: list[str], summary: str) -> dict[str, Any]:
    return {
        'event_id': sha1(json.dumps(
            {'phase': phase, 'group_id': group_id, 'case_ids': case_ids},
            ensure_ascii=False,
            sort_keys=True,
        ).encode()).hexdigest()[:16],
        'phase': phase,
        'source': 'repair',
        'kind': phase,
        'status': status,
        'severity': 'info' if status == 'completed' else 'warning',
        'title': phase.replace('_', ' '),
        'summary': summary,
        'group_id': group_id,
        'function_block_id': block,
        'case_ids': case_ids,
    }


def _roots(value: Any, default: tuple[str, ...]) -> list[str]:
    roots = [_path(item) for item in _items(value)]
    return [root for root in roots if root] or list(default)


def _path(value: Any) -> str:
    text = str(value or '').strip()
    if not text or text.startswith('/') or '\\' in text or '//' in text:
        return ''
    parts = text.strip('/').split('/')
    if any(part in {'', '.', '..'} for part in parts):
        return ''
    return '/'.join(parts)


def _items(value: Any) -> list[Any]:
    if isinstance(value, list):
        return value
    if isinstance(value, tuple):
        return list(value)
    return [] if value in (None, '') else [value]


def _bounded_int(value: Any, default: int, minimum: int, maximum: int) -> int:
    try:
        number = int(value)
    except (TypeError, ValueError):
        number = default
    return max(minimum, min(maximum, number))


def _number(value: Any, default: float) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return default
