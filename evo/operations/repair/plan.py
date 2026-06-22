from __future__ import annotations

import hashlib
import json
import os
import shlex
import shutil
import subprocess
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from evo.operations import abtest
from evo.operations.common import (
    METRICS,
    as_list,
    clip,
    int_between,
    json_safe,
    stable_text,
    text,
)
from evo.operations.eval import eval_summary

from .opencode import run_opencode_streaming, trace_payload


def repair_plan(analysis: Mapping[str, Any], policy: Mapping[str, Any]) -> dict[str, Any]:
    repairable = list(analysis.get('repairable_cases') or [])
    rows = [row for row in as_list(analysis.get('rows')) if isinstance(row, Mapping)]
    evidence = [_repair_case_evidence(row) for row in rows if _repair_case_selected(row, repairable)]
    return {
        'status': 'planned' if repairable else 'skipped_no_repairable_case',
        'target_cases': repairable,
        'policy': dict(policy),
        'analysis_summary': {
            'category_counts': dict(analysis.get('category_counts') or {}),
            'fine_category_counts': dict(analysis.get('fine_category_counts') or {}),
            'top_failure_patterns': as_list(analysis.get('top_failure_patterns'))[:5],
        },
        'evidence_cases': evidence[:_evidence_budget(policy)],
        'evidence_total': len(evidence),
    }


def candidate_workspace(plan: Mapping[str, Any], services: Any) -> dict[str, Any]:
    if plan.get('status') != 'planned':
        return {'status': 'skipped', 'repair_plan': plan, 'workspace_kind': 'managed_worktree'}
    policy = plan.get('policy') if isinstance(plan.get('policy'), Mapping) else {}
    source = _algorithm_source_root(
        policy.get('candidate_source_dir') or os.getenv('LAZYMIND_EVO_CHAT_SOURCE') or '/app/algorithm',
    )
    workspace = _repair_workspace(policy, plan)
    _prepare_repair_workspace(source, workspace)
    return {
        'status': 'ready',
        'repair_plan': plan,
        'workspace_kind': 'managed_worktree',
        'workspace_ref': str(workspace),
        'source_dir': str(source),
        'git_head': _git(workspace, 'rev-parse', '--verify', 'HEAD'),
    }


def repair_loop(workspace: Mapping[str, Any], cases: Mapping[str, Any], baseline_judges: Mapping[str, Any],
                eval_policy: Mapping[str, Any], candidate_config: Mapping[str, Any], services: Any) -> dict[str, Any]:
    planned = ((workspace.get('repair_plan') or {}).get('status') == 'planned')
    if not planned:
        return {'status': 'skipped', 'attempts': [], 'diagnostics': [], 'message': ''}
    _raise_if_cancelled(services)
    workspace_ref = text(workspace.get('workspace_ref'))
    plan = workspace.get('repair_plan') if isinstance(workspace.get('repair_plan'), Mapping) else {}
    policy = plan.get('policy') if isinstance(plan.get('policy'), Mapping) else {}
    budget = _repair_attempt_budget(policy)
    attempts = []
    session_id = ''
    memory: dict[str, Any] = {
        'attempted_strategies': [],
        'failed_strategies': [],
        'failed_cases': {},
        'regressed_cases': [],
    }
    feedback: dict[str, Any] = {'memory': memory}
    best_attempt: dict[str, Any] = {}
    for attempt in range(1, budget + 1):
        _raise_if_cancelled(services)
        _reset_repair_workspace(workspace_ref)
        analysis = _failure_analysis(plan, attempt, feedback, services) if attempt > 1 else {}
        if analysis:
            memory = _merge_analysis_memory(memory, analysis)
        diagnosis = _repair_diagnosis(plan, policy, services, attempt, feedback, analysis)
        result = _run_repair_attempt(
            workspace_ref=workspace_ref,
            task=_opencode_task(plan, workspace, diagnosis, analysis, memory, attempt),
            policy=policy,
            session_id=session_id,
            attempt=attempt,
            services=services,
        )
        session_id = text(result.get('session_id')) or session_id
        trace = dict(result.get('trace') or {})
        diff_info = _repair_workspace_diff(workspace_ref)
        diff = text(diff_info.get('diff'))
        files = [text(item) for item in as_list(diff_info.get('files')) if text(item)]
        verification = _verify_repair_workspace(Path(workspace_ref), policy)
        diff_scope = _diff_scope(files, policy)
        mini_eval = (
            _mini_eval_attempt(workspace_ref, diff, plan, cases, baseline_judges, eval_policy, candidate_config,
                               policy, services, attempt)
            if diff.strip() and verification['status'] == 'passed' and diff_scope['status'] == 'passed'
            else {'status': 'skipped', 'accepted': False, 'reason': 'pre_validation_failed'}
        )
        status = 'validated' if mini_eval.get('accepted') else 'failed'
        attempts.append({
            'attempt': attempt,
            'status': status,
            'failure_analysis': analysis,
            'diagnosis': diagnosis,
            'opencode_trace': trace,
            'files_changed': files,
            'diff': diff,
            'verification': verification,
            'diff_scope': diff_scope,
            'mini_eval': mini_eval,
            'failure': '' if status == 'validated' else _repair_failure(result, diff, verification, diff_scope,
                                                                        mini_eval),
        })
        if status == 'validated':
            best_attempt = attempts[-1]
            break
        best_attempt = _better_attempt(best_attempt, attempts[-1])
        memory = _repair_memory(memory, attempts[-1])
        feedback = _next_feedback(attempts[-1], memory)
        _reset_repair_workspace(workspace_ref)
    stop_reason = 'validated' if best_attempt.get('status') == 'validated' else f'attempt_budget_exhausted:{budget}'
    return {
        'status': 'validated' if best_attempt.get('status') == 'validated' else 'no_validated_patch',
        'attempt_budget': budget,
        'attempt_count': len(attempts),
        'stop_reason': stop_reason,
        'best_attempt': best_attempt.get('attempt'),
        'best_attempt_status': text(best_attempt.get('status')),
        'best_attempt_reason': text(best_attempt.get('failure')),
        'best_metrics': (best_attempt.get('mini_eval') or {}).get('metrics', {}) if best_attempt else {},
        'memory': memory,
        'attempts': attempts,
        'diagnostics': list(plan.get('target_cases') or []),
        'workspace_ref': workspace_ref,
        'message': (
            'Repair loop produced a validated candidate patch.'
            if best_attempt.get('status') == 'validated'
            else f'Repair loop exhausted {len(attempts)}/{budget} attempts without improving answer_correctness.'
        ),
    }


