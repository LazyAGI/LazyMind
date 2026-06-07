from __future__ import annotations

import hashlib
import json
import os
import random
import shutil
import subprocess
from pathlib import Path
from typing import Any
from uuid import uuid4

import numpy as np

from ..analysis.candidate import candidate_failure_categories, candidate_row_refs, candidate_trace_summary
from ..analysis.candidate import classify_candidate_case
from ..analysis.utils import METRICS, score, short, typed_payload
from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import json_object, validate_case_id
from ...ids import validate_id
from ...runtime import AdapterCall, OperationContext, OperationOutput, evo_llm
from ..eval.judge_answer import PROMPT as JUDGE_PROMPT
from ..eval.judge_answer import _failure_type, _hits, _judge_contexts, _quality, _recall, _scores
from ..eval.rag_answer import KB_CHAT_TOOLS, _call_chat
from .candidate import command_args, default_repair_scope, ensure_git_baseline, start_candidate_process, terminate_pid
from .opencode import PASSTHROUGH_PREFIXES, run_opencode_streaming, trace_payload

DEFAULT_REPAIR_ATTEMPT_BUDGET = 3

REPAIR_DIRECTION_CONFIG = {
    'retrieval_doc_miss': ('improve retrieval document recall',
                           ['lazymind/chat/engine/tools/algo/kb_adaptive_topk.py',
                            'lazymind/chat/engine/tools/algo/search_kb.py'],
                           'Make one conservative retrieval recall improvement inside allowed roots.'),
    'retrieval_chunk_miss': ('improve chunk recall and final context selection',
                             ['lazymind/chat/engine/tools/algo/kb_adaptive_topk.py',
                              'lazymind/chat/engine/tools/algo/kb_context_expansion.py'],
                             'Make one conservative chunk recall improvement inside allowed roots.'),
    'tool_result_integration_issue': ('preserve successful tool outputs through final answer synthesis',
                                      ['lazymind/chat/service/chat_service.py',
                                       'lazymind/chat/service/component/event_translator.py'],
                                      'Make one minimal tool-result integration fix inside allowed roots.'),
    'tool_execution_issue': ('preserve successful tool execution outputs',
                             ['lazymind/chat/service/chat_service.py',
                              'lazymind/chat/engine/tools/kb.py'],
                             'Preserve successful tool_call_trace results when tool execution raises.'),
}
PATCH_IGNORES = ('.evo_repair_logs',), {'opencode.json'}


class BuildRepairLoopPlanOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        report_ref = ArtifactRef.parse(str(ctx.params.get('classification_report_ref') or ''))
        report = typed_payload(ctx, report_ref, 'ClassificationReport')
        eval_ref = ArtifactRef.parse(str(report.get('eval_report_ref') or ''))
        eval_report = typed_payload(ctx, eval_ref, 'EvalReport')
        output_id = validate_id(str(ctx.params.get('output_id') or 'repair_loop_plan'), 'output_id')
        priorities = [p for p in report.get('priorities') or [] if isinstance(p, dict)]
        category = _category(ctx, priorities)
        targets, baseline = _target_rows(ctx, report, priorities, category), _baseline(ctx)
        guards, guard_meta = _goodcase_guard(ctx, eval_report, targets)
        delta = float(ctx.params.get('target_mean_delta', 0.02))
        policy = {'primary_metric': str(ctx.params.get('primary_metric') or 'answer_correctness'),
                  'target_mean_delta': round(delta / 100 if delta >= 1 else delta, 4),
                  'goodcase_regression_ratio_limit': round(float(ctx.params.get(
                      'goodcase_regression_ratio_limit', 0.34
                  )), 4)}
        if policy['primary_metric'] not in METRICS:
            raise ValueError(f'unsupported primary_metric: {policy["primary_metric"]}')
        payload = {'id': output_id, 'classification_report_ref': str(report_ref), 'eval_report_ref': str(eval_ref),
                   'eval_dataset_ref': str(eval_report.get('eval_dataset_ref') or report.get('eval_dataset_ref') or ''),
                   'target': {'fine_category': category,
                              'coarse_categories': sorted({str(row.get('coarse_category') or '') for row in targets}),
                              'badcase_ids': [row['case_id'] for row in targets],
                              'fine_refs': [row['fine_classification_ref'] for row in targets],
                              'baseline_judge_refs': [row['judge_result_ref'] for row in targets]},
                   'guard': {'goodcase_ids': [row['case_id'] for row in guards],
                             'goodcase_judge_refs': [row['judge_ref'] for row in guards],
                             **guard_meta,
                             'sample_seed': str(ctx.params.get('random_seed')
                                                or f'{ctx.run_id}:{report_ref}:{category}')},
                   'policy': policy, 'baseline': baseline,
                   'loop_state': {'opencode_session_id': '',
                                  'candidate_workspace_ref': baseline.get('code_base_ref') or ''},
                   'source_message_id': str(ctx.params.get('source_message_id') or '')}
        refs = [report_ref, eval_ref, *[ArtifactRef.parse(row['fine_classification_ref']) for row in targets],
                *[ArtifactRef.parse(row['judge_ref']) for row in guards]]
        refs += [ArtifactRef.parse(ref) for ref in (
            baseline.get('source_ref'), baseline.get('metric_baseline_ref'),
        ) if ref]
        ctx.report_progress(phase='repair_plan', status='success', message=f'planned repair loop for {category}',
                            detail={'target_badcases': len(targets), 'guard_goodcases': len(guards)})
        return OperationOutput([_draft(output_id, 'RepairLoopPlan', payload, ctx, list(dict.fromkeys(refs)))])


