from __future__ import annotations

import json
from collections.abc import Mapping
from hashlib import sha1
from typing import Any

DEFAULT_ALLOWED_ROOTS = ('algorithm/lazymind/chat', 'algorithm/lazymind/parsing')
DEFAULT_BLOCKED_ROOTS = ('tests', '.git', 'lazyllm', 'evo', 'data')
BUDGET_LIMITS = {
    'target_case_budget': (8, 1, 30),
    'neighbor_case_budget': (3, 0, 12),
    'goodcase_guard_budget': (2, 0, 12),
    'cross_block_guard_budget': (2, 0, 12),
    'evidence_case_budget': (8, 1, 30),
    'seed_file_budget': (5, 1, 10),
}
def build_repair_plan(analysis: Mapping[str, Any], policy: Mapping[str, Any]) -> dict[str, Any]:
    rows = [row for row in _items(analysis.get('rows')) if isinstance(row, Mapping)]
    allowed_roots = _roots(policy.get('allowed_roots'), DEFAULT_ALLOWED_ROOTS)
    blocked_roots = _roots(policy.get('blocked_roots'), DEFAULT_BLOCKED_ROOTS)
    budgets = {key: _bounded_int(policy.get(key), *limits) for key, limits in BUDGET_LIMITS.items()}
    policy_view = {'allowed_roots': allowed_roots, 'blocked_roots': blocked_roots}
    policy_view.update({key: budgets[key] for key in budgets if key in policy})

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
            'checks': {'ready': False, 'errors': [{'code': code}]},
        }

    if not rows:
        return empty_plan('blocked_no_analysis_rows', blocked=True)
    if not _inside_domain_roots(allowed_roots):
        return empty_plan('blocked_invalid_allowed_roots', blocked=True)
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
        elif confidence < 0.5:
            reason = 'selected group confidence below patch threshold'
        elif group.get('issue_category') == 'tracing' and badcase_count < 2:
            reason = 'single tracing-only badcase requires deeper analysis before patch'
        else:
            reason = ''
        group['deeper_analysis_reason'] = reason
        groups.append(group)

    if not groups:
        return empty_plan('blocked_no_repairable_group', blocked=True)

    selected_rank, selected = next(
        ((rank, group) for rank, group in enumerate(groups, start=1) if not group.get('deeper_analysis_reason')),
        (1, groups[0]),
    )
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
        'selection_reason': 'highest-ranked repairable group in analysis repair_group_queue',
        'target_cases': target,
        'neighbor_cases': neighbor,
        'goodcase_guard_cases': good,
        'cross_block_guard_cases': cross,
        'validation_case_ids': validation_cases,
        'group_rank': selected_rank,
        'candidate_group_count': len(groups),
    }

    blocked_reason = selected.get('deeper_analysis_reason') or ''
    invariants = list(dict.fromkeys([
        *list(selected.get('invariants') or ()),
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
            'overall_score average must improve across validation cases',
            'badcase overall_score average must improve by at least 0.10',
            'goodcase overall_score average must not drop by more than 0.05',
            f"validation cases: {', '.join(validation_cases)}",
        ],
        'rollback_triggers': [
            'empty diff',
            'blocked root touched',
            'compile or smoke validation failed',
            'candidate service cannot start or route to algorithm_id',
            'overall_score average does not improve',
            'badcase overall_score average improves by less than 0.10',
            'goodcase overall_score average drops by more than 0.05',
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
            'repair_group_count': len(_items(analysis.get('repair_group_queue'))),
        },
        'checks': {
            'ready': not blocked_reason,
            'errors': [{'code': blocked_reason}] if blocked_reason else [],
        },
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


def _inside_domain_roots(roots: list[str]) -> bool:
    return bool(roots) and all(
        any(root == domain or root.startswith(f'{domain}/') for domain in DEFAULT_ALLOWED_ROOTS)
        for root in roots
    )


def _items(value: Any) -> list[Any]:
    if isinstance(value, (list, tuple)):
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