def verified_patch(loop: Mapping[str, Any]) -> dict[str, Any]:
    attempts = [item for item in as_list(loop.get('attempts')) if isinstance(item, Mapping)]
    winner = next((item for item in reversed(attempts) if item.get('status') == 'validated'), {})
    status = 'verified' if winner else 'skipped' if loop.get('status') == 'skipped' else 'no_patch'
    diff = text(winner.get('diff')) if winner else ''
    return {
        'status': status,
        'diff': diff,
        'patch': diff,
        'content': diff or 'No verified code changes were produced for this repair step.\n',
        'repair_loop': loop,
        'workspace_ref': text(loop.get('workspace_ref')),
        'files': list(winner.get('files_changed') or []),
        'mini_eval': dict(winner.get('mini_eval') or {}) if winner else {},
        'winning_attempt': winner.get('attempt') if winner else None,
    }


def _repair_case_selected(row: Mapping[str, Any], repairable: list[Any]) -> bool:
    selected = {text(item.get('case_id')) for item in repairable if isinstance(item, Mapping)}
    return not selected or text(row.get('case_id')) in selected


def _repair_case_evidence(row: Mapping[str, Any]) -> dict[str, Any]:
    case = row.get('case') if isinstance(row.get('case'), Mapping) else {}
    answer = row.get('rag_answer') if isinstance(row.get('rag_answer'), Mapping) else {}
    judge = row.get('judge') if isinstance(row.get('judge'), Mapping) else {}
    return {
        'case_id': text(row.get('case_id')),
        'category': text(row.get('fine_category') or row.get('coarse_category')),
        'reason': text(row.get('reason')),
        'question_type': text(case.get('question_type')),
        'question': clip(case.get('question'), 360),
        'reference_answer': clip(case.get('answer'), 600),
        'actual_answer': clip(answer.get('answer'), 600),
        'reference_doc_ids': as_list(case.get('reference_doc_ids')),
        'reference_chunk_ids': as_list(case.get('reference_chunk_ids')),
        'actual_doc_ids': as_list(answer.get('doc_ids')),
        'actual_chunk_ids': as_list(answer.get('chunk_ids')),
        'tool_errors': as_list(answer.get('tool_errors')),
        'metrics': {key: judge.get(key) for key in METRICS},
        'trace': _compact_trace(row.get('trace_summary')),
        'contexts': [clip(item, 320) for item in as_list(answer.get('contexts'))[:2]],
    }


def _failure_analysis(
    plan: Mapping[str, Any],
    attempt: int,
    feedback: Mapping[str, Any],
    services: Any,
) -> dict[str, Any]:
    prompt = (
        '你是 LazyRAG evo 修复复盘器。基于上一轮 mini-eval 反馈，输出下一轮必须修复和必须避免的要点。'
        '只输出 JSON，不要 markdown。字段: root_causes, must_fix_cases, regressed_cases, failed_strategies, '
        'next_patch_strategy, validation_focus。\n'
        f'Attempt: {attempt}\n'
        f'Previous feedback: {json.dumps(json_safe(dict(feedback)), ensure_ascii=False, sort_keys=True)}\n'
        'Evidence cases: '
        f'{json.dumps(json_safe(list(plan.get("evidence_cases") or [])), ensure_ascii=False, sort_keys=True)}'
    )
    raw = clip(services.llm_complete(prompt), 4000)
    try:
        data = json.loads(raw)
    except json.JSONDecodeError:
        data = {'raw_analysis': raw}
    if not isinstance(data, Mapping):
        data = {'raw_analysis': raw}
    rows = [row for row in as_list(feedback.get('case_feedback')) if isinstance(row, Mapping)]
    must_fix = _case_ids(data.get('must_fix_cases')) or [
        text(row.get('case_id')) for row in rows
        if text(row.get('candidate_failure_type')) != 'none'
        or text(row.get('outcome')) in {'regressed', 'missing_candidate', 'missing_baseline'}
    ]
    return {
        'root_causes': [text(item) for item in as_list(data.get('root_causes') or data.get('root_cause')) if text(item)],
        'must_fix_cases': list(dict.fromkeys(case_id for case_id in must_fix if case_id)),
        'regressed_cases': _case_ids(data.get('regressed_cases')) or [
            text(row.get('case_id')) for row in rows if text(row.get('outcome')) == 'regressed'
        ],
        'failed_strategies': [clip(item, 360) for item in as_list(data.get('failed_strategies')) if text(item)],
        'next_patch_strategy': clip(data.get('next_patch_strategy') or data.get('patch_strategy'), 1200),
        'validation_focus': [text(item) for item in as_list(data.get('validation_focus')) if text(item)],
        **({'raw_analysis': clip(data.get('raw_analysis'), 1200)} if data.get('raw_analysis') else {}),
    }