class RepairLoopAgentOperation:
    def __init__(self, llm: Any | None = None, model_config: dict[str, Any] | None = None):
        self.llm, self.model_config = llm, model_config or {}

    def execute(self, ctx: OperationContext) -> OperationOutput:
        plan_ref = ArtifactRef.parse(str(ctx.params.get('repair_loop_plan_ref') or ''))
        plan = typed_payload(ctx, plan_ref, 'RepairLoopPlan')
        output_id = validate_id(str(ctx.params.get('output_id') or 'repair_loop_agent'), 'output_id')
        workspace = _workspace(ctx, plan)
        ensure_git_baseline(workspace)
        previous = _latest_state(ctx, plan_ref)
        memory = typed_payload(ctx, ArtifactRef.parse(previous['last_memory_ref']), 'RepairLoopMemory') \
            if previous.get('last_memory_ref') else {}
        session = str(previous.get('opencode_session_id')
                      or (plan.get('loop_state') or {}).get('opencode_session_id') or '')
        attempt = int(previous.get('current_attempt') or (plan.get('loop_state') or {}).get('current_attempt') or 0)
        start, budget, drafts, decision, evaluation, patch = attempt, _attempt_budget(ctx), [], {}, {}, {}
        while budget is None or attempt - start < budget:
            attempt += 1
            ctx.check_interrupt()
            ctx.report_progress(phase='repair_loop', status='running', message=f'starting repair attempt {attempt}',
                                detail={'attempt': attempt, 'attempt_budget': budget or 'unlimited'})
            made, decision, evaluation, memory, session, patch = _attempt(
                ctx, plan_ref, plan, workspace, attempt, memory, session, self._model(), self.model_config
            )
            drafts.extend(made)
            _checkpoint(workspace, attempt, *PATCH_IGNORES)
            if decision['decision'] == 'passed':
                vid = f'verified_repair_{output_id}_attempt_{attempt}'
                refs = [
                    plan_ref, ArtifactRef.parse(f"{patch['id']}@v1"),
                    ArtifactRef.parse(f"{evaluation['id']}@v1"),
                ]
                drafts.append(_draft(
                    vid, 'VerifiedRepair', _verified(vid, plan_ref, workspace, patch, evaluation, plan), ctx, refs
                ))
                break
            if budget is None or attempt - start < budget:
                ctx.report_progress(phase='repair_loop', status='running',
                                    message=f'attempt {attempt} failed; continuing',
                                    detail={'failure': evaluation.get('failure_summary', '')})
        status = 'success' if decision.get('decision') == 'passed' else 'failed'
        ctx.report_progress(phase='repair_loop', status=status,
                            message=f"repair attempt {attempt} {decision.get('decision')}",
                            detail={'decision': decision.get('decision'),
                                    'evaluation_status': evaluation.get('status')})
        return OperationOutput(drafts)

    def _model(self):
        if self.llm is None:
            self.llm = evo_llm(self.model_config)
        return self.llm


def _attempt(ctx, plan_ref, plan, workspace, attempt, memory, session, llm, model_config):
    hypothesis = _hypothesis(ctx, plan_ref, plan, attempt, workspace, memory)
    repair_plan = _repair_plan(ctx, plan_ref, hypothesis, plan, attempt)
    opencode = _run_opencode(ctx, plan, repair_plan, hypothesis, workspace, attempt, session)
    trace = trace_payload(opencode, f"{repair_plan['id']}@v1", attempt)
    patch = _patch(ctx, workspace, plan_ref, repair_plan['id'], trace, attempt, repair_plan, opencode)
    service, proc = _service(ctx, workspace, patch, attempt)
    try:
        evaluation, candidate_report, candidate_drafts = _evaluate(
            ctx, plan, service, patch, attempt, llm, model_config
        )
    finally:
        if proc:
            terminate_pid(proc.pid)
    decision = _decision(evaluation, attempt)
    memory = _memory(hypothesis, patch, evaluation, candidate_report, attempt)
    state = _state(plan_ref, workspace, attempt, opencode.session_id or session, memory, patch, evaluation, decision)
    artifacts = [('RepairHypothesis', hypothesis), ('RepairPlan', repair_plan), ('OpenCodeRunTrace', trace),
                 ('CodePatchCandidate', patch), ('CandidateServiceRun', service), ('RepairEvaluation', evaluation),
                 ('RepairLoopDecision', decision), ('RepairLoopMemory', memory), ('RepairLoopState', state)]
    drafts = [_draft(payload['id'], schema, payload, ctx, [plan_ref]) for schema, payload in artifacts]
    drafts.extend(candidate_drafts)
    refs = [plan_ref, *[
        ArtifactRef.parse(ref) for row in candidate_report.get('cases', []) for ref in candidate_row_refs(row)
    ]]
    drafts.append(_draft(candidate_report['id'], 'CandidateClassificationReport', candidate_report, ctx, refs))
    return drafts, decision, evaluation, memory, opencode.session_id or session, patch


def _hypothesis(ctx, plan_ref, plan, attempt, workspace, memory) -> dict[str, Any]:
    report = typed_payload(ctx, ArtifactRef.parse(str(plan.get('classification_report_ref') or '')),
                           'ClassificationReport')
    fine_refs = [ArtifactRef.parse(ref) for ref in (plan.get('target') or {}).get('fine_refs') or []]
    fines = [typed_payload(ctx, ref, 'CaseFineClassification') for ref in fine_refs]
    category = str((plan.get('target') or {}).get('fine_category') or (
        fines[0].get('fine_category') if fines else ''
    ))
    cfg, scope = _direction_config(category), _repair_scope(ctx)
    traces = _trace_observations(fines, memory or {})
    sources = _source_files(workspace, scope, cfg)
    target_priority = next((p for p in report.get('priorities') or [] if p.get('fine_category') == category), {})
    edit = _edit_focus(cfg, traces, sources, memory or {})
    trace_steps = [{'case_id': c.get('case_id'), 'trace_id': c.get('trace_id'),
                    'step_ids': c.get('selected_step_ids', [])} for c in traces.get('cases', [])]
    return {'id': _aid('repair_hypothesis', attempt), 'repair_loop_plan_ref': str(plan_ref), 'attempt': attempt,
            'tool_investigation': [{'tool': 'classification_report', 'observation': target_priority},
                                   {'tool': 'analysis_trace_plan', 'observation': traces},
                                   {'tool': 'source_entrypoints', 'observation': sources}],
            'analysis_card': {'fine_category': category, 'conclusion': cfg['direction'],
                              'root_cause': target_priority, 'edit_focus': edit,
                              'trace_finding': _trace_finding(traces),
                              'entrypoints': sources,
                              'previous_failure_summary': (memory or {}).get('next_focus', ''),
                              'avoid_repeating': (memory or {}).get('failed_patch_summaries', [])[-3:]},
            'supported_directions': sorted({str(f.get('fine_category') or '') for f in fines}),
            'rejected_directions': (memory or {}).get('rejected_directions', []),
            'trace_steps_read': trace_steps, 'source_files_read': sources}


