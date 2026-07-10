from __future__ import annotations

import hashlib
import json
import os
import re
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from unidiff import PatchSet

from evo.operations.public_contracts import RepairPatch, algo_id, dump_contract

from .candidate import validate_candidate_patch
from .code_index import build_code_index
from .localize import localize_repair
from .opencode import run_opencode_streaming
from .report import read_worker_report
from .trace import safe_emit, trace_cursor
from .validation import pre_validate
from .workspace import (
    apply_diff,
    algorithm_source_root,
    git,
    prepare_workspace,
    reset_workspace,
    source_fingerprint,
    workspace_diff,
    workspace_fingerprint,
    workspace_path,
)

DEFAULT_SOURCE = '/app/algorithm'
PROVIDER_NAME = re.compile(r'^[a-z0-9][a-z0-9_-]*$')
FINAL_PATCH_STATUSES = {'validated', 'exhausted_with_patch'}


def prepare_candidate_workspace(
    plan: Mapping[str, Any],
    repair_policy: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    if plan.get('status') != 'planned':
        return {
            'status': 'failed',
            'reason': f"repair plan is not planned: {_text(plan.get('status')) or 'missing_status'}",
            'repair_plan_ref': _plan_ref(plan),
            'workspace_kind': 'managed_worktree',
        }
    policy = _runtime_policy(plan, repair_policy)
    source = algorithm_source_root(policy.get('candidate_source_dir') or os.getenv('LAZYMIND_EVO_CHAT_SOURCE')
                                   or DEFAULT_SOURCE)
    workspace = workspace_path(policy, plan)
    objective_hash = hashlib.sha1(json.dumps(plan.get('objective') or {}, sort_keys=True).encode()).hexdigest()[:12]
    prepare_workspace(source, workspace, objective_hash)
    source_hash = source_fingerprint(source)['source_hash']
    return {
        'status': 'ready',
        'workspace_kind': 'managed_worktree',
        'workspace_ref': str(workspace),
        'source_dir': str(source),
        'source_hash': source_hash,
        'objective_hash': objective_hash,
        'git_head': git(workspace, 'rev-parse', '--verify', 'HEAD'),
        'repair_plan_ref': _plan_ref(plan),
    }


def run_repair_loop(workspace: Mapping[str, Any], cases: tuple[Mapping[str, Any], ...],
                    baseline_judges: tuple[Mapping[str, Any], ...], eval_policy: Mapping[str, Any],
                    candidate_config: Mapping[str, Any], repair_policy: Mapping[str, Any], ctx: Any,
                    plan: Mapping[str, Any] | None = None,
                    trace: Any | None = None) -> dict[str, Any]:
    plan = plan if isinstance(plan, Mapping) else {}
    baseline_algo_id = next((text for judge in baseline_judges for text in (algo_id(judge),) if text), '')
    ready = _ready_workspace(workspace, plan, repair_policy)
    if ready.get('status') != 'ready':
        reason = _text(ready.get('reason')) or 'repair workspace is not ready'
        safe_emit(trace, 'repair.loop_completed', status='failed', terminal=True, payload={'reason': reason})
        return _result('failed', plan, workspace, [], {}, reason, baseline_algo_id,
                       trace_cursor(trace))
    root = Path(str(workspace['workspace_ref'])).resolve()
    case_map = {_text(case.get('id')): case for case in cases
                if isinstance(case, Mapping) and _text(case.get('id'))}
    baseline_map = {_text(judge.get('case_id')): judge for judge in baseline_judges
                    if isinstance(judge, Mapping) and _text(judge.get('case_id'))}
    policy = _runtime_policy(plan, repair_policy)
    missing_validation = _validation_input_gap(plan, case_map, baseline_map)
    if missing_validation:
        safe_emit(trace, 'repair.loop_completed', status='failed', terminal=True,
                  payload={'reason': missing_validation})
        return _result('failed', plan, workspace, [], {}, missing_validation, baseline_algo_id, trace_cursor(trace))
    index = build_code_index(root)
    localization = localize_repair(index, plan)
    opencode_config = _opencode_config_from_policy(policy)
    if not opencode_config:
        reason = 'missing opencode model configuration'
        safe_emit(trace, 'repair.loop_completed', status='failed', terminal=True, payload={'reason': reason})
        return _result('failed', plan, workspace, [], {}, reason, baseline_algo_id, trace_cursor(trace))
    attempts, session_id = [], ''
    budget = _int(policy.get('repair_attempt_budget'), 10, 1, 20)

    for attempt_no in range(1, budget + 1):
        safe_emit(trace, 'repair.attempt_started', status='started', attempt=attempt_no,
                  payload={'budget': budget, 'localization_status': localization.get('status')})
        reset_workspace(root)
        artifact_dir = root / '.evo_repair_logs' / 'opencode' / f'attempt_{attempt_no}'
        task = _task_card(plan, workspace, localization, attempt_no, artifact_dir / 'worker_report.json', attempts)
        run = run_opencode_streaming(
            workdir=str(root),
            prompt=json.dumps(task, ensure_ascii=False, indent=2),
            artifact_dir=artifact_dir,
            session_id=session_id,
            config=opencode_config,
            timeout_s=_int(policy.get('opencode_timeout_s') or os.getenv('LAZYMIND_EVO_CODE_TIMEOUT_S'),
                           900, 30, 7200),
            trace=trace,
            attempt=attempt_no,
        )
        session_id = run.session_id or session_id
        report = read_worker_report(artifact_dir / 'worker_report.json')
        diff_info = workspace_diff(root)
        worker_failure = _worker_failure(run)
        pre = pre_validate(root, diff_info, plan, policy, trace, attempt_no)
        if worker_failure:
            candidate = _rejected_candidate('worker_failed', worker_failure)
        elif pre.get('status') != 'passed':
            candidate = _rejected_candidate('pre_validation_failed', _text(pre.get('reason')) or 'pre_validation_failed')
        else:
            candidate = validate_candidate_patch(root, diff_info['diff'], plan, case_map, baseline_map, eval_policy,
                                                 candidate_config, ctx, trace, attempt_no)
        status = 'validated' if candidate.get('accepted') is True else 'failed'
        attempt = {
            'attempt': attempt_no,
            'status': status,
            'opencode': {
                'returncode': getattr(run, 'returncode', None),
                'last_error': getattr(run, 'last_error', None),
                'configured': bool(opencode_config),
            },
            'worker_report': report,
            'localization': localization,
            'pre_validation': pre,
            'candidate_validation': candidate,
            'workspace_ref': str(root),
            'files_changed': diff_info['files'],
            'diff': diff_info['diff'],
        }
        attempts.append(attempt)
        safe_emit(trace, 'repair.attempt_completed', status='completed' if status == 'validated' else 'failed',
                  attempt=attempt_no, payload={
                      'status': status,
                      'reason': candidate.get('reason'),
                      'files_changed': diff_info['files'],
                  })
        if status == 'validated':
            safe_emit(trace, 'repair.loop_completed', status='completed', terminal=True,
                      payload={'status': 'validated', 'attempt_count': len(attempts)})
            return _result('validated', plan, workspace, attempts, attempt, 'validated repair patch',
                           baseline_algo_id, trace_cursor(trace))
    fallback = _latest_prevalidated_patch(attempts)
    if fallback:
        try:
            reset_workspace(root)
            apply_diff(root, str(fallback.get('diff') or ''))
        except Exception as exc:
            reason = f'exhausted patch could not be restored: {type(exc).__name__}'
            safe_emit(trace, 'repair.loop_completed', status='failed', terminal=True,
                      payload={'status': 'failed', 'attempt_count': len(attempts), 'reason': reason})
            reset_workspace(root)
            return _result('failed', plan, workspace, attempts, {}, reason, baseline_algo_id, trace_cursor(trace))
        safe_emit(trace, 'repair.loop_completed', status='completed', terminal=True,
                  payload={
                      'status': 'exhausted_with_patch',
                      'attempt_count': len(attempts),
                      'fallback_attempt': fallback.get('attempt'),
                  })
        return _result(
            'exhausted_with_patch',
            plan,
            workspace,
            attempts,
            fallback,
            'repair exhausted validation attempts; using latest pre-validated patch',
            baseline_algo_id,
            trace_cursor(trace),
        )
    safe_emit(trace, 'repair.loop_completed', status='failed', terminal=True,
              payload={'status': 'failed', 'attempt_count': len(attempts)})
    reset_workspace(root)
    return _result('failed', plan, workspace, attempts, {}, 'repair exhausted attempts without a validated patch',
                   baseline_algo_id, trace_cursor(trace))


def build_verified_patch(run_id: str, loop: Mapping[str, Any]) -> dict[str, Any]:
    status = _text(loop.get('status'))
    if status not in FINAL_PATCH_STATUSES:
        raise ValueError(f"repair did not produce a final patch: {status or 'missing_status'}")
    attempts = loop.get('attempts') if isinstance(loop.get('attempts'), list) else []
    final = attempts[-1] if attempts and isinstance(attempts[-1], Mapping) else {}
    candidate = final.get('candidate_validation') if isinstance(final.get('candidate_validation'), Mapping) else {}
    if status == 'validated' and candidate.get('accepted') is not True:
        raise ValueError('validated repair patch requires accepted candidate validation')
    raw_diff = loop.get('winning_patch_diff')
    diff = raw_diff if isinstance(raw_diff, str) else str(raw_diff or '')
    diff_by_file = _diff_by_file(diff)
    if not diff_by_file:
        raise ValueError('final repair patch requires a non-empty diff')
    workspace_ref = _text(loop.get('workspace_ref'))
    if not workspace_ref:
        raise ValueError('final repair patch requires workspace_ref')
    return dump_contract(RepairPatch, {
        'run_id': run_id,
        'algo_id': _text(loop.get('algo_id')),
        'candidate_algo_id': _text(loop.get('candidate_algo_id')),
        'status': 'verified' if status == 'validated' else 'unvalidated',
        'workspace_ref': workspace_ref,
        'diff': diff_by_file,
    })


def _ready_workspace(workspace: Mapping[str, Any], plan: Mapping[str, Any],
                     repair_policy: Mapping[str, Any]) -> dict[str, str]:
    if workspace.get('status') != 'ready' or plan.get('status') != 'planned':
        return {'status': 'failed', 'reason': 'repair plan is not runnable'}
    policy = _runtime_policy(plan, repair_policy)
    objective_hash = hashlib.sha1(json.dumps(plan.get('objective') or {}, sort_keys=True).encode()).hexdigest()[:12]
    try:
        root = Path(_text(workspace.get('workspace_ref'))).resolve()
        expected_root = workspace_path(policy, plan).resolve()
        fingerprint = workspace_fingerprint(root)
        workspace_head = git(root, 'rev-parse', '--verify', 'HEAD')
    except (OSError, RuntimeError, ValueError):
        return {'status': 'failed', 'reason': 'candidate workspace artifact failed integrity check'}
    if (
        root != expected_root
        or fingerprint.get('source_hash') != workspace.get('source_hash')
        or fingerprint.get('source_dir') != workspace.get('source_dir')
        or fingerprint.get('objective_hash') != objective_hash
        or workspace_head != workspace.get('git_head')
        or not (root / 'algorithm' / 'lazymind' / 'chat').exists()
    ):
        return {'status': 'failed', 'reason': 'candidate workspace artifact failed integrity check'}
    return {'status': 'ready', 'reason': ''}


def _validation_input_gap(
    plan: Mapping[str, Any],
    cases: Mapping[str, Mapping[str, Any]],
    baseline: Mapping[str, Mapping[str, Any]],
) -> str:
    objective = plan.get('objective') if isinstance(plan.get('objective'), Mapping) else {}
    required = [_text(item) for item in objective.get('validation_case_ids') or [] if _text(item)]
    if not required:
        return 'repair plan does not define validation cases'
    missing_cases = [case_id for case_id in required if case_id not in cases]
    missing_baseline = [case_id for case_id in required if case_id in cases and case_id not in baseline]
    if missing_cases:
        return f"repair validation cases missing: {', '.join(missing_cases[:5])}"
    if missing_baseline:
        return f"repair baseline judges missing: {', '.join(missing_baseline[:5])}"
    return ''


def _task_card(plan: Mapping[str, Any], workspace: Mapping[str, Any], localization: Mapping[str, Any],
               attempt: int, report_path: Path, previous_attempts: list[Mapping[str, Any]] | None = None) -> dict[str, Any]:
    prior = _attempt_feedback(previous_attempts or [])
    return {
        'mode': 'lazyrag_validated_repair_v3',
        'attempt': attempt,
        'objective': plan.get('objective'),
        'brief': plan.get('brief') if isinstance(plan.get('brief'), Mapping) else {},
        'workspace': {'path': workspace.get('workspace_ref'), 'source_dir': workspace.get('source_dir')},
        'previous_attempts': prior,
        'localization': {
            'domain': localization.get('domain'),
            'ranked_symbols': list(localization.get('ranked_symbols') or ())[:12],
            'weak_hints': localization.get('weak_hints'),
        },
        'hard_constraints': [
            'Leave a non-empty git diff that directly addresses the selected repair group.',
            'Edit only under algorithm/lazymind/chat or algorithm/lazymind/parsing.',
            'Do not edit tests, eval, data, generated files, secrets, or vendored lazyllm.',
            'Do not add fallback, retry, second-pass, or "if empty then try original query" retrieval behavior.',
            'Do not treat validation failure by broadening search breadth or bypassing the selected evidence contract.',
            'If retrieved evidence is present but ids are missing, repair evidence propagation, source serialization, or parsing contracts instead of adding retrieval fallbacks.',
            'Use ranked symbols as localization evidence, not as a hard file whitelist.',
            'The host repair loop accepts only when validation overall_score improves, badcase overall_score average gains at least 0.10, and goodcase overall_score average drops no more than 0.05.',
            'Read previous_attempts before editing; do not repeat a rejected strategy, file-only retry tweak, or metric-neutral change.',
            'If a previous attempt failed overall_score_gate, change the root-cause hypothesis before editing.',
            f'Write a JSON worker report to {report_path.as_posix()}.',
        ],
        'worker_report_schema': {
            'status': 'edited',
            'mode': 'patch',
            'files_changed': ['algorithm/lazymind/chat/... or algorithm/lazymind/parsing/...'],
            'confirmed_locations': [{'path': '...', 'symbol': '...', 'line_start': 1, 'line_end': 2,
                                     'evidence': '...'}],
            'touched_symbols': ['...'],
            'change_intent': 'minimal behaviorful repair',
            'risk': 'low|medium|high',
            'notes': '',
        },
    }


def _attempt_feedback(attempts: list[Mapping[str, Any]]) -> list[dict[str, Any]]:
    return [_attempt_feedback_item(attempt) for attempt in attempts[-3:]]


def _attempt_feedback_item(attempt: Mapping[str, Any]) -> dict[str, Any]:
    candidate = attempt.get('candidate_validation') if isinstance(attempt.get('candidate_validation'), Mapping) else {}
    delta = candidate.get('analysis_delta') if isinstance(candidate.get('analysis_delta'), Mapping) else {}
    analysis = candidate.get('candidate_analysis') if isinstance(candidate.get('candidate_analysis'), Mapping) else {}
    return {
        'attempt': attempt.get('attempt'),
        'status': _text(attempt.get('status')),
        'files_changed': list(attempt.get('files_changed') or [])[:8],
        'pre_validation': _pick(_mapping(attempt.get('pre_validation')), ('status', 'reason')),
        'candidate_validation': _pick(candidate, ('status', 'accepted', 'reason')),
        'analysis_delta': _pick(delta, (
            'target_group_status',
            'target_remaining_badcase_count',
            'target_remaining_delta',
            'target_badcase_count',
            'new_group_count',
            'goodcase_guard_status',
            'target_metric_delta',
            'metric_delta',
            'overall_score_gate',
            'recommended_action',
        )),
        'failed_cases': _failed_case_feedback(analysis),
    }


def _failed_case_feedback(analysis: Mapping[str, Any]) -> list[dict[str, Any]]:
    rows = analysis.get('rows') if isinstance(analysis.get('rows'), list) else []
    result = []
    for row in rows:
        if not isinstance(row, Mapping) or _text(row.get('issue_type')) == 'correct':
            continue
        answer = row.get('rag_answer') if isinstance(row.get('rag_answer'), Mapping) else {}
        judge = row.get('judge') if isinstance(row.get('judge'), Mapping) else {}
        trace = row.get('trace_summary') if isinstance(row.get('trace_summary'), Mapping) else {}
        result.append({
            'case_id': _text(row.get('case_id')),
            'issue_type': _text(row.get('issue_type')),
            'affected_block': _text(row.get('affected_block')),
            'failure_mode': _text(row.get('failure_mode')),
            'quality_label': _text(judge.get('quality_label')),
            'failure_type': _text(judge.get('failure_type')),
            'retrieval_failure_type': _text(judge.get('retrieval_failure_type')),
            'overall_score': judge.get('overall_score'),
            'answer_correctness': judge.get('answer_correctness'),
            'retrieval_quality_score': judge.get('retrieval_quality_score'),
            'doc_ids': len(answer.get('doc_ids') or []),
            'chunk_ids': len(answer.get('chunk_ids') or []),
            'retrieval_steps': len(trace.get('retrieval_steps') or []),
            'error_stage_count': len(trace.get('error_stages') or []),
            'judge_reason': _clip(judge.get('reason'), 260),
            'answer_excerpt': _clip(answer.get('answer'), 260),
        })
    return result[:6]


def _result(status: str, plan: Mapping[str, Any], workspace: Mapping[str, Any], attempts: list[Mapping[str, Any]],
            best: Mapping[str, Any], message: str, algo_id_value: str = '',
            trace_cursor: Mapping[str, Any] | None = None) -> dict[str, Any]:
    winner = best if status in FINAL_PATCH_STATUSES else {}
    candidate = winner.get('candidate_validation') if isinstance(winner.get('candidate_validation'), Mapping) else {}
    service = candidate.get('service') if isinstance(candidate.get('service'), Mapping) else {}
    diff = str(winner.get('diff') or '')
    workspace_ref = _text(winner.get('workspace_ref')) or _text(workspace.get('workspace_ref'))
    return {
        'id': 'repair.loop_result',
        'status': status,
        'message': message,
        'algo_id': algo_id_value,
        'attempt_count': len(attempts),
        'files_changed': winner.get('files_changed') or [],
        'workspace_ref': workspace_ref,
        'candidate_algo_id': _text(service.get('algorithm_id')),
        'winning_patch_diff': diff,
        'selected_group': _group_summary(plan.get('selected_group')),
        'attempts': attempts,
        'trace_cursor': dict(trace_cursor or {}),
    }


def _latest_prevalidated_patch(attempts: list[Mapping[str, Any]]) -> Mapping[str, Any]:
    for attempt in reversed(attempts):
        if not str(attempt.get('diff') or '').strip():
            continue
        pre = attempt.get('pre_validation') if isinstance(attempt.get('pre_validation'), Mapping) else {}
        if pre.get('status') == 'passed':
            return attempt
    return {}


def _worker_failure(run: Any) -> str:
    if getattr(run, 'last_error', None):
        return _text(getattr(run, 'last_error'))
    returncode = getattr(run, 'returncode', 0)
    if returncode:
        return f'opencode exited with {returncode}'
    return ''


def _rejected_candidate(reason: str, detail: str = '') -> dict[str, Any]:
    return {
        'status': 'rejected',
        'accepted': False,
        'reason': reason,
        'detail': detail,
    }


def _diff_by_file(diff: str) -> dict[str, str]:
    if not diff.strip():
        return {}
    result = {}
    for patched in PatchSet(diff.splitlines(True)):
        source = _text(patched.source_file).removeprefix('a/')
        target = _text(patched.target_file).removeprefix('b/')
        path = source if target == '/dev/null' else target or source
        if path:
            result[path] = str(patched)
    return result


def _group_summary(value: object) -> dict[str, Any]:
    group = value if isinstance(value, Mapping) else {}
    return _pick(group, (
        'group_id',
        'function_block_id',
        'issue_type',
        'failure_mode',
        'badcase_count',
        'representative_case_id',
        'confidence_score',
        'candidate_files',
        'case_ids',
        'trace_cluster_id',
        'trace_signature',
    ))


def _plan_ref(plan: Mapping[str, Any]) -> dict[str, Any]:
    objective_hash = hashlib.sha1(
        json.dumps(plan.get('objective') or {}, sort_keys=True).encode()
    ).hexdigest()[:12]
    return {
        'status': _text(plan.get('status')),
        'objective_hash': objective_hash,
        'selected_group': _group_summary(plan.get('selected_group')),
    }


def _opencode_config_from_policy(policy: Mapping[str, Any]) -> dict[str, str]:
    llm_config = policy.get('llm_config') if isinstance(policy.get('llm_config'), Mapping) else {}
    role = llm_config.get('evo_llm') if isinstance(llm_config.get('evo_llm'), Mapping) else {}
    if not role:
        return {}
    model = _text(role.get('model'))
    base_url = _text(role.get('base_url')).rstrip('/')
    provider = _text(role.get('provider') or role.get('source')).lower()
    if not PROVIDER_NAME.fullmatch(provider):
        return {}
    if provider == 'qwen' and base_url == 'https://dashscope.aliyuncs.com':
        base_url = f'{base_url}/compatible-mode/v1'
    api_key = _text(role.get('api_key'))
    skip_auth = role.get('skip_auth') is True
    if not (provider and model and base_url and (api_key or skip_auth)):
        return {}
    return {
        'model': f'{provider}/{model}',
        'provider': provider,
        'provider_model': model,
        'provider_label': provider,
        'base_url': base_url,
        'api_key': api_key,
        'skip_auth': 'true' if skip_auth else '',
    }


def _runtime_policy(plan: Mapping[str, Any], repair_policy: Mapping[str, Any] | None) -> dict[str, Any]:
    safe = plan.get('policy') if isinstance(plan.get('policy'), Mapping) else {}
    raw = repair_policy if isinstance(repair_policy, Mapping) else {}
    safe_keys = {
        'allowed_roots',
        'blocked_roots',
        'target_case_budget',
        'neighbor_case_budget',
        'goodcase_guard_budget',
        'cross_block_guard_budget',
        'evidence_case_budget',
        'seed_file_budget',
    }
    runtime_keys = {
        'candidate_source_dir',
        'candidate_workdir',
        'workspace_namespace',
        'thread_id',
        'verification_commands',
        'repair_attempt_budget',
        'opencode_timeout_s',
        'max_patch_bytes',
        'llm_config',
    }
    return {
        **{key: safe[key] for key in safe_keys if key in safe},
        **{key: raw[key] for key in runtime_keys if key in raw},
    }


def _int(value: Any, default: int, low: int, high: int) -> int:
    try:
        number = int(value)
    except (TypeError, ValueError):
        number = default
    return max(low, min(high, number))


def _text(value: Any) -> str:
    return str(value or '').strip()


def _mapping(value: Any) -> Mapping[str, Any]:
    return value if isinstance(value, Mapping) else {}


def _pick(value: Mapping[str, Any], keys: tuple[str, ...]) -> dict[str, Any]:
    return {key: value[key] for key in keys if key in value}


def _clip(value: Any, limit: int) -> str:
    text = ' '.join(str(value or '').split())
    return text if len(text) <= limit else text[:limit - 1] + '…'