def _repair_diagnosis(plan: Mapping[str, Any], policy: Mapping[str, Any], services: Any, attempt: int,
                      feedback: Mapping[str, Any], analysis: Mapping[str, Any]) -> dict[str, Any]:
    prompt = (
        '你是 LazyRAG evo 修复规划器。基于压缩后的失败 case、trace 特征和上一轮复测反馈，'
        '分析最可能的代码修复方向。不要编造外部信息。输出简洁中文，包含 root_cause、target_files、'
        'patch_strategy、validation_focus、risk。\n\n'
        f'Attempt: {attempt}\n'
        f'Repair policy: {json.dumps(json_safe(_diagnosis_policy(policy)), ensure_ascii=False, sort_keys=True)}\n'
        'Analysis summary: '
        f'{json.dumps(json_safe(dict(plan.get("analysis_summary") or {})), ensure_ascii=False, sort_keys=True)}\n'
        f'Failure analysis: {json.dumps(json_safe(dict(analysis)), ensure_ascii=False, sort_keys=True)}\n'
        f'Previous feedback: {json.dumps(json_safe(dict(feedback)), ensure_ascii=False, sort_keys=True)}\n'
        'Evidence cases: '
        f'{json.dumps(json_safe(list(plan.get("evidence_cases") or [])), ensure_ascii=False, sort_keys=True)}'
    )
    return {
        'attempt': attempt,
        'model_analysis': clip(services.llm_complete(prompt), 4000),
        'target_cases': list(plan.get('target_cases') or []),
        'analysis_summary': dict(plan.get('analysis_summary') or {}),
        'failure_analysis': dict(analysis),
        'previous_feedback': dict(feedback),
    }


def _opencode_task(plan: Mapping[str, Any], workspace: Mapping[str, Any],
                   diagnosis: Mapping[str, Any], analysis: Mapping[str, Any],
                   memory: Mapping[str, Any], attempt: int) -> dict[str, Any]:
    policy = plan.get('policy') if isinstance(plan.get('policy'), Mapping) else {}
    seed_files = as_list(policy.get('seed_files')) or [
        'lazymind/chat/engine/prompts/guidance.py',
        'lazymind/chat/engine/prompts/system_prompt.py',
        'lazymind/chat/engine/agent_core.py',
        'lazymind/chat/service/chat_service.py',
        'lazymind/chat/engine/tools/kb.py',
    ]
    task = {
        'mode': 'lazyrag_evo_repair_patch_once',
        'attempt': attempt,
        'objective': 'Patch the LazyRAG chat/RAG implementation so the failing evo evaluation cases improve.',
        'workspace': {'path': workspace.get('workspace_ref'), 'source_dir': workspace.get('source_dir')},
        'allowed_roots': as_list(policy.get('allowed_roots')) or ['lazymind/chat'],
        'blocked_roots': as_list(policy.get('blocked_roots')) or ['tests', '.git', 'lazyllm'],
        'seed_files': seed_files,
        'evidence_cases': plan.get('evidence_cases') or [],
        'failure_analysis': analysis,
        'repair_memory': memory,
        'validation_targets': {
            'must_fix_cases': [
                text(item.get('case_id') if isinstance(item, Mapping) else item)
                for item in as_list((analysis.get('must_fix_cases') if isinstance(analysis, Mapping) else None)
                                    or list(memory.get('failed_cases', {})))
                if text(item.get('case_id') if isinstance(item, Mapping) else item)
            ],
            'must_not_regress': [
                text(row.get('case_id')) for row in as_list(memory.get('regressed_cases'))
                if isinstance(row, Mapping) and text(row.get('case_id'))
            ],
        },
        'diagnosis': diagnosis,
        'instructions': [
            'Read only the seed_files first. Do not inspect vendored lazyllm sources unless a seed file directly '
            'points to an allowed-root wrapper that must be changed.',
            'You must make one smallest code change in allowed_roots that addresses the observed '
            'RAG/tool/generation failure, unless the seed files prove no safe code patch exists.',
            'Do not edit tests, vendored lazyllm code, secrets, or unrelated modules.',
            'If the evidence points to bad source/OCR data rather than code, still inspect retrieval/chat handling '
            'and only patch when a code-level improvement is justified.',
            'Treat tool_errors as first-class evidence. If a tool is unavailable or inactive despite a retrieval '
            'need, fix the candidate chat tool registration, prompt/tool naming, or dispatch behavior instead of '
            'masking the failure in evaluation code.',
            'After editing, run: python -m compileall -q lazymind/chat.',
            'Stop immediately after the first minimal patch and leave the git diff in the workspace.',
            'Do not repeat a failed strategy listed in repair_memory.attempted_strategies or '
            'repair_memory.failed_strategies.',
        ],
        'stop_condition': (
            'A git diff exists in allowed_roots. If no safe patch exists, write a final note explaining the exact '
            'seed file evidence; do not continue broad exploration.'
        ),
    }
    return json_safe(task)


def _run_repair_attempt(
    *,
    workspace_ref: str,
    task: Mapping[str, Any],
    policy: Mapping[str, Any],
    session_id: str,
    attempt: int,
    services: Any,
) -> Mapping[str, Any]:
    root = Path(workspace_ref).resolve()
    env = _opencode_env_from_llm_config(getattr(services, 'llm_config', {}))
    if not env:
        return _failed_repair_attempt('missing_llm_config', attempt)
    result = run_opencode_streaming(
        container='',
        workdir=str(root),
        prompt=json.dumps(task, ensure_ascii=False, indent=2),
        artifact_dir=root / '.evo_repair_logs' / 'opencode' / f'attempt_{attempt}',
        session_id=session_id,
        env=env,
        timeout_s=int_between(policy.get('opencode_timeout_s') or os.getenv(
            'LAZYMIND_EVO_CODE_TIMEOUT_S'), 900, 30, 7200),
        first_response_timeout_s=int_between(
            policy.get('opencode_first_response_timeout_s') or os.getenv('LAZYMIND_EVO_CODE_FIRST_RESPONSE_TIMEOUT_S'),
            300,
            10,
            1800,
        ),
    )
    return {'session_id': result.session_id, 'trace': trace_payload(result, 'repair.plan', attempt), 'raw': result}