def _repair_plan(ctx, plan_ref, hypothesis, plan, attempt) -> dict[str, Any]:
    cfg, scope = _direction_config(str((plan.get('target') or {}).get('fine_category') or '')), _repair_scope(ctx)
    edit = (hypothesis.get('analysis_card') or {}).get('edit_focus') or cfg['edit_instruction']
    return {'id': _aid('repair_plan', attempt), 'repair_loop_plan_ref': str(plan_ref),
            'repair_hypothesis_ref': f"{hypothesis['id']}@v1", 'attempt': attempt,
            'change_plan': {'scope': 'minimal', **scope,
                            'edits': list(dict.fromkeys([edit, cfg['edit_instruction']])),
                            'user_note': str(ctx.params.get('repair_instruction') or '').strip(),
                            'repair_direction': cfg['direction']}}


def _run_opencode(ctx, plan, repair_plan, hypothesis, workspace, attempt, session):
    env = {key: value for key, value in os.environ.items() if value and key.startswith(PASSTHROUGH_PREFIXES)}
    scope = repair_plan['change_plan']
    ctx.report_progress(phase='opencode', status='running', message=f'starting opencode attempt {attempt}',
                        detail={'repair_scope': {key: scope.get(key) for key in (
                            'allowed_roots', 'seed_files', 'blocked_roots', 'allow_new_files'
                        )}})
    return run_opencode_streaming(
        container=_opencode_container(ctx), workdir=str(workspace),
        prompt=_opencode_prompt(plan, repair_plan, hypothesis),
        artifact_dir=workspace / '.evo_repair_logs' / 'opencode' / f'attempt_{attempt}',
        session_id=session, env=env,
        timeout_s=int(ctx.params.get('opencode_timeout_s') or os.getenv('OPENCODE_TIMEOUT_S')
                      or os.getenv('LAZYMIND_EVO_CODE_TIMEOUT_S') or 900),
        first_response_timeout_s=int(ctx.params.get('opencode_first_response_timeout_s')
                                     or os.getenv('OPENCODE_FIRST_RESPONSE_TIMEOUT_S')
                                     or os.getenv('LAZYMIND_EVO_CODE_FIRST_RESPONSE_TIMEOUT_S') or 300),
        register_cancel=ctx.register_cancel_callback,
        on_event=lambda _e, c: _opencode_progress(ctx, c, attempt))


def _opencode_progress(ctx: OperationContext, compact: dict[str, Any], attempt: int) -> None:
    ui = compact.get('ui_event') if isinstance(compact.get('ui_event'), dict) else {}
    title = str(ui.get('title') or '').strip()
    summary = str(compact.get('summary') or ui.get('summary') or '').strip()
    event = str(compact.get('event_type') or 'event').strip()
    status = 'failed' if event in {'error', 'setup_failed', 'process_failed', 'timeout',
                                   'first_response_timeout'} else 'running'
    message = summary or title or f'opencode {event}'
    ctx.report_progress(
        phase='opencode',
        status=status,
        message=f'attempt {attempt}: {message[:140]}',
        current_item=str(ui.get('kind') or event),
        detail=compact,
    )


def _opencode_container(ctx: OperationContext) -> str:
    container = str(ctx.params.get('opencode_container') or os.getenv('EVO_FLOW_OPENCODE_CONTAINER') or '')
    return container if container and shutil.which('docker') else ''


def _opencode_prompt(plan, repair_plan, hypothesis=None) -> str:
    target, guard, change = plan.get('target') or {}, plan.get('guard') or {}, repair_plan.get('change_plan') or {}
    card = (hypothesis or {}).get('analysis_card') or {'edit_focus': change.get('repair_direction', '')}
    return json.dumps({
        'objective': 'Apply exactly one minimal production code patch, then stop.',
        'fine_category': target.get('fine_category', ''),
        'analysis': {k: card.get(k) for k in ('fine_category', 'conclusion', 'root_cause', 'edit_focus',
                                              'trace_finding', 'entrypoints', 'previous_failure_summary',
                                              'avoid_repeating')},
        'target_badcases': target.get('badcase_ids', []), 'guard_goodcases': guard.get('goodcase_ids', []),
        'repair_scope': {key: change.get(key) for key in (
            'allowed_roots', 'seed_files', 'blocked_roots', 'allow_new_files'
        )},
        'required_edits': [item for item in change.get('edits', []) if item], 'user_note': change.get('user_note', ''),
        'constraints': [
            'make a real production code diff; do not stop with only findings or a no-op',
            'patch the nearest execution boundary in an allowed seed file if the named handler is absent',
            'preserve decorator return values and public tool docstrings',
            'do not edit tests', 'do not touch blocked_roots or paths outside allowed_roots',
            'do not change unrelated retrieval parameters',
        ],
    }, ensure_ascii=False, indent=2)


def _patch(ctx, workspace, plan_ref, repair_ref, trace, attempt, repair_plan, opencode) -> dict[str, Any]:
    changed, created, _ = _git_status(workspace, *PATCH_IGNORES)
    _git_add_intent(workspace, created)
    diff = _git(workspace, ['diff', '--', *changed]) if changed else ''
    check = _scope_check(changed, created, repair_plan['change_plan'])
    opencode_error = isinstance(opencode.last_error, dict) and opencode.last_error.get('type')
    failure = f"opencode_{opencode.last_error['type']}" if opencode_error else ''
    failure = failure or (
        'opencode_mapping_failed' if trace.get('mapping_status') not in {'complete', 'events_and_diff'} else ''
    )
    failure = failure or ('opencode_failed' if opencode.returncode else '') or check['failure']
    failure = failure or ('no_diff' if not changed or not diff.strip() else '')
    check |= {'status': 'failed' if failure else 'passed', 'failure': failure,
              'opencode_returncode': opencode.returncode, 'opencode_error': opencode.last_error}
    ctx.report_progress(phase='repair_patch', status='success' if check['status'] == 'passed' else 'failed',
                        message=f"patch scope {check['status']}",
                        detail={'attempt': attempt, 'files_changed': changed, 'failure': failure})
    return {'id': _aid('code_patch_candidate', attempt), 'repair_loop_plan_ref': str(plan_ref),
            'repair_plan_ref': f'{repair_ref}@v1', 'attempt': attempt,
            'workspace_ref': str(workspace), 'files_changed': changed, 'files_created': created,
            'diff': diff, 'scope_check': check, 'opencode_run_trace_ref': f"{trace['id']}@v1"}