def _failed_repair_attempt(reason: str, attempt: int) -> Mapping[str, Any]:
    trace = {
        'id': f'opencode_run_trace_attempt_{attempt}',
        'repair_plan_ref': 'repair.plan',
        'attempt': attempt,
        'returncode': 1,
        'raw_paths': {},
        'prompt_delivery': {'mode': 'skipped', 'instruction': '', 'prompt_path': ''},
        'provider': '',
        'model': '',
        'mapping_status': 'failed',
        'session_mapping': {'status': 'unmapped', 'source': 'llm_config', 'session_id': ''},
        'event_counts': {'configuration_error': 1},
        'ui_events': [{
            'index': 0,
            'kind': 'error',
            'title': 'opencode 配置缺失',
            'summary': 'repair requires evo_llm or llm in request llm_config with provider/model/base_url/api_key',
            'paths': [],
            'status': 'failed',
            'raw_event_index': 0,
        }],
        'files_modified': [],
        'last_error': {
            'type': reason,
            'message': 'repair requires evo_llm or llm in request llm_config with provider/model/base_url/api_key',
        },
        'duration_seconds': 0.0,
        'setup_seconds': 0.0,
        'first_response_seconds': None,
        'first_response_diagnosis': {'kind': reason, 'setup_seconds': 0.0, 'first_response_seconds': None},
    }
    return {'session_id': '', 'trace': trace, 'raw': {'last_error': trace['last_error']}}


def _mini_eval_attempt(workspace_ref: str, diff: str, plan: Mapping[str, Any], cases: Mapping[str, Any],
                       baseline_judges: Mapping[str, Any], eval_policy: Mapping[str, Any],
                       candidate_config: Mapping[str, Any], policy: Mapping[str, Any], services: Any,
                       attempt: int) -> dict[str, Any]:
    selected = _mini_eval_cases(plan, cases, baseline_judges, policy)
    if not selected:
        return {'status': 'skipped', 'accepted': False, 'reason': 'no_mini_eval_cases'}
    patch = {'status': 'verified', 'workspace_ref': workspace_ref, 'diff': diff}
    try:
        service = abtest.candidate_service(_candidate_config(candidate_config, policy), patch, services)
    except Exception as error:  # noqa: BLE001 - mini-eval must return an attempt artifact instead of aborting repair.
        return {
            'status': 'candidate_service_failed',
            'accepted': False,
            'reason': f'candidate_service_failed:{type(error).__name__}',
            'case_ids': list(selected),
            'execution_failures': [{'case_id': '', 'failure_type': 'candidate_service_failed',
                                    'reason': f'{type(error).__name__}: {error}'}],
        }
    if service.get('status') != 'ready':
        return {'status': 'candidate_service_failed', 'accepted': False, 'reason': 'candidate_service_not_ready',
                'service': service, 'case_ids': list(selected),
                'execution_failures': [{'case_id': '', 'failure_type': 'candidate_service_not_ready',
                                        'reason': text(service.get('status'))}]}
    candidate_judges = {}
    for case_id, case in selected.items():
        _raise_if_cancelled(services)
        try:
            answer = abtest.candidate_rag_answer(case, service, services)
            candidate_judges[case_id] = abtest.candidate_judge(answer, eval_policy, services)
        except Exception as error:  # noqa: BLE001 - keep the attempt inspectable and allow another repair round.
            candidate_judges[case_id] = _failed_candidate_judge(case, service, error)
    baseline_summary = eval_summary({case_id: baseline_judges[case_id]
                                    for case_id in selected if case_id in baseline_judges})
    candidate_summary = abtest.candidate_summary(candidate_judges)
    comparison = abtest.compare_abtest(baseline_summary, candidate_summary)
    baseline_has_goodcase = bool(_baseline_good_case_ids(cases, baseline_judges))
    gate = _mini_eval_gate(comparison, candidate_summary, policy, baseline_has_goodcase)
    return {
        'status': 'accepted' if gate['accepted'] else 'rejected',
        'accepted': gate['accepted'],
        'reason': gate['reason'],
        'attempt': attempt,
        'case_ids': list(selected),
        'service': {
            'status': service.get('status'),
            'algorithm_id': service.get('algorithm_id'),
            'service_url': service.get('service_url'),
        },
        'metrics': comparison.get('metrics', {}),
        'case_deltas': comparison.get('case_deltas', []),
        'goodcase_guard': comparison.get('goodcase_guard', {}),
        'guard_required': baseline_has_goodcase,
        'candidate_summary': {
            'total': candidate_summary.get('total'),
            'metrics': candidate_summary.get('metrics'),
            'execution_failures': candidate_summary.get('execution_failures'),
        },
        'feedback_cases': _mini_eval_feedback_cases(comparison, candidate_judges, baseline_judges),
        'execution_failures': candidate_summary.get('execution_failures') or [],
    }


def _failed_candidate_judge(case: Mapping[str, Any], service: Mapping[str, Any], error: Exception) -> dict[str, Any]:
    case_id = text(case.get('id'))
    reason = f'CandidateEvalError: {type(error).__name__}: {error}'
    return {
        'case_id': case_id,
        'case': case,
        'rag_answer': {'case_id': case_id, 'case': case, 'status': 'failed', 'answer': '', 'chat_error': {
            'type': 'candidate_eval_exception', 'message': reason}},
        **{key: 0.0 for key in METRICS},
        'retrieval_failure_type': 'retrieval_miss',
        'quality_label': 'infra_failure',
        'failure_type': 'infra_failure',
        'is_correct': False,
        'reason': reason[:500],
        'trace_id': '',
        'target': {'algorithm_id': text(service.get('algorithm_id')), 'routed_algorithm_id': ''},
        'tool_errors': [],
    }


def _mini_eval_cases(plan: Mapping[str, Any], cases: Mapping[str, Any],
                     baseline_judges: Mapping[str, Any], policy: Mapping[str, Any]) -> dict[str, Mapping[str, Any]]:
    budget = int_between(policy.get('mini_eval_case_budget'), 6, 1, 20)
    good_candidates = _baseline_good_case_ids(cases, baseline_judges)
    guard_budget = int_between(policy.get('mini_eval_guard_budget'), 2, 0, 10)
    if good_candidates and guard_budget < 1:
        guard_budget = 1
    guard_budget = min(guard_budget, budget)
    target_ids = [text(item.get('case_id')) for item in as_list(plan.get('target_cases')) if isinstance(item, Mapping)]
    bad_ids = [case_id for case_id in target_ids if case_id in cases][:max(0, budget - guard_budget)]
    good_ids = [case_id for case_id in good_candidates if case_id not in bad_ids][:guard_budget]
    chosen = list(dict.fromkeys([*bad_ids, *good_ids]))
    if len(chosen) < budget:
        extras = [
            case_id for case_id, judge in baseline_judges.items()
            if case_id in cases and case_id not in chosen and text(judge.get('failure_type')) != 'infra_failure'
        ]
        chosen.extend(extras[:budget - len(chosen)])
    return {case_id: cases[case_id] for case_id in chosen if isinstance(cases.get(case_id), Mapping)}


def _baseline_good_case_ids(cases: Mapping[str, Any], baseline_judges: Mapping[str, Any]) -> list[str]:
    return [
        case_id for case_id, judge in baseline_judges.items()
        if case_id in cases and (judge.get('is_correct') or text(judge.get('quality_label')) == 'good')
    ]


def _mini_eval_gate(comparison: Mapping[str, Any], candidate_summary: Mapping[str, Any],
                    policy: Mapping[str, Any], baseline_has_goodcase: bool) -> dict[str, Any]:
    metrics = comparison.get('metrics') if isinstance(comparison.get('metrics'), Mapping) else {}
    delta = metrics.get('delta') if isinstance(metrics.get('delta'), Mapping) else {}
    min_gain = float(policy.get('answer_correctness_min_gain') or 0.0001)
    gain = float(delta.get('answer_correctness') or 0.0)
    guard = comparison.get('goodcase_guard') if isinstance(comparison.get('goodcase_guard'), Mapping) else {}
    failures = as_list(candidate_summary.get('execution_failures'))
    if comparison.get('status') != 'completed':
        return {'accepted': False, 'reason': text(comparison.get('verdict') or comparison.get('status'))}
    if failures:
        return {'accepted': False, 'reason': 'candidate_execution_failed'}
    if guard.get('status') == 'failed':
        return {'accepted': False, 'reason': 'goodcase_guard_failed'}
    if baseline_has_goodcase and guard.get('status') != 'passed':
        return {'accepted': False, 'reason': f"goodcase_guard_{text(guard.get('status') or 'missing')}"}
    if gain < min_gain:
        return {'accepted': False, 'reason': f'answer_correctness_not_improved:{gain}'}
    return {'accepted': True, 'reason': f'answer_correctness_improved:{gain}'}


def _candidate_config(config: Mapping[str, Any], policy: Mapping[str, Any]) -> dict[str, Any]:
    extra = policy.get('candidate') if isinstance(policy.get('candidate'), Mapping) else {}
    merged = {**dict(config), **dict(extra)}
    merged.pop('algorithm_id', None)
    return merged


def _evidence_budget(policy: Mapping[str, Any]) -> int:
    return int_between(policy.get('evidence_case_budget'), 8, 1, 30)


def _diagnosis_policy(policy: Mapping[str, Any]) -> dict[str, Any]:
    keep = ('allowed_roots', 'blocked_roots', 'seed_files', 'repair_attempt_budget',
            'mini_eval_case_budget', 'mini_eval_guard_budget', 'answer_correctness_min_gain')
    return {key: policy.get(key) for key in keep if key in policy}


def _compact_trace(value: Any) -> dict[str, Any]:
    trace = value if isinstance(value, Mapping) else {}
    return {
        'available': bool(trace.get('trace_available')),
        'route': text(trace.get('route_signature')),
        'bottleneck_stage': text(trace.get('bottleneck_stage')),
        'error_stages': [
            {'stage': text(item.get('stage')), 'error': clip(item.get('error'), 120)}
            for item in as_list(trace.get('error_stages'))[:3]
            if isinstance(item, Mapping)
        ],
        'retrieved_doc_ids': as_list(trace.get('retrieved_doc_ids'))[:5],
        'retrieved_chunk_ids': as_list(trace.get('retrieved_chunk_ids'))[:5],
    }


def _next_feedback(attempt: Mapping[str, Any], memory: Mapping[str, Any]) -> dict[str, Any]:
    mini_eval = attempt.get('mini_eval') if isinstance(attempt.get('mini_eval'), Mapping) else {}
    guard = mini_eval.get('goodcase_guard') if isinstance(mini_eval.get('goodcase_guard'), Mapping) else {}
    return {
        'failed_attempt': attempt.get('attempt'),
        'failure': text(attempt.get('failure')),
        'mini_eval_status': text(mini_eval.get('status')),
        'mini_eval_reason': text(mini_eval.get('reason')),
        'execution_failures': as_list(mini_eval.get('execution_failures'))[:5],
        'guard_violations': as_list(guard.get('violations'))[:5],
        'case_feedback': as_list(mini_eval.get('feedback_cases'))[:5],
        'memory': dict(memory),
    }