def _service(ctx, workspace, patch, attempt):
    log_path = workspace / '.evo_repair_logs' / f'candidate_service_attempt_{attempt}.log'
    command = command_args(ctx.params.get('candidate_service_command'))
    chat_url = str(ctx.params.get('candidate_chat_url') or '')
    health_url = str(ctx.params.get('candidate_healthcheck_url') or '')
    process = {'pid': 0, 'log_path': str(log_path)}
    health, proc = {'status': 'not_started' if not command else 'pending'}, None
    if patch['scope_check']['status'] != 'passed':
        health = {'status': 'not_started', 'reason': patch['scope_check'].get('failure') or 'patch_failed'}
    elif command:
        ctx.report_progress(phase='repair_candidate_service', status='running',
                            message='starting candidate service from candidate worktree',
                            detail={'workspace_ref': str(workspace), 'command': command})
        try:
            proc, health, process = start_candidate_process(
                ctx, workspace, command, chat_url, health_url, log_path,
                timeout_s=int(ctx.params.get('candidate_health_timeout_s', 60)))
            ctx.report_progress(phase='repair_candidate_service', status='success',
                                message='candidate service healthcheck passed', detail={'pid': proc.pid})
        except Exception as exc:
            health = {'status': 'failed', 'error': str(exc)}
            ctx.report_progress(phase='repair_candidate_service', status='failed',
                                message='candidate service healthcheck failed', detail=health)
    return {'id': _aid('candidate_service', attempt), 'code_patch_candidate_ref': f"{patch['id']}@v1",
            'attempt': attempt, 'workspace_ref': str(workspace), 'service_url': chat_url,
            'dataset_name': str(ctx.params.get('dataset_name') or ''), 'healthcheck': health,
            'process': process}, proc


def _evaluate(ctx, plan, service, patch, attempt, llm, model_config):
    failure = patch['scope_check'].get('failure') if patch['scope_check']['status'] != 'passed' else ''
    failure = failure or ('' if service.get('healthcheck', {}).get('status') == 'passed'
                          else 'candidate service was not started from candidate worktree')
    target_url = service.get('service_url')
    dataset_name = str(ctx.params.get('dataset_name') or service.get('dataset_name') or '')
    if not failure:
        failure = '' if target_url and dataset_name and str(target_url).endswith('/api/chat/stream') else (
            'candidate_chat_url and dataset_name are required for real repair evaluation')
    if failure:
        return _incomplete(attempt, failure), _candidate_report(attempt, [], failure), []
    case_ids = plan['target']['badcase_ids'] + plan['guard']['goodcase_ids']
    rows = [_eval_case(ctx, plan, case_id, target_url, dataset_name, model_config, llm) for case_id in case_ids]
    split = len(plan['target']['badcase_ids'])
    bad, good, primary = rows[:split], rows[split:], plan['policy']['primary_metric']
    overall = _overall(bad, primary, float(plan['policy']['target_mean_delta']))
    guard = _guard(good, float(plan['policy'].get('goodcase_regression_ratio_limit', 0.34)),
                   str((plan.get('guard') or {}).get('mode') or 'sampled'))
    commands = [_run_command(ctx, command, attempt, service.get('workspace_ref'))
                for command in ctx.params.get('verification_commands') or []]
    failed_command = any(item['exit_code'] for item in commands)
    status = 'passed' if overall['passed'] and guard['passed'] and not failed_command else 'failed'
    failure = '' if status == 'passed' else '; '.join(item for item in (
        overall.get('failure'), guard.get('failure'), 'verification command failed' if failed_command else ''
    ) if item)
    classified = [classify_candidate_case(ctx, row, plan, attempt, llm) for row in _candidate_focus(rows, bad)]
    evaluation = {'id': _aid('repair_evaluation', attempt), 'attempt': attempt, 'status': status,
                  'overall_eval': overall, 'badcase_eval': _bad(bad, primary), 'goodcase_impact': guard,
                  'goodcase_guard': guard,
                  'candidate_classification_report_ref': f"{_aid('candidate_classification_report', attempt)}@v1",
                  'command_results': commands, 'failure_summary': failure,
                  'next_attempt_guidance': failure or 'target reached'}
    return evaluation, _candidate_report(attempt, [row for row, _ in classified], failure), [
        draft for _, drafts in classified for draft in drafts
    ]