def _merge_analysis_memory(memory: Mapping[str, Any], analysis: Mapping[str, Any]) -> dict[str, Any]:
    return {
        **dict(memory),
        'failed_strategies': [
            *as_list(memory.get('failed_strategies')),
            *[clip(item, 360) for item in as_list(analysis.get('failed_strategies')) if text(item)],
        ][-10:],
    }


def _repair_memory(memory: Mapping[str, Any], attempt: Mapping[str, Any]) -> dict[str, Any]:
    mini_eval = attempt.get('mini_eval') if isinstance(attempt.get('mini_eval'), Mapping) else {}
    analysis = attempt.get('failure_analysis') if isinstance(attempt.get('failure_analysis'), Mapping) else {}
    failed_cases = dict(memory.get('failed_cases') or {})
    for row in as_list(mini_eval.get('feedback_cases')):
        if (isinstance(row, Mapping) and text(row.get('case_id'))
                and (text(row.get('candidate_failure_type')) != 'none'
                     or text(row.get('outcome')) in {'regressed', 'missing_candidate', 'missing_baseline'})):
            failed_cases[text(row.get('case_id'))] = {
                'outcome': text(row.get('outcome')),
                'failure_type': text(row.get('candidate_failure_type')),
                'reason': clip(row.get('candidate_reason'), 180),
                'answer': clip(row.get('candidate_answer'), 220),
                'trace_id': text(row.get('candidate_trace_id')),
            }
    return {
        'attempted_strategies': [*as_list(memory.get('attempted_strategies')), {
            'attempt': attempt.get('attempt'),
            'failure': text(attempt.get('failure')),
            'files_changed': as_list(attempt.get('files_changed')),
            'strategy': clip(analysis.get('next_patch_strategy') or (attempt.get('diagnosis') or {}).get(
                'model_analysis'), 500),
        }][-10:],
        'failed_strategies': as_list(memory.get('failed_strategies'))[-10:],
        'failed_cases': failed_cases,
        'regressed_cases': [
            *as_list(memory.get('regressed_cases')),
            *[
                {'case_id': text(row.get('case_id')), 'delta': row.get('delta'),
                 'failure_type': text(row.get('candidate_failure_type'))}
                for row in as_list(mini_eval.get('case_deltas'))
                if isinstance(row, Mapping) and text(row.get('outcome')) == 'regressed'
            ],
        ][-10:],
    }


def _case_ids(value: Any) -> list[str]:
    return [
        text(item.get('case_id') if isinstance(item, Mapping) else item)
        for item in as_list(value)
        if text(item.get('case_id') if isinstance(item, Mapping) else item)
    ]


def _mini_eval_feedback_cases(comparison: Mapping[str, Any], candidate_judges: Mapping[str, Any],
                              baseline_judges: Mapping[str, Any]) -> list[dict[str, Any]]:
    judges = {
        text(row.get('case_id')): row
        for row in candidate_judges.values()
        if isinstance(row, Mapping) and text(row.get('case_id'))
    }
    rows = []
    for delta in as_list(comparison.get('case_deltas')):
        if not isinstance(delta, Mapping):
            continue
        case_id = text(delta.get('case_id'))
        judge = judges.get(case_id, {})
        baseline = baseline_judges.get(case_id) if isinstance(baseline_judges.get(case_id), Mapping) else {}
        case = baseline.get('case') if isinstance(baseline.get('case'), Mapping) else {}
        baseline_answer = baseline.get('rag_answer') if isinstance(baseline.get('rag_answer'), Mapping) else {}
        answer = judge.get('rag_answer') if isinstance(judge.get('rag_answer'), Mapping) else {}
        score_delta = delta.get('delta') if isinstance(delta.get('delta'), Mapping) else {}
        rows.append({
            'case_id': case_id,
            'outcome': text(delta.get('outcome')),
            'question_type': text(case.get('question_type')),
            'question': clip(case.get('question'), 240),
            'reference_answer': clip(case.get('answer'), 320),
            'baseline_answer': clip(baseline_answer.get('answer'), 320),
            'answer_correctness_delta': score_delta.get('answer_correctness'),
            'chunk_recall_delta': score_delta.get('chunk_recall'),
            'candidate_failure_type': text(judge.get('failure_type') or delta.get('candidate_failure_type')),
            'candidate_reason': clip(judge.get('reason'), 280),
            'candidate_answer': clip(answer.get('answer'), 360),
            'candidate_trace_id': text(judge.get('trace_id') or answer.get('trace_id')),
        })
    return rows[:8]


def _repair_attempt_budget(policy: Mapping[str, Any]) -> int:
    return int_between(policy.get('repair_attempt_budget') or os.getenv('EVO_REPAIR_ATTEMPT_BUDGET'), 100, 1, 100)


def _better_attempt(best: Mapping[str, Any], candidate: Mapping[str, Any]) -> dict[str, Any]:
    if not best:
        return dict(candidate)
    best_delta = ((best.get('mini_eval') or {}).get('metrics') or {}).get('delta') or {}
    candidate_delta = ((candidate.get('mini_eval') or {}).get('metrics') or {}).get('delta') or {}
    return dict(candidate) if _metric_delta(candidate_delta) > _metric_delta(best_delta) else dict(best)