def _eval_case(ctx, plan, case_id, target_url, dataset_name, model_config, llm) -> dict[str, Any]:
    dataset = typed_payload(ctx, ArtifactRef.parse(str(plan.get('eval_dataset_ref') or '')), 'EvalDataset')
    case_id = validate_case_id(case_id)
    case_ref = ArtifactRef.parse(str(dataset['case_refs'][list(dataset['case_ids']).index(case_id)]))
    case = typed_payload(ctx, case_ref, 'DatasetCase')
    before, primary = _metric_baseline(ctx, plan, case_id), plan['policy']['primary_metric']
    try:
        payload = {'query': case['question'], 'history': [], 'trace': True,
                   'session_id': f'repair-{ctx.operation_run_id}-{case_id}-{uuid4().hex[:6]}',
                   'dataset': dataset_name, 'filters': {'kb_id': [dataset_name]}, 'reasoning': False,
                   'available_tools': KB_CHAT_TOOLS}
        rag = AdapterCall('rag.candidate.chat', lambda req: _call_chat(
            ctx, req['target_chat_url'], {**req['payload'], 'llm_config': model_config or None},
            timeout_s=float(ctx.params.get('candidate_case_timeout_s', 90)),
        )).run(ctx, {'target_chat_url': target_url, 'payload': payload},
               phase='repair_candidate_rag', item_ref=case_id).response
        scores = _repair_judge_scores(ctx, llm, {'prompt': JUDGE_PROMPT.format(
            question=case['question'], answer=case['answer'], guidance=case['grading_guidance'],
            rag_answer=rag['answer'], contexts='\n\n'.join(_judge_contexts(rag.get('contexts'))),
        )}, case_id)
        doc_hits, doc_misses = _hits(case.get('reference_doc_ids'), rag.get('doc_ids'))
        chunk_hits, chunk_misses = _hits(case.get('reference_chunk_ids'), rag.get('chunk_ids'))
        after = scores | {'doc_recall': _recall(doc_hits, doc_misses),
                          'context_recall': _recall(chunk_hits, chunk_misses)}
        reason, failed, trace_summary = scores.get('reason'), False, candidate_trace_summary(ctx, rag)
    except Exception as exc:
        rag = {'answer': '', 'contexts': [], 'doc_ids': [], 'chunk_ids': [], 'trace_id': '', 'kb_errors': [str(exc)]}
        reason, failed = str(exc)[:300], True
        after = {metric: 0.0 for metric in METRICS} | {'quality_label': 'bad',
                                                       'failure_type': 'candidate_execution_failed'}
        trace_summary = {'trace_available': False, 'error': reason}
    after['quality_label'] = _quality(after['answer_correctness'], after['faithfulness'],
                                      after['doc_recall'], after['context_recall'])
    after['failure_type'] = 'candidate_execution_failed' if failed else _failure_type(
        after['quality_label'], after['answer_correctness'], after['faithfulness'], after['doc_recall'],
        after['context_recall'])
    delta = {metric: round(float(after[metric]) - float(before[metric]), 4) for metric in METRICS}
    outcome = 'failed' if after['failure_type'] == 'candidate_execution_failed' else 'unchanged'
    outcome = 'improved' if outcome == 'unchanged' and delta[primary] > 0 else outcome
    outcome = 'regressed' if outcome == 'unchanged' and delta[primary] < 0 else outcome
    rag_keys = ('answer', 'contexts', 'doc_ids', 'chunk_ids', 'trace_id', 'kb_errors')
    candidate_rag = {
        key: rag.get(key, [] if key in {'contexts', 'doc_ids', 'chunk_ids', 'kb_errors'} else '')
        for key in rag_keys
    }
    return {'case_id': case_id, 'case_ref': str(case_ref), 'baseline_judge_ref': _baseline_ref(plan, case_id),
            'candidate_rag_answer': candidate_rag,
            'candidate_trace_summary': trace_summary,
            'candidate_judge_result': {**{metric: after[metric] for metric in METRICS},
                                       'quality_label': after['quality_label'],
                                       'failure_type': after['failure_type'],
                                       'reason': reason,
                                       'defect': reason if after['failure_type'] == 'candidate_execution_failed'
                                       else ''},
            'before': before, 'after': {metric: after[metric] for metric in METRICS}, 'delta': delta,
            'outcome': outcome}


def _repair_judge_scores(ctx: OperationContext, llm: Any, request: dict[str, Any], case_id: str) -> dict[str, Any]:
    raw = AdapterCall('llm.repair_judge', lambda payload: llm(payload['prompt'], stream=False)).run(
        ctx, request, phase='repair_judge', item_ref=case_id
    ).response
    return _scores(json_object(raw))


def _category(ctx: OperationContext, priorities: list[dict[str, Any]]) -> str:
    requested = str(ctx.params.get('fine_category') or '').strip()
    available = [str(item.get('fine_category') or '') for item in priorities]
    if requested and requested not in available:
        raise ValueError(f'fine_category not found in ClassificationReport priorities: {requested}')
    if not (requested or available):
        raise ValueError('ClassificationReport has no repair priorities')
    return requested or available[0]


def _target_rows(ctx, report, priorities, category) -> list[dict[str, Any]]:
    cases = {validate_case_id(str(row.get('case_id') or '')): row
             for row in report.get('cases') or [] if isinstance(row, dict)}
    priority = next((item for item in priorities if item.get('fine_category') == category), {})
    requested = [validate_case_id(str(item)) for item in ctx.params.get('target_case_ids') or []]
    reps = [str(ref).rsplit('@v', 1)[0].replace('case_fine_classification_', '')
            for ref in priority.get('representative_case_refs') or []]
    ids = requested or reps or list(priority.get('case_ids') or [])
    if not requested:
        ids = ids[: int(ctx.params.get('target_case_sample_size') or (1 if reps else len(ids)))]
    rows = []
    for case_id in ids:
        row = cases.get(validate_case_id(str(case_id)))
        if not row or row.get('fine_category') != category:
            raise ValueError(f'target case is not in selected fine_category {category}: {case_id}')
        fine = typed_payload(ctx, ArtifactRef.parse(str(row.get('fine_classification_ref') or '')),
                             'CaseFineClassification')
        rows.append({**row, 'judge_result_ref': str(fine.get('judge_result_ref') or '')})
    if not rows:
        raise ValueError('repair target badcase set is empty')
    return rows


def _goodcase_guard(ctx, eval_report, targets) -> tuple[list[dict[str, str]], dict[str, Any]]:
    bad_ids = {str(row.get('case_id') or '') for row in eval_report.get('bad_cases') or []}
    bad_ids |= {row['case_id'] for row in targets}
    candidates = []
    for raw_ref in eval_report.get('judge_result_refs') or []:
        ref = ArtifactRef.parse(str(raw_ref))
        judge = typed_payload(ctx, ref, 'JudgeResult')
        case_id = validate_case_id(str(judge.get('case_id') or ''))
        if case_id not in bad_ids and judge.get('quality_label') == 'good':
            candidates.append({'case_id': case_id, 'judge_ref': str(ref)})
    target_count, meta = len(targets), _guard_meta(ctx, len(candidates), len(targets))
    if not candidates:
        return [], meta | {'mode': 'no_goodcase', 'target_badcase_count': target_count}
    sample_size = meta['sample_size']
    rng = random.Random(str(ctx.params.get('random_seed') or f'{ctx.run_id}:'
                            f"{ctx.params.get('classification_report_ref')}"))
    guards = rng.sample(candidates, sample_size) if sample_size else []
    mode = 'sampled' if guards else 'disabled'
    return sorted(guards, key=lambda item: item['case_id']), meta | {'mode': mode,
                                                                     'target_badcase_count': target_count}


def _guard_meta(ctx: OperationContext, goodcase_count: int, badcase_count: int) -> dict[str, Any]:
    ratio = _goodcase_guard_ratio(ctx)
    sample_cap = min(int(goodcase_count * ratio), badcase_count)
    seed_text = str(ctx.params.get('random_seed') or f'{ctx.run_id}:{ctx.params.get("classification_report_ref")}')
    seed = int(hashlib.sha256(seed_text.encode()).hexdigest()[:16], 16)
    sample_size = int(np.random.default_rng(seed).binomial(sample_cap, ratio))
    return {'goodcase_pool_size': goodcase_count, 'target_badcase_count': badcase_count,
            'distribution': 'binomial', 'guard_ratio': ratio, 'sample_cap': sample_cap,
            'sample_size': sample_size}


def _goodcase_guard_ratio(ctx: OperationContext) -> float:
    value = float(ctx.params.get('goodcase_guard_ratio', 0.5))
    value = value / 100 if value > 1 else value
    return max(0.0, min(1.0, value))


def _baseline(ctx: OperationContext) -> dict[str, Any]:
    ref = str(ctx.params.get('verified_repair_ref') or '').strip()
    if not ref:
        return {'mode': 'original', 'source_ref': '', 'code_base_ref': '', 'metric_baseline_ref': '',
                'metric_baseline': {}}
    verified = typed_payload(ctx, ArtifactRef.parse(ref), 'VerifiedRepair')
    evaluation = typed_payload(ctx, ArtifactRef.parse(str(verified.get('winning_evaluation_ref') or '')),
                               'RepairEvaluation')
    workspace = str(verified.get('candidate_workspace_ref') or '')
    if verified.get('status') != 'ready_for_review' or evaluation.get('status') != 'passed' or (
        workspace and not Path(workspace).exists()
    ):
        raise ValueError(f'invalid VerifiedRepair baseline: {ref}')
    snapshot = verified.get('metric_after_snapshot') if isinstance(verified.get('metric_after_snapshot'), dict) else {}
    if not snapshot:
        raise ValueError(f'VerifiedRepair has no metric baseline snapshot: {ref}')
    return {'mode': 'verified_repair', 'source_ref': ref, 'code_base_ref': workspace,
            'metric_baseline_ref': str(verified.get('winning_evaluation_ref') or ''), 'metric_baseline': snapshot}


def _baseline_ref(plan: dict[str, Any], case_id: str) -> str:
    for bucket, key in ((plan['target'], 'baseline_judge_refs'), (plan['guard'], 'goodcase_judge_refs')):
        ids = bucket.get('badcase_ids') or bucket.get('goodcase_ids')
        if case_id in ids:
            return bucket[key][ids.index(case_id)]
    raise ValueError(f'case is not in repair loop plan: {case_id}')


def _metric_baseline(ctx: OperationContext, plan: dict[str, Any], case_id: str) -> dict[str, Any]:
    metrics = ((plan.get('baseline') or {}).get('metric_baseline') or {}).get(case_id)
    metrics = metrics or typed_payload(ctx, ArtifactRef.parse(_baseline_ref(plan, case_id)), 'JudgeResult')
    return {metric: metrics.get(metric, 0.0) for metric in METRICS}


def _overall(rows: list[dict[str, Any]], primary: str, target: float) -> dict[str, Any]:
    before, after = _avg(row['before'][primary] for row in rows), _avg(row['after'][primary] for row in rows)
    delta, failed = round(after - before, 4), sum(row['outcome'] == 'failed' for row in rows)
    failure = 'candidate execution failed' if failed else (
        '' if delta >= target else 'overall mean did not improve enough'
    )
    return {'passed': not failure,
            'summary': {'primary_metric': primary, 'case_count': len(rows), 'before_mean': before,
                        'after_mean': after, 'delta_mean': delta, 'required_delta_mean': target,
                        'failed_case_count': failed},
            'failure': failure}


def _bad(rows: list[dict[str, Any]], primary: str) -> dict[str, Any]:
    delta = {metric: round(_avg(row['after'][metric] for row in rows) - _avg(row['before'][metric] for row in rows), 4)
             for metric in METRICS}
    counts = {outcome: sum(row['outcome'] == outcome for row in rows)
              for outcome in ('improved', 'unchanged', 'regressed')}
    return {'passed': True,
            'summary': {'primary_metric': primary, 'before_mean': _avg(row['before'][primary] for row in rows),
                        'after_mean': _avg(row['after'][primary] for row in rows), 'delta_mean': delta[primary],
                        'guard_delta_mean': delta,
                        **{f'{outcome}_case_count': count for outcome, count in counts.items()}},
            'case_outcomes': rows, 'failure': ''}


def _guard(rows: list[dict[str, Any]], limit: float, mode: str) -> dict[str, Any]:
    if not rows:
        return {'passed': True, 'skipped': True,
                'summary': {'mode': mode, 'case_count': 0, 'regressed_case_count': 0,
                            'regression_ratio': 0.0, 'allowed_regression_ratio': limit},
                'cases': [], 'failure': ''}
    cases = [{**row, 'regressed': row['outcome'] in {'regressed', 'failed'}} for row in rows]
    regressed = sum(row['regressed'] for row in cases)
    ratio = round(regressed / len(cases), 4)
    return {'passed': ratio <= limit,
            'summary': {'mode': mode, 'case_count': len(cases), 'regressed_case_count': regressed,
                        'regression_ratio': ratio, 'allowed_regression_ratio': limit},
            'cases': cases, 'failure': '' if ratio <= limit else 'sampled goodcase regression ratio exceeded budget'}


def _incomplete(attempt: int, reason: str) -> dict[str, Any]:
    guard = {'passed': False, 'cases': [], 'summary': {}, 'failure': reason}
    return {'id': _aid('repair_evaluation', attempt), 'attempt': attempt, 'status': 'incomplete',
            'overall_eval': {'passed': False, 'summary': {}, 'failure': reason},
            'badcase_eval': {'passed': False, 'summary': {}, 'case_outcomes': [], 'failure': reason},
            'goodcase_impact': guard, 'goodcase_guard': guard,
            'candidate_classification_report_ref': f"{_aid('candidate_classification_report', attempt)}@v1",
            'command_results': [], 'failure_summary': reason, 'next_attempt_guidance': reason}


def _candidate_report(attempt: int, rows: list[dict[str, Any]], failure: str) -> dict[str, Any]:
    return {'id': _aid('candidate_classification_report', attempt), 'attempt': attempt,
            'case_count': len(rows), 'cases': rows, 'summary': failure}