def _metric_delta(delta: Mapping[str, Any]) -> float:
    value = delta.get('answer_correctness')
    return float(value) if value is not None else -1.0


def _reset_repair_workspace(workspace_ref: str) -> None:
    workspace = Path(text(workspace_ref)).resolve()
    _git(workspace, 'reset', '--hard', 'HEAD')
    _git(workspace, 'clean', '-fd', '-e', '.evo_repair_logs', '--', '.')


def _repair_workspace_diff(workspace_ref: str) -> Mapping[str, Any]:
    workspace = Path(text(workspace_ref)).resolve()
    untracked = [
        path for path in _git(workspace, 'ls-files', '--others', '--exclude-standard').splitlines()
        if path and path != 'opencode.json' and not path.startswith('.evo_repair_logs/')
        and '__pycache__/' not in path and not path.endswith('.pyc')
    ]
    if untracked:
        _git(workspace, 'add', '-N', '--', *untracked)
    return {'diff': _git(workspace, 'diff', '--'), 'files': _git(workspace, 'diff', '--name-only').splitlines()}


def _diff_scope(files: list[str], policy: Mapping[str, Any]) -> dict[str, Any]:
    allowed = [_norm_root(item) for item in (as_list(policy.get('allowed_roots')) or ['lazymind/chat'])]
    blocked = [_norm_root(item) for item in (as_list(policy.get('blocked_roots')) or ['tests', '.git', 'lazyllm'])]
    violations = [path for path in files if not _path_allowed(path, allowed) or _path_allowed(path, blocked)]
    return {'status': 'passed' if not violations else 'failed',
            'allowed_roots': allowed, 'blocked_roots': blocked, 'violations': violations}


def _norm_root(value: Any) -> str:
    return text(value).strip().strip('/').rstrip('/')


def _path_allowed(path: str, roots: list[str]) -> bool:
    normalized = _norm_root(path)
    return any(normalized == root or normalized.startswith(f'{root}/') for root in roots if root)


def _repair_failure(result: Any, diff: str, verification: Mapping[str, Any], diff_scope: Mapping[str, Any],
                    mini_eval: Mapping[str, Any]) -> str:
    raw = result.get('raw') if isinstance(result, Mapping) else result
    last_error = raw.get('last_error') if isinstance(raw, Mapping) else getattr(raw, 'last_error', None)
    if isinstance(last_error, Mapping):
        return text(last_error.get('type') or last_error.get('message') or 'opencode_failed')
    if not diff.strip():
        return 'no_diff'
    if verification.get('status') != 'passed':
        return 'verification_failed'
    if diff_scope.get('status') != 'passed':
        return 'diff_scope_violation'
    return text(mini_eval.get('reason') or mini_eval.get('status') or 'mini_eval_rejected')


def _repair_workspace(policy: Mapping[str, Any], plan: Mapping[str, Any]) -> Path:
    base = _repair_base_dir()
    configured = text(policy.get('candidate_workdir'))
    if configured:
        workspace = Path(configured).resolve()
        if not _path_within(workspace, base):
            raise RuntimeError(f'candidate workspace must be under managed repair dir: {base}')
        return workspace
    identity = {'target_cases': plan.get('target_cases'), 'evidence': plan.get('evidence_cases')}
    suffix = hashlib.sha1(stable_text(identity).encode('utf-8')).hexdigest()[:12]
    return base / suffix / 'candidate'


def _repair_base_dir() -> Path:
    return (Path(os.getenv('LAZYMIND_EVO_BASE_DIR') or '/var/lib/lazymind/evo') / 'work' / 'repair').resolve()


def _algorithm_source_root(value: Any) -> Path:
    path = Path(text(value)).resolve()
    for candidate in (path, *path.parents):
        if _is_algorithm_source(candidate):
            return candidate
    return path


def _path_within(path: Path, root: Path) -> bool:
    resolved, resolved_root = path.resolve(), root.resolve()
    return resolved == resolved_root or resolved_root in resolved.parents


def _prepare_repair_workspace(source: Path, workspace: Path) -> None:
    if not _is_algorithm_source(source):
        raise RuntimeError(f'candidate source is not LazyRAG algorithm dir: {source}')
    if _path_overlaps(source, workspace):
        raise RuntimeError(f'candidate workspace must be outside source tree: source={source}, workspace={workspace}')
    if not workspace.exists():
        _copy_algorithm_source(source, workspace)
    if not _is_algorithm_source(workspace):
        raise RuntimeError(f'candidate workspace is not LazyRAG algorithm dir: {workspace}')
    abtest.normalize_candidate_sources(workspace)
    _ensure_git_baseline(workspace)
    _git(workspace, 'reset', '--hard', 'HEAD')
    _git(workspace, 'clean', '-fd', '--', '.')
    abtest.normalize_candidate_sources(workspace)
    if _git_status_code(workspace, 'diff', '--quiet', '--'):
        _git(workspace, 'add', '.')
        _git(workspace, '-c', 'user.email=evo@example.local', '-c', 'user.name=evo',
             'commit', '-m', 'candidate runtime baseline')


def _copy_algorithm_source(source: Path, target: Path) -> None:
    target.mkdir(parents=True, exist_ok=True)
    ignore = shutil.ignore_patterns('.git', '.evo_repair_logs', '__pycache__', '*.pyc')
    for name in ('lazymind', 'chat', 'common', 'vocab', 'parsing', 'processor'):
        if (source / name).exists():
            shutil.copytree(source / name, target / name, ignore=ignore, dirs_exist_ok=True)
    for name in ('.dockerignore', 'Dockerfile', 'config.py', 'requirements.txt'):
        if (source / name).exists():
            shutil.copy2(source / name, target / name)