def _candidate_focus(rows, bad) -> list[dict[str, Any]]:
    bad_ids = {id(row) for row in bad}
    return [row for row in rows if row['outcome'] in {'regressed', 'failed'}
            or id(row) in bad_ids and row['outcome'] == 'unchanged']


def _trace_observations(fines, memory) -> dict[str, Any]:
    focus = {item.get('case_id') for item in memory.get('candidate_failure_categories', []) if item.get('case_id')}
    cases = []
    for fine in sorted(fines, key=lambda item: (str(item.get('case_id') or '') not in focus,
                                                str(item.get('case_id') or '')))[:3]:
        steps = (fine.get('trace_plan') or {}).get('priority_steps') or []
        cases.append({'case_id': str(fine.get('case_id') or ''),
                      'trace_id': str(fine.get('trace_id') or ''),
                      'selected_step_ids': [str(step.get('step_id') or step.get('index') or '')
                                            for step in steps[:4]]})
    return {'cases': cases, 'source': 'CaseFineClassification.trace_plan'}


def _edit_focus(cfg, trace_obs, source_obs, memory) -> str:
    source = ', '.join(source_obs[:3]) or 'allowed repair roots'
    cases = ', '.join(str(item.get('case_id') or '') for item in trace_obs.get('cases', [])[:3])
    previous = f" Previous failure: {memory.get('next_focus')}." if memory.get('next_focus') else ''
    return short(f"{cfg['edit_instruction']} Focus files: {source}. Target cases: {cases}.{previous}", 800)


def _trace_finding(trace_obs) -> str:
    parts = [f"{case.get('case_id')}:{','.join(case.get('selected_step_ids') or []) or 'no_step'}"
             for case in trace_obs.get('cases', [])[:3]]
    return '; '.join(parts) or 'trace evidence unavailable; use classification summary'


def _source_files(workspace: Path, scope: dict[str, Any], cfg: dict[str, Any] | None = None) -> list[str]:
    out, roots = [], list(scope.get('allowed_roots') or [])
    preferred = [*(cfg or {}).get('preferred_files', []), *scope.get('seed_files', [])]
    for rel in [path for path in preferred if path and _path_in(path, roots)][:8]:
        if rel in out or not (workspace / rel).is_file():
            continue
        out.append(rel)
    return out


def _scope_check(changed: list[str], created: list[str], change: dict[str, Any]) -> dict[str, Any]:
    allowed, blocked = list(change.get('allowed_roots') or []), list(change.get('blocked_roots') or [])
    outside, blocked_hits = [path for path in changed if not _path_in(path, allowed)], [
        path for path in changed if _path_in(path, blocked)
    ]
    new_hits = created if created and not change.get('allow_new_files', True) else []
    failure = next((label for items, label in (
        (blocked_hits, 'blocked_files'), (outside, 'outside_allowed_roots'), (new_hits, 'new_files_not_allowed'),
    ) if items), '')
    return {'status': 'passed' if not failure else 'failed',
            'unexpected_files': sorted(set(outside + blocked_hits + new_hits)),
            'allow_new_files': bool(change.get('allow_new_files', True)), 'failure': failure}


def _repair_scope(ctx: OperationContext) -> dict[str, Any]:
    raw = ctx.params.get('repair_scope') if isinstance(ctx.params.get('repair_scope'), dict) else {}
    scope = default_repair_scope() | raw
    return {'allowed_roots': [item.rstrip('/') for item in _norm_paths(scope.get('allowed_roots'))],
            'seed_files': _norm_paths(scope.get('seed_files')),
            'blocked_roots': [item.rstrip('/') for item in _norm_paths(scope.get('blocked_roots'))],
            'allow_new_files': bool(scope.get('allow_new_files', True))}


def _direction_config(category: str) -> dict[str, Any]:
    direction, files, edit = REPAIR_DIRECTION_CONFIG.get(
        category, ('improve the shared failure mode with a minimal code change', [],
                   'Make the smallest safe production code change inside allowed roots.'))
    return {'direction': direction, 'preferred_files': files, 'edit_instruction': edit}


def _decision(evaluation: dict[str, Any], attempt: int) -> dict[str, Any]:
    passed = evaluation['status'] == 'passed'
    return {'id': _aid('repair_loop_decision', attempt), 'attempt': attempt,
            'decision': 'passed' if passed else 'continue',
            'reason': evaluation.get('failure_summary') or 'patch passed scope and verification',
            'next_attempt': attempt + (0 if passed else 1),
            'blocking_failures': [] if passed else [{'kind': 'repair_verification_failed',
                                                     'status': evaluation['status']}]}


def _memory(hypothesis, patch, evaluation, report, attempt) -> dict[str, Any]:
    failed = [] if evaluation['status'] == 'passed' else [{
        'attempt': attempt, 'reason': evaluation.get('failure_summary', ''),
        'avoid': 'repeat same edit without new evidence',
    }]
    return {'id': _aid('repair_loop_memory', attempt),
            'supported_directions': hypothesis.get('supported_directions', []),
            'rejected_directions': hypothesis.get('rejected_directions', []),
            'trace_steps_read': hypothesis.get('trace_steps_read', []),
            'source_files_read': hypothesis.get('source_files_read', []),
            'failed_patch_summaries': failed, 'candidate_failure_categories': candidate_failure_categories(report),
            'next_focus': evaluation.get('next_attempt_guidance', '')}


def _state(plan_ref, workspace, attempt, session, memory, patch, evaluation, decision) -> dict[str, Any]:
    return {'id': _aid('repair_loop_state', attempt), 'repair_loop_plan_ref': str(plan_ref), 'current_attempt': attempt,
            'opencode_session_id': session, 'candidate_workspace_ref': str(workspace),
            'last_memory_ref': f"{memory.get('id')}@v1", 'last_patch_ref': f"{patch.get('id')}@v1",
            'last_evaluation_ref': f"{evaluation.get('id')}@v1", 'status': decision.get('decision', '')}


def _verified(vid, plan_ref, workspace, patch, evaluation, plan) -> dict[str, Any]:
    rows = (evaluation.get('badcase_eval') or {}).get('case_outcomes', [])
    rows += (evaluation.get('goodcase_impact') or {}).get('cases', [])
    return {'id': vid, 'repair_loop_plan_ref': str(plan_ref), 'winning_patch_ref': f"{patch['id']}@v1",
            'winning_evaluation_ref': f"{evaluation['id']}@v1", 'candidate_workspace_ref': str(workspace),
            'baseline_mode': (plan.get('baseline') or {}).get('mode', 'original'),
            'metric_baseline_ref': f"{evaluation['id']}@v1",
            'metric_after_snapshot': {row['case_id']: row.get('after', {}) for row in rows if row.get('case_id')},
            'status': 'ready_for_review',
            'summary': 'repair loop target reached; final acceptance is validated by downstream ABTest'}


def _latest_state(ctx: OperationContext, plan_ref: ArtifactRef) -> dict[str, Any]:
    latest: dict[str, Any] = {}
    for path in sorted(ctx.artifact_graph.manifest_dir.glob('repair_loop_state_attempt_*.json')):
        try:
            state = typed_payload(ctx, ctx.artifact_graph.latest_ref(path.stem), 'RepairLoopState')
        except Exception:
            continue
        if state.get('repair_loop_plan_ref') == str(plan_ref) and int(state.get('current_attempt') or 0) >= int(
            latest.get('current_attempt') or 0
        ):
            latest = state
    return latest


def _run_command(ctx: OperationContext, command: Any, attempt: int, workspace_ref: Any) -> dict[str, Any]:
    workspace = Path(str(workspace_ref or ctx.params.get('candidate_workdir') or ctx.draft_dir))
    argv = command_args(command)
    path = workspace / '.evo_repair_logs' / f'test_log_attempt_{attempt}_{len(argv)}.log'
    path.parent.mkdir(parents=True, exist_ok=True)
    result = subprocess.run(argv, cwd=str(workspace), capture_output=True, text=True,
                            timeout=int(ctx.params.get('verification_timeout_s', 600)), check=False)
    path.write_text((result.stdout or '') + (result.stderr or ''), encoding='utf-8')
    return {'command': argv, 'exit_code': result.returncode, 'log_path': str(path)}


def _workspace(ctx: OperationContext, plan: dict[str, Any]) -> Path:
    raw = str((plan.get('baseline') or {}).get('code_base_ref') or ctx.params.get('candidate_workdir') or '').strip()
    if not raw:
        raw = next((str(ctx.artifact_graph.get(ref).get('workspace_ref') or '') for ref in ctx.input_refs
                    if ctx.artifact_graph.schema_name(ref) == 'CandidateWorkspace'), '')
    workspace = Path(raw or ctx.draft_dir / 'candidate').resolve()
    if (plan.get('baseline') or {}).get('code_base_ref') and not workspace.exists():
        raise ValueError(f'verified repair workspace does not exist: {workspace}')
    workspace.mkdir(parents=True, exist_ok=True)
    return workspace


def _attempt_budget(ctx: OperationContext) -> int | None:
    raw = (
        ctx.params.get('repair_attempt_budget') or ctx.params.get('max_attempts')
        or os.getenv('EVO_REPAIR_ATTEMPT_BUDGET')
    )
    if raw in (None, ''):
        return DEFAULT_REPAIR_ATTEMPT_BUDGET
    budget = int(raw)
    return budget if budget > 0 else None


def _draft(artifact_id, schema, payload, ctx, refs) -> ArtifactDraft:
    return ArtifactDraft(validate_id(artifact_id, 'artifact_id'), schema, payload, ctx.operation_run_id,
                         input_refs=refs)


def _aid(prefix: str, attempt: int) -> str:
    return f'{prefix}_attempt_{attempt}'


def _norm_paths(items: Any) -> list[str]:
    out = []
    for item in items or []:
        rel = _rel_path(str(item))
        if rel and rel not in out:
            out.append(rel)
    return out


def _rel_path(path: str) -> str:
    raw, rel = path.strip(), Path(path.strip()).as_posix()
    return '' if Path(raw).is_absolute() or not rel or rel == '.' or rel.startswith('../') or '/../' in rel else rel


def _path_in(path: str, roots: list[str]) -> bool:
    return any(path == root or path.startswith(f'{root}/') for root in roots)


def _git(workspace: Path, args: list[str]) -> str:
    result = subprocess.run(['git', '-c', f'safe.directory={workspace}', '-C', str(workspace), *args],
                            capture_output=True, text=True, check=False)
    return result.stdout if result.returncode == 0 else ''


def _git_status(workspace, ignored_roots=(), ignored_files=None) -> tuple[list[str], list[str], list[str]]:
    buckets: tuple[list[str], list[str], list[str]] = ([], [], [])
    for line in _git(workspace, ['status', '--porcelain', '--untracked-files=all']).splitlines():
        code, path = line[:2], _rel_path(line[3:].split(' -> ')[-1]) if len(line) >= 4 else ''
        if _ignored_git_path(path, ignored_roots, ignored_files):
            continue
        buckets[0].append(path)
        buckets[1].extend([path] if code == '??' or 'A' in code else [])
        buckets[2].extend([path] if 'D' in code else [])
    return tuple(sorted(set(items)) for items in buckets)


def _ignored_git_path(path: str, ignored_roots=(), ignored_files=None) -> bool:
    parts = set(Path(path).parts)
    return not path or path in (ignored_files or set()) or path.endswith('.pyc') or '__pycache__' in parts or _path_in(
        path, list(ignored_roots)
    )


def _git_add_intent(workspace: Path, paths: list[str]) -> None:
    if paths:
        _git(workspace, ['add', '-N', *paths])


def _checkpoint(workspace, attempt, ignored_roots=(), ignored_files=None) -> None:
    changed, created, _ = _git_status(workspace, ignored_roots, ignored_files)
    if not changed:
        return
    _git_add_intent(workspace, created)
    _git(workspace, ['add', '--', *changed])
    subprocess.run(['git', '-c', f'safe.directory={workspace}', '-C', str(workspace),
                    '-c', 'user.email=evo@example.local', '-c', 'user.name=evo',
                    'commit', '-m', f'repair attempt {attempt}'], check=False, capture_output=True, text=True)


def _avg(values: Any) -> float:
    nums = [score(value) for value in values]
    return round(sum(nums) / len(nums), 4) if nums else 0.0