def _is_algorithm_source(path: Path) -> bool:
    return (path / 'lazymind' / 'chat' / 'app.py').exists()


def _path_overlaps(left: Path, right: Path) -> bool:
    left_resolved, right_resolved = left.resolve(), right.resolve()
    return (
        left_resolved == right_resolved
        or left_resolved in right_resolved.parents
        or right_resolved in left_resolved.parents
    )


def _ensure_git_baseline(workspace: Path) -> None:
    if not (workspace / '.git').exists():
        _git(workspace, 'init')
    if _git_status_code(workspace, 'rev-parse', '--verify', 'HEAD'):
        _git(workspace, 'add', '.')
        _git(workspace, '-c', 'user.email=evo@example.local', '-c', 'user.name=evo', 'commit', '-m', 'baseline')


def _verify_repair_workspace(workspace: Path, policy: Mapping[str, Any]) -> dict[str, Any]:
    results = []
    for command in _verification_commands(policy):
        try:
            completed = subprocess.run(command, cwd=str(workspace), capture_output=True,
                                       text=True, timeout=120, check=False)
        except subprocess.TimeoutExpired as error:
            results.append({
                'command': command,
                'returncode': None,
                'stdout': clip(error.stdout, 2000),
                'stderr': clip(error.stderr or str(error), 2000),
                'error_type': 'timeout',
            })
            return {'status': 'failed', 'results': results}
        except Exception as error:  # noqa: BLE001 - verification failure must remain an inspectable repair artifact.
            results.append({
                'command': command,
                'returncode': None,
                'stdout': '',
                'stderr': clip(str(error), 2000),
                'error_type': type(error).__name__,
            })
            return {'status': 'failed', 'results': results}
        results.append({
            'command': command,
            'returncode': completed.returncode,
            'stdout': clip(completed.stdout, 2000),
            'stderr': clip(completed.stderr, 2000),
        })
        if completed.returncode:
            return {'status': 'failed', 'results': results}
    return {'status': 'passed', 'results': results}


def _verification_commands(policy: Mapping[str, Any]) -> list[list[str]]:
    raw = as_list(policy.get('verification_commands')) or [['python', '-m', 'compileall', '-q', 'lazymind/chat']]
    commands = []
    for item in raw:
        if isinstance(item, str):
            commands.append(shlex.split(item))
        elif isinstance(item, (list, tuple)):
            commands.append([text(part) for part in item if text(part)])
    return [command for command in commands if command]


def _git(workspace: Path, *args: str) -> str:
    result = subprocess.run(
        ['git', '-c', f'safe.directory={workspace}', '-C', str(workspace), *args],
        capture_output=True,
        text=True,
        timeout=60,
        check=False,
    )
    if result.returncode:
        raise RuntimeError((result.stderr or result.stdout).strip())
    return result.stdout.strip()


def _git_status_code(workspace: Path, *args: str) -> int:
    return subprocess.run(
        ['git', '-c', f'safe.directory={workspace}', '-C', str(workspace), *args],
        capture_output=True,
        text=True,
        timeout=60,
        check=False,
    ).returncode


def _raise_if_cancelled(services: Any) -> None:
    if hasattr(services, 'raise_if_cancelled'):
        services.raise_if_cancelled()


def _opencode_env_from_llm_config(llm_config: Mapping[str, Any] | None) -> dict[str, str]:
    cfg = llm_config or {}
    role = cfg.get('evo_llm') or cfg.get('llm') if isinstance(cfg, Mapping) else {}
    if not isinstance(role, Mapping):
        return {}
    model = text(role.get('model'))
    base_url = _opencode_base_url(text(role.get('base_url') or role.get('url')))
    api_key = text(role.get('api_key'))
    provider = _opencode_provider(role, model, base_url)
    if not (provider and model and base_url and api_key):
        return {}
    safe = ''.join(ch if ch.isalnum() else '_' for ch in provider.upper())
    key_env = f'OPENCODE_{safe}_API_KEY'
    return {
        'OPENCODE_MODEL': f'{provider}/{model}',
        'OPENCODE_PROVIDER': provider,
        'OPENCODE_PROVIDER_MODEL': model,
        'OPENCODE_PROVIDER_LABEL': provider,
        'OPENCODE_PROVIDER_BASE_URL': base_url.rstrip('/'),
        'OPENCODE_PROVIDER_KEY_ENV': key_env,
        key_env: api_key,
    }


def _opencode_provider(role: Mapping[str, Any], model: str, base_url: str) -> str:
    raw = text(role.get('provider') or role.get('source')
               or _provider_from_url(base_url) or _provider_from_model(model))
    return ''.join(ch.lower() if ch.isalnum() else '_' for ch in raw).strip('_')


def _provider_from_url(url: str) -> str:
    lowered = url.lower()
    if 'deepseek' in lowered:
        return 'deepseek'
    if 'dashscope' in lowered or 'qwen' in lowered or 'aliyun' in lowered:
        return 'qwen'
    if 'openai' in lowered:
        return 'openai'
    if 'siliconflow' in lowered:
        return 'siliconflow'
    return ''


def _opencode_base_url(url: str) -> str:
    if not url:
        return ''
    lowered = url.rstrip('/').lower()
    if 'dashscope.aliyuncs.com' in lowered and not lowered.endswith('/compatible-mode/v1'):
        return 'https://dashscope.aliyuncs.com/compatible-mode/v1'
    return url


def _provider_from_model(model: str) -> str:
    lowered = model.lower()
    if lowered.startswith('deepseek'):
        return 'deepseek'
    if lowered.startswith('qwen') or lowered.startswith('qwen/'):
        return 'qwen'
    if lowered.startswith('gpt-') or lowered.startswith('openai/'):
        return 'openai'
    return ''
